package main

import (
	"context"
	"fmt"
	bridge "github.com/sambhav/gobridge"
	"os"
	"sync/atomic"
	"time"
)

type Input struct {
	Count int   `json:"count"`
	Fail  *bool `json:"fail,omitempty"`
}

func main() {
	r := bridge.New()
	var active atomic.Int64
	err := bridge.RegisterStream(r, "numbers", "Stream exact integers.", func(ctx context.Context, input Input, yield func(int64) error) error {
		active.Add(1)
		defer active.Add(-1)
		for i := 0; i < input.Count; i++ {
			if err := yield(int64(i)); err != nil {
				return err
			}
		}
		if input.Fail != nil && *input.Fail {
			return &bridge.Error{Code: "invalid_argument", Message: "requested failure", Details: map[string]string{"field": "fail"}}
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	if err = bridge.Bind(r, "active", active.Load); err != nil {
		panic(err)
	}
	if err = bridge.Bind(r, "explode", func() error { return fmt.Errorf("private-secret") }); err != nil {
		panic(err)
	}
	if err = bridge.Bind(r, "wait", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Minute):
			return nil
		}
	}); err != nil {
		panic(err)
	}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "bridge" {
		args = args[1:]
	}
	if err = r.Run(context.Background(), args, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
