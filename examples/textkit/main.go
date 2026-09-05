package main

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"time"

	bridge "github.com/sambhav/gobridge"
)

type AnalyzeInput struct {
	Text string `json:"text"`
}
type Analysis struct {
	Words       int   `json:"words"`
	Characters  int   `json:"characters"`
	Computation int64 `json:"computation"`
	ProcessID   int   `json:"process_id"`
}
type WaitInput struct {
	Milliseconds int `json:"milliseconds"`
}
type WaitResult struct {
	ProcessID int `json:"process_id"`
}
type Empty struct{}
type Health struct {
	ProcessID int   `json:"process_id"`
	Active    int64 `json:"active"`
}

func main() {
	r := bridge.New()
	cache := bridge.NewMemo[string, Analysis](128, time.Minute)
	var computes, active atomic.Int64
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(bridge.Register(r, "analyze", "Count words and Unicode code points; cache results in Go.", func(ctx context.Context, in AnalyzeInput) (Analysis, error) {
		return cache.Get(ctx, in.Text, func(context.Context) (Analysis, error) {
			return Analysis{len(strings.Fields(in.Text)), len([]rune(in.Text)), computes.Add(1), os.Getpid()}, nil
		})
	}))
	must(bridge.Register(r, "wait", "Wait with cooperative cancellation.", func(ctx context.Context, in WaitInput) (WaitResult, error) {
		if in.Milliseconds < 0 || in.Milliseconds > 60000 {
			return WaitResult{}, bridge.Failure("invalid_argument", "milliseconds must be 0..60000")
		}
		active.Add(1)
		defer active.Add(-1)
		t := time.NewTimer(time.Duration(in.Milliseconds) * time.Millisecond)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return WaitResult{}, ctx.Err()
		case <-t.C:
			return WaitResult{os.Getpid()}, nil
		}
	}))
	must(bridge.Register(r, "health", "Inspect the daemon for the integration example.", func(context.Context, Empty) (Health, error) { return Health{os.Getpid(), active.Load()}, nil }))
	r.Main()
}
