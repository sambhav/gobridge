package gobridge

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise real framing over independent pipes, keeping stdout draining so a
// stalled assertion cannot leave a daemon goroutine blocked on its next reply.
type objectServerSession struct {
	t          *testing.T
	encoder    *json.Encoder
	responses  <-chan map[string]json.RawMessage
	readErrors <-chan error
}

func startObjectServer(t *testing.T, r *Registry) *objectServerSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	responses := make(chan map[string]json.RawMessage, 32)
	readErrors := make(chan error, 1)
	go func() { done <- r.Serve(ctx, inR, outW, 8) }()
	go func() {
		decoder := json.NewDecoder(outR)
		for {
			var response map[string]json.RawMessage
			if err := decoder.Decode(&response); err != nil {
				readErrors <- err
				return
			}
			select {
			case responses <- response:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil && err != context.Canceled {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not stop after EOF")
		}
		_ = outW.Close()
		_ = outR.Close()
		_ = inR.Close()
	})
	return &objectServerSession{t: t, encoder: json.NewEncoder(inW), responses: responses, readErrors: readErrors}
}

func (s *objectServerSession) send(req request) {
	s.t.Helper()
	if err := s.encoder.Encode(req); err != nil {
		s.t.Fatalf("send %s: %v", req.Method, err)
	}
}

func (s *objectServerSession) read(id string) map[string]json.RawMessage {
	s.t.Helper()
	select {
	case response := <-s.responses:
		var gotID string
		if err := json.Unmarshal(response["id"], &gotID); err != nil || gotID != id {
			s.t.Fatalf("response ID: got %q, %v; want %q", gotID, err, id)
		}
		return response
	case err := <-s.readErrors:
		s.t.Fatalf("read response: %v", err)
	case <-time.After(2 * time.Second):
		s.t.Fatalf("timed out reading %s", id)
	}
	return nil
}

func requireWireSuccess(t *testing.T, response map[string]json.RawMessage, want string) {
	t.Helper()
	if _, ok := response["error"]; ok {
		t.Fatalf("unexpected error envelope: %s", response["error"])
	}
	result, ok := response["result"]
	if !ok || string(result) != want {
		t.Fatalf("result present=%v value=%s, want %s", ok, result, want)
	}
}

func requireWireError(t *testing.T, response map[string]json.RawMessage, code string) {
	t.Helper()
	if _, ok := response["result"]; ok {
		t.Fatalf("error envelope contains result: %s", response["result"])
	}
	var failure Error
	if err := json.Unmarshal(response["error"], &failure); err != nil || failure.Code != code {
		t.Fatalf("error envelope: got %#v, %v; want %s", failure, err, code)
	}
}

func TestServerObjectHandshakeAndInitializeOnce(t *testing.T) {
	r, calls := newTestObject(t)
	s := startObjectServer(t, r)
	s.send(request{ID: "hello", Method: "$hello"})
	response := s.read("hello")
	var schema Schema
	if err := json.Unmarshal(response["result"], &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Constructor == nil || schema.Constructor.Name != "counterOptions" || len(schema.Constructor.Fields) != 2 {
		t.Fatalf("missing constructor metadata: %#v", schema)
	}
	if calls.Load() != 0 {
		t.Fatal("hello invoked constructor")
	}
	s.send(request{ID: "before", Method: "add", Params: json.RawMessage(`{"amount":1}`)})
	requireWireError(t, s.read("before"), "failed_precondition")
	s.send(request{ID: "init", Method: "$init", Params: json.RawMessage(`{"initial":10}`)})
	requireWireSuccess(t, s.read("init"), "null")
	if calls.Load() != 1 {
		t.Fatal("constructor did not run once")
	}
	s.send(request{ID: "add", Method: "add", Params: json.RawMessage(`{"amount":2}`)})
	requireWireSuccess(t, s.read("add"), "12")
	s.send(request{ID: "again", Method: "$init", Params: json.RawMessage(`{"initial":100}`)})
	requireWireError(t, s.read("again"), "failed_precondition")
	s.send(request{ID: "unchanged", Method: "add", Params: json.RawMessage(`{"amount":1}`)})
	requireWireSuccess(t, s.read("unchanged"), "13")
	if calls.Load() != 1 {
		t.Fatal("repeated init reconstructed receiver")
	}
	s.send(request{ID: "hello_again", Method: "$hello"})
	response = s.read("hello_again")
	var after Schema
	if err := json.Unmarshal(response["result"], &after); err != nil || after.Hash != schema.Hash {
		t.Fatalf("initialization changed schema: %v", err)
	}
}

func TestServerConstructorFailuresAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name        string
		constructor any
		params      string
		timeout     int64
		code        string
	}{
		{"error", func(counterOptions) (*boundCounter, error) {
			return nil, Failure("invalid_argument", "configuration refused")
		}, `{"initial":1}`, 0, "invalid_argument"},
		{"panic", func(counterOptions) *boundCounter { panic("private details") }, `{"initial":1}`, 0, "internal"},
		{"bad_config", func(counterOptions) *boundCounter { return &boundCounter{} }, `{"initial":null}`, 0, "invalid_argument"},
		{"timeout", func(ctx context.Context, _ counterOptions) (*boundCounter, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, `{"initial":1}`, 20, "deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			object, err := NewObject(r, tc.constructor)
			if err != nil {
				t.Fatal(err)
			}
			if err := object.Bind("add", (*boundCounter).Add, "amount"); err != nil {
				t.Fatal(err)
			}
			s := startObjectServer(t, r)
			s.send(request{ID: "init", Method: "$init", Params: json.RawMessage(tc.params), TimeoutMS: tc.timeout})
			response := s.read("init")
			requireWireError(t, response, tc.code)
			if tc.name == "panic" {
				var failure Error
				_ = json.Unmarshal(response["error"], &failure)
				if failure.Message != "constructor panicked" {
					t.Fatalf("panic detail leaked: %s", failure.Message)
				}
			}
			s.send(request{ID: "add", Method: "add", Params: json.RawMessage(`{"amount":1}`)})
			requireWireError(t, s.read("add"), "failed_precondition")
			s.send(request{ID: "retry", Method: "$init", Params: json.RawMessage(`{"initial":0}`)})
			requireWireError(t, s.read("retry"), "failed_precondition")
		})
	}
}

func TestServerConstructorCancellation(t *testing.T) {
	r := New()
	started := make(chan struct{})
	var calls atomic.Int64
	_, err := NewObject(r, func(ctx context.Context, _ counterOptions) (*boundCounter, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	s := startObjectServer(t, r)
	s.send(request{ID: "init", Method: "$init", Params: json.RawMessage(`{"initial":0}`)})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("constructor did not start")
	}
	s.send(request{Method: "$cancel", Params: json.RawMessage(`{"id":"init"}`)})
	requireWireError(t, s.read("init"), "cancelled")
	if calls.Load() != 1 || !r.NeedsInit() {
		t.Fatal("cancelled constructor was marked successful")
	}
}

func TestServerSuccessfulScalarAndVoidEnvelopes(t *testing.T) {
	cases := []struct {
		name string
		fn   any
		want string
	}{
		{"zero", func() int { return 0 }, "0"},
		{"negative", func() int { return -3 }, "-3"},
		{"false_value", func() bool { return false }, "false"},
		{"empty_string", func() string { return "" }, `""`},
		{"text", func() string { return "hello" }, `"hello"`},
		{"float_value", func() float64 { return 1.5 }, "1.5"},
		{"void_value", func() {}, "null"},
		{"error_only", func() error { return nil }, "null"},
		{"nil_pointer", func() *int { return nil }, "null"},
		{"nil_slice", func() []int { return nil }, "null"},
		{"nil_map", func() map[string]int { return nil }, "null"},
		{"slice_value", func() []int { return []int{1, 2} }, "[1,2]"},
	}
	r := New()
	for _, tc := range cases {
		if err := Bind(r, tc.name, tc.fn); err != nil {
			t.Fatal(err)
		}
	}
	s := startObjectServer(t, r)
	for _, tc := range cases {
		s.send(request{ID: tc.name, Method: tc.name, Params: json.RawMessage(`{}`)})
		requireWireSuccess(t, s.read(tc.name), tc.want)
	}
}
