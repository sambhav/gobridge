package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestAnnotatedObject(t *testing.T) {
	r, err := NewGobridge()
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
	if err != nil || value.(Stats).Calls != 50 {
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
