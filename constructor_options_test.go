package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type optionCounter struct{ value int }
type counterOption func(*optionCounter)

func (c *optionCounter) Value() int { return c.value }

func TestFunctionalConstructorDefaultsZeroAndErrors(t *testing.T) {
	for _, test := range []struct {
		json string
		want int
		fail bool
	}{{`{}`, 7, false}, {`{"initial":0}`, 0, false}, {`{"initial":9}`, 9, false}, {`{"initial":-1}`, 0, true}} {
		t.Run(test.json, func(t *testing.T) {
			called := false
			r := New()
			o, err := NewObject(r, func(ctx context.Context, options ...counterOption) *optionCounter {
				called = true
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				c := &optionCounter{7}
				for _, option := range options {
					option(c)
				}
				return c
			}, ConstructorOption("initial", func(value int) (counterOption, error) {
				if value < 0 {
					return nil, errors.New("negative initial")
				}
				return func(c *optionCounter) { c.value = value }, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err = o.Bind("value", (*optionCounter).Value); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err = r.GeneratePython(&out, "Counter", "counter"); err != nil {
				t.Fatal(err)
			}
			if called {
				t.Fatal("generation ran constructor")
			}
			err = r.Initialize(context.Background(), json.RawMessage(test.json))
			if test.fail {
				if err == nil || !strings.Contains(err.Error(), "negative initial") {
					t.Fatalf("error %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			value, err := r.Call(context.Background(), "value", nil)
			if err != nil || value != test.want {
				t.Fatalf("got %v, %v", value, err)
			}
		})
	}
}
func TestFunctionalConstructorRejectsInvalidFactories(t *testing.T) {
	ctor := func(...counterOption) *optionCounter { return &optionCounter{} }
	for _, factory := range []OptionFactory{ConstructorOption("initial", nil), ConstructorOption("initial", func(int) int { return 1 }), ConstructorOption("initial", func(int, int) counterOption { return nil }), ConstructorOption("command", func(int) counterOption { return nil })} {
		r := New()
		if _, err := NewObject(r, ctor, factory); err == nil {
			t.Fatal("accepted invalid factory")
		}
		if r.constructor != nil {
			t.Fatal("failed registration mutated registry")
		}
	}
}

func TestGroupedConstructorOptions(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int
		fail  bool
	}{
		{`{}`, 7, false}, {`{"retry":{"attempts":0,"delay_ms":10}}`, 10, false},
		{`{"retry":{"attempts":2,"delay_ms":10}}`, 12, false},
		{`{"retry":{"attempts":2}}`, 0, true}, {`{"retry":{"attempts":null,"delay_ms":10}}`, 0, true},
		{`{"retry":{"attempts":-1,"delay_ms":10}}`, 0, true},
		{`{"retry":{"attempts":2,"delay_ms":10,"extra":0}}`, 0, true},
	} {
		t.Run(test.input, func(t *testing.T) {
			r := New()
			calls := 0
			o, err := NewObject(r, func(options ...counterOption) *optionCounter {
				c := &optionCounter{7}
				for _, option := range options {
					option(c)
				}
				return c
			}, ConstructorOption("retry", func(attempts, delay int) (counterOption, error) {
				calls++
				if attempts < 0 {
					return nil, errors.New("negative attempts")
				}
				return func(c *optionCounter) { c.value = attempts + delay }, nil
			}, "attempts", "delay_ms"))
			if err != nil {
				t.Fatal(err)
			}
			if err = o.Bind("value", (*optionCounter).Value); err != nil {
				t.Fatal(err)
			}
			schema := r.Schema()
			group := schema.Constructor.Fields[0].Type.Elem
			if group.Name != "RetryOptions" || len(group.Fields) != 2 {
				t.Fatalf("bad group %+v", group)
			}
			var py, ts bytes.Buffer
			if err = r.GeneratePython(&py, "Example", "example"); err != nil {
				t.Fatal(err)
			}
			if err = r.GenerateTypeScript(&ts, "Example", "example"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(py.String(), "retry: RetryOptions | None = None") || !strings.Contains(ts.String(), "readonly delayMs: number") {
				t.Fatal("missing typed grouped API")
			}
			if calls != 0 {
				t.Fatal("generation called factory")
			}
			err = r.Initialize(context.Background(), json.RawMessage(test.input))
			if test.fail {
				if err == nil {
					t.Fatal("accepted invalid group")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			value, err := r.Call(context.Background(), "value", nil)
			if err != nil || value != test.want {
				t.Fatalf("got %v, %v", value, err)
			}
		})
	}
}

func TestGroupedConstructorParameterNames(t *testing.T) {
	for _, names := range [][]string{nil, {"only"}, {"same", "same"}, {"self", "delay"}, {"attempts", "bad-name"}} {
		r := New()
		_, err := NewObject(r, func(...counterOption) *optionCounter { return nil }, ConstructorOption("retry", func(int, int) counterOption { return nil }, names...))
		if err == nil {
			t.Fatalf("accepted names %v", names)
		}
	}
}
