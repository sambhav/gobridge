package sourcegen

import (
	"bytes"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeSource(t *testing.T, dir, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateDeterministicAndCompile(t *testing.T) {
	dir := t.TempDir()
	// Local names deliberately collide with the generator's preferred locals.
	writeSource(t, dir, "library.go", `package main
import ctx "context"
type Options struct { Prefix *string `+"`json:\"prefix,omitempty\"`"+` }
type Greeter struct { prefix string }
//gobridge:constructor
func NewGreeter(options Options) *Greeter { return &Greeter{prefix: "Hi "} }
// ReadURL handles acronym boundaries and grouped arguments.
//gobridge:export read_url
func (g Greeter) ReadURL(requestContext ctx.Context, userID, urlPath string) string { return g.prefix + userID + urlPath }
//gobridge:export registry_value
func _registry() string { return "function" }
var _gobridge = 1
var _object = 2
var _err = 3
func main() { r, err := NewGobridge(); if err != nil { panic(err) }; r.Main() }
`)
	output := "zz_gobridge.gen.go"
	if err := Generate(dir, output); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, output))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"read_url", (*Greeter).ReadURL, "user_id", "url_path"`, `Describe("read_url", "ReadURL handles acronym boundaries and grouped arguments.")`, `"github.com/sambhav/gobridge"`} {
		if !bytes.Contains(first, []byte(expected)) {
			t.Errorf("generated adapter lacks %q:\n%s", expected, first)
		}
	}
	if err := Generate(dir, output); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, output))
	if !bytes.Equal(first, second) {
		t.Fatal("regeneration changed adapter")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	writeSource(t, dir, "go.mod", "module sourcegentest\n\ngo 1.23\n\nrequire github.com/sambhav/gobridge v0.0.0\nreplace github.com/sambhav/gobridge => "+filepath.ToSlash(root)+"\n")
	binary := filepath.Join(dir, "generated-example")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile generated adapter: %v\n%s", err, output)
	}
	cmd = exec.Command(binary, "--config", "{}", "read_url", "--user_id", "Sam", "--url_path", "/hello")
	if output, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != `"Hi Sam/hello"` {
		t.Fatalf("generated CLI: %v\n%s", err, output)
	}
}

func TestRejections(t *testing.T) {
	tests := []struct{ name, source, want string }{
		{"generic function", "//gobridge:export\nfunc Echo[T any](value T) T { return value }", "generic declarations"},
		{"variadic", "//gobridge:export\nfunc Join(names ...string) {}", "variadic"},
		{"unnamed", "//gobridge:export\nfunc Greet(string) {}", "unnamed parameters"},
		{"blank", "//gobridge:export\nfunc Greet(_ string) {}", "blank parameters"},
		{"duplicate export", "//gobridge:export greet\nfunc First() {}\n//gobridge:export greet\nfunc Second() {}", "collides"},
		{"parameter collision", "//gobridge:export\nfunc Read(userID, user_id string) {}", "collides after snake_case"},
		{"multiple constructors", "type Opt struct{}; type Obj struct{}\n//gobridge:constructor\nfunc NewOne(opt Opt) *Obj { return nil }\n//gobridge:constructor\nfunc NewTwo(opt Opt) *Obj { return nil }", "multiple constructors"},
		{"missing constructor", "type Obj struct{}\n//gobridge:export\nfunc (o *Obj) Greet() {}", "no matching"},
		{"wrong receiver", "type Opt struct{}; type Obj struct{}; type Other struct{}\n//gobridge:constructor\nfunc NewObj(opt Opt) *Obj { return nil }\n//gobridge:export\nfunc (o *Other) Greet() {}", "no matching"},
		{"generic receiver", "type Obj[T any] struct{}\n//gobridge:export\nfunc (o *Obj[T]) Greet() {}", "generic or unnamed receivers"},
		{"constructor method", "type Obj struct{}\n//gobridge:constructor\nfunc (o *Obj) New(opt string) *Obj { return nil }", "constructor must be a function"},
		{"bad constructor result", "type Obj struct{}\n//gobridge:constructor\nfunc NewObj(opt string) Obj { return Obj{} }", "pointer to a named"},
		{"bad constructor parameters", "type Obj struct{}\n//gobridge:constructor\nfunc NewObj(first, second string) *Obj { return nil }", "one named options"},
		{"invalid rename", "//gobridge:export bad-name\nfunc Greet() {}", "invalid operation name"},
		{"unknown annotation", "//gobridge:expotr\nfunc Greet() {}", "unknown annotation"},
		{"duplicate annotation", "//gobridge:export\n//gobridge:constructor\nfunc Greet() {}", "only one"},
		{"extra annotation args", "//gobridge:export first second\nfunc Greet() {}", "use //gobridge:export"},
		{"constructor annotation args", "//gobridge:constructor name\nfunc Greet() {}", "takes no arguments"},
		{"existing declaration", "func NewGobridge() {}\n//gobridge:export\nfunc Greet() {}", "already declared"},
		{"no annotations", "func Greet() {}", "no //gobridge:export"},
		{"context position", "import ctx \"context\"\n//gobridge:export\nfunc Greet(name string, request ctx.Context) {}", "must be the first"},
		{"grouped context", "import \"context\"\n//gobridge:export\nfunc Greet(first, second context.Context) {}", "single context parameter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSource(t, dir, "input.go", "package fixture\n"+tt.source+"\n")
			err := Generate(dir, "zz_gobridge.gen.go")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", err, tt.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "zz_gobridge.gen.go")); !os.IsNotExist(err) {
				t.Fatal("rejected input wrote an output file")
			}
		})
	}
}

func TestBuildConstraintsAndExcludedFiles(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "input.go", "package fixture\n//gobridge:export\nfunc Common() {}\n")
	writeSource(t, dir, "linux.go", "//go:build linux\n\npackage fixture\n//gobridge:export\nfunc LinuxOnly() {}\n")
	writeSource(t, dir, "windows.go", "//go:build windows\n\npackage fixture\n//gobridge:export\nfunc WindowsOnly() {}\n")
	writeSource(t, dir, "broken_test.go", "this is intentionally not Go")
	writeSource(t, dir, "zz_gobridge.gen.go", "this is intentionally not Go")
	for _, goos := range []string{"linux", "windows"} {
		ctx := build.Default
		ctx.GOOS = goos
		data, err := render(dir, "zz_gobridge.gen.go", ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`"common"`)) || !bytes.Contains(data, []byte(`"`+goos+`_only"`)) {
			t.Fatalf("%s generated incorrect operations:\n%s", goos, data)
		}
		other := "windows"
		if goos == "windows" {
			other = "linux"
		}
		if bytes.Contains(data, []byte(`"`+other+`_only"`)) {
			t.Fatalf("included operation for %s when building %s", other, goos)
		}
	}
}

func TestProtectHandwrittenOutput(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "input.go", "package fixture\n//gobridge:export\nfunc Greet() {}\n")
	const handwritten = "package fixture\n// precious handwritten code\n"
	writeSource(t, dir, "zz_gobridge.gen.go", handwritten)
	if err := Generate(dir, "zz_gobridge.gen.go"); err == nil || !strings.Contains(err.Error(), "handwritten") {
		t.Fatalf("got %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "zz_gobridge.gen.go"))
	if string(data) != handwritten {
		t.Fatal("handwritten file changed")
	}
	for _, output := range []string{"../outside.go", "input.txt", "input_test.go", "_ignored.go", ".ignored.go"} {
		if err := Generate(dir, output); err == nil {
			t.Errorf("accepted output %q", output)
		}
	}
}

func TestCheckDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	const output = "zz_gobridge.gen.go"
	const source = "package fixture\n//gobridge:export\nfunc Greet() {}\n"
	writeSource(t, dir, "input.go", source)
	if err := Check(dir, output); err == nil || !strings.Contains(err.Error(), "missing or stale") {
		t.Fatalf("missing output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, output)); !os.IsNotExist(err) {
		t.Fatal("check wrote missing output")
	}
	if err := Generate(dir, output); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir, output); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, output))
	writeSource(t, dir, "input.go", source+"\n//gobridge:export\nfunc NewOperation() {}\n")
	if err := Check(dir, output); err == nil || !strings.Contains(err.Error(), "missing or stale") {
		t.Fatalf("changed source: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, output))
	if !bytes.Equal(before, after) {
		t.Fatal("check rewrote stale output")
	}
}

func TestSnakeCase(t *testing.T) {
	for input, expected := range map[string]string{"Greet": "greet", "ReadURL": "read_url", "HTTPServer": "http_server", "userID": "user_id", "URL": "url", "Version2ID": "version2_id", "already_named": "already_named"} {
		if got := snakeCase(input); got != expected {
			t.Errorf("snakeCase(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestConstructorWithoutMethods(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "input.go", `package fixture
type Options struct{}
type Greeter struct{}
//gobridge:constructor
func NewGreeter(options Options) *Greeter { return nil }
//gobridge:export
func Greet() string { return "hello" }
`)
	data, err := render(dir, "zz_gobridge.gen.go", build.Default)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("_object")) || !bytes.Contains(data, []byte("_, _err := _gobridge.NewObject")) {
		t.Fatalf("constructor with no methods emitted an unused variable:\n%s", data)
	}
}

func TestContextImportForms(t *testing.T) {
	for _, contextImport := range []struct{ statement, parameter string }{
		{`import ctx "context"`, "ctx.Context"},
		{"import ctx `context`", "ctx.Context"},
		{`import . "context"`, "Context"},
	} {
		dir := t.TempDir()
		writeSource(t, dir, "input.go", "package fixture\n"+contextImport.statement+"\n//gobridge:export\nfunc Greet(request "+contextImport.parameter+", name string) {}\n")
		data, err := render(dir, "zz_gobridge.gen.go", build.Default)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`"greet", Greet, "name"`)) || bytes.Contains(data, []byte(`"request"`)) {
			t.Fatalf("context parameter was not omitted for %s:\n%s", contextImport.statement, data)
		}
	}
}

func TestFunctionalOptionAnnotations(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "library.go", `package example
//gobridge:python ExampleClient
type Example struct{}
type Option func(*Example)
//gobridge:constructor
func New(options ...Option)*Example{return nil}
//gobridge:option
//gobridge:python request_timeout
func WithTimeout(value int)Option{return nil}
//gobridge:export
//gobridge:ts getValue
func (e *Example) Value()int{return 0}
`)
	if err := Generate(dir, "zz_gobridge.gen.go"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "zz_gobridge.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`NewObject(`, `ConstructorOption("timeout", WithTimeout)`, `"ExampleConfig.timeout": "request_timeout"`, `Class: "ExampleClient"`, `"value": "getValue"`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("missing %q: %s", want, data)
		}
	}
	if err := Check(dir, "zz_gobridge.gen.go"); err != nil {
		t.Fatal(err)
	}
}
