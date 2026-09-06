package gobridge

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type generationOptions struct {
	Initial       int     `json:"initial"`
	Options       string  `json:"options"`
	ResolveBinary string  `json:"resolve_binary"`
	Timeout       int     `json:"timeout"`
	Label         *string `json:"label,omitempty"`
}

// Execute the generated artifact against a tiny transport contract, without
// requiring the Python runtime package or starting subprocess daemons. This
// catches syntax, annotation, constructor mapping, and local-name shadowing.
func TestPythonGeneratedSignaturesAndHelperShadowing(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python executable unavailable; Python CI also exercises generated clients")
	}
	r := New()
	type record struct {
		Value string `json:"value"`
	}
	if _, err := NewObject(r, func(generationOptions) *boundCounter { return &boundCounter{} }); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		Bind(r, "echo", func(value int) int { return value }, "value"),
		Bind(r, "do_nothing", func() {}),
		Bind(r, "roundtrip", func(values []int) []int { return values }, "values"),
		Bind(r, "describe_names", func(decode string, control int, result bool) string { return decode }, "decode", "control", "result"),
		Bind(r, "record_value", func(value record) record { return value }, "record"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	var source bytes.Buffer
	if err := r.GeneratePython(&source, "Example", "example"); err != nil {
		t.Fatal(err)
	}
	script := `
import asyncio, dataclasses, inspect, sys, types

bridge = types.ModuleType("gobridge")
class RuntimeOptions:
    command = None
    timeout = 30
    max_pending = 128
    startup_timeout = 5
class Client:
    def __init__(self, command, **kwargs):
        self.command, self.kwargs = command, kwargs
    def call(self, method, params, **kwargs):
        if method == "echo": return params["value"]
        if method == "do_nothing": return None
        if method == "roundtrip": return params["values"]
        if method == "record_value": return {"value": params["record"].value}
        if method == "describe_names":
            assert params["result"] is False
            return params["decode"] + ":" + str(params["control"])
        raise AssertionError(method)
    async def acall(self, method, params, **kwargs):
        return self.call(method, params, **kwargs)
class DefaultControl:
    def __init__(self, factory): self.factory, self.instance = factory, None
    def configure(self, **kwargs): self.config = kwargs
    def client(self):
        assert self.instance is not None
        return self.instance
def decode(cls, value):
    if dataclasses.is_dataclass(cls):
        assert isinstance(cls, type), "parameter shadowed generated dataclass type"
        return cls(**value)
    if cls is None:
        assert value is None
    elif cls is int:
        assert type(value) is int
    elif cls is str:
        assert type(value) is str
    return value
def resolve_binary(path, stem): return ["bundled-" + stem]
for name in ("RuntimeOptions", "Client", "DefaultControl", "decode", "resolve_binary"):
    setattr(bridge, name, globals()[name])
bridge.require_sync = lambda: None
sys.modules["gobridge"] = bridge
sys.modules["gobridge.defaults"] = bridge
sys.modules["gobridge.runtime"] = bridge
module = types.ModuleType("generated_example")
module.__file__ = "generated_example.py"
sys.modules[module.__name__] = module
exec(compile(sys.stdin.read(), module.__file__, "exec"), module.__dict__)

assert inspect.iscoroutinefunction(module.greet) if hasattr(module, "greet") else inspect.iscoroutinefunction(module.echo)
assert not hasattr(module, "aio") and not hasattr(module, "control")
signature = inspect.signature(module.Example)
assert signature.parameters["initial"].kind is inspect.Parameter.KEYWORD_ONLY
assert signature.parameters["initial"].default is inspect.Parameter.empty
assert signature.parameters["label"].default is None
assert signature.parameters["_runtime"].default is None
assert inspect.signature(module.echo).parameters["value"].annotation == "int"
assert inspect.signature(module.echo).return_annotation == "int"
assert inspect.signature(module.do_nothing).return_annotation == "None"
assert inspect.signature(module.roundtrip).return_annotation == "list[int] | None"

client = module.SyncExample(initial=10, options="custom", resolve_binary="provider", timeout=17)
assert client.command == ["bundled-example"]
assert client.kwargs["timeout"] == 30
assert client.kwargs["init"] == {"initial": 10, "options": "custom", "resolve_binary": "provider", "timeout": 17, "label": None}
assert client.echo(value=0) == 0
assert client.do_nothing() is None
assert client.roundtrip(values=[1, 2]) == [1, 2]
assert client.describe_names(decode="safe", control=2, result=False) == "safe:2"
assert client.record_value(record=module.record(value="go")) == module.record(value="go")
module.configure(initial=10, options="custom", resolve_binary="provider", timeout=17)
assert module._bridge_defaults.config["options"] == "custom"
module._bridge_defaults.instance = client
assert module.echo_sync(value=0) == 0
assert module.describe_names_sync(decode="module", control=3, result=False) == "module:3"
assert asyncio.run(module.describe_names(decode="async", control=4, result=False)) == "async:4"
assert asyncio.run(module.do_nothing()) is None
async_client = module.Example(initial=10, options="custom", resolve_binary="provider", timeout=17)
assert asyncio.run(async_client.echo(value=2)) == 2
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", script)
	command.Stdin = &source
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated Python failed: %v\n%s", err, output)
	}
}

func TestPythonGenerationRejectsSymbolAndSchemaCollisions(t *testing.T) {
	t.Run("class", func(t *testing.T) {
		for _, class := range []string{"Client", "RuntimeOptions", "DefaultControl", "bad_name"} {
			var source bytes.Buffer
			if err := New().GeneratePython(&source, class, "service"); err == nil {
				t.Fatalf("accepted class %s", class)
			}
			if source.Len() != 0 {
				t.Fatal("wrote partial artifact before validation")
			}
		}
	})
	t.Run("type", func(t *testing.T) {
		type RuntimeOptions struct {
			Value int `json:"value"`
		}
		r := New()
		if err := Bind(r, "example", func() RuntimeOptions { return RuntimeOptions{} }); err != nil {
			t.Fatal(err)
		}
		var source bytes.Buffer
		if err := r.GeneratePython(&source, "Example", "example"); err == nil || !strings.Contains(err.Error(), "conflicts") {
			t.Fatalf("accepted reserved output type: %v", err)
		}
	})
	t.Run("synthesized_input", func(t *testing.T) {
		type AddParams struct {
			Text string `json:"text"`
		}
		r := New()
		if err := Bind(r, "add", func(value int) AddParams { return AddParams{} }, "value"); err != nil {
			t.Fatal(err)
		}
		var source bytes.Buffer
		if err := r.GeneratePython(&source, "Example", "example"); err == nil || !strings.Contains(err.Error(), "conflicting type name") {
			t.Fatalf("accepted incompatible type definitions: %v", err)
		}
	})
}

func TestPythonClassNameRejectsKeywords(t *testing.T) {
	for _, name := range []string{"None", "True", "False"} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			if err := New().GeneratePython(&out, name, "example"); err == nil {
				t.Fatal("accepted a class name that produces invalid Python syntax")
			}
			if out.Len() != 0 {
				t.Fatal("failed generation wrote partial output")
			}
		})
	}
}

func TestPythonGenerationRejectsSyncHelperCollisions(t *testing.T) {
	r := New()
	for _, name := range []string{"greet", "greet_sync"} {
		if err := Bind(r, name, func() string { return "hello" }); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := r.GeneratePython(&output, "Greeter", "greeter"); err == nil || !strings.Contains(err.Error(), "sync helper") {
		t.Fatalf("collision not rejected: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("partial output written")
	}
}
