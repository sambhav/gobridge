package gobridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

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
