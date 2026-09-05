package gobridge

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Run exposes the same registry as a CLI, schema command, generator, or daemon.
// Operation flags use JSON literals, except strings which are passed as text.
func (r *Registry) Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	var config json.RawMessage
	if len(args) > 0 && args[0] == "--config" {
		if len(args) < 3 {
			return fmt.Errorf("--config requires a JSON object followed by an operation")
		}
		config = json.RawMessage(args[1])
		args = args[2:]
		if args[0] == "serve" || args[0] == "schema" || args[0] == "generate-python" || args[0] == "help" || args[0] == "--help" {
			return fmt.Errorf("--config is supported for direct operation commands only")
		}
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "Commands: serve, schema, generate-python, <operation> [--json OBJECT | --field value ...]")
		if r.constructor != nil {
			fmt.Fprintln(out, "Constructor: --config OBJECT <operation> ... (omitted config defaults to {})")
		}
		for _, n := range r.names() {
			fmt.Fprintf(out, "  %-20s %s\n", n, r.ops[n].description)
		}
		return nil
	}
	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		n := flags.Int("max-concurrency", 64, "maximum concurrent operations")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected arguments")
		}
		return r.Serve(ctx, in, out, *n)
	case "schema":
		return json.NewEncoder(out).Encode(r.Schema())
	case "generate-python":
		flags := flag.NewFlagSet("generate-python", flag.ContinueOnError)
		flags.SetOutput(stderr)
		class := flags.String("class", "Service", "Python client class name")
		binary := flags.String("binary", "service", "bundled executable name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected arguments")
		}
		return r.GeneratePython(out, *class, *binary)
	}
	op, ok := r.ops[args[0]]
	if !ok {
		return Failure("not_found", "unknown operation: "+args[0])
	}
	var raw []byte
	if len(args) > 1 && args[1] == "--json" {
		if len(args) != 3 {
			return fmt.Errorf("--json requires exactly one JSON object (or - for stdin)")
		}
		raw = []byte(args[2])
		if args[2] == "-" {
			var err error
			raw, err = io.ReadAll(io.LimitReader(in, MaxFrame+1))
			if err != nil {
				return err
			}
		}
	} else {
		fields := map[string]Type{}
		for _, f := range op.inputSchema().Fields {
			fields[f.Name] = f.Type
		}
		values := map[string]any{}
		for j := 1; j < len(args); j += 2 {
			if j+1 >= len(args) || !strings.HasPrefix(args[j], "--") {
				return fmt.Errorf("expected --field value pairs")
			}
			n := strings.ReplaceAll(strings.TrimPrefix(args[j], "--"), "-", "_")
			t, ok := fields[n]
			if !ok {
				return fmt.Errorf("unknown field %q", n)
			}
			if _, ok := values[n]; ok {
				return fmt.Errorf("duplicate field %q", n)
			}
			for t.Kind == "ptr" {
				t = *t.Elem
			}
			if t.Kind == "string" {
				values[n] = args[j+1]
			} else {
				var v json.RawMessage = []byte(args[j+1])
				if !json.Valid(v) {
					return fmt.Errorf("%s requires a JSON literal", n)
				}
				values[n] = v
			}
		}
		raw, _ = json.Marshal(values)
	}
	if len(raw) > MaxFrame {
		return fmt.Errorf("input exceeds frame limit")
	}
	if r.constructor != nil {
		if len(config) > MaxFrame {
			return fmt.Errorf("constructor config exceeds frame limit")
		}
		if err := r.Initialize(ctx, config); err != nil {
			return err
		}
	} else if config != nil {
		return fmt.Errorf("--config requires a registered constructor")
	}
	v, err := r.Call(ctx, args[0], raw)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(v)
}

// Main runs with the process streams and sets a nonzero exit status on error.
func (r *Registry) Main() {
	if err := r.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(wireError(err))
		os.Exit(1)
	}
}
