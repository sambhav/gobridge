package gobridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestCompactHelloPreservesIdentityWithoutConstructing(t *testing.T) {
	object, calls := newTestObject(t)
	for _, registry := range []*Registry{New(), object} {
		full, err := registry.hello(json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		compact, err := registry.hello(json.RawMessage(`{"compact":true}`))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(compact)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		var hash string
		if err := json.Unmarshal(fields["schema_hash"], &hash); err != nil {
			t.Fatal(err)
		}
		schema := full.(Schema)
		if hash != schema.Hash || string(fields["protocol"]) != "1" || fields["operations"] != nil {
			t.Fatalf("bad compact hello: %s", data)
		}
		if (fields["constructor"] != nil) != (schema.Constructor != nil) {
			t.Fatalf("lost constructor presence: %s", data)
		}
	}
	if calls.Load() != 0 || !object.NeedsInit() {
		t.Fatal("hello ran constructor")
	}
}

func TestDirectResponseEncodingMatchesJSONEnvelope(t *testing.T) {
	for _, response := range []Response{
		{ID: "1", Result: nil}, {ID: "<&\"", Result: map[string]any{"text": "<&\u2028", "nil": nil}},
		{ID: "2", Error: &Error{"invalid_argument", "bad input"}},
	} {
		want, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		got, err := response.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s != %s", got, want)
		}
	}
}

func TestServerCancellationBusyAndEOF(t *testing.T) {
	r := New()
	started, ended := make(chan struct{}, 4), make(chan struct{}, 4)
	_ = Register(r, "wait", "", func(ctx context.Context, in testInput) (testOutput, error) {
		started <- struct{}{}
		<-ctx.Done()
		ended <- struct{}{}
		return testOutput{}, ctx.Err()
	})
	inW, inR := func() (*io.PipeWriter, *io.PipeReader) { r, w := io.Pipe(); return w, r }()
	outR, outW := io.Pipe()
	defer outR.Close()
	defer outW.Close()
	done := make(chan error, 1)
	go func() { done <- r.Serve(context.Background(), inR, outW, 1) }()
	enc := json.NewEncoder(inW)
	scan := bufio.NewScanner(outR)
	send := func(id, method string, params any) {
		t.Helper()
		if err := enc.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			t.Error(err)
		}
	}
	read := func() Response {
		t.Helper()
		if !scan.Scan() {
			t.Fatal(scan.Err())
		}
		var resp Response
		if err := json.Unmarshal(scan.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	send("a", "wait", testInput{"x", 1})
	<-started
	go send("b", "wait", testInput{"x", 1})
	if resp := read(); resp.Error.Code != "busy" {
		t.Fatal(resp)
	}
	go send("", "$cancel", map[string]string{"id": "a"})
	if resp := read(); resp.Error.Code != "cancelled" {
		t.Fatal(resp)
	}
	<-ended
	send("c", "wait", testInput{"x", 1})
	<-started
	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("EOF failed to stop server")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("EOF failed to cancel handler")
	}
}
