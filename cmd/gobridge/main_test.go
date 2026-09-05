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
