# Hello World: a Go library, a CLI, and a Python package

This tutorial grows one example from an ordinary Go function to generated
Python classes, concurrency, and a wheel containing its Go binary. Run the
commands from the repository root. Development requires Go 1.23+ and Python
3.10+; users of the resulting wheel only need Python.

The working example lives in [`examples/hello`](../examples/hello). The API
design for module-level convenience functions and constructor options is in
[`API_DESIGN.md`](API_DESIGN.md); the steps here describe the class-based
example and will grow as those features land.

## 1. Start with an ordinary Go function

Your library keeps its normal Go API:

```go
func Greet(name string) string {
    return "Hello, " + name + "!"
}
```

The wrapper imports gobridge, defines the typed wire boundary, and registers
an operation. JSON tags become Python keyword names and CLI flags:

```go
package main

import (
    "context"
    bridge "github.com/sambhav/gobridge"
)

func Greet(name string) string { return "Hello, " + name + "!" }

type GreetInput struct {
    Name string `json:"name"`
}

type Greeting struct {
    Message string `json:"message"`
}

func main() {
    r := bridge.New()
    err := bridge.Register(r, "greet", "Greet someone.",
        func(_ context.Context, in GreetInput) (Greeting, error) {
            return Greeting{Message: Greet(in.Name)}, nil
        })
    if err != nil {
        panic(err)
    }
    r.Main()
}
```

`Greet` has no bridge dependency. The adapter can call a function from another
Go module or a method on a Go object. Registration checks the wire types before
serving, so unsupported types fail during setup.

## 2. Use the CLI

Build the checked-in example, which also includes the cache operation used
later in this tutorial:

```sh
go build -o bin/hello ./examples/hello
./bin/hello greet --name world
```

Output:

```json
{"message":"Hello, world!"}
```

The same operation accepts a JSON object:

```sh
./bin/hello greet --json '{"name":"world"}'
./bin/hello help
./bin/hello schema
```

On Windows, build `bin/hello.exe` and invoke `.\bin\hello.exe`. The Python
verification and packaging scripts choose the executable suffix automatically.

## 3. Generate the Python API

```sh
python -m pip install -e "./python[dev]"
./bin/hello generate-python --class Hello --binary hello > examples/hello/hello.py
python examples/hello/demo.py
```

The generated file is checked in so you can review its actual signatures.
The generator creates a `Hello` class, an `AsyncHello` class, and immutable,
typed dataclasses for request and result types. There is no Pydantic or other
runtime dependency.

The demo imports `hello.py` beside itself. For your own development script,
place the generated module beside that script, or add `examples/hello` to
your Python import path:

```python
from hello import Hello

with Hello("./bin/hello") as hello:
    result = hello.greet(name="world")
    print(result.message)  # Hello, world!
```

The method has a real `name: str` keyword argument and returns a `Greeting`.
Editor completion works without dynamic attribute lookup. A misspelled keyword
fails as an ordinary Python `TypeError`. Go validates the wire input before
calling the handler. Python type annotations provide editor/static-checking
support; they are not a second runtime validation framework.

Creating a client is lazy. Its first call or context entry starts a private Go
daemon over stdio. Leaving the context closes the connection and stops the
daemon. Keep the client alive to retain its Go state and amortize startup.

## 4. Add async calls and threads

Async methods have the same keyword arguments and return types:

```python
import asyncio
from hello import AsyncHello

async def main():
    async with AsyncHello("./bin/hello") as hello:
        results = await asyncio.gather(
            hello.greet(name="Ada"),
            hello.greet(name="Grace"),
        )
        print([result.message for result in results])

asyncio.run(main())
```

Share a synchronous client across threads:

```python
from concurrent.futures import ThreadPoolExecutor
from hello import Hello

with Hello("./bin/hello") as hello, ThreadPoolExecutor(max_workers=8) as pool:
    results = list(pool.map(lambda name: hello.greet(name=name), ["Ada", "Grace"]))
    print([result.message for result in results])
```

Each client has one multiplexed connection; request IDs route results back to
the right caller. Threads and async tasks using that client share its daemon.
Two clients own two daemons. Go handlers may run concurrently, so shared state
in your Go library must be concurrency-safe.

## 5. Keep caching in Go

The example also exposes `cached_greet`. Its cache is created once, before
serving, and has a capacity and TTL:

```go
cache := bridge.NewMemo[string, CachedGreeting](128, time.Minute)
var computations atomic.Int64

err := bridge.Register(r, "cached_greet", "Greet with a Go cache.",
    func(ctx context.Context, in GreetInput) (CachedGreeting, error) {
        return cache.Get(ctx, in.Name, func(context.Context) (CachedGreeting, error) {
            return CachedGreeting{
                Message: Greet(in.Name),
                Computation: computations.Add(1),
                ProcessID: os.Getpid(),
            }, nil
        })
    })
if err != nil {
    panic(err)
}
```

See the complete [Go example](../examples/hello/main.go) for imports and the
`CachedGreeting` struct. The computation counter and process ID are demonstration
fields that make cache hits and process ownership visible:

```python
from hello import Hello

with Hello("./bin/hello") as hello:
    first = hello.cached_greet(name="world")
    again = hello.cached_greet(name="world")
    assert first == again
    assert first.computation == 1
```

The runnable demo checks that concurrent threads and async tasks receive the
same cached computation. The Go cache coalesces simultaneous misses for the
same key. Include every input and relevant identity in cache keys, and return
immutable values or copies. Greeting is intentionally trivial; cache costly,
pure operations in a real library.

Each CLI invocation exits after one call, so its in-memory cache ends with the
process. Python reuses the daemon for the lifetime of its client.

## 6. Use multiprocessing with independent ownership

Each Python process needs its own daemon and connection. The clearest pattern
creates a client inside each worker:

```python
import multiprocessing as mp
from pathlib import Path
from hello import Hello

def greet_worker(args):
    binary, name = args
    with Hello(binary) as hello:
        return hello.cached_greet(name=name)

if __name__ == "__main__":
    binary = str(Path("./bin/hello").resolve())
    with mp.get_context("spawn").Pool(2) as pool:
        results = pool.map(greet_worker, [(binary, "Ada"), (binary, "Grace")])
        print([result.message for result in results])
```

For a long-lived worker, retain a client in that worker and close it on worker
shutdown. Pickling a client copies its configuration, not a running daemon.
After `fork`, inherited pipe copies are discarded and child transport state is
reset on use; the parent's client stays usable. Prefer `spawn` or `forkserver`
when other libraries or native runtimes have active threads.

## 7. Ship the binary as Python package data

Build a wheel for your target platform:

```sh
python -m pip install setuptools wheel
python tools/build_wheels.py --go-package ./examples/hello --package hello --class Hello --binary hello --distribution gobridge-hello-example --targets linux-amd64
```

Use `darwin-arm64`, `darwin-amd64`, `windows-amd64`, `windows-arm64`, or
`linux-arm64` for another supported target; omit `--targets` to build all six.
The defaults still build the existing `textkit` example for CI.

The recipe builds the host executable to generate bindings, cross-compiles the
target executable with `CGO_ENABLED=0`, and packages the files as:

```text
hello/__init__.py        generated classes and dataclasses
hello/py.typed           typing marker
hello/_bin/hello         platform-specific Go executable (.exe on Windows)
```

Install from the output directory on the matching target host:

```sh
python -m pip install --no-index --find-links dist gobridge-hello-example
python -c 'from hello import Hello; h=Hello(); print(h.greet(name="packaged").message); h.close()'
```

No binary path or Go installation is required. `Hello()` resolves the executable
relative to its installed package and launches it on first use. The separate
`gobridge-runtime` wheel is included in `dist` and installed as a dependency.

This is a local/private wheel recipe. Linux wheels use `linux_*` platform tags;
public manylinux/musllinux distribution needs a separate compatibility and
release workflow. Dependencies using cgo also need target C toolchains and a
different build configuration.

## 8. Verify the tutorial and the installed wheel

```sh
python tools/check.py
python tools/test_wheel.py --example hello
```

The first command builds both examples, rejects stale generated bindings, runs
Go race tests and vet, and executes pytest integration tests including this
tutorial's CLI, typed sync/async calls, and concurrent demo. The second installs
the built Hello wheel into a fresh virtual environment and checks that its
bundled executable works through both Python clients.

Next additions are tracked in [the API design](API_DESIGN.md) and
[the implementation plan](PLAN.md): module-level functions, Go constructor
options mapped to Python initialization, richer methods, and TypeScript.
