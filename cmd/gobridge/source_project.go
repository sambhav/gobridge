package main

import (
	"encoding/json"
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// discoverProject reads package comments without compiling or running user code.
// Build constraints match adapter generation; dependencies and build outputs are
// excluded so installed/generated packages cannot become modules accidentally.
func discoverProject() (project, error) {
	p := project{Version: "0.1.0"}
	metadata := map[string]string{}
	packages := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." {
				name := entry.Name()
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "node_modules" || name == "build" || name == "dist" || name == "bin" || name == "testdata" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "zz_gobridge.gen.go" || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		dir := filepath.Dir(path)
		match, err := build.Default.MatchFile(dir, name)
		if err != nil {
			return err
		}
		if !match {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		if file.Doc == nil {
			return nil
		}
		values := map[string]string{}
		for _, comment := range file.Doc.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if !strings.HasPrefix(text, "gobridge:") {
				continue
			}
			directive := strings.TrimSpace(strings.TrimPrefix(text, "gobridge:"))
			if strings.TrimSpace(directive) == "" {
				return fmt.Errorf("%s: empty gobridge package setting", path)
			}
			key := strings.Fields(directive)[0]
			value := strings.TrimSpace(strings.TrimPrefix(directive, key))
			if value == "" {
				return fmt.Errorf("%s: //gobridge:%s needs a value", fset.Position(comment.Pos()), key)
			}
			if _, ok := values[key]; ok {
				return fmt.Errorf("%s: duplicate //gobridge:%s", path, key)
			}
			values[key] = value
		}
		if len(values) == 0 {
			return nil
		}
		if values["module"] == "" {
			return fmt.Errorf("%s: package settings require //gobridge:module NAME", path)
		}
		if packages[dir] {
			return fmt.Errorf("%s: declare module settings once per Go package", path)
		}
		packages[dir] = true
		m := module{Name: values["module"], Source: "./" + filepath.ToSlash(dir)}
		if dir == "." {
			m.Source = "."
		}
		for key, value := range values {
			switch key {
			case "module":
			case "command":
				m.Command = value
			case "command-prefix":
				if err := json.Unmarshal([]byte(value), &m.CommandPrefix); err != nil || len(m.CommandPrefix) == 0 {
					return fmt.Errorf("%s: command-prefix must be a nonempty JSON string array", path)
				}
			case "python-module":
				m.Python.Module = value
			case "ts-export":
				m.TypeScript.Export = value
			case "python-package":
				m.Python.Package = value
			case "ts-package":
				m.TypeScript.Package = value
			case "version":
				return fmt.Errorf("%s: pass the application version with --version instead of a Go comment", path)
			case "distribution", "npm", "repository", "license", "python-requires", "npm-dependencies":
				if previous, ok := metadata[key]; ok && previous != value {
					return fmt.Errorf("%s: conflicting distribution setting //gobridge:%s; declare it once or use the same value", path, key)
				}
				metadata[key] = value
			default:
				return fmt.Errorf("%s: unknown package setting //gobridge:%s", path, key)
			}
		}
		if m.Command == "" {
			if file.Name.Name == "main" {
				m.Command = m.Source
			} else {
				m.Command = "./cmd/" + file.Name.Name
			}
		}
		p.Modules = append(p.Modules, m)
		return nil
	})
	if err != nil {
		return p, err
	}
	if len(p.Modules) == 0 {
		return p, fmt.Errorf("no bridge modules found: add //gobridge:module acme.greeter above the Go package declaration, or run gobridge init")
	}
	if len(p.Modules) == 1 && p.Modules[0].TypeScript.Export == "" {
		p.Modules[0].TypeScript.Export = "."
	}
	p.Name = p.Modules[0].Python.Module
	if p.Name == "" {
		p.Name = p.Modules[0].Name
	}
	p.PythonDistribution, p.NPMPackage = metadata["distribution"], metadata["npm"]
	p.Repository, p.License = metadata["repository"], metadata["license"]
	for key, destination := range map[string]any{"python-requires": &p.PythonRequires, "npm-dependencies": &p.NPMDependencies} {
		if value := metadata[key]; value != "" {
			if err := json.Unmarshal([]byte(value), destination); err != nil {
				return p, fmt.Errorf("//gobridge:%s: %w", key, err)
			}
		}
	}
	return p, nil
}
