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
	Modules            []module                `json:"modules,omitempty"`
	Python             *pythonDistribution     `json:"python,omitempty"`
	TypeScript         *typescriptDistribution `json:"typescript,omitempty"`
	PythonPackage      string                  `json:"python_package,omitempty"`
	TypeScriptPackage  string                  `json:"typescript_package,omitempty"`
	PythonRequires     []string                `json:"python_requires,omitempty"`
	NPMDependencies    map[string]string       `json:"npm_dependencies,omitempty"`
	Name               string                  `json:"name"`
	Class              string                  `json:"class"`
	Command            string                  `json:"command"`
	Source             string                  `json:"source"`
	Version            string                  `json:"version"`
	PythonDistribution string                  `json:"python_distribution"`
	NPMPackage         string                  `json:"npm_package"`
	Repository         string                  `json:"repository"`
	License            string                  `json:"license"`
}

func loadProject() (project, error) {
	p := project{Name: "service", Command: ".", Version: "0.1.0"}
	file, err := os.Open("gobridge.json")
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer file.Close()
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	decoded := &p
	if err := dec.Decode(&decoded); err != nil {
		return p, fmt.Errorf("gobridge.json: %w", err)
	}
	if decoded == nil {
		return p, fmt.Errorf("gobridge.json: expected a JSON object, got null")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("gobridge.json: expected one JSON object")
	}
	if p.Python != nil {
		if p.PythonDistribution != "" || p.PythonRequires != nil {
			return p, fmt.Errorf("use python or legacy python_distribution/python_requires, not both")
		}
		p.PythonDistribution = p.Python.Distribution
		p.PythonRequires = p.Python.Requires
	}
	if p.TypeScript != nil {
		if p.NPMPackage != "" || p.NPMDependencies != nil {
			return p, fmt.Errorf("use typescript or legacy npm_package/npm_dependencies, not both")
		}
		p.NPMPackage = p.TypeScript.Package
		p.NPMDependencies = p.TypeScript.Dependencies
	}
	if len(p.Modules) > 0 && (p.Source != "" || p.Class != "" || p.PythonPackage != "" || p.TypeScriptPackage != "" || p.Command != ".") {
		return p, fmt.Errorf("put source, command, class and package additions inside modules")
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
	for _, reserved := range strings.Fields("Client AsyncClient RuntimeOptions DefaultControl Promise Record Uint8Array") {
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
