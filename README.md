# gobridge

Keep your library in Go. Generate a CLI and typed Python functions and classes,
with the Go binary bundled inside a platform wheel. TypeScript comes next after
the Go, CLI, Python, and CLI-embedding workflow is fully tested.

**Development preview:** no Go version tags or Python packages have been
published. Build and use the examples from this checkout.

## Ordinary Go functions become ordinary Python functions

```go
//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }
```

The generated Python package exposes the matching function:

```python
from annotated import greet

print(greet(name="World"))  # Hello, World!
```

Go callers import the library and call `Greet` directly. Python starts the
bundled executable on its first call and reuses a private daemon. Importing
starts no process; a packaged wheel needs no Go installation or executable path.

Constructors and instance methods preserve real Go state:

```python
from annotated import Greeter

with Greeter(prefix="Hey, ") as greeter:
    print(greeter.welcome(name="Sam"))  # Hey, Sam
    print(greeter.stats().calls)       # 1
    greeter.reset()
    assert greeter.stats().calls == 0
```

`prefix` comes from the Go constructor's options struct. Each instance owns a
separate daemon and Go object. Methods have real signatures; model results are
lightweight dataclasses. The runtimes have no mandatory Pydantic dependency.
Async code can use `await aio.greet(...)` or an explicit `AsyncGreeter` instance.

## Run it from a checkout

Requires Go 1.23+ and Python 3.10+. From the repository root in a POSIX shell:

```sh
go generate ./examples/annotated
go build -o bin/annotated ./examples/annotated/cmd/annotated
./bin/annotated generate-python --class Greeter --binary annotated > examples/annotated/annotated.py
python -m pip install -e "./python[dev]"
PYTHONPATH=examples/annotated python -c 'from annotated import control, greet; control.configure(command="./bin/annotated"); print(greet(name="World")); control.close()'
```

Development uses `control.configure(command=...)` for module functions or
`Greeter("./bin/annotated", prefix="Hey, ")` for an explicit instance. Installed
wheels locate their package-data executable automatically.

Use the same operations from the CLI:

```sh
./bin/annotated greet --name World
./bin/annotated welcome --help
./bin/annotated --config '{"prefix":"Hey, "}' welcome --name Sam
```

Help shows types, required/optional fields, documentation, validation limits,
and constructor options. Operation stdout is JSON; errors go to stderr.

Run the portable verification entrypoint, including on Windows:

```sh
python tools/check.py
```

It builds the examples, verifies generated files, runs Go race tests and vet,
and executes pytest against actual subprocesses. Windows manual commands use
`bin/annotated.exe`. CI exercises Linux, macOS, Windows, supported Python
versions, minimum Go, and clean platform-wheel installations.

## Ownership and performance

| Usage | Go state |
| --- | --- |
| Module functions and `aio` | One lazy default per generated module per Python process |
| Threads/tasks sharing a client | Shared daemon and state |
| Separate client instances | Separate daemons and objects |
| `control.scope(...)` | Isolated state; previous client restored on exit |
| Multiprocessing workers | Fresh connections and state; parent's daemon stays owned by the parent |

Requests are multiplexed over anonymous stdio pipes, with bounded admission,
cooperative cancellation, explicit shutdown, and schema checks. Crashes fail
pending calls; operations are never automatically replayed. Prefer `spawn` or
`forkserver` when other libraries have active threads.

Go owns the work and reusable state. The optional `Memo` cache provides bounded
TTL/LRU storage and per-key request coalescing. Keep clients alive to amortize
startup; IPC is intended for useful library operations. See the
[measurements](docs/PERFORMANCE.md) and [lifecycle design](docs/ARCHITECTURE.md).

## Guides

| You want to… | Start here |
| --- | --- |
| Import the library directly from Go | [Native Go usage](docs/GO_CONSUMER.md) |
| Expose your existing functions and methods | [Source annotations](docs/SOURCE_GENERATION.md), [Go registration API](docs/GO_API.md) |
| Use generated Python functions and classes | [Hello World](docs/HELLO_WORLD.md), [Python ownership and controls](docs/API_DESIGN.md) |
| Explore CLI flags and JSON configuration | [CLI guide](docs/CLI.md) |
| Add the bridge to an existing CLI or Cobra app | [Embedding guide](docs/EMBEDDING.md) |
| Declare shared docs and validation | [Field metadata](docs/FIELD_METADATA.md) |
| Bundle a cross-compiled binary in Python | [Wheel packaging](docs/HELLO_WORLD.md#7-ship-the-binary-as-python-package-data) |
| Follow remaining work | [Implementation plan](docs/PLAN.md) |

The wheel recipe targets Linux, macOS and Windows on amd64/arm64 with
`CGO_ENABLED=0`. Libraries using cgo need target C toolchains. Linux wheels use
`linux_*` tags for local/private distribution; public manylinux/musllinux
compatibility and release automation remain planned work.
