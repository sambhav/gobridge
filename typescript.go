package gobridge

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

var typescriptIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

var typescriptReserved = func() map[string]bool {
	m := map[string]bool{}
	for _, name := range strings.Fields("await break case catch class const continue debugger default delete do else enum export extends false finally for function if implements import in instanceof interface let new null package private protected public return static super switch this throw true try typeof var void while with yield any bigint boolean never number object string symbol unknown undefined abstract as asserts declare infer is keyof namespace readonly require global type unique using satisfies override") {
		m[name] = true
	}
	return m
}()

var typescriptMethodReserved = func() map[string]bool {
	m := map[string]bool{}
	for _, name := range strings.Fields("constructor prototype then call start close schema control toString toLocaleString valueOf hasOwnProperty isPrototypeOf propertyIsEnumerable") {
		m[name] = true
	}
	return m
}()

// tsCamel matches the schema codec's lower_snake_case to lowerCamelCase mapping.
func tsCamel(name string) string {
	parts := strings.Split(name, "_")
	var b strings.Builder
	b.WriteString(parts[0])
	for _, part := range parts[1:] {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]))
			b.WriteString(part[1:])
		}
	}
	return b.String()
}

func tsType(t Type) string {
	switch t.Kind {
	case "void":
		return "void"
	case "struct":
		return t.Name
	case "ptr":
		elem := tsType(*t.Elem)
		if strings.HasSuffix(elem, " | null") {
			return elem
		}
		return elem + " | null"
	case "slice":
		elem := tsType(*t.Elem)
		if strings.Contains(elem, " | ") || strings.HasPrefix(elem, "readonly ") {
			elem = "(" + elem + ")"
		}
		return "readonly " + elem + "[] | null"
	case "map":
		return "Record<string, " + tsType(*t.Elem) + "> | null"
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int64":
		return "bigint"
	default:
		return "number"
	}
}

// JSON string quoting also produces valid JavaScript literals for control
// characters, unlike Go-specific string escapes such as \a or \UXXXXXXXX.
func tsQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func tsJSDoc(b *strings.Builder, indent, description string, constraints *Constraints) {
	var lines []string
	if description = strings.TrimSpace(description); description != "" {
		lines = strings.Split(strings.ReplaceAll(description, "\r\n", "\n"), "\n")
	}
	if constraints != nil {
		if constraints.Minimum != "" {
			lines = append(lines, "@minimum "+string(constraints.Minimum))
		}
		if constraints.Maximum != "" {
			lines = append(lines, "@maximum "+string(constraints.Maximum))
		}
		if constraints.MinLength != nil {
			lines = append(lines, fmt.Sprintf("@minLength %d", *constraints.MinLength))
		}
		if constraints.MaxLength != nil {
			lines = append(lines, fmt.Sprintf("@maxLength %d", *constraints.MaxLength))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(b, indent+"/**")
	for _, line := range lines {
		fmt.Fprintln(b, indent+" * "+strings.ReplaceAll(line, "*/", "* /"))
	}
	fmt.Fprintln(b, indent+" */")
}

func tsFields(b *strings.Builder, fields []Field) {
	for _, field := range fields {
		tsJSDoc(b, "  ", field.Description, field.Constraints)
		optional := ""
		if field.Type.Kind == "ptr" {
			optional = "?"
		}
		fmt.Fprintf(b, "  readonly %s%s: %s;\n", tsCamel(field.Name), optional, tsType(field.Type))
	}
}

func tsRequired(fields []Field) bool {
	for _, field := range fields {
		if field.Type.Kind != "ptr" {
			return true
		}
	}
	return false
}

// GenerateTypeScript emits ESM bindings with concrete readonly models, exact
// int64 bigint types, lazy instance/module clients, and separate runtime options.
// The matching package runtime is gobridge-runtime (Node.js 24 and newer).
func (r *Registry) GenerateTypeScript(w io.Writer, class, binary string) error {
	if !regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`).MatchString(class) || typescriptReserved[class] {
		return fmt.Errorf("class must be a TypeScript class identifier")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(binary) {
		return fmt.Errorf("binary must be a filename stem")
	}
	reserved := map[string]bool{"schema": true, "configure": true, "session": true, "shutdown": true, "Promise": true, "Record": true}
	if reserved[class] {
		return fmt.Errorf("class %s conflicts with generated TypeScript symbols", class)
	}
	optionsName := class + "Options"
	reserved[class], reserved[optionsName] = true, true
	schema := r.Schema()
	types := map[string]Type{}
	var visit func(Type) error
	visit = func(t Type) error {
		if t.Elem != nil {
			return visit(*t.Elem)
		}
		if t.Kind != "struct" {
			return nil
		}
		if !typescriptIdentifier.MatchString(t.Name) || typescriptReserved[t.Name] || reserved[t.Name] || strings.HasPrefix(t.Name, "_bridge") {
			return fmt.Errorf("type %q conflicts with generated TypeScript symbols or is not an identifier", t.Name)
		}
		if previous, ok := types[t.Name]; ok {
			if !reflect.DeepEqual(previous, t) {
				return fmt.Errorf("conflicting TypeScript type name %s", t.Name)
			}
			return nil
		}
		types[t.Name] = t
		fields := map[string]string{}
		for _, field := range t.Fields {
			name := tsCamel(field.Name)
			if !typescriptIdentifier.MatchString(name) || name == "constructor" || name == "prototype" || name == "__proto__" || name == "_runtime" {
				return fmt.Errorf("field %s.%s maps to reserved TypeScript property %q", t.Name, field.Name, name)
			}
			if previous, ok := fields[name]; ok {
				return fmt.Errorf("fields %s.%s and %s map to the same TypeScript property %s", t.Name, previous, field.Name, name)
			}
			fields[name] = field.Name
			if err := visit(field.Type); err != nil {
				return err
			}
		}
		return nil
	}
	if schema.Constructor != nil {
		if err := visit(*schema.Constructor); err != nil {
			return err
		}
	}
	operations := map[string]string{}
	for _, op := range schema.Operations {
		name := tsCamel(op.Name)
		if !typescriptIdentifier.MatchString(name) || typescriptReserved[name] || typescriptMethodReserved[name] || reserved[name] || strings.HasPrefix(name, "_bridge") {
			return fmt.Errorf("operation %q maps to reserved TypeScript method %q", op.Name, name)
		}
		if previous, ok := operations[name]; ok {
			return fmt.Errorf("operations %s and %s map to the same TypeScript method %s", previous, op.Name, name)
		}
		operations[name] = op.Name
		if err := visit(op.Input); err != nil {
			return err
		}
		if err := visit(op.Output); err != nil {
			return err
		}
	}
	for name := range operations {
		if _, ok := types[name]; ok {
			return fmt.Errorf("operation %s conflicts with generated TypeScript type", name)
		}
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintln(&b, "// Generated by gobridge; DO NOT EDIT.")
	fmt.Fprintln(&b, "import { Client as _bridgeClient, DefaultControl as _bridgeDefaultControl, resolveBinary as _bridgeResolveBinary, parseSchema as _bridgeParseSchema, encode as _bridgeEncode, decode as _bridgeDecode } from \"gobridge-runtime\";")
	fmt.Fprintln(&b, "import type { CallOptions as _bridgeCallOptions, RuntimeOptions as _bridgeRuntimeOptions, Schema as _bridgeSchema } from \"gobridge-runtime\";")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "/** Shared schema, parsed from JSON text to preserve exact int64 constraint literals. */")
	fmt.Fprintf(&b, "export const schema: _bridgeSchema = _bridgeParseSchema(%s);\n\n", tsQuote(string(data)))
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "export interface %s {\n", name)
		tsFields(&b, types[name].Fields)
		fmt.Fprint(&b, "}\n\n")
	}
	fmt.Fprintf(&b, "export interface %s {\n", optionsName)
	if schema.Constructor != nil {
		tsFields(&b, schema.Constructor.Fields)
	}
	fmt.Fprintln(&b, "  /** Optional transport settings; ordinary use resolves the bundled executable. */")
	fmt.Fprint(&b, "  readonly _runtime?: _bridgeRuntimeOptions;\n}\n\n")
	for index := range schema.Operations {
		fmt.Fprintf(&b, "const _bridgeOp%d = schema.operations[%d]!;\n", index, index)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "export class %s extends _bridgeClient {\n", class)
	defaultOptions := " = {}"
	if schema.Constructor != nil && tsRequired(schema.Constructor.Fields) {
		defaultOptions = ""
	}
	fmt.Fprintf(&b, "  constructor(options: %s%s) {\n", optionsName, defaultOptions)
	fmt.Fprintln(&b, "    const { _runtime, ..._bridgeConfig } = options;\n    const _bridgeOptions = _runtime ?? {};")
	if schema.Constructor == nil {
		fmt.Fprintf(&b, "    _bridgeEncode({ kind: \"struct\", name: %s, fields: [] }, _bridgeConfig);\n", tsQuote(optionsName))
	}
	fmt.Fprintf(&b, "    super(_bridgeOptions.command ?? _bridgeResolveBinary(import.meta.url, %s), {\n", tsQuote(binary))
	fmt.Fprintln(&b, "      ..._bridgeOptions,\n      expectedSchema: schema.schema_hash,")
	if schema.Constructor != nil {
		fmt.Fprintln(&b, "      init: _bridgeEncode(schema.constructor!, _bridgeConfig),")
	}
	fmt.Fprint(&b, "    });\n  }\n\n")
	for index, op := range schema.Operations {
		tsJSDoc(&b, "  ", op.Description, nil)
		params := "options?: _bridgeCallOptions"
		input := "{}"
		if len(op.Input.Fields) > 0 {
			params = "params: " + op.Input.Name + ", " + params
			input = "params"
		}
		fmt.Fprintf(&b, "  async %s(%s): Promise<%s> {\n", tsCamel(op.Name), params, tsType(op.Output))
		fmt.Fprintf(&b, "    const _bridgeResult = await super.call(%s, _bridgeEncode(_bridgeOp%d.input, %s), options);\n", tsQuote(op.Name), index, input)
		fmt.Fprintf(&b, "    return _bridgeDecode<%s>(_bridgeOp%d.output, _bridgeResult);\n  }\n\n", tsType(op.Output), index)
	}
	fmt.Fprint(&b, "}\n\n")
	fmt.Fprintf(&b, "const _bridgeDefaults = new _bridgeDefaultControl<%s, %s>((options) => new %s(options));\n\n", optionsName, class, class)
	fmt.Fprintf(&b, "export function configure(options: %s): void {\n  _bridgeDefaults.configure(options);\n}\n\n", optionsName)
	fmt.Fprintf(&b, "export function session<R>(options: %s, callback: (client: %s) => R | Promise<R>): Promise<R> {\n  return _bridgeDefaults.scope(options, callback);\n}\n\n", optionsName, class)
	fmt.Fprint(&b, "export function shutdown(): Promise<void> {\n  return _bridgeDefaults.close();\n}\n\n")
	for _, op := range schema.Operations {
		tsJSDoc(&b, "", op.Description, nil)
		params := "options?: _bridgeCallOptions"
		args := "options"
		if len(op.Input.Fields) > 0 {
			params = "params: " + op.Input.Name + ", " + params
			args = "params, options"
		}
		fmt.Fprintf(&b, "export function %s(%s): Promise<%s> {\n  return _bridgeDefaults.client().%s(%s);\n}\n\n", tsCamel(op.Name), params, tsType(op.Output), tsCamel(op.Name), args)
	}
	_, err = io.WriteString(w, b.String())
	return err
}
