package main

import (
	"context"
	"strings"
	"time"

	bridge "github.com/sambhav/gobridge"
)

type Child struct {
	Name string `json:"name"`
}
type Payload struct {
	Child    Child            `json:"child"`
	Optional *Child           `json:"optional,omitempty"`
	Items    []Child          `json:"items"`
	Labels   map[string]Child `json:"labels"`
	Big      int64            `json:"big"`
}
type NativeValues struct {
	Data  []byte        `json:"data"`
	At    time.Time     `json:"at"`
	Delay time.Duration `json:"delay"`
}
type Empty struct{}
type Large struct {
	Text string `json:"text"`
}

func main() {
	r := bridge.New()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(bridge.Register(r, "echo", "Round-trip nested data.", func(_ context.Context, p Payload) (Payload, error) { return p, nil }))
	must(bridge.Register(r, "explode", "Panic isolation fixture.", func(context.Context, Empty) (Empty, error) { panic("private panic details") }))
	must(bridge.Register(r, "large", "Oversized output fixture.", func(context.Context, Empty) (Large, error) { return Large{strings.Repeat("x", bridge.MaxFrame)}, nil }))
	must(bridge.Register(r, "native_values", "Round-trip bytes and lossless time values.", func(_ context.Context, p NativeValues) (NativeValues, error) { return p, nil }))
	r.Main()
}
