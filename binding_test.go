package gobridge

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBindOrdinaryFunctions(t *testing.T) {
	cases := []struct {
		name   string
		fn     any
		params []string
		raw    string
		want   any
		kind   string
	}{
		{"add", func(a, b int) int { return a + b }, []string{"left", "right"}, `{"left":2,"right":3}`, 5, "int"},
		{"greet", func(name string) string { return "hello " + name }, []string{"name"}, `{"name":"world"}`, "hello world", "string"},
		{"ready", func() bool { return false }, nil, `{}`, false, "bool"},
		{"ratio", func(v float64) float64 { return v / 2 }, []string{"value"}, `{"value":3}`, 1.5, "float64"},
		{"noop", func() {}, nil, `{}`, nil, "void"},
		{"error_only", func() error { return nil }, nil, `{}`, nil, "void"},
		{"with_error", func(v int) (int, error) { return v, nil }, []string{"value"}, `{"value":0}`, 0, "int"},
		{"items", func(v []int) []int { return v }, []string{"values"}, `{"values":[1,2]}`, []int{1, 2}, "slice"},
		{"labels", func(v map[string]int) map[string]int { return v }, []string{"values"}, `{"values":{"x":3}}`, map[string]int{"x": 3}, "map"},
		{"struct_value", func(v testInput) testInput { return v }, []string{"value"}, `{"value":{"text":"x","count":2}}`, testInput{"x", 2}, "struct"},
		{"optional", func(v *int) bool { return v == nil }, []string{"value"}, `{}`, true, "bool"},
		{"optional_null", func(v *int) bool { return v == nil }, []string{"value"}, `{"value":null}`, true, "bool"},
		{"optional_value", func(v *int) int { return *v }, []string{"value"}, `{"value":7}`, 7, "int"},
		{"null_slice", func(v []int) bool { return v == nil }, []string{"values"}, `{"values":null}`, true, "bool"},
		{"null_map", func(v map[string]int) bool { return v == nil }, []string{"values"}, `{"values":null}`, true, "bool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if err := Bind(r, tc.name, tc.fn, tc.params...); err != nil {
				t.Fatal(err)
			}
			got, err := r.Call(context.Background(), tc.name, []byte(tc.raw))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tc.want)
			}
			schema := r.Schema().Operations[0]
			if schema.Input.Kind != "struct" || !strings.HasSuffix(schema.Input.Name, "Params") || schema.Output.Kind != tc.kind {
				t.Fatalf("unexpected schema: %#v", schema)
			}
		})
	}
}

func TestBindRejectsInvalidSignatures(t *testing.T) {
	var nilFunc func(int) int
	cases := []struct {
		name   string
		fn     any
		params []string
	}{
		{"nil", nil, nil}, {"typed_nil", nilFunc, []string{"value"}}, {"not_func", 1, nil},
		{"variadic", func(...int) {}, []string{"values"}},
		{"missing_names", func(int) {}, nil}, {"extra_names", func() {}, []string{"value"}},
		{"duplicate_names", func(int, int) {}, []string{"value", "value"}},
		{"invalid_name", func(int) {}, []string{"value-name"}},
		{"keyword", func(int) {}, []string{"class"}},
		{"self", func(int) {}, []string{"self"}},
		{"shadow_builtin", func(*int) {}, []string{"int"}},
		{"context_later", func(int, context.Context) {}, []string{"value", "context"}},
		{"interface", func(any) {}, []string{"value"}},
		{"unsigned", func(uint) {}, []string{"value"}},
		{"bytes", func([]byte) {}, []string{"value"}},
		{"map_keys", func(map[int]string) {}, []string{"value"}},
		{"two_values", func() (int, string) { return 0, "" }, nil},
		{"three_results", func() (int, int, error) { return 0, 0, nil }, nil},
		{"error_first", func() (error, int) { return nil, 0 }, nil},
		{"two_errors", func() (error, error) { return nil, nil }, nil},
		{"interface_result", func() any { return 1 }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if err := Bind(r, "example", tc.fn, tc.params...); err == nil {
				t.Fatal("accepted invalid binding")
			}
			if len(r.ops) != 0 {
				t.Fatal("invalid registration mutated registry")
			}
		})
	}
	if err := Bind(nil, "add", func() {}); err == nil {
		t.Fatal("accepted nil registry")
	}
	var zero Registry
	if err := Bind(&zero, "add", func() {}); err != nil {
		t.Fatal(err)
	}
	if err := Bind(&zero, "add", func() {}); err == nil {
		t.Fatal("accepted duplicate")
	}
	for _, name := range []string{"Bad", "close", "lifecycle", "control", "aio", "schema", "class"} {
		if err := Bind(New(), name, func() {}); err == nil {
			t.Fatalf("accepted reserved name %s", name)
		}
	}
}

func TestBindStrictArguments(t *testing.T) {
	r := New()
	var calls int
	if err := Bind(r, "echo", func(value int, label *string) int { calls++; return value }, "value", "label"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `null`, `[]`, `{"value":null}`, `{"value":true}`, `{"value":1.5}`, `{"value":"1"}`, `{"value":1,"extra":2}`, `{"value":1,"label":3}`, `{"value":1,"LABEL":"alias"}`, `{"value":1} {}`} {
		_, err := r.Call(context.Background(), "echo", []byte(raw))
		if err == nil || wireError(err).Code != "invalid_argument" {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	if calls != 0 {
		t.Fatal("called handler with invalid input")
	}
}

func TestBindContextErrorsAndPanics(t *testing.T) {
	type key struct{}
	r := New()
	if err := Bind(r, "context_value", func(ctx context.Context) (string, error) { return ctx.Value(key{}).(string), nil }); err != nil {
		t.Fatal(err)
	}
	got, err := r.Call(context.WithValue(context.Background(), key{}, "ok"), "context_value", nil)
	if err != nil || got != "ok" {
		t.Fatalf("context lost: %v, %v", got, err)
	}
	if err := Bind(r, "fail", func() error { return Failure("invalid_argument", "test failure") }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Call(context.Background(), "fail", nil); err == nil || wireError(err).Code != "invalid_argument" {
		t.Fatalf("error lost: %v", err)
	}
	if err := Bind(r, "explode", func() int { panic("sensitive detail") }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Call(context.Background(), "explode", nil); err == nil || wireError(err).Message != "operation panicked" {
		t.Fatalf("panic leaked: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Call(ctx, "context_value", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

type boundCounter struct{ value atomic.Int64 }

func (c *boundCounter) Add(amount int64) int64 { return c.value.Add(amount) }

func TestBindBoundMethodConcurrency(t *testing.T) {
	r := New()
	counter := &boundCounter{}
	if err := Bind(r, "add", counter.Add, "amount"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Call(context.Background(), "add", []byte(`{"amount":1}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := counter.value.Load(); got != 32 {
		t.Fatalf("state not shared: %d", got)
	}
}

func TestBindCLIAndDescriptions(t *testing.T) {
	r := New()
	if err := Bind(r, "greet", func(name string, prefix *string) string {
		if prefix != nil {
			return *prefix + " " + name
		}
		return name
	}, "name", "prefix"); err != nil {
		t.Fatal(err)
	}
	if err := r.Describe("greet", "Greet someone."); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := r.Run(context.Background(), []string{"greet", "--name", "world", "--prefix", "hello"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "\"hello world\"\n" {
		t.Fatalf("unexpected CLI result: %s", out.String())
	}
	if got := r.Schema().Operations[0].Description; got != "Greet someone." {
		t.Fatal(got)
	}
	if err := r.Describe("missing", ""); err == nil {
		t.Fatal("accepted missing operation")
	}
}

func TestBindSchemaStableAcrossRegistrationOrder(t *testing.T) {
	one, two := New(), New()
	add := func(left, right int) int { return left + right }
	ready := func() bool { return true }
	for _, err := range []error{Bind(one, "add_values", add, "left", "right"), Bind(one, "ready", ready), Bind(two, "ready", ready), Bind(two, "add_values", add, "left", "right")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if one.Schema().Hash != two.Schema().Hash {
		t.Fatal("registration order changed hash")
	}
	op := one.Schema().Operations[0]
	if op.Input.Name != "AddValuesParams" || op.Input.Fields[0].Name != "left" || op.Input.Fields[1].Name != "right" {
		t.Fatalf("unexpected schema: %#v", op)
	}
}
