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
