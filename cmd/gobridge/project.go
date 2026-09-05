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
}

func loadProject() (project, error) {
	p := project{Name: "service", Class: "Service", Command: ".", Version: "0.1.0"}
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
	// Derive the class when a manifest supplies only its package name.
	p.Class = ""
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("gobridge.json: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("gobridge.json: expected one JSON object")
	}
	if p.Class == "" {
		for _, part := range strings.Split(p.Name, "_") {
			if part != "" {
				p.Class += strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}
	return p, nil
}

func (p project) validate() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(p.Name) || p.Name == "gobridge" {
		return fmt.Errorf("name must be a lowercase Python package identifier other than gobridge")
	}
	for _, word := range strings.Fields("False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield") {
		if p.Name == word {
			return fmt.Errorf("name %q is a Python keyword", p.Name)
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
