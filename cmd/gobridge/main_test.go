package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAndCheckCommands(t *testing.T) {
	dir := t.TempDir()
	source := "package fixture\n//gobridge:export\nfunc Greet(name string) string { return name }\n"
	if err := os.WriteFile(filepath.Join(dir, "input.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	args := []string{"generate", "--dir", dir, "--output", "adapter.go"}
	if err := run(append(args, "--check"), &stderr); err == nil || !strings.Contains(err.Error(), "missing or stale") {
		t.Fatalf("check missing adapter: %v", err)
	}
	if err := run(args, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := run(append(args, "--check"), &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "adapter.go"))
	if err != nil || !bytes.Contains(data, []byte(`"greet", Greet, "name"`)) {
		t.Fatalf("adapter: %v\n%s", err, data)
	}
	if err := run([]string{"unknown"}, &stderr); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command: %v", err)
	}
}

func TestDottedPackageNames(t *testing.T) {
	for _, name := range []string{"greeter", "acme.greeter", "acme.tools.greeter"} {
		p := project{Name: name, Command: ".", Version: "0.1.0"}
		if err := p.validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if p.Class != "Greeter" {
			t.Errorf("%s: class %q", name, p.Class)
		}
	}
	for _, name := range []string{"", ".greeter", "acme.", "acme..greeter", "acme/greeter", "acme.class", "Acme.greeter", "acme.foo-bar", "gobridge", "gobridge.greeter"} {
		p := project{Name: name, Class: "Greeter", Command: ".", Version: "0.1.0"}
		if err := p.validate(); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	p := project{Name: "acme.tools.greeter"}
	if p.packagePath() != filepath.Join("acme", "tools", "greeter") || p.binaryName() != "acme_tools_greeter" || p.distributionName() != "acme-tools-greeter" {
		t.Fatalf("incorrect namespace naming: %+v", p)
	}
}
