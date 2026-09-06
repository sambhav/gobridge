package gobridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type counterOptions struct {
	Initial int64   `json:"initial"`
	Label   *string `json:"label,omitempty"`
}

func newTestObject(t *testing.T) (*Registry, *atomic.Int64) {
	t.Helper()
	r := New()
	var constructors atomic.Int64
	object, err := NewObject(r, func(opts counterOptions) *boundCounter {
		constructors.Add(1)
		c := &boundCounter{}
		c.value.Store(opts.Initial)
		return c
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := object.Bind("add", (*boundCounter).Add, "amount"); err != nil {
		t.Fatal(err)
	}
	return r, &constructors
}

func TestObjectSchemaDoesNotConstruct(t *testing.T) {
	r, calls := newTestObject(t)
	schema := r.Schema()
	if calls.Load() != 0 || !r.NeedsInit() {
		t.Fatal("schema initialized receiver")
	}
	if schema.Constructor == nil || schema.Constructor.Name != "counterOptions" || len(schema.Constructor.Fields) != 2 {
		t.Fatalf("missing constructor schema: %#v", schema.Constructor)
	}
	if len(schema.Operations[0].Input.Fields) != 1 || schema.Operations[0].Input.Fields[0].Name != "amount" {
		t.Fatal("receiver leaked into method arguments")
	}
	if _, err := r.Call(context.Background(), "add", []byte(`{"amount":1}`)); err == nil || wireError(err).Code != "failed_precondition" {
		t.Fatalf("accepted uninitialized method: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("call implicitly initialized receiver")
	}
}

func TestObjectInitializationStateAndIsolation(t *testing.T) {
	r, calls := newTestObject(t)
	if err := r.Initialize(context.Background(), []byte(`{"initial":10}`)); err != nil {
		t.Fatal(err)
	}
	if r.NeedsInit() || calls.Load() != 1 {
		t.Fatal("initialization state wrong")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Call(context.Background(), "add", []byte(`{"amount":1}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := r.Call(context.Background(), "add", []byte(`{"amount":2}`))
	if err != nil || got != int64(32) {
		t.Fatalf("state lost: %v, %v", got, err)
	}
	if err := r.Initialize(context.Background(), []byte(`{"initial":0}`)); err == nil || wireError(err).Code != "failed_precondition" {
		t.Fatalf("allowed reinitialization: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("constructor ran twice")
	}
	other, _ := newTestObject(t)
	if err := other.Initialize(context.Background(), []byte(`{"initial":100}`)); err != nil {
		t.Fatal(err)
	}
	got, err = other.Call(context.Background(), "add", []byte(`{"amount":1}`))
	if err != nil || got != int64(101) {
		t.Fatalf("receiver shared across registries: %v, %v", got, err)
	}
}

func TestObjectConstructorFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fn     any
		config string
		code   string
	}{
		{"error", func(counterOptions) (*boundCounter, error) { return nil, Failure("invalid_argument", "bad config") }, `{"initial":1}`, "invalid_argument"},
		{"panic", func(counterOptions) *boundCounter { panic("secret") }, `{"initial":1}`, "internal"},
		{"nil", func(counterOptions) *boundCounter { return nil }, `{"initial":1}`, "internal"},
		{"missing_config", func(counterOptions) *boundCounter { return &boundCounter{} }, `{}`, "invalid_argument"},
		{"unknown_empty_config", func() *boundCounter { return &boundCounter{} }, `{ "extra": 2 }`, "invalid_argument"},
		{"unknown_config", func(counterOptions) *boundCounter { return &boundCounter{} }, `{"initial":1,"extra":2}`, "invalid_argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if _, err := NewObject(r, tc.fn); err != nil {
				t.Fatal(err)
			}
			err := r.Initialize(context.Background(), []byte(tc.config))
			if err == nil || wireError(err).Code != tc.code {
				t.Fatalf("unexpected failure: %v", err)
			}
			if !r.NeedsInit() {
				t.Fatal("failed ctor marked initialized")
			}
			if err := r.Initialize(context.Background(), []byte(`{"initial":1}`)); err == nil || wireError(err).Code != "failed_precondition" {
				t.Fatalf("failed ctor retried: %v", err)
			}
		})
	}
}

func TestObjectConstructorContextAndCancellation(t *testing.T) {
	type key struct{}
	r := New()
	_, err := NewObject(r, func(ctx context.Context, opts counterOptions) (*boundCounter, error) {
		if ctx.Value(key{}) != "marker" {
			return nil, errors.New("context lost")
		}
		return &boundCounter{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Initialize(ctx, []byte(`{"initial":0}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := r.Initialize(context.WithValue(context.Background(), key{}, "marker"), []byte(`{"initial":0}`)); err != nil {
		t.Fatal(err)
	}
}

func TestObjectRejectsInvalidConstructorsAndMethods(t *testing.T) {
	type badConfig struct {
		Command string `json:"command"`
	}
	type otherCounter struct{}
	for _, fn := range []any{nil, 3, func() {}, func(int) *boundCounter { return nil }, func(counterOptions) boundCounter { return boundCounter{} }, func(counterOptions) (*boundCounter, int) { return nil, 0 }, func(counterOptions) *struct{} { return nil }, func(badConfig) *boundCounter { return nil }} {
		r := New()
		if _, err := NewObject(r, fn); err == nil {
			t.Fatalf("accepted constructor %T", fn)
		}
		if r.constructor != nil {
			t.Fatal("invalid constructor mutated registry")
		}
	}
	r := New()
	obj, err := NewObject(r, func(counterOptions) *boundCounter { return &boundCounter{} })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewObject(r, func(counterOptions) *boundCounter { return &boundCounter{} }); err == nil {
		t.Fatal("accepted second constructor")
	}
	for _, method := range []any{nil, 3, func() {}, (&boundCounter{}).Add, func(*otherCounter, int64) int64 { return 0 }} {
		if err := obj.Bind("add", method, "amount"); err == nil {
			t.Fatalf("accepted method %T", method)
		}
	}
	if err := New().Initialize(context.Background(), nil); err == nil {
		t.Fatal("initialized without constructor")
	}
}

func TestObjectConfigAllowsApplicationRuntimeNames(t *testing.T) {
	type options struct {
		Timeout    int  `json:"timeout"`
		MaxPending *int `json:"max_pending,omitempty"`
	}
	if _, err := NewObject(New(), func(options) *boundCounter { return &boundCounter{} }); err != nil {
		t.Fatal(err)
	}
}

func TestConstructorChangesSchemaHashWithoutChangingLegacyHash(t *testing.T) {
	r := testRegistry(t)
	s := r.Schema()
	data, _ := json.Marshal(s.Operations)
	if s.Hash != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatal("no-constructor schema hash changed")
	}
	if _, err := NewObject(r, func(counterOptions) *boundCounter { return &boundCounter{} }); err != nil {
		t.Fatal(err)
	}
	if r.Schema().Hash == s.Hash {
		t.Fatal("constructor missing from schema hash")
	}
}

func TestObjectCLIConfig(t *testing.T) {
	r, calls := newTestObject(t)
	var out bytes.Buffer
	if err := r.Run(context.Background(), []string{"schema"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("schema CLI constructed receiver")
	}
	out.Reset()
	if err := r.Run(context.Background(), []string{"--config", `{"initial":10}`, "add", "--amount", "2"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "12\n" || calls.Load() != 1 {
		t.Fatalf("unexpected CLI result: %s", out.String())
	}
}

func TestObjectWithoutConfig(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, true)
	for _, withContext := range []bool{false, true} {
		r := New()
		called := 0
		var fn any = func() *boundCounter { called++; return &boundCounter{} }
		if withContext {
			fn = func(ctx context.Context) (*boundCounter, error) {
				if ctx.Value(contextKey{}) != true {
					t.Error("constructor lost context")
				}
				called++
				return &boundCounter{}, nil
			}
		}
		object, err := NewObject(r, fn)
		if err != nil {
			t.Fatal(err)
		}
		if err := object.Bind("add", (*boundCounter).Add, "amount"); err != nil {
			t.Fatal(err)
		}
		for _, generate := range []func() error{
			func() error { return r.GeneratePython(&bytes.Buffer{}, "Counter", "counter") },
			func() error { return r.GenerateTypeScript(&bytes.Buffer{}, "Counter", "counter") },
		} {
			if err := generate(); err != nil {
				t.Fatal(err)
			}
		}
		if called != 0 || r.Schema().Constructor == nil || len(r.Schema().Constructor.Fields) != 0 {
			t.Fatal("generation initialized constructor or exposed parameters")
		}
		if err := r.Initialize(ctx, nil); err != nil {
			t.Fatal(err)
		}
		value, err := r.Call(ctx, "add", []byte(`{"amount":2}`))
		if err != nil || value != int64(2) || called != 1 {
			t.Fatalf("result %v, error %v, constructors %d", value, err, called)
		}
	}
}
