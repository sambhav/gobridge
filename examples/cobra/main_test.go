package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHostCommandsAndHelp(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"version"}, "host development\n"},
		{[]string{"status"}, "ready\n"},
		{[]string{"--help"}, "An existing CLI with an embedded library daemon"},
		{[]string{"bridge", "--help"}, "Private library integration"},
		{[]string{"bridge", "serve", "--help"}, "host bridge serve"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := execute(context.Background(), tt.args, io.NopCloser(strings.NewReader("")), &out, &errOut)
			if err != nil || !strings.Contains(out.String(), tt.want) || errOut.Len() != 0 {
				t.Fatalf("execute = %v, stdout=%q, stderr=%q", err, out.String(), errOut.String())
			}
			for _, unmounted := range []string{"generate-python", "welcome", "greet", "--prefix"} {
				if strings.Contains(out.String(), unmounted) {
					t.Errorf("host help exposed unmounted bridge API %q", unmounted)
				}
			}
		})
	}
}

func TestDaemonArgumentErrorsDoNotPrintProtocolNoise(t *testing.T) {
	for _, args := range [][]string{
		{"greet"}, {"bridge", "schema"}, {"bridge", "welcome"},
		{"bridge", "serve", "unexpected"}, {"bridge", "serve", "--unknown"},
	} {
		var out, errOut bytes.Buffer
		err := execute(context.Background(), args, io.NopCloser(strings.NewReader("")), &out, &errOut)
		if err == nil || out.Len() != 0 || errOut.Len() != 0 {
			t.Errorf("args=%v: error=%v stdout=%q stderr=%q", args, err, out.String(), errOut.String())
		}
	}
}

func TestNestedDaemonProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, send := io.Pipe()
	receive, out := io.Pipe()
	defer send.Close()
	defer receive.Close()
	defer out.Close()
	// Unblock test-side I/O too if an assertion or timeout ends the exchange.
	stopOutput := context.AfterFunc(ctx, func() { _ = receive.Close() })
	defer stopOutput()
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- execute(ctx, []string{"bridge", "serve"}, in, out, &diagnostics) }()
	encoder, decoder := json.NewEncoder(send), json.NewDecoder(receive)
	exchange := func(id, method string, params any) json.RawMessage {
		t.Helper()
		if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.ID != id || len(response.Error) != 0 || response.Result == nil {
			t.Fatalf("bad response: %+v", response)
		}
		return response.Result
	}
	hello := exchange("hello", "$hello", map[string]any{})
	var schema struct {
		Protocol    int             `json:"protocol"`
		Constructor json.RawMessage `json:"constructor"`
	}
	if err := json.Unmarshal(hello, &schema); err != nil || schema.Protocol != 1 || schema.Constructor == nil {
		t.Fatalf("handshake = %s, %v", hello, err)
	}
	exchange("init", "$init", map[string]any{"prefix": "Cobra: "})
	if got := string(exchange("welcome", "welcome", map[string]any{"name": "Sam"})); got != `"Cobra: Sam"` {
		t.Fatalf("welcome = %s", got)
	}
	if got := string(exchange("greet", "greet", map[string]any{"name": "World"})); got != `"Hello, World!"` {
		t.Fatalf("greet = %s", got)
	}
	if got := string(exchange("reset", "reset", map[string]any{})); got != "null" {
		t.Fatalf("reset = %s", got)
	}
	if err := send.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil || diagnostics.Len() != 0 {
			t.Fatalf("EOF = %v, diagnostics=%q", err, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal("host did not return after EOF")
	}
}

func TestContextCancellationUnblocksIdleInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in, send := io.Pipe()
	defer send.Close()
	owned := &notifyingInput{ReadCloser: in, started: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- execute(ctx, []string{"bridge", "serve"}, owned, io.Discard, io.Discard) }()
	select {
	case <-owned.started:
	case <-time.After(2 * time.Second):
		t.Fatal("host did not start reading protocol input")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled host returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not close owned input")
	}
}

type notifyingInput struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (in *notifyingInput) Read(data []byte) (int, error) {
	in.once.Do(func() { close(in.started) })
	return in.ReadCloser.Read(data)
}
