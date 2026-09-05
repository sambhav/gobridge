# API guide

Start with the [quick start](../README.md#quick-start). This guide covers
registration, development, state ownership, embedding, and transport limits.

## Expose your Go library

Apply the comments from the quick start to an existing importable Go package. Native Go consumers
continue to call your functions and constructors directly; the generated adapter
only adds the CLI and Python/TypeScript interfaces. You can add
`//go:generate gobridge generate --dir .` for standard `go generate` workflows.

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


## Develop without rebuilding by hand

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


## Share state deliberately

Python operations are real `async def` functions. Synchronous `_sync` functions
and `SyncGreeter` call the transport directly and reject calls from a running
event loop. Both interfaces share the same module default.

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


## Use TypeScript

Node 24+ packages expose Promise-returning functions, classes and typed options:

```ts
import { greet, Greeter } from "greeter";

console.log(await greet({ name: "World" }));
await using greeter = new Greeter({ prefix: "Hey, " });
console.log(await greeter.welcome({ name: "Sam" }));
```

Use `try/finally` with `await greeter.close()` without async disposal. Methods and
nested fields use camelCase. Readonly interfaces describe plain JavaScript values;
they do not deep-freeze results. Worker threads and child processes create their
own clients. Pass configuration rather than live clients to workers.

Optional lifecycle functions have the same names as Python:

```ts
import { configure, session, shutdown, welcome } from "greeter";

configure({ prefix: "Default: " });
await session({ prefix: "Scoped: " }, async greeter => {
  console.log(await welcome({ name: "Sam" }));
});
await shutdown();
```

Sessions use AsyncLocalStorage; finish child tasks before returning. The runtime
ships ESM JavaScript and declarations with no production dependencies. Browsers
cannot spawn local processes and are outside this transport's scope.


## Embed only the daemon in Cobra

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
from greeter import Greeter, RuntimeOptions

async with Greeter(prefix="Hey, ", _runtime=RuntimeOptions(
    command=["./host", "bridge"],
)) as greeter:
    print(await greeter.welcome(name="Sam"))
```

TypeScript uses `_runtime: { command: ["./host", "bridge"] }`. Both append `serve`
without shell parsing. Generate bindings from the same registry at build time;
the shipped host only needs the daemon command. The complete
[Cobra example](../examples/cobra/main.go) covers stream ownership and cancellation.
It is a separate Go module so the core library has no Cobra dependency.


## Tune behavior

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
[Contributing](../CONTRIBUTING.md).
