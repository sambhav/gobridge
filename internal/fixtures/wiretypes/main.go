package main

import (
	"context"
	"strings"

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
	r.Main()
}
