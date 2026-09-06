package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsAndPrivateErrors(t *testing.T) {
	var logs bytes.Buffer
	var event CallEvent
	r := New(WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))), WithObserver(func(_ context.Context, e CallEvent) { event = e }))
	if err := Bind(r, "fail", func() error { return errors.New("private-secret") }); err != nil {
		t.Fatal(err)
	}
	_, err := r.Call(context.WithValue(context.Background(), requestIDKey{}, "request-7"), "fail", nil)
	if wireError(err).Message != "internal operation error" || event.RequestID != "request-7" || event.Err != err {
		t.Fatalf("bad event/error: %+v %v", event, err)
	}
	if strings.Contains(logs.String(), "private-secret") || !strings.Contains(logs.String(), "request-7") {
		t.Fatal(logs.String())
	}
	r.observer = func(context.Context, CallEvent) { panic("observer") }
	_, _ = r.Call(context.Background(), "fail", nil)
}

func TestMemoInvalidationDuringLoad(t *testing.T) {
	memo := NewMemo[string, int](2, time.Minute)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan int)
	go func() {
		v, _ := memo.Get(context.Background(), "key", func(context.Context) (int, error) { close(entered); <-release; return 1, nil })
		done <- v
	}()
	<-entered
	memo.Delete("key")
	v, err := memo.Get(context.Background(), "key", func(context.Context) (int, error) { return 2, nil })
	if err != nil || v != 2 {
		t.Fatal(v, err)
	}
	close(release)
	<-done
	v, _ = memo.Get(context.Background(), "key", func(context.Context) (int, error) { return 3, nil })
	if v != 2 {
		t.Fatal("stale load repopulated", v)
	}
	memo.Clear()
	v, _ = memo.Get(context.Background(), "key", func(context.Context) (int, error) { return 4, nil })
	if v != 4 {
		t.Fatal(v)
	}
}

type streamTestInput struct {
	Count int `json:"count"`
}

func TestStreamPullCancellationAndCapacity(t *testing.T) {
	r := New()
	stopped := make(chan struct{})
	if err := RegisterStream(r, "items", "", func(ctx context.Context, in streamTestInput, yield func(int) error) error {
		defer close(stopped)
		for i := 0; i < in.Count; i++ {
			if err := yield(i); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := &streamSession{ctx: context.Background(), registry: r, limit: 1, cursors: map[string]*streamCursor{}}
	defer s.closeAll()
	value, err := s.invoke(context.Background(), "$stream_open", json.RawMessage(`{"method":"items","params":{"count":100}}`))
	if err != nil {
		t.Fatal(err)
	}
	cursor := value.(map[string]string)["cursor"]
	if _, err = s.invoke(context.Background(), "$stream_open", json.RawMessage(`{"method":"items","params":{"count":1}}`)); wireError(err).Code != "busy" {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"cursor": cursor})
	if _, err = s.invoke(context.Background(), "$stream_next", raw); err != nil {
		t.Fatal(err)
	}
	s.close(cursor)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("producer leaked after close")
	}
	if _, err = s.invoke(context.Background(), "$stream_next", raw); wireError(err).Code != "not_found" {
		t.Fatal(err)
	}
}

func TestBindStreamAndBatchLimits(t *testing.T) {
	r := New()
	if err := Bind(r, "count", func(ctx context.Context, n int, yield func(int) error) error {
		for i := 0; i < n; i++ {
			if err := yield(i); err != nil {
				return err
			}
		}
		return nil
	}, "n"); err != nil {
		t.Fatal(err)
	}
	if !r.Schema().Operations[0].Stream {
		t.Fatal("stream metadata missing")
	}
	var got []int
	if err := r.ops["count"].stream(context.Background(), json.RawMessage(`{"n":3}`), func(value any) error { got = append(got, value.(int)); return nil }); err != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	if _, err := r.Batch(context.Background(), make([]BatchCall, 129)); wireError(err).Code != "resource_exhausted" {
		t.Fatal(err)
	}
	if err := Bind(r, "large", func() string { return strings.Repeat("x", MaxFrame) }); err != nil {
		t.Fatal(err)
	}
	results, err := r.Batch(context.Background(), []BatchCall{{Method: "large"}, {Method: "count"}})
	if err != nil || results[0].Error.Code != "resource_exhausted" || results[1].Error.Code != "resource_exhausted" {
		t.Fatal(results, err)
	}
}

func TestCancelPendingStreamRead(t *testing.T) {
	r := New()
	stopped := make(chan struct{})
	if err := RegisterStream(r, "slow", "", func(ctx context.Context, in streamTestInput, yield func(int) error) error {
		defer close(stopped)
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	s := &streamSession{ctx: context.Background(), registry: r, limit: 1, cursors: map[string]*streamCursor{}}
	defer s.closeAll()
	opened, err := s.invoke(context.Background(), "$stream_open", json.RawMessage(`{"method":"slow","params":{"count":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(opened)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.invoke(ctx, "$stream_next", raw); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancel did not release stream")
	}
}
