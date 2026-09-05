# Hello World: a Go library, a CLI, and a Python package

This tutorial grows one example from an ordinary Go function to generated
Python classes, concurrency, and a wheel containing its Go binary. Run the
commands from the repository root. Development requires Go 1.23+ and Python
3.10+; users of the resulting wheel only need Python.

The working example lives in [`examples/hello`](../examples/hello). After
installing its platform wheel, the simplest Python API is an ordinary import:

```python
from hello import greet

print(greet(name="world").message)  # Hello, world!
```

The first call starts the bundled Go binary; later calls reuse it. Importing
the package starts no processes. You can also create explicit instances or use
scoped overrides when you need separate state. See [the API design](API_DESIGN.md)
for the ownership model and upcoming Go function/constructor helpers.

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
The generator creates module functions, an async `aio` namespace, a `Hello`
class, an `AsyncHello` class, and immutable, typed dataclasses for request and
result types. There is no Pydantic or other runtime dependency.

The demo imports `hello.py` beside itself. For your own development script,
place the generated module beside that script, or add `examples/hello` to
your Python import path:

```python
from hello import control, greet

# Development only: installed wheels locate their bundled executable.
control.configure(command="./bin/hello")
print(greet(name="world").message)  # Hello, world!
control.close()
```

The function has a real `name: str` keyword argument and returns a `Greeting`.
Editor completion works without dynamic attribute lookup. A misspelled keyword
fails as an ordinary Python `TypeError`. Go validates the wire input before
calling the handler. Python type annotations provide editor/static-checking
support; they are not a second runtime validation framework.

Module functions reuse one lazy default client per generated module per Python
process. Sync and async module calls share that client's Go state. Normal
interpreter exit attempts cleanup; `control.close()` gives you an explicit
reset. After reset, a later module call creates a fresh default. A crashed daemon
does not restart itself or replay operations.

Use an explicit class to own a separate daemon:

```python
from hello import Hello

with Hello("./bin/hello") as hello:
    print(hello.greet(name="explicit").message)
```

Creating a client is lazy. Its first call or context entry starts the daemon.
Leaving its context closes it permanently. Keep the client alive to retain its
Go state and amortize startup. Installed wheels support `Hello()` without a
path. Advanced transport options stay separate from library arguments:

```python
from gobridge import RuntimeOptions
from hello import Hello

with Hello(_runtime=RuntimeOptions(command="./bin/hello", timeout=5)) as hello:
    result = hello.greet(name="configured", _timeout=1)
```

## 4. Add async calls and threads

For async code, the `aio` namespace has the same keyword arguments and return
types as the synchronous module functions:

```python
import asyncio
from hello import aio, control, greet

control.configure(command="./bin/hello")  # Development only.

async def main():
    print(greet(name="sync").message)
    print((await aio.greet(name="async")).message)

try:
    asyncio.run(main())
finally:
    control.close()
```

These calls share one default daemon. Synchronous functions remain blocking;
use `aio` for calls that must leave your event loop responsive.

Explicit async instances work too:

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

### Isolate module functions temporarily

Scopes give existing code that imports functions a temporary, separate client:

```python
from hello import cached_greet, control

control.configure(command="./bin/hello")
original = cached_greet(name="world")
with control.scope(command="./bin/hello") as isolated:
    scoped = cached_greet(name="world")
    assert scoped.process_id != original.process_id
    assert scoped == isolated.cached_greet(name="world")
assert cached_greet(name="world") == original
control.close()
```

Use `async with control.scope(...)` in async code. Nested scopes restore the
previous client, including when the body raises. Scopes follow Python context
variables: child async tasks inherit the current scope. New OS threads do not
automatically inherit it; pass the explicit client to threads when scoped state
must be shared. Each explicit scope owns and closes its own daemon. Default
configuration is separate and cannot be replaced while the default is live.

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
python -c 'from hello import greet; print(greet(name="packaged").message)'
```

No binary path or Go installation is required. The default client and `Hello()`
resolve the executable relative to the installed package and launch it on first
use. The separate
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

The first command builds the examples, rejects stale generated bindings, runs
Go race tests and vet, and executes pytest integration tests including this
tutorial's CLI, typed sync/async calls, default sharing, scoped restoration,
and concurrent demo. The second installs
the built Hello wheel into a fresh virtual environment and checks that its
bundled executable works through both Python clients.

Continue with [source annotations](SOURCE_GENERATION.md) to expose ordinary Go
functions, constructors and methods, or use the same library through the
[TypeScript API](TYPESCRIPT.md). [The implementation plan](PLAN.md) tracks
release hardening and future resource models.
