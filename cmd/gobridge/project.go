package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type project struct {
	Modules            []module
	PythonPackage      string
	TypeScriptPackage  string
	PythonRequires     []string
	NPMDependencies    map[string]string
	Name               string
	Class              string
	Command            string
	Source             string
	Version            string
	PythonDistribution string
	NPMPackage         string
	Repository         string
	License            string
}

// projectFile is the only accepted gobridge.json format. Module settings live
// in modules even when the distribution contains just one module.
type projectFile struct {
	Version    string                 `json:"version"`
	Repository string                 `json:"repository,omitempty"`
	License    string                 `json:"license,omitempty"`
	Python     pythonDistribution     `json:"python"`
	TypeScript typescriptDistribution `json:"typescript"`
	Modules    []module               `json:"modules"`
}

func loadProject() (project, error) {
	p := project{Name: "service", Command: ".", Version: "0.1.0"}
	file, err := os.Open("gobridge.json")
	if err != nil {
		return p, fmt.Errorf("open gobridge.json (run gobridge init to create a project): %w", err)
	}
	defer file.Close()
	config := &projectFile{Version: "0.1.0"}
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&config); err != nil {
		return p, fmt.Errorf("gobridge.json: %w; use the modules-based format in README.md", err)
	}
	if config == nil {
		return p, fmt.Errorf("gobridge.json: expected an object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("gobridge.json: expected one JSON object")
	}
	if len(config.Modules) == 0 {
		return p, fmt.Errorf("gobridge.json: at least one module is required")
	}
	p.Modules = config.Modules
	p.Version = config.Version
	p.Repository = config.Repository
	p.License = config.License
	p.PythonDistribution = config.Python.Distribution
	p.PythonRequires = config.Python.Requires
	p.NPMPackage = config.TypeScript.Package
	p.NPMDependencies = config.TypeScript.Dependencies
	p.Name = p.Modules[0].Python.Module
	if p.Name == "" {
		p.Name = p.Modules[0].Name
	}
	return p, nil
}

func (p *project) validate() error {
	parts := strings.Split(p.Name, ".")
	if parts[0] == "gobridge" {
		return fmt.Errorf("name must not use the gobridge runtime namespace")
	}
	for _, part := range parts {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(part) {
			return fmt.Errorf("name must be dot-separated lowercase Python package identifiers")
		}
		for _, word := range strings.Fields("False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield") {
			if part == word {
				return fmt.Errorf("name component %q is a Python keyword", part)
			}
		}
	}
	if p.Class == "" {
		for _, part := range strings.Split(p.Name[strings.LastIndex(p.Name, ".")+1:], "_") {
			if part != "" {
				p.Class += strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}
	if !regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`).MatchString(p.Class) {
		return fmt.Errorf("class must be a capitalized identifier")
	}
	for _, reserved := range strings.Fields("Client RuntimeOptions DefaultControl Promise Record Uint8Array") {
		if p.Class == reserved {
			return fmt.Errorf("class %q conflicts with generated symbols", p.Class)
		}
	}
	if p.Command == "" {
		return fmt.Errorf("command must name a Go command package")
	}
	if _, err := pythonPackageVersion(p.Version); err != nil {
		return err
	}
	return nil
}

func absolute(path string) string { value, _ := filepath.Abs(path); return filepath.Clean(value) }

// Namespace separators become directories for Python and underscores for binaries.
func (p project) packagePath() string { return filepath.Join(strings.Split(p.Name, ".")...) }
func (p project) binaryName() string  { return strings.ReplaceAll(p.Name, ".", "_") }
func (p project) distributionName() string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(p.Name)
}

// manifest presents the same schema for scaffolding and inspected build plans.
func (p project) manifest() projectFile {
	return projectFile{Version: p.Version, Repository: p.Repository, License: p.License,
		Python:     pythonDistribution{Distribution: p.PythonDistribution, Requires: p.PythonRequires},
		TypeScript: typescriptDistribution{Package: p.NPMPackage, Dependencies: p.NPMDependencies}, Modules: p.moduleSpecs()}
}

func loadCommandProject(args []string) (project, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return project{}, nil
	}
	return loadProject()
}
