package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type testInput struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}
type testOutput struct {
	Value string `json:"value"`
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r := New()
	err := Register(r, "repeat", "Repeat text.", func(ctx context.Context, in testInput) (testOutput, error) {
		if in.Count < 0 {
			return testOutput{}, Failure("invalid_argument", "negative count")
		}
		return testOutput{strings.Repeat(in.Text, in.Count)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestRegistryValidationAndCLI(t *testing.T) {
	r := testRegistry(t)
	for _, raw := range []string{`{}`, `{"text":null,"count":1}`, `{"text":"a","count":1,"unknown":2}`, `{"text":"a","count":1.5}`, `{"text":"a","count":null}`, `null`, `{"text":"a","count":1} {}`} {
		_, err := r.Call(context.Background(), "repeat", json.RawMessage(raw))
		if err == nil || wireError(err).Code != "invalid_argument" {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	var out bytes.Buffer
	if err := r.Run(context.Background(), []string{"repeat", "--text", "hi", "--count", "2"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\"value\":\"hihi\"}\n" {
		t.Fatal(out.String())
	}
}
func TestPanicIsolation(t *testing.T) {
	r := New()
	_ = Register(r, "panic_op", "", func(context.Context, testInput) (testOutput, error) { panic("private details") })
	_, err := r.Call(context.Background(), "panic_op", []byte(`{"text":"a","count":1}`))
	if wireError(err).Message != "operation panicked" {
		t.Fatal(err)
	}
}
