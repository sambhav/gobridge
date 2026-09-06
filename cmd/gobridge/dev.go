package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sambhav/gobridge/internal/sourcegen"
)

var embedPatternTokens = regexp.MustCompile("`[^`]*`|\"(?:[^\"\\\\]|\\\\.)*\"|[^\\s]+")

const devMarker = "gobridge development package v1\n"

type devOptions struct {
	selectedModule *resolvedModule
	project
	output     string
	app        []string
	interval   time.Duration
	once       bool
	typescript bool
}

func runDev(ctx context.Context, args []string, log io.Writer) error {
	p, err := loadCommandProject(args)
	if err != nil {
		return err
	}
	options := devOptions{project: p}
	flags := flag.NewFlagSet("dev", flag.ContinueOnError)
	flags.SetOutput(log)
	flags.StringVar(&options.Version, "version", p.Version, "application package version")
	moduleName := flags.String("module", "", "module name declared in Go comments or gobridge.json")
	flags.StringVar(&options.output, "python", "", "generated Python package directory (default build/<package/path>)")
	flags.BoolVar(&options.typescript, "typescript", false, "generate a local npm package and restart a Node application")
	flags.DurationVar(&options.interval, "interval", 500*time.Millisecond, "source polling interval")
	flags.BoolVar(&options.once, "once", false, "build one matching package and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(p.Modules) > 0 || *moduleName != "" {
		modules, err := options.project.resolveModules()
		if err != nil {
			return err
		}
		if *moduleName == "" && len(modules) == 1 {
			*moduleName = modules[0].Name
		}
		for _, module := range modules {
			if module.Name == *moduleName {
				m := module
				options.selectedModule = &m
				options.project = m.project(options.project)
				break
			}
		}
		if options.selectedModule == nil {
			return fmt.Errorf("select a configured module with --module NAME")
		}

	}
	options.app = flags.Args()
	if err := options.validate(); err != nil {
		return err
	}
	if err := validateCustomization(options.project, !options.typescript, options.typescript); err != nil {
		return err
	}
	if options.interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if options.once && len(options.app) > 0 {
		return fmt.Errorf("--once does not run an application; omit --once for reload")
	}
	if options.typescript {
		if options.output != "" {
			return fmt.Errorf("--python and --typescript are mutually exclusive")
		}
		if options.NPMPackage == "" {
			options.NPMPackage = options.distributionName()
		}
		if !validNPMName(options.NPMPackage) {
			return fmt.Errorf("invalid npm package %q", options.NPMPackage)
		}
		options.output = filepath.Join("node_modules", filepath.FromSlash(options.NPMPackage))
	}
	if options.output == "" {
		options.output = filepath.Join("build", options.packagePath())
	}
	options.output = absolute(options.output)
	packageRoot := options.output
	parts := strings.Split(options.Name, ".")
	for i := len(parts) - 1; !options.typescript && i >= 0; i-- {
		if filepath.Base(packageRoot) != parts[i] {
			return fmt.Errorf("--python must end in the import package path %q", options.packagePath())
		}
		if i < len(parts)-1 {
			if _, err := os.Stat(filepath.Join(packageRoot, "__init__.py")); err == nil {
				return fmt.Errorf("namespace parent must not contain __init__.py: %s", packageRoot)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		packageRoot = filepath.Dir(packageRoot)
	}
	if err := prepareDevOutput(options.output); err != nil {
		return err
	}
	if options.once {
		return buildDev(ctx, options, log)
	}
	manifest, _ := os.ReadFile("gobridge.json")
	configurationChanged := false
	goHash, pyHash, err := sourceHashes(options.output, options.PythonPackage, options.TypeScriptPackage)
	if err != nil {
		return err
	}
	var app *devApp
	ready := false
	defer func() {
		if app != nil {
			app.stop()
		}
	}()
	rebuild := func() bool {
		if err := buildDev(ctx, options, log); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintln(log, "Build failed; keeping the last working package:", err)
			}
			return false
		}
		ready = true
		return true
	}
	restart := func() {
		if !ready {
			return
		}
		if app != nil {
			app.stop()
			app = nil
		}
		if len(options.app) > 0 {
			var err error
			app, err = startDevApp(options.app, packageRoot)
			if err != nil {
				fmt.Fprintln(log, "Application:", err)
			}
		}
	}
	if rebuild() {
		restart()
	}
	fmt.Fprintln(log, "Watching Go, embedded assets, and application source; Ctrl-C stops development.")
	ticker := time.NewTicker(options.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		nextManifest, _ := os.ReadFile("gobridge.json")
		if string(nextManifest) != string(manifest) {
			manifest = nextManifest
			configurationChanged = true
			fmt.Fprintln(log, "gobridge.json changed; restart gobridge dev to apply configuration. Keeping the last working application.")
			continue
		}
		if configurationChanged {
			continue
		}
		nextGo, nextPy, err := sourceHashes(options.output, options.PythonPackage, options.TypeScriptPackage)
		if err != nil {
			fmt.Fprintln(log, "Watch:", err)
			continue
		}
		if nextGo != goHash {
			current, err := loadProject()
			if err != nil {
				fmt.Fprintln(log, "Build failed; keeping the last working package:", err)
				continue
			}
			if !reflect.DeepEqual(current, p) {
				configurationChanged = true
				fmt.Fprintln(log, "Go package settings changed; restart gobridge dev to apply configuration. Keeping the last working application.")
				continue
			}
			goHash, pyHash = nextGo, nextPy
			if rebuild() {
				restart()
			}
		} else if nextPy != pyHash {
			pyHash = nextPy
			restart()
		}
	}
}

func prepareDevOutput(output string) error {
	entries, err := os.ReadDir(output)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) > 0 {
		marker, err := os.ReadFile(filepath.Join(output, ".gobridge-dev"))
		if err != nil || string(marker) != devMarker {
			return fmt.Errorf("refusing to overwrite an existing package: %s", output)
		}
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, ".gobridge-dev"), []byte(devMarker), 0644)
}

func buildDev(ctx context.Context, options devOptions, log io.Writer) error {
	if options.Source != "" {
		if err := sourcegen.Generate(options.Source, "zz_gobridge.gen.go"); err != nil {
			return err
		}
	}
	stage, err := os.MkdirTemp(options.output, ".build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary := filepath.Join(stage, "service"+suffix)
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, options.Command)
	build.Stdout = log
	build.Stderr = log
	build.Env = hostGoEnv()
	if err := build.Run(); err != nil {
		return err
	}
	file, err := os.Open(binary)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return err
	}
	if options.typescript {
		return buildDevTypeScript(ctx, options, binary, log)
	}
	stem := options.binaryName() + "-" + hex.EncodeToString(hash.Sum(nil))[:24]
	namesJSON := "{}"
	if options.selectedModule != nil {
		data, _ := json.Marshal(options.selectedModule.Python.Rename)
		namesJSON = string(data)
	}
	var prefix []string
	if options.selectedModule != nil {
		prefix = options.selectedModule.CommandPrefix
	}
	prefixJSON, _ := json.Marshal(prefix)
	args := append(append([]string{}, prefix...), "generate-python", "--class", options.Class, "--binary", stem, "--names", namesJSON, "--command-prefix", string(prefixJSON))
	generate := exec.CommandContext(ctx, binary, args...)
	generate.Stderr = log
	bindings, err := generate.Output()
	if err != nil {
		return err
	}
	if options.PythonPackage != "" {
		return buildDevPythonPackage(ctx, options, stage, binary, stem, suffix, bindings, log)
	}
	bindings, err = bundlePython(ctx, options.output, bindings, log)
	if err != nil {
		return err
	}
	binaryDir := filepath.Join(options.output, "_bin")
	if err := os.MkdirAll(binaryDir, 0755); err != nil {
		return err
	}
	target := filepath.Join(binaryDir, stem+suffix)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.Chmod(binary, 0755); err != nil {
			return err
		}
		if err := os.Rename(binary, target); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(options.output, "py.typed"), nil); err != nil {
		return err
	}
	// The import entrypoint is the commit point; all referenced assets exist first.
	if err := atomicWrite(filepath.Join(options.output, "__init__.py"), bindings); err != nil {
		return err
	}
	// CPython timestamp caches can otherwise reuse old source after a fast edit.
	_ = os.RemoveAll(filepath.Join(options.output, "__pycache__"))
	fmt.Fprintln(log, "Updated", options.output, "with", stem)
	return nil
}

func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".publish-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Chmod(name, 0644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func hostGoEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "GOOS" && key != "GOARCH" {
			env = append(env, entry)
		}
	}
	return env
}

func sourceHashes(output string, packages ...string) (string, string, error) {
	goHash, pyHash := sha256.New(), sha256.New()
	embedded := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." {
				if marker, e := os.ReadFile(filepath.Join(path, ".gobridge-dev")); e == nil && string(marker) == devMarker {
					return filepath.SkipDir
				}
			}
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" || entry.Name() == "__pycache__" || entry.Name() == "dist" || entry.Name() == "bin" || absolute(path) == output) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "zz_gobridge.gen.go" || entry.Name() == "gobridge.json" {
			return nil
		}
		custom := false
		for _, root := range packages {
			if root != "" {
				rel, e := filepath.Rel(absolute(root), absolute(path))
				if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					custom = true
				}
			}
		}
		var writer io.Writer
		switch {
		case custom:
			writer = goHash
		case strings.HasSuffix(path, ".go"), entry.Name() == "go.mod", entry.Name() == "go.sum", entry.Name() == "go.work":
			writer = goHash
		case strings.HasSuffix(path, ".py"), strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".mts"), strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".mjs"), strings.HasSuffix(path, ".cts"), strings.HasSuffix(path, ".cjs"):
			writer = pyHash
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".go") {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "//go:embed ") && !strings.HasPrefix(line, "//go:embed\t") {
					continue
				}
				for _, pattern := range embedPatternTokens.FindAllString(strings.TrimSpace(strings.TrimPrefix(line, "//go:embed")), -1) {
					if strings.HasPrefix(pattern, "\"") || strings.HasPrefix(pattern, "`") {
						decoded, e := strconv.Unquote(pattern)
						if e != nil {
							continue
						}
						pattern = decoded
					}
					all := strings.HasPrefix(pattern, "all:")
					pattern = strings.TrimPrefix(pattern, "all:")
					matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), filepath.FromSlash(pattern)))
					for _, match := range matches {
						_ = filepath.WalkDir(match, func(asset string, e fs.DirEntry, err error) error {
							if err != nil {
								return nil
							}
							if e.Type()&os.ModeSymlink != 0 {
								return nil
							}
							if !all && asset != match && (strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_")) {
								if e.IsDir() {
									return filepath.SkipDir
								}
								return nil
							}
							if !e.IsDir() {
								embedded[asset] = true
							}
							return nil
						})
					}
				}
			}
		}
		fmt.Fprintf(writer, "%s\x00%d\x00", path, len(data))
		_, err = writer.Write(data)
		return err
	})
	if err == nil {
		paths := make([]string, 0, len(embedded))
		for path := range embedded {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			data, e := os.ReadFile(path)
			if e != nil {
				err = e
				break
			}
			fmt.Fprintf(goHash, "embed:%s\x00%d\x00", path, len(data))
			goHash.Write(data)
		}
	}
	return hex.EncodeToString(goHash.Sum(nil)), hex.EncodeToString(pyHash.Sum(nil)), err
}

type devApp struct {
	command *exec.Cmd
	done    chan struct{}
}

func startDevApp(argv []string, packageParent string) (*devApp, error) {
	command := exec.Command(argv[0], argv[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PYTHONPATH") {
			env = append(env, entry)
		}
	}
	path := packageParent
	if old := os.Getenv("PYTHONPATH"); old != "" {
		path += string(os.PathListSeparator) + old
	}
	command.Env = append(env, "PYTHONPATH="+path)
	if err := command.Start(); err != nil {
		return nil, err
	}
	app := &devApp{command: command, done: make(chan struct{})}
	go func() { _ = command.Wait(); close(app.done) }()
	return app, nil
}
func (app *devApp) stop() {
	select {
	case <-app.done:
		return
	default:
	}
	if err := app.command.Process.Signal(os.Interrupt); err != nil {
		_ = app.command.Process.Kill()
	}
	select {
	case <-app.done:
	case <-time.After(2 * time.Second):
		_ = app.command.Process.Kill()
		<-app.done
	}
}
