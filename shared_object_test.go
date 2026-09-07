package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

type sharedConfig struct {
	Name string `json:"name"`
}
type sharedReceiver struct {
	name  string
	calls int
}

func (r *sharedReceiver) Read() string { r.calls++; return fmt.Sprintf("%s:%d", r.name, r.calls) }

func TestSharedObjectConcurrentRequests(t *testing.T) {
	r := New()
	var constructed atomic.Int64
	object, err := NewSharedObject(r, func(ctx context.Context, config sharedConfig) (*sharedReceiver, error) {
		constructed.Add(1)
		return &sharedReceiver{name: config.Name}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := object.Bind("read", (*sharedReceiver).Read); err != nil {
		t.Fatal(err)
	}
	if r.NeedsInit() {
		t.Fatal("shared objects must not require initialization")
	}
	if err := r.Initialize(context.Background(), nil); err == nil {
		t.Fatal("shared objects must reject process initialization")
	}
	s := r.Schema()
	if s.Constructor != nil || s.SharedConstructor == nil || !s.Operations[0].Shared {
		t.Fatalf("incorrect shared schema: %+v", s)
	}
	for _, compact := range []bool{false, true} {
		hello, err := r.hello(json.RawMessage(fmt.Sprintf(`{"compact":%t}`, compact)))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(hello)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if fields["constructor"] != nil {
			t.Fatal("hello advertises process-owned constructor")
		}
	}
	if constructed.Load() != 0 {
		t.Fatal("schema generation ran constructor")
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw := json.RawMessage(fmt.Sprintf(`{"config":{"name":"%d"},"params":{}}`, i))
			result, err := r.Call(context.Background(), "read", raw)
			if err != nil || result != fmt.Sprintf("%d:1", i) {
				t.Errorf("result %v, err %v", result, err)
			}
		}(i)
	}
	wg.Wait()
	if constructed.Load() != 64 {
		t.Fatalf("constructed %d receivers", constructed.Load())
	}
	for _, raw := range []string{`{}`, `null`, `{"config":{},"params":{}}`, `{"config":{"name":3},"params":{}}`, `{"config":{"name":"a"},"params":{"unknown":1}}`, `{"config":{"name":"a"},"params":{},"extra":1}`, `{"config":{"name":"a"},"params":{}} {}`} {
		if _, err := r.Call(context.Background(), "read", json.RawMessage(raw)); wireError(err).Code != "invalid_argument" {
			t.Errorf("%s: %v", raw, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Call(ctx, "read", json.RawMessage(`{"config":{"name":"a"},"params":{}}`)); err != context.Canceled {
		t.Fatal(err)
	}
	if constructed.Load() != 64 {
		t.Fatal("invalid or cancelled input ran constructor")
	}
}

func TestSharedConstructorOptionsAndEmptyConfig(t *testing.T) {
	type option func(*sharedReceiver)
	for _, functional := range []bool{false, true} {
		r := New()
		var o *Object
		var err error
		if functional {
			o, err = NewSharedObject(r, func(options ...option) *sharedReceiver {
				receiver := &sharedReceiver{name: "default"}
				for _, option := range options {
					option(receiver)
				}
				return receiver
			}, ConstructorOption("name", func(name string) option { return func(r *sharedReceiver) { r.name = name } }))
		} else {
			o, err = NewSharedObject(r, func() *sharedReceiver { return &sharedReceiver{name: "default"} })
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Bind("read", (*sharedReceiver).Read); err != nil {
			t.Fatal(err)
		}
		raw := `{"config":{},"params":{}}`
		want := "default:1"
		if functional {
			raw = `{"config":{"name":"option"},"params":{}}`
			want = "option:1"
		}
		got, err := r.Call(context.Background(), "read", json.RawMessage(raw))
		if err != nil || got != want {
			t.Fatalf("got %v, %v", got, err)
		}
		var py, ts bytes.Buffer
		if err := r.GeneratePython(&py, "Shared", "shared"); err != nil {
			t.Fatal(err)
		}
		if err := r.GenerateTypeScript(&ts, "Shared", "shared"); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(py.Bytes(), []byte("class Shared:")) || !bytes.Contains(ts.Bytes(), []byte("class Shared {")) {
			t.Fatal("missing lightweight classes")
		}
	}
}
