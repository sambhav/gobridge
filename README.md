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

Install a generated package to use it, or add gobridge to your Go project to
build your own. Python users need Python 3.10+; Node users need Node 24+.
Only library authors need Go.

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

Install the complete example from PyPI:

```sh
python -m pip install gobridge-greeter-example
python -c 'from greeter import greet_sync; print(greet_sync(name="World"))'
```

This prints `Hello, World!`. Pip installs the Python runtime dependency and the
wheel for your platform, including its Go executable. There is no separate binary
download, Go installation or post-install build. The distribution name is
`gobridge-greeter-example`; the Python import is `greeter`.

For Node, use `npm install gobridge-greeter-example`, then follow
[the TypeScript example](#7-use-typescript).

## 2. Call from Python

Create `app.py`:

```python
import asyncio
from greeter import greet

async def main():
    print(await greet(name="World"))

asyncio.run(main())
```

Run `python app.py`. In an async application or notebook, use `await` in
its existing event loop. Importing starts no process. Calls reuse one private Go
daemon, and normal process exit cleans it up. No process starts until you make the first call.

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
go run ./cmd/greeter \
  --config '{"prefix":"Hey, "}' welcome --name Sam
```

## 4. Expose your Go library

Start in your Go module. Install gobridge and its author command:

```sh
go get github.com/sambhav/gobridge@latest
go install github.com/sambhav/gobridge/cmd/gobridge@latest
python -m pip install 'gobridge-runtime[build]'
```

Use a Python virtual environment for development, and put Go's binary directory
on `PATH`. Go 1.23+ is supported. The `build` extra installs the wheel-building
tools; generated packages depend only on the small runtime.

For a new example, run `go mod init example.com/greeter` before `go get`. Put this
in `greeter.go`:

```go
package greeter

//go:generate gobridge generate --dir .

//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }
```

Create `cmd/greeter/main.go` (use your actual module import path):

```go
package main

import greeter "example.com/greeter"

func main() {
    registry, err := greeter.NewGobridge()
    if err != nil { panic(err) }
    registry.Main()
}
```

Add `gobridge.json` at the module root:

```json
{
  "name": "greeter",
  "source": ".",
  "command": "./cmd/greeter",
  "version": "0.1.0"
}
```

Run `gobridge dev --once`. This generates the Go adapter and an importable Python
package in `build/greeter`. `gobridge dev -- python app.py` also places that package
on Python's import path and restarts your app on edits. Your native Go consumers
continue to call `greeter.Greet(...)` directly.

Run `go run ./cmd/greeter greet --name World` for the CLI. Use `--help` or
`greet --help` for types and argument help. Operations print JSON to stdout;
diagnostics go to stderr. `--json '{"name":"World"}'` and `--json -` also work.

Add `//gobridge:constructor` to a constructor taking a named options struct, and
`//gobridge:export` to its methods to get the classes shown earlier. The
[complete greeter source](examples/greeter/greeter.go) demonstrates defaults,
functional options, methods and concurrent state.

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

In `gobridge.json`, `name` supplies the import and binary name; the class defaults
to `Greeter`. `source` selects the library package; omit it for manual registration.
`command` selects the Go executable. Optional `class`, `python_distribution`,
`npm_package`, `repository`, and `license` customize package metadata. Distribution
names can differ from the import, for example `acme-greeter` and `@acme/greeter`.

## 5. Develop without rebuilding by hand

Run the dev command without `--once` to watch source changes. Add your Python
command after `--` to restart it after successful updates:

```sh
gobridge dev -- python app.py
```

The loop watches Go/Python source and Go module files beneath the current working
directory. Go edits regenerate adapters, build the executable and regenerate
Python bindings. Omit `source` from the manifest for manual registration. Python-only edits restart
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

From your Go project, build the formats you need:

```sh
gobridge build --python
gobridge build --typescript
# Or build both:
gobridge build --python --typescript
```

That is the complete build step. It reads `gobridge.json`, regenerates adapters,
cross-compiles the executable and packages typed bindings beside it. Python build
tools come from the `build` extra installed earlier. TypeScript builds additionally
need Node 24+ and npm; the compiler is installed in temporary staging.

| Output | What you publish | What consumers install |
| --- | --- | --- |
| `dist/*.whl` | Your Python wheels, one per platform | `pip install acme-greeter` |
| `dist/npm/*.tgz` | Your npm package, containing selected platforms | `npm install @acme/greeter` |

The names above illustrate `python_distribution` and `npm_package`. Only your
packages appear in the output; their exact gobridge runtime dependency is installed
from the registry automatically. You do not rebuild or republish gobridge itself.
For an offline bundle, opt into `--include-runtime` and install all the artifacts
from that directory.

All six Linux/macOS/Windows × amd64/arm64 targets are built by default, with
`CGO_ENABLED=0`. Linux binaries are checked for static linkage before receiving
manylinux/musllinux wheel tags. Libraries requiring cgo need their own target build
recipe. Use `--targets linux-amd64,darwin-arm64` to select fewer targets,
`--output` to change the directory, and `--version 0.2.0` to override the manifest.

Test locally with `pip install --find-links dist acme-greeter` or
`npm install ./dist/npm/acme-greeter-0.1.0.tgz`. Then use your usual registry tools:

```sh
python -m pip install twine
python -m twine upload dist/*.whl
npm publish ./dist/npm/acme-greeter-0.1.0.tgz --access public
```

Configure [PyPI trusted publishing](https://docs.pypi.org/trusted-publishers/)
and [npm trusted publishing](https://docs.npmjs.com/trusted-publishers/) in your own
repository to publish from GitHub without ongoing API tokens. A first npm
publication still needs an authenticated account. Package names are claimed on
first successful publication, subject to registry rules.

**Releasing gobridge itself:** versioning is coordinated across Go, Python, npm
and the example. Normal merges update an automatic release PR. Merge that PR in
GitHub to tag the Go module, build and test the release, and publish packages to
PyPI/npm. See [maintainer release setup](CONTRIBUTING.md#releases-from-github) for
the one-time registry connection and the GitHub UI retry path.

For custom build systems, the executable also supports `generate-python` and
`generate-typescript`. Generate from the same registry shipped in your binary;
the runtime checks its schema against the generated bindings.

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
