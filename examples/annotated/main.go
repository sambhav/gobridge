// Annotated demonstrates ordinary Go declarations exposed without handwritten adapters.
package main

//go:generate go run ../../cmd/gobridge generate --dir . --output zz_gobridge.gen.go

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
)

// Greet returns a friendly greeting using an ordinary Go function.
//
//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }

// Options adapts constructor configuration to simple serializable data.
type Options struct {
	Prefix *string `json:"prefix,omitempty"`
}

// Greeter owns state in one daemon; atomic fields support concurrent calls.
type Greeter struct {
	prefix string
	calls  atomic.Int64
}

// NewGreeter applies defaults once when a Python client starts its daemon.
//
//gobridge:constructor
func NewGreeter(options Options) (*Greeter, error) {
	prefix := "Welcome, "
	if options.Prefix != nil {
		prefix = *options.Prefix
	}
	return &Greeter{prefix: prefix}, nil
}

// Welcome greets someone with this client's configured prefix.
//
//gobridge:export
func (g *Greeter) Welcome(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	g.calls.Add(1)
	return g.prefix + name, nil
}

// Stats is returned as a typed Python dataclass.
type Stats struct {
	Calls     int64 `json:"calls"`
	ProcessID int   `json:"process_id"`
}

// Statistics reports calls owned by this instance.
//
//gobridge:export stats
func (g *Greeter) Statistics() Stats {
	return Stats{Calls: g.calls.Load(), ProcessID: os.Getpid()}
}

// Reset clears the instance's call counter and returns None in Python.
//
//gobridge:export
func (g *Greeter) Reset() { g.calls.Store(0) }

func main() {
	r, err := NewGobridge()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	r.Main()
}
