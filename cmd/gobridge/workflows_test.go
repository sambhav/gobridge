package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inDirectory(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestInitDryRunAndOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	var out bytes.Buffer
	args := []string{"--dir", dir, "--module", "example.test/project", "--name", "acme.tools.greeter", "--npm-package", "@acme/greeter"}
	if err := runInit(append(args, "--dry-run"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote files")
	}
	if err := runInit(args, &out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "cmd/bridge/main.go"))
	if !bytes.Contains(data, []byte(`"example.test/project/bridge"`)) {
		t.Fatal(string(data))
	}
	before, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err := runInit(args, &out); err == nil {
		t.Fatal("accepted overwrite")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !bytes.Equal(before, after) {
		t.Fatal("overwrote module")
	}
	existing := filepath.Join(t.TempDir(), "existing")
	writeTestFile(t, filepath.Join(existing, "go.mod"), "module example.test/existing\n\ngo 1.23\n")
	if err := runInit([]string{"--dir", existing}, &out); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(existing, "cmd/bridge/main.go"))
	if !bytes.Contains(data, []byte(`"example.test/existing/bridge"`)) {
		t.Fatal(string(data))
	}
}

func TestBuildValidationBeforeGeneration(t *testing.T) {
	dir := t.TempDir()
	inDirectory(t, dir)
	writeTestFile(t, "gobridge.json", `{"name":"acme.greeter","source":".","command":"."}`)
	writeTestFile(t, "main.go", "package main\n//gobridge:export\nfunc Greet()string{return \"hello\"}\nfunc main(){}\n")
	for _, args := range [][]string{{"--targets", "bogus"}, {"--version", "01.2.3"}, {"--distribution", "bad/name"}, {"--typescript", "--npm-package", "../bad"}, {"--targets", "linux-amd64,linux-amd64"}} {
		if err := runBuild(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if _, err := os.Stat("zz_gobridge.gen.go"); !os.IsNotExist(err) {
			t.Fatal("validation generated source")
		}
		if _, err := os.Stat("dist"); !os.IsNotExist(err) {
			t.Fatal("validation created output")
		}
	}
}

func TestArtifactPublication(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	output := filepath.Join(root, "dist")
	writeTestFile(t, filepath.Join(source, "one.whl"), "one")
	writeTestFile(t, filepath.Join(source, "npm/two.tgz"), "two")
	if err := publishArtifacts(source, output, buildPlan{Targets: []string{"linux-amd64"}}, false); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(output, "gobridge-build.json"))
	var manifest map[string]any
	if json.Unmarshal(original, &manifest) != nil {
		t.Fatal("invalid manifest")
	}
	writeTestFile(t, filepath.Join(source, "one.whl"), "changed")
	if err := publishArtifacts(source, output, buildPlan{}, false); err == nil {
		t.Fatal("accepted conflicting overwrite")
	}
	preserved, _ := os.ReadFile(filepath.Join(output, "gobridge-build.json"))
	if !bytes.Equal(original, preserved) {
		t.Fatal("failure changed completion marker")
	}
	data, _ := os.ReadFile(filepath.Join(output, "one.whl"))
	if string(data) != "one" {
		t.Fatal("failure changed artifact")
	}
	if err := publishArtifacts(source, output, buildPlan{}, true); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(output, "one.whl"))
	if string(data) != "changed" {
		t.Fatal("replacement missing")
	}
	writeTestFile(t, filepath.Join(output, ".gobridge-build-lock"), "")
	if err := publishArtifacts(source, output, buildPlan{}, true); err == nil {
		t.Fatal("ignored lock")
	}
}

func TestEmbedAndApplicationWatching(t *testing.T) {
	inDirectory(t, t.TempDir())
	writeTestFile(t, "main.go", "package main\nimport _ \"embed\"\n//go:embed assets/*.txt\nvar data string\n")
	writeTestFile(t, "assets/message.txt", "one")
	writeTestFile(t, "app.mts", "one")
	go1, app1, err := sourceHashes(absolute("node_modules/@acme/greeter"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, "dev.log", "log output")
	writeTestFile(t, "requests.txt", "app output")
	go2, app2, _ := sourceHashes(absolute("node_modules/@acme/greeter"))
	if go1 != go2 || app1 != app2 {
		t.Fatal("application output triggers reload")
	}
	writeTestFile(t, "app.mts", "two")
	go2, app2, _ = sourceHashes(absolute("node_modules/@acme/greeter"))
	if go1 != go2 || app1 == app2 {
		t.Fatal("TS edits must only restart app")
	}
	writeTestFile(t, "assets/new.txt", "two")
	go2, _, _ = sourceHashes(absolute("node_modules/@acme/greeter"))
	if go1 == go2 {
		t.Fatal("embed addition missed")
	}
	_ = os.Remove("assets/new.txt")
	go2, _, _ = sourceHashes(absolute("node_modules/@acme/greeter"))
	if go1 != go2 {
		t.Fatal("embed removal missed")
	}
	writeTestFile(t, "build/acme/sibling/.gobridge-dev", devMarker)
	writeTestFile(t, "build/acme/sibling/generated.py", "one")
	go2, app2, _ = sourceHashes(absolute("node_modules/@acme/greeter"))
	writeTestFile(t, "build/acme/sibling/generated.py", "two")
	go3, app3, _ := sourceHashes(absolute("node_modules/@acme/greeter"))
	if go2 != go3 || app2 != app3 {
		t.Fatal("sibling generated package triggers reload")
	}
}

func TestCustomizationGuards(t *testing.T) {
	inDirectory(t, t.TempDir())
	writeTestFile(t, "custom/_bindings.py", "collision")
	if err := validateCustomization(project{PythonPackage: "custom"}, true, false); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatal(err)
	}
	if err := validateCustomization(project{PythonPackage: "../outside"}, true, false); err == nil {
		t.Fatal("accepted traversal")
	}
	if err := validateCustomization(project{PythonRequires: []string{"x\nInjected: value"}}, true, false); err == nil {
		t.Fatal("accepted metadata injection")
	}
}

func TestNullProjectManifest(t *testing.T) {
	inDirectory(t, t.TempDir())
	writeTestFile(t, "gobridge.json", "null")
	if _, err := loadProject(); err == nil {
		t.Fatal("accepted null project manifest")
	}
}
