# gobridge

Write a Go library. Use it from Go, the CLI, Python, and TypeScript.
Generated packages include the Go executable and start it when first needed.
Your users import functions or create objects; Go owns the work and reusable state.

```go
//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }
```

```python
from greeter import greet

message = await greet(name="World")  # "Hello, World!"
```

**Development preview:** nothing is published yet. This is the single user guide;
follow it until you have what you need. The async-first Python API and dev command
below are the next documented increment on the draft PR.

1. [Try a function](#1-try-a-function)
2. [Call from Python](#2-call-from-python)
3. [Add options and state](#3-add-options-and-state)
4. [Expose your Go library](#4-expose-your-go-library)
5. [Develop without rebuilding by hand](#5-develop-without-rebuilding-by-hand)
6. [Share state deliberately](#6-share-state-deliberately)
7. [Use TypeScript](#7-use-typescript)
8. [Ship a package](#8-ship-a-package)
9. [Embed only the daemon in Cobra](#9-embed-only-the-daemon-in-cobra)
10. [Tune behavior](#10-tune-behavior)

## 1. Try a function

Development requires Go 1.23+ and Python 3.10+. Clone this branch, then run:

```sh
git clone --branch feat/go-cli-python https://github.com/sambhav/gobridge.git
cd gobridge
python -m pip install -e ./python
go generate ./examples/greeter
go run ./examples/greeter/cmd/greeter greet --name World
```

The last command prints `"Hello, World!"`. Explore the CLI with `--help` or
`welcome --help`. Operation help shows argument types, validation and constructor
options. Calls print JSON to stdout; errors and logs go to stderr. JSON input
also works: `greet --json '{"name":"World"}'`, or `--json -` to read stdin.

## 2. Call from Python

Build a local importable package into `build/greeter`:

```sh
go run ./cmd/gobridge dev --once
```

Create `build/app.py` so Python finds the package beside your script:

```python
import asyncio
from greeter import greet

async def main():
    print(await greet(name="World"))

asyncio.run(main())
```

Run `python build/app.py`. In an async application or notebook, use `await` in
its existing event loop. Importing starts no process. Calls reuse one private Go
daemon, and normal process exit cleans it up. A packaged wheel has the same import
experience without requiring a Go toolchain.

**Python is async by default.** Every operation is a real `async def` with typed,
keyword-only arguments. Python yields while Go works, so calls can overlap:

```python
messages = await asyncio.gather(greet(name="Sam"), greet(name="Chhavi"))
```

For a synchronous script, import the generated `_sync` version:

```python
from greeter import greet_sync

print(greet_sync(name="World"))
```

It calls the synchronous transport directly. It never creates or nests an event
loop and rejects calls from an already-running loop before sending an operation.
Both versions share the same default Go state. There is no `aio` namespace or
function whose return type changes depending on its caller. Use
[Python's normal runner](https://docs.python.org/3/library/asyncio-runner.html)
when your whole application is async.

## 3. Add options and state

A Go constructor becomes a Python constructor. Each instance owns an independent
Go object and daemon:

```python
from greeter import Greeter

async with Greeter(prefix="Hey, ") as greeter:
    print(await greeter.welcome(name="Sam"))  # Hey, Sam
    print((await greeter.stats()).calls)      # 1
    await greeter.reset()
```

Results are normal scalars or frozen dataclasses. Void Go methods return `None`.
There is no required Pydantic dependency. Keep a client alive for useful work;
creating a process per call is expensive.

Synchronous applications use `SyncGreeter` and ordinary `with`:

```python
from greeter import SyncGreeter

with SyncGreeter(prefix="Hey, ") as greeter:
    print(greeter.welcome(name="Sam"))
```

The CLI supplies the same options before an operation:

```sh
go run ./examples/greeter/cmd/greeter \
  --config '{"prefix":"Hey, "}' welcome --name Sam
```

## 4. Expose your Go library

Keep your library in an importable Go package. Add `//gobridge:export` to functions
or methods you want to expose and `//gobridge:constructor` to a constructor:

```go
type Options struct {
    Prefix *string `json:"prefix,omitempty"`
}

//gobridge:constructor
func NewGreeter(options Options) (*Greeter, error) {
    // Apply your library's defaults or functional options here.
    return newGreeter(options), nil
}

//gobridge:export
func (g *Greeter) Welcome(ctx context.Context, name string) (string, error) {
    return g.welcome(ctx, name)
}
```

The bodies above stand in for your library's implementation; the
[complete example](examples/greeter/greeter.go) includes a concurrent counter.
Install the generator with `go install ./cmd/gobridge`, put Go's binary directory
on `PATH`, and add `//go:generate gobridge generate --dir .` to your library.
Run `go generate ./...` in your project.

Generation creates `NewGobridge() (*gobridge.Registry, error)` in the same package.
A [small command package](examples/greeter/cmd/greeter/main.go) calls it,
checks the error, and runs `registry.Main()`. Native Go consumers import the
library and call `Greet` or `NewGreeter` directly in their own process.

| Declaration | Behavior |
| --- | --- |
| `//gobridge:export` | Expose a function/method using its source parameter names. |
| `//gobridge:export custom_name` | Choose the operation's name. |
| `//gobridge:constructor` | Initialize one object per daemon from a named options struct. |
| Leading `context.Context` | Runtime supplies deadlines and cancellation. |
| `T`, `(T, error)`, `error`, or no return | Typed result or exception; void becomes `None`/`undefined`. |

Only annotated declarations are exposed. Names become snake_case in Python and
camelCase in TypeScript. Constructors never run to generate bindings or help.
Unsupported signatures and name collisions fail generation, including operations
that would shadow generated `_sync` helpers. One constructor per registry is supported.
The generator scans one package, respects its build constraints and never overwrites
handwritten output. Use `gobridge generate --dir . --check` to check adapter drift.

For direct registration:

```go
registry := gobridge.New()
err := gobridge.Bind(registry, "greet", Greet, "name")
```

Check registration errors during setup. Reflection cannot recover source argument
names, so `Bind` supplies them explicitly. `NewObject(registry, NewGreeter)` plus
`object.Bind("welcome", (*Greeter).Welcome, "name")` registers an object directly.
`Register` accepts typed request/response functions when you already have request
structs and want a direct Go invocation path.

Keep build settings in `gobridge.json` at your project's root:

```json
{
  "name": "greeter",
  "source": ".",
  "command": "./cmd/greeter"
}
```

`name` supplies the Python import and binary name; the client class defaults to
`Greeter`. `source` selects the annotated library package; omit it for manual
registration. `command` selects the Go executable. Optional `class`, `version`,
`python_distribution`, and `npm_package` customize generated artifacts. This
checkout includes a manifest pointing at its example, so the commands below work
without extra flags. Installed authors use `gobridge` instead of `go run ./cmd/gobridge`.

## 5. Develop without rebuilding by hand

Run the dev command without `--once` to watch source changes. Add your Python
command after `--` to restart it after successful updates:

```sh
go run ./cmd/gobridge dev -- python build/app.py
```

The loop watches Go/Python source and Go module files beneath the current working
directory. Go edits regenerate adapters, build the executable and regenerate
Python bindings. Omit `--dir` for manual registration. Python-only edits restart
the application without rebuilding Go. Output and dependency directories are ignored.

Each successful build writes a binary under a content-derived filename, then
atomically publishes bindings pointing to that exact binary. Old imports keep
using their original binary; new imports get the new pair. Build failures leave
the last working package and application intact. Handwritten packages are never
overwritten. The application's `PYTHONPATH` includes the generated package's parent.

The watcher restarts its application rather than mutating live imported modules
or moving state between daemons. Stop it with Ctrl-C. Old binaries remain available
for existing clients; remove generated output when all development clients have
stopped. Restart the command after changing project settings. Packaged releases
use fixed artifacts.

## 6. Share state deliberately

Most applications need only imported functions or explicit clients:

| Usage | Go state |
| --- | --- |
| Imported async and sync functions | One lazy default per generated module and Python process. |
| Threads/tasks sharing a client | Shared object and cache. |
| Separate clients | Independent objects and daemons. |
| Multiprocessing workers | Fresh daemons; parent pipes and Go state are never transferred. |
| Session block | Temporary isolated state, restored on exit. |

Set default options before the first call with `configure(prefix="Default: ")`.
For temporary options on imported functions:

```python
from greeter import session, welcome

async with session(prefix="Scoped: ") as greeter:
    print(await welcome(name="Sam"))
    print((await greeter.stats()).calls)
```

`session_sync(...)` uses `with` and yields `SyncGreeter`. Nested sessions restore
the previous client. Concurrent async tasks stay isolated; child tasks inherit
their session and should finish before it exits. Pass an explicit client to new
threads when they need scoped state. Session options do not inherit defaults.

`await shutdown()` closes and resets the module default; `shutdown_sync()` does
the same in synchronous code. Do this before reconfiguring an existing default.
Closing an explicit client never reopens it. Crashes and failed constructors are
never silently retried or replayed.

Pickles contain client configuration, not pipes, locks, pending work or Go state.
Prefer `spawn` or `forkserver` with active threads. Fork hooks protect gobridge's
resources; they cannot repair locks in unrelated libraries or native extensions.

## 7. Use TypeScript

Node 24+ packages expose Promise-returning functions, classes and typed options:

```ts
import { greet, Greeter } from "gobridge-greeter-example";

console.log(await greet({ name: "World" }));
await using greeter = new Greeter({ prefix: "Hey, " });
console.log(await greeter.welcome({ name: "Sam" }));
console.log((await greeter.stats()).calls); // 1n: Go int64 is bigint
```

Use `try/finally` with `await greeter.close()` without async disposal. Methods and
nested fields use camelCase. Readonly interfaces describe plain JavaScript values;
they do not deep-freeze results. Worker threads and child processes create their
own clients. Pass configuration rather than live clients to workers.

Optional lifecycle functions have the same names as Python:

```ts
import { configure, session, shutdown, welcome } from "gobridge-greeter-example";

configure({ prefix: "Default: " });
await session({ prefix: "Scoped: " }, async greeter => {
  console.log(await welcome({ name: "Sam" }));
});
await shutdown();
```

Sessions use AsyncLocalStorage; finish child tasks before returning. The runtime
ships ESM JavaScript and declarations with no production dependencies. Browsers
cannot spawn local processes and are outside this transport's scope.

## 8. Ship a package

Bundle cross-compiled executables with generated bindings. Consumers need only
Python or Node. These reference recipes build Linux/macOS/Windows on amd64/arm64
with `CGO_ENABLED=0`.

Build wheels with one command:

```sh
python -m pip install setuptools wheel
go run ./cmd/gobridge build --python
python -m pip install --no-index --find-links dist gobridge-greeter-example
```

Build npm packages, or both formats together:

```sh
go run ./cmd/gobridge build --typescript
go run ./cmd/gobridge build --python --typescript
```

The builder reads `gobridge.json`, regenerates Go adapters, cross-compiles the
command and generates bindings from its actual schema. It stages the matching
runtime from the project's Go module version, so it also works outside this
checkout without locating repository scripts. Wheels go to `dist/`; npm tarballs
go to `dist/npm/`. `--output`, `--version`, and
`--targets linux-amd64,darwin-arm64` override the defaults. All six targets are
built by default; no language flag means Python.

Python builds require Python 3.10+, setuptools and wheel. TypeScript builds also
require Node 24+ and npm; the builder installs pinned compiler tools in temporary
staging. Consumers need no compiler, Go toolchain or install script. Pip selects
the matching wheel. An npm package contains its selected binaries under
`_bin/<platform>-<arch>`:

```sh
npm install --offline --ignore-scripts \
  ./dist/npm/gobridge-runtime-0.1.0.tgz \
  ./dist/npm/gobridge-greeter-example-0.1.0.tgz
```

Artifacts are local until you explicitly publish them to your registry. Linux
wheels currently use generic `linux_*` tags for local/private distribution;
public manylinux/musllinux certification is pending. Libraries requiring cgo
need target C toolchains and a different recipe.

For your own build system, generate bindings directly:

```sh
go build -o bin/greeter ./examples/greeter/cmd/greeter
bin/greeter generate-python --class Greeter --binary greeter > greeter.py
bin/greeter generate-typescript --class Greeter --binary greeter > greeter.ts
```

Windows uses `bin/greeter.exe`. Commit generated source and check drift in CI.
The runtime checks that the executable's schema matches its bindings.

## 9. Embed only the daemon in Cobra

Your host can mount only `serve`, including beneath `host bridge serve`:

```go
bridge := &cobra.Command{Use: "bridge", Args: cobra.NoArgs,
    RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}
bridge.AddCommand(&cobra.Command{
    Use: "serve", Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
    RunE: func(cmd *cobra.Command, args []string) error {
        return registry.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), 64)
    },
})
root.AddCommand(bridge)
```

`Serve` returns errors and never exits the host. Cobra owns parsing, hooks, help
and other commands. Set protocol output to stdout and diagnostics to stderr;
startup hooks must follow that rule too. Supply the context with `ExecuteContext`.
Cancellation must also unblock the input reader: a host owning stdin can close
it with `context.AfterFunc`. Borrowed streams retain their owner's shutdown
mechanism. EOF cancels the session.

Select the host's command prefix with optional transport settings:

```python
from greeter import Greeter
from gobridge import RuntimeOptions

async with Greeter(prefix="Hey, ", _runtime=RuntimeOptions(
    command=["./host", "bridge"],
)) as greeter:
    print(await greeter.welcome(name="Sam"))
```

TypeScript uses `_runtime: { command: ["./host", "bridge"] }`. Both append `serve`
without shell parsing. Generate bindings from the same registry at build time;
the shipped host only needs the daemon command. The complete
[Cobra example](examples/cobra/main.go) covers stream ownership and cancellation.
It is a separate Go module so the core library has no Cobra dependency.

## 10. Tune behavior

Supported values are strings, booleans, signed integers, finite floats, named
structs, slices, string-keyed maps and pointers. Struct fields need explicit
`json` names. Pointer inputs are optional/nullable; slices/maps are required but
can be null. TypeScript omits absent `omitempty` pointer properties. Custom
marshalers, recursive types, interfaces, variadic functions and multiple non-error
results need adapters.

| Go type | Python | TypeScript |
| --- | --- | --- |
| `int8` / `int16` / `int32` | `int` within Go range | `number` within Go range |
| `int` | `int` within target Go range | Safe integer `number` |
| `int64` | Exact `int` | Exact `bigint`, even for small values |
| `float32` / `float64` | Finite `float` | Finite `number` |
| Named struct | Frozen dataclass | Readonly interface |

Use Go `int64` when Node needs the full range. Unsafe Numbers fail explicitly.
Bigints cross the existing JSON protocol as exact numeric literals; application
`JSON.stringify` still needs its own bigint-aware serializer.

Declare shared docs and validation beside fields:

```go
type Request struct {
    Name string `json:"name" doc:"Name to greet." validate:"minlen=1,maxlen=80"`
    Age  *int   `json:"age,omitempty" validate:"min=0,max=120"`
}
```

Bounds are inclusive; string lengths count Unicode code points. Length rules also
apply to slices/maps. Constraints validate non-null values without changing
nullability. Invalid tags fail registration. Go validates inputs for every
transport; Python metadata and TypeScript JSDoc expose the same rules. Defaults
belong in the Go constructor. Native callers retain the library's own validation.

Python calls accept `_timeout=2.0` in seconds. TypeScript uses a separate final
`{ timeoutMs: 2000, signal: abort.signal }` argument. Canceling a Python async task
propagates cancellation to Go. Handlers must honor their context. Typed errors
cover invalid arguments, busy admission, timeout and daemon failure. Operations
are never replayed after uncertain outcomes.

Client-wide Python settings use `_runtime=RuntimeOptions(timeout=5,
startup_timeout=5, max_pending=64)`. Node uses `timeoutMs`, `startupTimeoutMs`, and
`maxPending`. Startup has a separate budget for handshake and initialization.
Frames are limited to 1 MiB; pending work and write queues are bounded.

Protect mutable Go receivers as you would for goroutines. The optional `Memo`
cache supplies bounded TTL/LRU storage and coalesces loads per key. One waiter's
cancellation leaves other waiters running; the last cancellation stops the loader
context. Errors are not cached. Keep cached reference-containing values immutable
or copy them. Reuse clients and batch useful work to amortize IPC.

For test commands, protocol details, measurements and release work, see
[Contributing](CONTRIBUTING.md).
