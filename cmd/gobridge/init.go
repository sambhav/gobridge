package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func runInit(args []string, log io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(log)
	dir := flags.String("dir", ".", "project directory")
	module := flags.String("module", "", "Go module import path (required for a new module)")
	name := flags.String("name", "greeter", "Python import path, including optional namespaces")
	npm := flags.String("npm-package", "", "npm package, including optional @scope")
	dry := flags.Bool("dry-run", false, "print planned files without writing")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected init arguments")
	}
	p := project{Name: *name, Command: "./cmd/bridge", Source: "./bridge", Version: "0.1.0", NPMPackage: *npm}
	if err := p.validate(); err != nil {
		return err
	}
	p.PythonDistribution = p.distributionName()
	if p.NPMPackage == "" {
		p.NPMPackage = p.distributionName()
	}
	if len(p.NPMPackage) > 214 || !regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`).MatchString(p.NPMPackage) || p.NPMPackage == "gobridge-runtime" {
		return fmt.Errorf("invalid npm package %q", p.NPMPackage)
	}
	files := map[string]string{}
	existing, err := os.ReadFile(filepath.Join(*dir, "go.mod"))
	if err == nil {
		match := regexp.MustCompile(`(?m)^module\s+([^\s]+)`).FindSubmatch(existing)
		if len(match) != 2 {
			return fmt.Errorf("cannot read module path from go.mod")
		}
		found := strings.Trim(string(match[1]), `"`)
		if *module != "" && *module != found {
			return fmt.Errorf("--module disagrees with existing go.mod")
		}
		*module = found
	} else if os.IsNotExist(err) {
		if *module == "" {
			return fmt.Errorf("--module is required for a new Go module")
		}
		files["go.mod"] = fmt.Sprintf("module %s\n\ngo 1.23.0\n\nrequire github.com/sambhav/gobridge v%s\n", *module, scaffoldVersion())
	} else {
		return err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*$`).MatchString(*module) || strings.Contains(*module, "..") {
		return fmt.Errorf("invalid Go module path %q", *module)
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	files["gobridge.json"] = string(data) + "\n"
	files["bridge/greeter.go"] = "package bridge\n\n// Greet returns a friendly greeting.\n//gobridge:export\nfunc Greet(name string) string { return \"Hello, \" + name + \"!\" }\n"
	main, err := format.Source([]byte(fmt.Sprintf("package main\nimport (\"log\"; bridge %q)\nfunc main(){r,err:=bridge.NewGobridge();if err!=nil{log.Fatal(err)};r.Main()}\n", *module+"/bridge")))
	if err != nil {
		return err
	}
	files["cmd/bridge/main.go"] = string(main)
	files["app.py"] = fmt.Sprintf("from %s import Sync%s\n\nwith Sync%s() as client:\n    print(client.greet(name=\"World\"))\n", p.Name, p.Class, p.Class)
	files["app.mts"] = fmt.Sprintf("import { %s } from %q;\n\nawait using client = new %s();\nconsole.log(await client.greet({ name: \"World\" }));\n", p.Class, p.NPMPackage, p.Class)
	// Check the whole file set before writing anything. Never adopt existing files.
	for path := range files {
		dest := filepath.Join(*dir, path)
		for parent := absolute(filepath.Dir(dest)); ; parent = filepath.Dir(parent) {
			info, e := os.Lstat(parent)
			if e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return fmt.Errorf("init destination must not traverse symlinks or files: %s", parent)
			}
			if e != nil && !os.IsNotExist(e) {
				return e
			}
			if parent == absolute(*dir) || parent == filepath.Dir(parent) {
				break
			}
		}
		if _, err := os.Lstat(dest); err == nil {
			return fmt.Errorf("refusing to overwrite %s", dest)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if *dry {
		return json.NewEncoder(os.Stdout).Encode(files)
	}
	for path, data := range files {
		dest := filepath.Join(*dir, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(data)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	fmt.Fprintln(log, "Created project. Run:\n  gobridge generate --dir bridge\n  go mod tidy\n  gobridge dev -- python app.py\n  gobridge build --python --typescript")
	return nil
}

func scaffoldVersion() string {
	if v := toolVersion(); v != "dev" {
		return strings.TrimPrefix(v, "v")
	}
	return "1.1.0"
}
