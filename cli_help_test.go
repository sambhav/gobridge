package gobridge

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

type cliHelpModel struct {
	Enabled bool `json:"enabled"`
}

type cliHelpInput struct {
	DisplayName string                  `json:"display_name"`
	Label       *string                 `json:"label,omitempty"`
	Count       int64                   `json:"count"`
	Ratio       float64                 `json:"ratio"`
	Enabled     bool                    `json:"enabled"`
	Items       []string                `json:"items"`
	Models      map[string]cliHelpModel `json:"models"`
}

func newCLIHelpRegistry(t *testing.T) (*Registry, *atomic.Int64) {
	t.Helper()
	r := New()
	var calls atomic.Int64
	if err := Register(r, "inspect", "Inspect the supplied fields.", func(context.Context, cliHelpInput) (cliHelpModel, error) {
		calls.Add(1)
		return cliHelpModel{Enabled: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return r, &calls
}

func requireHelpLine(t *testing.T, output, expected string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.Join(strings.Fields(line), " ") == expected {
			return
		}
	}
	t.Fatalf("help is missing %q:\n%s", expected, output)
}

func TestCLIOperationHelpHasTypesAndNeverCallsHandler(t *testing.T) {
	r, calls := newCLIHelpRegistry(t)
	var previous string
	for _, args := range [][]string{{"inspect", "--help"}, {"inspect", "-h"}, {"help", "inspect"}} {
		var out, stderr bytes.Buffer
		if err := r.Run(context.Background(), args, strings.NewReader(""), &out, &stderr); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if calls.Load() != 0 || stderr.Len() != 0 {
			t.Fatalf("help called a handler or wrote errors: %d, %s", calls.Load(), stderr.String())
		}
		for _, line := range []string{
			"Inspect the supplied fields.",
			"--display-name string required",
			"--label string or null optional",
			"--count integer required",
			"--ratio number required",
			"--enabled boolean required",
			"--items array[string] or null required",
			"--models map[string, cliHelpModel] or null required",
			"Result: cliHelpModel",
		} {
			requireHelpLine(t, out.String(), line)
		}
		if !strings.Contains(out.String(), "--json OBJECT | --json -") {
			t.Fatal("help omitted JSON input forms")
		}
		if previous != "" && previous != out.String() {
			t.Fatal("operation help forms disagree")
		}
		previous = out.String()
	}
}

func TestCLITopLevelHelpFormsAgree(t *testing.T) {
	r, calls := newCLIHelpRegistry(t)
	var previous string
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		var out, stderr bytes.Buffer
		if err := r.Run(context.Background(), args, strings.NewReader(""), &out, &stderr); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, text := range []string{"inspect", "Inspect the supplied fields.", "serve", "schema", "generate-python", "<operation> --help", "help <operation>"} {
			if !strings.Contains(out.String(), text) {
				t.Fatalf("top-level help missing %q: %s", text, out.String())
			}
		}
		if previous != "" && previous != out.String() {
			t.Fatal("top-level help forms disagree")
		}
		previous = out.String()
	}
	if calls.Load() != 0 {
		t.Fatal("top-level help called the handler")
	}
}

func TestCLIHelpShowsScalarAndVoidResults(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		fn           any
	}{
		{"text", "string", func() string { panic("help must not call") }},
		{"count", "integer", func() int { panic("help must not call") }},
		{"number", "number", func() float64 { panic("help must not call") }},
		{"enabled", "boolean", func() bool { panic("help must not call") }},
		{"optional", "string or null", func() *string { panic("help must not call") }},
		{"nothing", "null", func() { panic("help must not call") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if err := Bind(r, tc.name, tc.fn); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := r.Run(context.Background(), []string{tc.name, "--help"}, strings.NewReader(""), &out, &out); err != nil {
				t.Fatal(err)
			}
			requireHelpLine(t, out.String(), "Result: "+tc.result)
		})
	}
}

func TestCLIConstructorHelpDoesNotInitializeOrValidateConfig(t *testing.T) {
	r, constructors := newTestObject(t)
	for _, args := range [][]string{
		{"-h"}, {"add", "--help"}, {"help", "add"},
		{"--config", `{"initial":10}`, "add", "--help"},
		{"--config", "not valid JSON", "add", "-h"},
	} {
		var out, stderr bytes.Buffer
		if err := r.Run(context.Background(), args, strings.NewReader(""), &out, &stderr); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if constructors.Load() != 0 || !r.NeedsInit() || stderr.Len() != 0 {
			t.Fatal("help initialized a constructor or reported an error")
		}
		if !strings.Contains(out.String(), "--config OBJECT") {
			t.Fatal("help omitted constructor configuration")
		}
		requireHelpLine(t, out.String(), "initial integer required")
		requireHelpLine(t, out.String(), "label string or null optional")
	}
	var out, stderr bytes.Buffer
	if err := r.Run(context.Background(), []string{"--config", `{"initial":10}`, "add", "--amount", "2"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if out.String() != "12\n" || stderr.Len() != 0 || constructors.Load() != 1 {
		t.Fatal("help affected later constructor invocation or polluted JSON output")
	}
}

func TestCLIMetadataRejectsUnexpectedArgumentsWithoutSideEffects(t *testing.T) {
	for _, args := range [][]string{
		{"schema", "extra"}, {"schema", "--help"},
		{"help", "add", "extra"}, {"help", "missing"},
		{"--help", "extra"}, {"-h", "add"},
		{"add", "--help", "extra"}, {"add", "-h", "extra"},
		{"generate-python", "--class", "Counter", "extra"},
		{"serve", "--max-concurrency", "2", "extra"},
		{"--config", `{}`, "-h"}, {"--config", `{}`, "schema"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r, constructors := newTestObject(t)
			var out, stderr bytes.Buffer
			if err := r.Run(context.Background(), args, strings.NewReader(""), &out, &stderr); err == nil {
				t.Fatal("accepted unexpected metadata arguments")
			}
			if out.Len() != 0 || constructors.Load() != 0 || !r.NeedsInit() {
				t.Fatal("invalid metadata command produced output or initialized the object")
			}
		})
	}
}

func TestCLIHelpDoesNotConsumeAStringFlagValue(t *testing.T) {
	r := testRegistry(t)
	var out, stderr bytes.Buffer
	if err := r.Run(context.Background(), []string{"repeat", "--text", "--help", "--count", "1"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\"value\":\"--help\"}\n" || stderr.Len() != 0 {
		t.Fatalf("string flag or JSON stdout changed: %s, %s", out.String(), stderr.String())
	}
	out.Reset()
	if err := r.Run(context.Background(), []string{"--config", `{}`, "repeat", "--help"}, strings.NewReader(""), &out, &stderr); err == nil {
		t.Fatal("accepted --config on a registry with no constructor")
	}
}

type cliMetadataAddress struct {
	City string `json:"city" doc:"City name." validate:"maxlen=80"`
}

type cliMetadataRequest struct {
	DisplayName string              `json:"display_name" doc:"Name to greet." validate:"minlen=1,maxlen=80"`
	Age         *int                `json:"age,omitempty" doc:"Optional age in years." validate:"min=0,max=120"`
	Large       int64               `json:"large" doc:"Exact integer limit." validate:"min=9007199254740993,max=9223372036854775807"`
	Empty       string              `json:"empty" validate:"minlen=0,maxlen=0"`
	Address     *cliMetadataAddress `json:"address,omitempty" doc:"Optional postal address."`
}

func TestCLIHelpDisplaysSharedFieldDocumentationAndLimits(t *testing.T) {
	r := New()
	if err := Register(r, "inspect", "Documented input.", func(context.Context, cliMetadataRequest) (cliHelpModel, error) {
		panic("help must not call the handler")
	}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := r.Run(context.Background(), []string{"inspect", "--help"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"--display-name string required Name to greet. (minlen=1, maxlen=80)",
		"--age integer or null optional Optional age in years. (min=0, max=120)",
		"--large integer required Exact integer limit. (min=9007199254740993, max=9223372036854775807)",
		"--empty string required (minlen=0, maxlen=0)",
		"--address cliMetadataAddress or null optional Optional postal address.",
		"address.city string required City name. (maxlen=80)",
	} {
		requireHelpLine(t, out.String(), expected)
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote an error: %s", stderr.String())
	}
}

func TestCLIBoundStructHelpShowsJSONPathsWithoutInventingFlags(t *testing.T) {
	r := New()
	var calls atomic.Int64
	if err := Bind(r, "greet", func(request cliMetadataRequest) string {
		calls.Add(1)
		return "Hello, " + request.DisplayName
	}, "request"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := r.Run(context.Background(), []string{"greet", "--help"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"--request cliMetadataRequest required",
		"request.display_name string required Name to greet. (minlen=1, maxlen=80)",
		"request.age integer or null optional Optional age in years. (min=0, max=120)",
		"request.address cliMetadataAddress or null optional Optional postal address.",
		"request.address.city string required City name. (maxlen=80)",
	} {
		requireHelpLine(t, out.String(), expected)
	}
	if !strings.Contains(out.String(), "Nested JSON members (requiredness applies when their parent is provided)") {
		t.Fatal("help does not explain that nested paths are JSON members")
	}
	for _, invented := range []string{"--request.display_name", "--request-display-name", "--display-name", "--city"} {
		if strings.Contains(out.String(), invented) {
			t.Fatalf("help invented nested flag %q", invented)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("help called the handler")
	}

	// The documented limits are enforced by the actual operation path, with
	// the same nested field names and no output or handler call on failure.
	out.Reset()
	bad := `{"display_name":"","large":9007199254740993,"empty":""}`
	err := r.Run(context.Background(), []string{"greet", "--request", bad}, strings.NewReader(""), &out, &stderr)
	if err == nil || wireError(err).Code != "invalid_argument" || !strings.Contains(err.Error(), "request.display_name") {
		t.Fatalf("CLI did not enforce the displayed nested constraint: %v", err)
	}
	if out.Len() != 0 || calls.Load() != 0 {
		t.Fatal("invalid input produced a result or reached the handler")
	}
	valid := `{"display_name":"Ada","large":9007199254740993,"empty":""}`
	if err := r.Run(context.Background(), []string{"greet", "--request", valid}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if out.String() != "\"Hello, Ada\"\n" || calls.Load() != 1 {
		t.Fatal("valid JSON input stopped working after metadata inspection")
	}
}

func TestCLIConstructorHelpUsesJSONNamesAndSharedMetadata(t *testing.T) {
	r := New()
	var constructors atomic.Int64
	object, err := NewObject(r, func(cliMetadataRequest) *boundCounter {
		constructors.Add(1)
		return &boundCounter{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := object.Bind("add", (*boundCounter).Add, "amount"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := r.Run(context.Background(), []string{"add", "--help"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"display_name string required Name to greet. (minlen=1, maxlen=80)",
		"age integer or null optional Optional age in years. (min=0, max=120)",
		"address.city string required City name. (maxlen=80)",
	} {
		requireHelpLine(t, out.String(), expected)
	}
	if strings.Contains(out.String(), "--display-name") || strings.Contains(out.String(), "--address.city") {
		t.Fatal("constructor JSON fields were presented as operation flags")
	}
	if constructors.Load() != 0 || !r.NeedsInit() || stderr.Len() != 0 {
		t.Fatal("metadata inspection initialized the object or reported errors")
	}
}
