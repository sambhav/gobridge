// Hello is a small, complete Go library exposed as a CLI and Python package.
package main

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	bridge "github.com/sambhav/gobridge"
)

// Greet is ordinary Go code. Your existing library does not depend on gobridge.
func Greet(name string) string { return "Hello, " + name + "!" }

type GreetInput struct {
	Name string `json:"name"`
}

type Greeting struct {
	Message string `json:"message"`
}

// The extra fields make daemon ownership and cache reuse visible in the demo.
type CachedGreeting struct {
	Message     string `json:"message"`
	Computation int64  `json:"computation"`
	ProcessID   int    `json:"process_id"`
}

func main() {
	r := bridge.New()
	must(bridge.Register(r, "greet", "Greet someone using an ordinary Go function.",
		func(_ context.Context, in GreetInput) (Greeting, error) {
			return Greeting{Message: Greet(in.Name)}, nil
		}))

	// This cache is private to each daemon, shared by that daemon's requests.
	cache := bridge.NewMemo[string, CachedGreeting](128, time.Minute)
	var computations atomic.Int64
	must(bridge.Register(r, "cached_greet", "Greet with a bounded Go cache shared by calls on this client.",
		func(ctx context.Context, in GreetInput) (CachedGreeting, error) {
			return cache.Get(ctx, in.Name, func(context.Context) (CachedGreeting, error) {
				return CachedGreeting{
					Message: Greet(in.Name), Computation: computations.Add(1), ProcessID: os.Getpid(),
				}, nil
			})
		}))
	r.Main()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
