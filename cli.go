package gobridge

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

func cliHelpFlag(s string) bool { return s == "--help" || s == "-h" }

func cliType(t Type) string {
	switch t.Kind {
	case "void":
		return "null"
	case "ptr":
		elem := cliType(*t.Elem)
		if strings.HasSuffix(elem, " or null") {
			return elem
		}
		return elem + " or null"
	case "slice":
		return "array[" + cliType(*t.Elem) + "] or null"
	case "map":
		return "map[string, " + cliType(*t.Elem) + "] or null"
	case "struct":
		return t.Name
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "float32", "float64":
		return "number"
	default:
		return "integer"
	}
}

func cliFieldNotes(field Field) string {
	notes := strings.Join(strings.Fields(field.Description), " ")
	if c := field.Constraints; c != nil {
		var limits []string
		if c.Minimum != "" {
			limits = append(limits, "min="+string(c.Minimum))
		}
		if c.Maximum != "" {
			limits = append(limits, "max="+string(c.Maximum))
		}
		if c.MinLength != nil {
			limits = append(limits, fmt.Sprintf("minlen=%d", *c.MinLength))
		}
		if c.MaxLength != nil {
			limits = append(limits, fmt.Sprintf("maxlen=%d", *c.MaxLength))
		}
		if len(limits) > 0 {
			if notes != "" {
				notes += " "
			}
			notes += "(" + strings.Join(limits, ", ") + ")"
		}
	}
	return notes
}

func cliField(out io.Writer, name string, field Field) {
	required := "required"
	if field.Type.Kind == "ptr" {
		required = "optional"
	}
	fmt.Fprintf(out, "  %s\t%s\t%s", name, cliType(field.Type), required)
	if notes := cliFieldNotes(field); notes != "" {
		fmt.Fprintf(out, "\t%s", notes)
	}
	fmt.Fprintln(out)
}

// cliFields renders the same metadata used by validation and Python generation.
// Configuration uses JSON keys; operation arguments use kebab-case flags.
// Nested structs describe JSON members, never additional CLI flags.
func cliFields(out io.Writer, fields []Field, flags bool) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, field := range fields {
		name := field.Name
		if flags {
			name = "--" + strings.ReplaceAll(name, "_", "-")
		}
		cliField(w, name, field)
	}
	_ = w.Flush()
}

func cliNestedFields(out io.Writer, fields []Field) {
	var nested strings.Builder
	nestedWriter := tabwriter.NewWriter(&nested, 0, 4, 2, ' ', 0)
	var visit func(Field, string)
	visit = func(field Field, path string) {
		t := field.Type
		for t.Kind == "ptr" {
			t = *t.Elem
		}
		if t.Kind != "struct" {
			return
		}
		for _, child := range t.Fields {
			childPath := path + "." + child.Name
			cliField(nestedWriter, childPath, child)
			visit(child, childPath)
		}
	}
	for _, field := range fields {
		visit(field, field.Name)
	}
	_ = nestedWriter.Flush()
	if nested.Len() > 0 {
		fmt.Fprintln(out, "\nNested JSON members (requiredness applies when their parent is provided):")
		fmt.Fprint(out, nested.String())
	}
}

func (r *Registry) cliConfigHelp(out io.Writer) {
	if r.constructor == nil {
		return
	}
	fmt.Fprintln(out, "\nConstructor: --config OBJECT before the operation (omitted: {}).")
	fmt.Fprintln(out, "JSON configuration fields:")
	fields := r.constructor.schema().Fields
	cliFields(out, fields, false)
	cliNestedFields(out, fields)
}

func (r *Registry) cliHelp(out io.Writer) {
	program := filepath.Base(os.Args[0])
	fmt.Fprintf(out, "Usage: %s <operation> [--field value ... | --json OBJECT | --json -]\n", program)
	fmt.Fprintln(out, "\nOperations:")
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, name := range r.names() {
		fmt.Fprintf(w, "  %s\t%s\n", name, r.ops[name].description)
	}
	_ = w.Flush()
	fmt.Fprintln(out, "\nCommands:")
	fmt.Fprintln(w, "  serve\tRun the private stdio daemon.")
	fmt.Fprintln(w, "  schema\tPrint the complete JSON schema.")
	fmt.Fprintln(w, "  generate-python\tGenerate typed Python functions and clients.")
	fmt.Fprintln(w, "  generate-typescript\tGenerate typed TypeScript functions and clients.")
	_ = w.Flush()
	fmt.Fprintf(out, "\nHelp: %s <operation> --help or %s help <operation>\n", program, program)
	r.cliConfigHelp(out)
}

func (r *Registry) cliOperationHelp(name string, out io.Writer) error {
	op, ok := r.ops[name]
	if !ok {
		return Failure("not_found", "unknown operation: "+name)
	}
	fmt.Fprintln(out, name)
	if op.description != "" {
		fmt.Fprintln(out, op.description)
	}
	config := ""
	if r.constructor != nil {
		config = "[--config OBJECT] "
	}
	fmt.Fprintf(out, "\nUsage: %s %s%s [--field value ... | --json OBJECT | --json -]\n", filepath.Base(os.Args[0]), config, name)
	fmt.Fprintln(out, "\nFlags:")
	fields := op.inputSchema().Fields
	cliFields(out, fields, true)
	fmt.Fprintln(out, "  --json OBJECT  Read the whole input as JSON; use - for stdin.")
	fmt.Fprintln(out, "  -h, --help     Show this help without running the operation.")
	cliNestedFields(out, fields)
	fmt.Fprintf(out, "\nResult: %s\n", cliType(describe(op.out)))
	r.cliConfigHelp(out)
	return nil
}

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
		if args[0] == "serve" || args[0] == "schema" || args[0] == "generate-python" || args[0] == "generate-typescript" || args[0] == "help" || cliHelpFlag(args[0]) {
			return fmt.Errorf("--config is supported for direct operation commands only")
		}
	}
	if len(args) == 0 {
		r.cliHelp(out)
		return nil
	}
	if cliHelpFlag(args[0]) || args[0] == "help" {
		if args[0] == "help" && len(args) == 2 {
			return r.cliOperationHelp(args[1], out)
		}
		if len(args) != 1 {
			return fmt.Errorf("unexpected arguments after %s", args[0])
		}
		r.cliHelp(out)
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
		if len(args) != 1 {
			return fmt.Errorf("schema does not accept arguments")
		}
		return json.NewEncoder(out).Encode(r.Schema())
	case "generate-python":
		flags := flag.NewFlagSet("generate-python", flag.ContinueOnError)
		flags.SetOutput(stderr)
		class := flags.String("class", "Service", "Python client class name")
		binary := flags.String("binary", "service", "bundled executable name")
		namesJSON := flags.String("names", "{}", "JSON class/operations/types/fields naming overrides")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected arguments")
		}
		var names Names
		if err := json.Unmarshal([]byte(*namesJSON), &names); err != nil {
			return err
		}
		return r.GeneratePython(out, *class, *binary, WithPython(names))
	case "generate-typescript":
		flags := flag.NewFlagSet("generate-typescript", flag.ContinueOnError)
		flags.SetOutput(stderr)
		class := flags.String("class", "Service", "TypeScript client class name")
		binary := flags.String("binary", "service", "bundled executable name")
		namesJSON := flags.String("names", "{}", "JSON class/operations/types/fields naming overrides")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected arguments")
		}
		var names Names
		if err := json.Unmarshal([]byte(*namesJSON), &names); err != nil {
			return err
		}
		return r.GenerateTypeScript(out, *class, *binary, WithTypeScript(names))
	}
	op, ok := r.ops[args[0]]
	if !ok {
		return Failure("not_found", "unknown operation: "+args[0])
	}
	if config != nil && r.constructor == nil {
		return fmt.Errorf("--config requires a registered constructor")
	}
	if len(args) > 1 && cliHelpFlag(args[1]) {
		if len(args) != 2 {
			return fmt.Errorf("unexpected arguments after %s", args[1])
		}
		return r.cliOperationHelp(args[0], out)
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
			if t.Kind == "string" || t.Kind == "timestamp" || t.Kind == "bytes" {
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
