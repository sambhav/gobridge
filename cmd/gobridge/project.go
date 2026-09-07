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

// projectFile keeps the common single-module layout flat. Modules are only
// needed when a distribution contains multiple independently configured bridges.
type projectFile struct {
	Name          string                  `json:"name,omitempty"`
	Source        string                  `json:"source,omitempty"`
	Command       string                  `json:"command,omitempty"`
	CommandPrefix []string                `json:"command_prefix,omitempty"`
	Version       string                  `json:"version"`
	Repository    string                  `json:"repository,omitempty"`
	License       string                  `json:"license,omitempty"`
	Python        *pythonDistribution     `json:"python,omitempty"`
	TypeScript    *typescriptDistribution `json:"typescript,omitempty"`
	Modules       []module                `json:"modules,omitempty"`
}

func loadProject() (project, error) {
	p := project{Version: "0.1.0"}
	file, err := os.Open("gobridge.json")
	if os.IsNotExist(err) {
		return discoverProject()
	}
	if err != nil {
		return p, fmt.Errorf("open gobridge.json: %w", err)
	}
	defer file.Close()
	config := &projectFile{Version: "0.1.0"}
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&config); err != nil {
		return p, fmt.Errorf("gobridge.json: %w; see Go package settings in README.md", err)
	}
	if config == nil {
		return p, fmt.Errorf("gobridge.json: expected an object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("gobridge.json: expected one JSON object")
	}
	if len(config.Modules) == 0 {
		if config.Name == "" {
			return p, fmt.Errorf("gobridge.json: set name to the Python import path (for example acme.greeter)")
		}
		source, command := config.Source, config.Command
		if command == "" {
			command = "./cmd/bridge"
		}
		// The scaffold needs no path settings. An explicit command without a
		// source supports manually registered or embedded binaries.
		if source == "" && config.Command == "" {
			source = "./bridge"
		}
		config.Modules = []module{{Name: config.Name, Source: source, Command: command, CommandPrefix: config.CommandPrefix, TypeScript: moduleTarget{Export: "."}}}
	} else if config.Name != "" || config.Source != "" || config.Command != "" || len(config.CommandPrefix) != 0 {
		return p, fmt.Errorf("gobridge.json: use either name/source/command or modules, not both")
	}
	p.Modules = config.Modules
	p.Version = config.Version
	p.Repository = config.Repository
	p.License = config.License
	if config.Python == nil {
		config.Python = &pythonDistribution{}
	}
	if config.TypeScript == nil {
		config.TypeScript = &typescriptDistribution{}
	}
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
	if p.Command == "" && len(p.Modules) == 0 {
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
	config := projectFile{Version: p.Version, Repository: p.Repository, License: p.License,
		Python:     &pythonDistribution{Distribution: p.PythonDistribution, Requires: p.PythonRequires},
		TypeScript: &typescriptDistribution{Package: p.NPMPackage, Dependencies: p.NPMDependencies}, Modules: p.moduleSpecs()}
	if len(p.Modules) == 0 {
		config.Modules = nil
		config.Name = p.Name
		config.Source = p.Source
		config.Command = p.Command
		if p.Source == "./bridge" && p.Command == "./cmd/bridge" {
			config.Source, config.Command = "", ""
		}
	}
	if (config.Python.Distribution == "" || config.Python.Distribution == p.distributionName()) && len(config.Python.Requires) == 0 {
		config.Python = nil
	}
	if (config.TypeScript.Package == "" || config.TypeScript.Package == p.distributionName()) && len(config.TypeScript.Dependencies) == 0 {
		config.TypeScript = nil
	}
	return config
}

func loadCommandProject(args []string) (project, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return project{}, nil
	}
	return loadProject()
}
