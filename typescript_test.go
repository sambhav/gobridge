package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type tsRecord struct {
	ProcessID int   `json:"process_id"`
	Calls     int64 `json:"calls" doc:"Exact count; closing text */ is escaped." validate:"min=9007199254740993,max=9223372036854775807"`
}

type tsPayload struct {
	TotalCount       int64               `json:"total_count"`
	OptionalCount    *int64              `json:"optional_count,omitempty"`
	SmallNumber      int32               `json:"small_number"`
	Ready            bool                `json:"ready"`
	Ratio            float64             `json:"ratio"`
	Children         []tsRecord          `json:"children"`
	NullableChildren []*tsRecord         `json:"nullable_children"`
	Lookup           map[string]tsRecord `json:"lookup"`
	Nested           [][]int             `json:"nested"`
}

func TestTypeScriptGeneratedPublicAPIAndExactSchema(t *testing.T) {
	type options struct {
		Prefix *string `json:"prefix,omitempty" doc:"Greeting prefix." validate:"maxlen=80"`
	}
	r := New()
	constructors := 0
	if _, err := NewObject(r, func(options) *boundCounter { constructors++; return &boundCounter{} }); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		Bind(r, "echo_values", func(request tsPayload) tsPayload { return request }, "request"),
		Bind(r, "count_value", func(value int64) int64 { return value }, "total_count"),
		Bind(r, "nothing", func() {}),
		Bind(r, "stats", func() tsRecord { return tsRecord{} }),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Describe("stats", "Return status.\nDo not close */ early.\a"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := r.GenerateTypeScript(&out, "Example", "example"); err != nil {
		t.Fatal(err)
	}
	if constructors != 0 {
		t.Fatal("TypeScript generation invoked constructor")
	}
	source := out.String()
	for _, want := range []string{
		`from "gobridge-runtime"`,
		"export interface ExampleOptions {",
		"readonly prefix?: string | null;",
		"@maxLength 80",
		"constructor(options: ExampleOptions = {})",
		"readonly totalCount: bigint;",
		"readonly optionalCount?: bigint | null;",
		"readonly smallNumber: number;",
		"readonly ready: boolean;",
		"readonly ratio: number;",
		"readonly children: readonly tsRecord[] | null;",
		"readonly nullableChildren: readonly (tsRecord | null)[] | null;",
		"readonly lookup: Record<string, tsRecord> | null;",
		"readonly nested: readonly (readonly number[] | null)[] | null;",
		"async echoValues(params: EchoValuesParams, options?: _bridgeCallOptions): Promise<tsPayload>",
		"async countValue(params: CountValueParams, options?: _bridgeCallOptions): Promise<bigint>",
		"async nothing(options?: _bridgeCallOptions): Promise<void>",
		"async stats(options?: _bridgeCallOptions): Promise<tsRecord>",
		"export function countValue(params: CountValueParams, options?: _bridgeCallOptions): Promise<bigint>",
		"return _bridgeDefaults.client().countValue(params, options);",
		`super.call("count_value",`,
		"init: _bridgeEncode(schema.constructor!, _bridgeConfig)",
		"expectedSchema: schema.schema_hash",
		`_bridgeResolveBinary(import.meta.url, "example")`,
		"@minimum 9007199254740993",
		"@maximum 9223372036854775807",
		" * Do not close * / early.",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated public API missing %q", want)
		}
	}
	const prefix = "export const schema: _bridgeSchema = _bridgeParseSchema("
	var literal string
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, prefix) {
			literal = strings.TrimSuffix(strings.TrimPrefix(line, prefix), ");")
		}
	}
	if literal == "" {
		t.Fatal("schema not embedded as a quoted JSON string")
	}
	var schemaJSON string
	if err := json.Unmarshal([]byte(literal), &schemaJSON); err != nil {
		t.Fatalf("schema string is not JSON/JavaScript-safe: %v", err)
	}
	expected, err := json.Marshal(r.Schema())
	if err != nil {
		t.Fatal(err)
	}
	if schemaJSON != string(expected) {
		t.Fatal("schema text changed, rounded bounds, or lost string escapes")
	}
}

func TestTypeScriptRequiredAndStatelessConstructors(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		type options struct {
			Initial int `json:"initial"`
		}
		r := New()
		if _, err := NewObject(r, func(options) *boundCounter { return &boundCounter{} }); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := r.GenerateTypeScript(&out, "Example", "example"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "constructor(options: ExampleOptions) {") {
			t.Fatal("required domain options became optional")
		}
	})
	t.Run("no_constructor", func(t *testing.T) {
		var out bytes.Buffer
		if err := New().GenerateTypeScript(&out, "Example", "example"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "constructor(options: ExampleOptions = {})") {
			t.Fatal("stateless constructor requires options")
		}
		if strings.Contains(out.String(), "schema.constructor") || strings.Contains(out.String(), "init:") {
			t.Fatal("stateless client generated constructor transport")
		}
	})
	t.Run("aliased_imports", func(t *testing.T) {
		var out bytes.Buffer
		if err := New().GenerateTypeScript(&out, "CallOptions", "example"); err != nil {
			t.Fatalf("private import aliases should permit a CallOptions client: %v", err)
		}
	})
}

func TestTypeScriptGenerationRejectsNamingCollisions(t *testing.T) {
	t.Run("operations", func(t *testing.T) {
		r := New()
		for _, name := range []string{"foo_bar", "foo__bar"} {
			if err := Bind(r, name, func() {}); err != nil {
				t.Fatal(err)
			}
		}
		var out bytes.Buffer
		if err := r.GenerateTypeScript(&out, "Example", "example"); err == nil || !strings.Contains(err.Error(), "same TypeScript method") {
			t.Fatalf("accepted camelCase collision: %v", err)
		}
		if out.Len() != 0 {
			t.Fatal("wrote partial output before validation")
		}
	})
	t.Run("properties", func(t *testing.T) {
		type collision struct {
			First  int `json:"field_name"`
			Second int `json:"field__name"`
		}
		r := New()
		if err := Bind(r, "example", func(value collision) collision { return value }, "value"); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := r.GenerateTypeScript(&out, "Example", "example"); err == nil || !strings.Contains(err.Error(), "same TypeScript property") {
			t.Fatalf("accepted field camelCase collision: %v", err)
		}
	})
	t.Run("lifecycle", func(t *testing.T) {
		for _, name := range []string{"constructor", "then", "to_string", "value_of", "to_locale_string", "has_own_property", "schema_", "let", "new"} {
			r := New()
			if err := Bind(r, name, func() {}); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := r.GenerateTypeScript(&out, "Example", "example"); err == nil {
				t.Fatalf("accepted reserved TypeScript operation %s", name)
			}
		}
	})
	t.Run("constructor_property", func(t *testing.T) {
		type options struct {
			Constructor string `json:"constructor"`
		}
		r := New()
		if _, err := NewObject(r, func(options) *boundCounter { return &boundCounter{} }); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := r.GenerateTypeScript(&out, "Example", "example"); err == nil {
			t.Fatal("accepted reserved constructor property")
		}
	})
	t.Run("types", func(t *testing.T) {
		type Record struct{}
		type Promise struct{}
		type Example struct{}
		type ExampleOptions struct{}
		type enum struct{}
		for _, value := range []any{Record{}, Promise{}, Example{}, ExampleOptions{}, enum{}} {
			typ := reflect.TypeOf(value)
			fn := reflect.MakeFunc(reflect.FuncOf(nil, []reflect.Type{typ}, false), func([]reflect.Value) []reflect.Value { return []reflect.Value{reflect.Zero(typ)} }).Interface()
			r := New()
			if err := Bind(r, "example", fn); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := r.GenerateTypeScript(&out, "Example", "example"); err == nil {
				t.Fatalf("accepted type collision %s", typ.Name())
			}
		}
	})
	t.Run("class_and_binary", func(t *testing.T) {
		for _, class := range []string{"Record", "Promise", "bad-name", "bad_name", ""} {
			var out bytes.Buffer
			if err := New().GenerateTypeScript(&out, class, "example"); err == nil {
				t.Fatalf("accepted class %q", class)
			}
		}
		for _, binary := range []string{"../example", "example.exe;run", ""} {
			var out bytes.Buffer
			if err := New().GenerateTypeScript(&out, "Example", binary); err == nil {
				t.Fatalf("accepted binary %q", binary)
			}
		}
	})
}

func TestTypeScriptDeterministicAcrossRegistrationOrder(t *testing.T) {
	one, two := New(), New()
	for _, name := range []string{"hello_world", "goodbye"} {
		if err := Bind(one, name, func(name string) string { return name }, "user_name"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"goodbye", "hello_world"} {
		if err := Bind(two, name, func(name string) string { return name }, "user_name"); err != nil {
			t.Fatal(err)
		}
	}
	var a, b bytes.Buffer
	if err := one.GenerateTypeScript(&a, "Example", "example"); err != nil {
		t.Fatal(err)
	}
	if err := two.GenerateTypeScript(&b, "Example", "example"); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("registration order changed generated TypeScript")
	}
}

func TestCLIGenerateTypeScript(t *testing.T) {
	r := New()
	if err := Bind(r, "hello", func(name string) string { return name }, "name"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := r.Run(context.Background(), []string{"generate-typescript", "--class", "Greeting", "--binary", "greeting"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "export class Greeting extends _bridgeClient") {
		t.Fatal("CLI did not generate requested TypeScript class")
	}
	for _, args := range [][]string{{"generate-typescript", "extra"}, {"--config", "{}", "generate-typescript"}} {
		if err := r.Run(context.Background(), args, strings.NewReader(""), &out, &stderr); err == nil {
			t.Fatalf("accepted invalid generator args %v", args)
		}
	}
	out.Reset()
	if err := r.Run(context.Background(), []string{"help"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "generate-typescript") {
		t.Fatal("root help omits TypeScript generation")
	}
}
