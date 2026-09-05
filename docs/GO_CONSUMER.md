# Use the same library directly from Go

**Docs-first checkpoint:** the annotated example is being split into an importable
`greeter` package and a small CLI entrypoint. Its Go and Python operation contracts
stay the same.

The library lives at `github.com/sambhav/gobridge/examples/annotated`, with package
name `greeter`. A Go application imports that package and calls its exported
functions and methods normally. Calls run inside the application's process;
constructors create ordinary Go values, and each value owns its own state.

```go
package main

import (
    "context"
    "fmt"

    greeter "github.com/sambhav/gobridge/examples/annotated"
)

func main() {
    fmt.Println(greeter.Greet("World")) // Hello, World!

    prefix := "Hey, "
    client, err := greeter.NewGreeter(greeter.Options{Prefix: &prefix})
    if err != nil {
        panic(err)
    }
    message, err := client.Welcome(context.Background(), "Sam")
    if err != nil {
        panic(err)
    }
    fmt.Println(message)                  // Hey, Sam
    fmt.Println(client.Statistics().Calls) // 1
    client.Reset()
}
```

Use `greeter.Options{}` for the default prefix `"Welcome, "`. The constructor
copies the configured prefix, so changing the caller's options afterward does
not change the instance. `Welcome` accepts the caller's context and returns a Go
error if it is already canceled. Multiple goroutines may call this example's
methods because its counter uses atomic operations. New instances have separate
counters; the library's own implementation defines concurrency behavior.

## Keep the library and executable separate

The annotated example has four small parts:

- `examples/annotated/greeter.go` contains the normal library API and opt-in comments.
- `examples/annotated/zz_gobridge.gen.go` registers those declarations for the bridge.
- `examples/annotated/cmd/annotated/main.go` starts the CLI or daemon.
- `examples/annotated/annotated.py` provides the generated Python API.

The command package uses the generated registration function:

```go
package main

import (
    "fmt"
    "os"

    greeter "github.com/sambhav/gobridge/examples/annotated"
)

func main() {
    registry, err := greeter.NewGobridge()
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    registry.Main()
}
```

Go callers use `Greet` and `NewGreeter` directly. The executable calls `NewGobridge`
to expose them over its CLI and private stdio protocol. Registration and schema
generation do not execute `NewGreeter`; a daemon runs that constructor once when
its owning Python client initializes it. Each Python client instance therefore
owns a separate Go instance, and the module convenience API reuses its own lazy
default client.

From the repository root, generate, test, and run the example:

```sh
go generate ./examples/annotated
go test -race ./examples/annotated/...
go run ./examples/annotated/cmd/annotated --config '{"prefix":"Hey, "}' welcome --name World
```

Cross-compile the command package when bundling it in a Python wheel. Native Go
applications build the library as part of their own program. The same exported
declarations serve both use cases; [SOURCE_GENERATION.md](SOURCE_GENERATION.md)
describes the annotation contract and [API_DESIGN.md](API_DESIGN.md) describes
Python ownership and control.
