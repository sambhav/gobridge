package gobridge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoCoalescingAndCancellation(t *testing.T) {
	m := NewMemo[string, int](2, time.Minute)
	var loads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (int, error) {
		loads.Add(1)
		close(started)
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	a, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := m.Get(a, "key", load); done <- err }()
	<-started
	var wg sync.WaitGroup
	for j := 0; j < 12; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := m.Get(context.Background(), "key", load)
			if err != nil || v != 42 {
				t.Errorf("%v %v", v, err)
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		n := m.flights["key"].waiters
		m.mu.Unlock()
		if n == 13 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiters did not join")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("caller was not cancelled")
	}
	close(release)
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatal(loads.Load())
	}
	v, err := m.Get(context.Background(), "key", load)
	if err != nil || v != 42 {
		t.Fatal(v, err)
	}
}
func TestMemoLastWaiterCancelsAndFailureNotCached(t *testing.T) {
	m := NewMemo[string, int](1, time.Minute)
	ended := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		_, _ = m.Get(ctx, "x", func(c context.Context) (int, error) { close(started); <-c.Done(); close(ended); return 0, c.Err() })
	}()
	<-started
	cancel()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("loader leaked")
	}
	v, err := m.Get(context.Background(), "x", func(context.Context) (int, error) { return 8, nil })
	if err != nil || v != 8 {
		t.Fatal(v, err)
	}
}
func TestMemoCapacityAndTTL(t *testing.T) {
	m := NewMemo[int, int](1, 5*time.Millisecond)
	var loads int
	get := func(k int) {
		t.Helper()
		_, err := m.Get(context.Background(), k, func(context.Context) (int, error) { loads++; return k, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	get(1)
	get(1)
	get(2)
	get(1)
	if loads != 3 {
		t.Fatal(loads)
	}
	time.Sleep(10 * time.Millisecond)
	get(1)
	if loads != 4 {
		t.Fatal(loads)
	}
}
