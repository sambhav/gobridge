package greeter_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	greeter "github.com/sambhav/gobridge/examples/greeter"
)

func TestAnnotatedObject(t *testing.T) {
	r, err := greeter.NewGobridge()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Initialize(context.Background(), json.RawMessage(`{"prefix":"Hey, "}`)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := r.Call(context.Background(), "welcome", json.RawMessage(`{"name":"Sam"}`))
			if err != nil || value != "Hey, Sam" {
				t.Errorf("welcome = %v, %v", value, err)
			}
		}()
	}
	wg.Wait()
	value, err := r.Call(context.Background(), "stats", json.RawMessage(`{}`))
	if err != nil || value.(greeter.Stats).Calls != 50 {
		t.Fatalf("stats = %v, %v", value, err)
	}
	value, err = r.Call(context.Background(), "reset", json.RawMessage(`{}`))
	if err != nil || value != nil {
		t.Fatalf("reset = %v, %v", value, err)
	}
	value, err = r.Call(context.Background(), "greet", json.RawMessage(`{"name":"World"}`))
	if err != nil || value != "Hello, World!" {
		t.Fatalf("greet = %v, %v", value, err)
	}
}

func TestNativeLibrary(t *testing.T) {
	if got := greeter.Greet("World"); got != "Hello, World!" {
		t.Fatalf("Greet = %q", got)
	}
	client, err := greeter.NewGreeter(greeter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	message, err := client.Welcome(context.Background(), "Sam")
	if err != nil || message != "Welcome, Sam" {
		t.Fatalf("Welcome = %q, %v", message, err)
	}
	stats := client.Statistics()
	if stats.Calls != 1 || stats.ProcessID != os.Getpid() {
		t.Fatalf("native calls should run inside the Go caller's process: %+v", stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Welcome(ctx, "Canceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context returned %v", err)
	}
	if got := client.Statistics().Calls; got != 1 {
		t.Fatalf("canceled call changed state: %d", got)
	}
}

func TestNativeConcurrentInstanceIsolation(t *testing.T) {
	firstPrefix, secondPrefix := "First: ", "Second: "
	first, err := greeter.NewGreeter(greeter.Options{Prefix: &firstPrefix})
	if err != nil {
		t.Fatal(err)
	}
	second, err := greeter.NewGreeter(greeter.Options{Prefix: &secondPrefix})
	if err != nil {
		t.Fatal(err)
	}
	// The constructor snapshots data supplied by its caller.
	firstPrefix, secondPrefix = "Changed", "Changed"
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, tt := range []struct {
				client *greeter.Greeter
				want   string
			}{{first, "First: Sam"}, {second, "Second: Sam"}} {
				if got, err := tt.client.Welcome(context.Background(), "Sam"); err != nil || got != tt.want {
					t.Errorf("Welcome = %q, %v; want %q", got, err, tt.want)
				}
			}
		}()
	}
	wg.Wait()
	if first.Statistics().Calls != 50 || second.Statistics().Calls != 50 {
		t.Fatalf("concurrent calls lost state: first=%+v second=%+v", first.Statistics(), second.Statistics())
	}
	first.Reset()
	if first.Statistics().Calls != 0 || second.Statistics().Calls != 50 {
		t.Fatalf("reset crossed instance boundaries: first=%+v second=%+v", first.Statistics(), second.Statistics())
	}
}
