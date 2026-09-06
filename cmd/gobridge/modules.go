package main

import (
	"fmt"
	"regexp"
	"strings"

	bridge "github.com/sambhav/gobridge"
)

type pythonDistribution struct {
	Distribution string   `json:"distribution,omitempty"`
	Requires     []string `json:"requires,omitempty"`
}
type typescriptDistribution struct {
	Package      string            `json:"package,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}
type moduleTarget struct {
	Module  string       `json:"module,omitempty"`
	Export  string       `json:"export,omitempty"`
	Class   string       `json:"class,omitempty"`
	Package string       `json:"package,omitempty"`
	Rename  bridge.Names `json:"rename,omitempty"`
}
type module struct {
	Name       string       `json:"name"`
	Source     string       `json:"source,omitempty"`
	Command    string       `json:"command"`
	Python     moduleTarget `json:"python,omitempty"`
	TypeScript moduleTarget `json:"typescript,omitempty"`
}

// Resolved modules are passed to the packagers, keeping CLI overrides and
// defaults in one place. Each module owns its executable and lifecycle.
type resolvedModule struct {
	Name       string       `json:"name"`
	Source     string       `json:"source,omitempty"`
	Command    string       `json:"command"`
	Binary     string       `json:"binary"`
	Python     moduleTarget `json:"python"`
	TypeScript moduleTarget `json:"typescript"`
}

func (p project) resolveModules() ([]resolvedModule, error) {
	modules := p.moduleSpecs()
	result := make([]resolvedModule, 0, len(modules))
	seen := map[string]bool{}
	for _, m := range modules {
		if m.Name == "" || seen["name:"+m.Name] {
			return nil, fmt.Errorf("missing or duplicate module name %q", m.Name)
		}
		seen["name:"+m.Name] = true
		if m.Python.Module == "" {
			m.Python.Module = m.Name
		}
		if m.TypeScript.Export == "" {
			m.TypeScript.Export = "./" + strings.ReplaceAll(m.Name, ".", "/")
		}
		pythonClass, typescriptClass := m.Python.Class, m.TypeScript.Class
		if m.Python.Export != "" || m.TypeScript.Module != "" {
			return nil, fmt.Errorf("module %s: module is Python-only and export is TypeScript-only", m.Name)
		}
		if m.Python.Rename.Class != "" || m.TypeScript.Rename.Class != "" {
			return nil, fmt.Errorf("module %s: put class beside rename, not inside it", m.Name)
		}
		check := project{Name: m.Python.Module, Class: m.Python.Class, Command: m.Command, Version: p.Version}
		if err := check.validate(); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.Name, err)
		}
		m.Python.Class = check.Class
		if m.TypeScript.Class == "" {
			m.TypeScript.Class = check.Class
		}
		check.Class = m.TypeScript.Class
		if err := check.validate(); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.Name, err)
		}
		export := m.TypeScript.Export
		if export != "." && !regexp.MustCompile(`^\./[a-z][a-z0-9_-]*(/[a-z][a-z0-9_-]*)*$`).MatchString(export) {
			return nil, fmt.Errorf("module %s: invalid TypeScript export %q", m.Name, export)
		}
		for _, part := range strings.Split(strings.TrimPrefix(export, "./"), "/") {
			if part == "_bin" || part == "_gobridge" || part == "node_modules" {
				return nil, fmt.Errorf("reserved module export %q", export)
			}
		}
		for _, part := range strings.Split(m.Python.Module, ".") {
			if part == "_bin" || part == "_gobridge" {
				return nil, fmt.Errorf("reserved Python module %q", m.Python.Module)
			}
		}
		for _, key := range []string{"python:" + m.Python.Module, "typescript:" + export} {
			if seen[key] {
				return nil, fmt.Errorf("duplicate module output %s", key)
			}
			seen[key] = true
		}
		m.Python.Rename.Class = pythonClass
		m.TypeScript.Rename.Class = typescriptClass
		result = append(result, resolvedModule{Name: m.Name, Source: m.Source, Command: m.Command, Binary: check.binaryName(), Python: m.Python, TypeScript: m.TypeScript})
	}
	return result, nil
}
func (m resolvedModule) project(p project) project {
	p.Modules = nil
	p.Name = m.Python.Module
	p.Source = m.Source
	p.Command = m.Command
	p.Class = m.Python.Class
	p.PythonPackage = m.Python.Package
	p.TypeScriptPackage = m.TypeScript.Package
	return p
}

func (p project) moduleSpecs() []module {
	modules := p.Modules
	if len(modules) == 0 {
		modules = []module{{Name: p.Name, Source: p.Source, Command: p.Command, Python: moduleTarget{Class: p.Class, Package: p.PythonPackage}, TypeScript: moduleTarget{Class: p.Class, Package: p.TypeScriptPackage, Export: "."}}}
	}
	return modules
}
