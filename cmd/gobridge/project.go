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
	Name               string `json:"name"`
	Class              string `json:"class"`
	Command            string `json:"command"`
	Source             string `json:"source"`
	Version            string `json:"version"`
	PythonDistribution string `json:"python_distribution"`
	NPMPackage         string `json:"npm_package"`
	Repository         string `json:"repository"`
	License            string `json:"license"`
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
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("gobridge.json: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("gobridge.json: expected one JSON object")
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
	if !regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`).MatchString(p.Class) {
		return fmt.Errorf("class must be a capitalized identifier")
	}
	if p.Command == "" {
		return fmt.Errorf("command must name a Go command package")
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(p.Version) {
		return fmt.Errorf("version must have the form 1.2.3")
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
