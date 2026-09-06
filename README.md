# gobridge

Expose ordinary Go libraries as a CLI and typed Python/TypeScript packages.
Generated packages bundle the Go executable and a private runtime. Calls reuse
one local subprocess per client, keeping objects and caches alive between calls.

This README is the complete user guide. [Contributing](CONTRIBUTING.md) covers
maintainer workflows and internals; [benchmarks](docs/performance.md) record
performance measurements and their reproduction commands.

[Quick start](#quick-start) · [Configuration](#configuration) ·
[Names](#names-in-go-source) · [Constructors](#constructors-and-functional-options) ·
[Build and publish](#build-and-publish) · [Development](#development) ·
[State and sessions](#share-state-deliberately) · [Types](#types-validation-and-runtime-settings) ·
[Streaming and batches](#streaming-and-batches) · [Errors and logging](#errors-and-observability) ·
[Manual registration](#manual-registration) · [Migration](#breaking-api-changes)

## Quick start

Authors need Go 1.23+ and Python 3.10+; TypeScript builds also need Node 24+ and
npm. Consumers need only Python 3.10+ or Node 24+. No Go installation, standalone
runtime package, network service, or browser transport is required.

```sh
go install github.com/sambhav/gobridge/cmd/gobridge@latest
gobridge init --dir greeter --module example.com/greeter --name acme.greeter --npm-package @acme/greeter
cd greeter
go mod tidy
gobridge dev -- python app.py
# Or: gobridge dev --typescript -- node app.mts
```

`init --check` previews the files without writing them. Add Go's binary directory
to your `PATH` if `gobridge` is not found. Scaffolding creates a normal Go module,
an annotated library, a command entrypoint, `gobridge.json`, and runnable apps.
For an existing project, install `github.com/sambhav/gobridge` with `go get` and
add the same library/entrypoint structure. Generated adapters stay in the library
package; native Go callers continue using the ordinary Go API.

```go
package bridge

//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }
```

A command such as `cmd/bridge/main.go` serves that registry:

```go
package main

import bridge "example.com/greeter/bridge"

func main() {
    registry, err := bridge.NewGobridge()
    if err != nil { panic(err) }
    registry.Main()
}
```

`gobridge generate --dir bridge` writes the adapter, and `--check` checks for drift.
`dev` and `build` regenerate it automatically. Generation respects host Go build
constraints and never overwrites a handwritten file. Only annotated declarations
are exposed; one constructor per registry is supported.

```python
import asyncio
from acme.greeter import greet, greet_sync

async def main():
    print(await greet(name="World"))

asyncio.run(main())
# In a synchronous script: print(greet_sync(name="World"))
```

```typescript
import { greet } from "@acme/greeter";
console.log(await greet({name: "World"}));
```

The same Go command provides a CLI:

```sh
go run ./cmd/bridge greet --name World
go run ./cmd/bridge greet --help
go run ./cmd/bridge greet --json '{"name":"World"}'
```

CLI results are JSON on stdout; diagnostics use stderr. `--json -` reads stdin.
Go errors become exceptions. Supported function returns are `T`, `(T, error)`,
`error`, or no result; void becomes Python `None` or TypeScript `undefined`.
A leading `context.Context` receives cancellation and deadlines automatically.

## Configuration

There is one `gobridge.json` format for one or many modules:

```json
{
  "version": "0.1.0",
  "python": {"distribution": "acme-sdk"},
  "typescript": {"package": "@acme/sdk"},
  "modules": [
    {
      "name": "greeter",
      "source": "./bridge",
      "command": "./cmd/bridge",
      "python": {"module": "acme.greeter", "class": "Greeter"},
      "typescript": {"export": ".", "class": "Greeter"}
    },
    {
      "name": "catalog",
      "source": "./catalog",
      "command": "./cmd/catalog",
      "python": {"module": "acme.catalog"},
      "typescript": {"export": "./catalog"}
    }
  ]
}
```

Include only modules present in your project. The second entry demonstrates
multiple modules; the scaffold creates a single entry. A module owns its command,
clients, bundled executable, and default lifecycle. Multiple modules can wrap
the same command with different names. All modules ship in one wheel per target
and one npm tarball. The complete [multi-module example](examples/modules/gobridge.json)
can be built from its directory with `go run ../../cmd/gobridge build --python --typescript`.

| Setting | Meaning/default |
| --- | --- |
| `version` | Application version; defaults to `0.1.0`. |
| `repository`, `license` | Distribution metadata. |
| `python.distribution` | pip/PyPI name; defaults to the first Python module path with dots/underscores replaced by hyphens. |
| `python.requires` | Application dependencies using names, extras, and version comparisons. |
| `typescript.package` | npm name, optionally `@scope/name`; same derived default as Python distribution. |
| `typescript.dependencies` | Application npm dependency map. |
| `modules[].name` | Required unique identifier used by `dev --module`. |
| `modules[].source` | Directory scanned for annotations; omit for manual registration. |
| `modules[].command` | Required Go main package. |
| `modules[].command_prefix` | Argument array locating bridge commands inside an existing binary, e.g. `["bridge"]`. |
| `modules[].python.module` | Python import path; defaults to module name. |
| `modules[].typescript.export` | npm export path, `.` or `./subpath`; defaults to module name with dots changed to slashes and prefixed by `./`. |
| `modules[].python.class`, `modules[].typescript.class` | Explicit generated class names; otherwise source annotations or a name derived from the Python import path. |
| `modules[].python.package`, `modules[].typescript.package` | Optional directories containing handwritten wrappers/assets. |
| `modules[].python.rename`, `modules[].typescript.rename` | Per-language operation, type, and field rename maps. |

Python dotted paths create PEP 420 namespace parents, which remain free of
`__init__.py` unless another configured module supplies one. The module leaf
contains typed bindings, `py.typed`, its private runtime, and its executable.
TypeScript ESM exports include JavaScript and declarations. Distribution names,
Python imports, npm export paths, and client class names are independent.

## Names in Go source

```go
//gobridge:python Product
//gobridge:ts ProductRecord
type Item struct {
    ItemID string `json:"item_id" python:"id" ts:"productID"`
}

//gobridge:export lookup
//gobridge:python find
//gobridge:ts findProduct
func Lookup(itemID string) Item { /* ... */ }
```

`//gobridge:export` determines the wire operation name. `json` tags determine wire
field names. Language annotations and `python`/`ts` tags affect generated public
names only; encoding and decoding preserve the Go JSON contract, including
nested structs and containers. Put language annotations on a constructor or its
receiver struct to name the generated client class. Put them on other model
structs to name generated dataclasses and TypeScript interfaces.

Set rename maps in a module's language settings, for example:

```json
{"python": {"rename": {"operations": {"lookup": "find"}, "types": {"Item": "Product"}, "fields": {"Item.item_id": "id"}}}}
```

Module rename maps override source annotations and tags. Operation map keys are
wire names. Type keys are Go type names, optionally qualified as
`example.com/project/catalog.Item`. Field keys use `Item.item_id` or `Item.ItemID`,
also optionally qualified. Bound function parameters use the generated input
model name, such as `LookupParams.item_id`. Unknown map keys, empty overrides,
invalid identifiers, reserved names, and public-name collisions fail generation.
Renaming does not change the wire schema hash.

Manual registries and generated adapters support composable Go options:

```go
r := gobridge.New(
    gobridge.WithPython(gobridge.Names{
        Class: "CatalogClient",
        Operations: map[string]string{"lookup": "find"},
    }),
)

// The generated adapter accepts the same options, overriding annotations.
r, err := catalog.NewGobridge(
    gobridge.WithTypeScript(gobridge.Names{
        Types: map[string]string{"Item": "Product"},
    }),
)
```

Options merge left to right and copy their maps. Per-generation options can also
be passed to `GeneratePython` and `GenerateTypeScript`; these override registry
options without mutating the registry. A configured language class overrides
the generator's fallback class argument. No separate builder is needed.

## Constructors and functional options

A constructor creates one Go object per client. For a simple config struct:

```go
type Config struct { Prefix *string `json:"prefix,omitempty"` }
type Greeter struct { prefix string }

//gobridge:constructor
func NewGreeter(config Config) *Greeter {
    prefix := "Hello, "
    if config.Prefix != nil { prefix = *config.Prefix }
    return &Greeter{prefix: prefix}
}

//gobridge:export
func (g *Greeter) Welcome(name string) string { return g.prefix + name }
```

```python
from acme.greeter import Greeter, SyncGreeter

async with Greeter(prefix="Hey, ") as client:
    print(await client.welcome(name="Sam"))

# In synchronous code:
# with SyncGreeter(prefix="Hey, ") as client:
#     print(client.welcome(name="Sam"))
```

The CLI accepts constructor data before the operation:
`--config '{"prefix":"Hey, "}' welcome --name Sam`. Defaults belong in Go.

Annotate the constructor and the option factories you want to expose:

```go
type Option func(*Catalog)

//gobridge:constructor
func New(options ...Option) *Catalog { /* apply defaults and options */ }

//gobridge:option endpoint
//gobridge:python base_url
//gobridge:ts baseURL
func WithEndpoint(endpoint string) Option { /* ... */ }

//gobridge:option retries
func WithRetries(retries int) (Option, error) { /* ... */ }
```

The resulting API uses native constructor arguments:

```python
async with Catalog(base_url="https://api.example", retries=0) as catalog:
    result = await catalog.lookup(item_id="123")
```

```typescript
const catalog = new Catalog({baseURL: "https://api.example", retries: 0});
try {
  const result = await catalog.lookup({itemId: "123"});
} finally {
  await catalog.close();
}
```

Omission (or Python `None`/JSON `null`) skips that factory and preserves the Go
default. Zero, false, and empty strings are explicit values. Factories run in
source declaration order (files sorted by name), regardless of keyword order.
Neither factories nor the constructor run while generating schemas or bindings.
Factories and constructors may return errors; those errors reach the client.

Supported constructors take `...Option`, optionally preceded by
`context.Context`, and return `*T` or `(*T, error)`. Factories return `Option` or `(Option, error)`. A single parameter stays a scalar
(or its existing wire model). Multiple parameters generate a grouped model:

```go
//gobridge:option retry
func WithRetry(attempts int, delayMs int) (Option, error) { /* ... */ }
```

```python
Catalog(retry=RetryOptions(attempts=3, delay_ms=100))
```

```typescript
new Catalog({retry: {attempts: 3, delayMs: 100}})
```

The generator reads parameter names from Go declarations. The group is optional;
inside a supplied group, non-pointer parameters are required, including explicit
zero/false values. Pointer parameters retain the usual nullable/optional behavior.
Values are passed to the factory in Go declaration order. Generated group names
use the option's wire name (`retry` becomes `RetryOptions`) and support the same
module type/field rename maps, e.g. `RetryOptions.delay_ms`. Use a slice for
repeated values. Arbitrary callbacks, overloaded options, and fluent builder methods need
an explicit Go adapter. Pointer-valued options cannot distinguish an omitted
keyword from explicitly passing null.

Manual registration uses the same adapter:

```go
object, err := gobridge.NewObject(r, New,
    gobridge.ConstructorOption("endpoint", WithEndpoint),
    gobridge.ConstructorOption("retries", WithRetries),
    gobridge.ConstructorOption("retry", WithRetry, "attempts", "delay_ms"),
)
```

The same `NewObject` entrypoint handles config-struct constructors. See the
[complete catalog example](examples/catalog/catalog.go) for executable code.

## Build and publish

```sh
gobridge build --check --python --typescript
gobridge build --python --typescript
# One platform and a separate application version:
gobridge build --python --targets linux-amd64 --version 0.2.0 --output dist/0.2.0
```

With no language flags, `build` produces Python wheels. All six Linux/macOS/Windows
× amd64/arm64 targets build by default with `CGO_ENABLED=0`. Use a comma-separated
`--targets` list for fewer platforms. Libraries requiring cgo need their own target
recipe. Wheels are built with Python's standard library; npm builds also compile
TypeScript with the version bundled in the selected GoBridge toolchain.

`--check` prints a JSON plan without generating adapters or artifacts. It checks
configuration, target names, command packages, and required tool versions, but
does not promise application code will compile. `--version`, `--distribution`,
and `--npm-package` override distribution settings for this build; module layout
and naming belong in the manifest.

All formats and targets are built in staging. Artifacts are published locally
with a final `gobridge-build.json` completion manifest containing hashes and sizes.
Different same-version artifacts require `--replace`. Concurrent publishers to
one output directory are rejected; ordinary publication failures restore prior
files. After a forcibly terminated build, remove its lock/staging files only once
no builder remains. Existing versions are retained.

```sh
pip install --no-index --find-links dist acme-sdk
npm install ./dist/npm/acme-sdk-0.1.0.tgz
```

Consumers install your package, with no separate GoBridge runtime. `build` never
uploads artifacts. To publish tested application packages:

```sh
python -m pip install twine
python -m twine check --strict dist/*.whl
python -m twine upload dist/*.whl
npm publish ./dist/npm/acme-sdk-0.1.0.tgz --access public
```

Use a clean output directory per release and upload all supported wheels. Registry
names/scopes must belong to you. Publish the npm tarball, not the Go project root.
The repository's release workflow publishes GoBridge CLI binaries, not downstream
applications or example packages. Rebuild your packages after upgrading GoBridge.

### Prereleases

| Manifest/npm version | Python distribution version |
| --- | --- |
| `1.2.3-alpha.0` | `1.2.3a0` |
| `1.2.3-beta.1` | `1.2.3b1` |
| `1.2.3-rc.1` | `1.2.3rc1` |
| `1.2.3` | `1.2.3` |

Use lowercase `alpha`, `beta`, or `rc` with an explicit nonnegative sequence.
Leading zeros, `v` prefixes, Python-style input versions, arbitrary labels,
epochs, dev/post releases, and build/local metadata are rejected. Components must
not exceed `9007199254740991`. `--version` and build plans use the same rules.
Publish npm prereleases with `--tag next` to keep them off `latest`; consumers can
install a specific version explicitly with pip or npm.

## Wrappers, assets, and dependencies

Set a module's `python.package` or `typescript.package` to an additions directory
inside the project. Files are copied into that module, including data assets.
Symlinks, hidden files, and runtime/generated paths are rejected, as are collisions
between modules. There are no arbitrary build hooks. Declare shared dependencies
in `python.requires` and `typescript.dependencies`; Python dev environments need
those dependencies installed separately.

Python generates `_bindings.py` when a wrapper directory is present. Supply its
`__init__.py` to define your public API:

```python
from ._bindings import greet_sync

def friendly(name: str) -> str:
    return greet_sync(name=name)
```

Use `importlib.resources.files(__package__)` for packaged data. If no initializer
is supplied, GoBridge re-exports generated bindings automatically.

TypeScript generates `generated.ts` alongside wrapper sources. Supply `index.ts`:

```typescript
export * from "./generated.js";
import { greet } from "./generated.js";
export async function friendly(name: string) { return greet({name}); }
```

GoBridge compiles wrappers and emits declarations. If no entrypoint is supplied,
it re-exports generated APIs. Read assets relative to `import.meta.url`. Additional
public subpaths should be configured as modules; the project README/license ship
with the package.

## Development

```sh
gobridge dev -- python app.py
gobridge dev --typescript -- node app.mts
gobridge dev --module catalog --once
```

A single configured module is selected automatically. With several modules,
`--module NAME` is required. Python outputs go under `build/<import/path>`;
`--python` changes that destination, which must end in the configured import path.
TypeScript outputs use `node_modules/<typescript.package>` and retain the selected
module's configured export. Selecting another module replaces that development
package; `build` packages all modules together.

`--once` generates one revision without watching or running an app. Dev rejects
existing installed or handwritten output packages: remove them deliberately before
switching to development ownership. It adds generated Python paths for the app.

Go/embedded asset changes rebuild the selected module. Application source changes
restart the app; wrapper changes rebuild the package. Failed builds leave the last
working package and application running. Immutable revisions keep old imports
paired with their original runtime and binary. Dev restarts apps rather than
mutating live imports or transferring Go state. Stop with Ctrl-C and remove old
revisions only after clients have stopped.

The watcher scans beneath the project directory, excluding generated/dependency
outputs. Changes in external local `replace` modules require restart. Manifest
edits pause reloads and request restart. Run your own application compiler/watch
command if you need transpilation beyond Node's native TypeScript support.

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
from acme.greeter import session, welcome

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
import { greet, Greeter } from "@acme/greeter";

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
import { configure, session, shutdown, welcome } from "@acme/greeter";

configure({ prefix: "Default: " });
await session({ prefix: "Scoped: " }, async greeter => {
  console.log(await welcome({ name: "Sam" }));
});
await shutdown();
```

Sessions use AsyncLocalStorage; finish child tasks before returning. The runtime
ships ESM JavaScript and declarations with no production dependencies. Browsers
cannot spawn local processes and are outside this transport's scope.


## Manual registration

Use annotations for ordinary source packages. When registering dynamically or
embedding GoBridge in another tool, use the same registry directly:

```go
registry := gobridge.New()
if err := gobridge.Bind(registry, "greet", Greet, "name"); err != nil { return err }
object, err := gobridge.NewObject(registry, NewGreeter)
if err != nil { return err }
if err := object.Bind("welcome", (*Greeter).Welcome, "name"); err != nil { return err }
```

`Bind` requires explicit argument names because reflection cannot recover them.
`Describe` attaches help text to a registered operation. `Register[I,O]` is the
direct typed request/response path for named structs, avoiding reflective handler
invocation. It remains useful for measured hot paths. `NewObject` handles both
config-struct and functional-option constructors; pass `ConstructorOption`
entries for the latter. Check all registration errors before serving.

`Main` is the standalone command entrypoint. `Serve` embeds only the transport
without owning CLI parsing or process exit. Generated sync/async APIs and scoped
sessions represent different execution/lifecycle behavior; choose the one that
matches your application.

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
from acme.greeter import Greeter, RuntimeOptions

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


## Embedded generation and packaging

For an existing binary, mount `registry.Run` underneath a private subcommand and
forward its remaining arguments and streams. For example, a Cobra command can use:

```go
bridgeCommand := &cobra.Command{
    Use: "bridge", Hidden: true, DisableFlagParsing: true,
    RunE: func(cmd *cobra.Command, args []string) error {
        return registry.Run(cmd.Context(), args,
            cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
    },
}
root.AddCommand(bridgeCommand)
```

Set the module's `"command_prefix": ["bridge"]` in `gobridge.json`. Build and dev
invoke `host bridge generate-python` or `host bridge generate-typescript`; generated
clients launch `host bridge serve` automatically. Prefixes are argv arrays, never
shell strings. Each module may select a different subcommand in the same host.
Generation builds the host but never initializes the registered receiver.
Avoid running application startup side effects before routing tooling commands.

When generating directly in Go, pass `WithCommandPrefix("bridge")` to
`GeneratePython` or `GenerateTypeScript`. Explicit runtime `command` overrides
still supply the complete executable/prefix; the runtime appends `serve`.
Hosts that expose only `Serve` can keep doing so, but packaging needs generation
commands too. The preceding Cobra example demonstrates the narrower serving-only
mount; this mount exposes the registry's tooling and operation commands privately.

## Streaming and batches

A producer takes a leading context and a final `yield func(T) error` parameter,
then returns `error`. `Bind` recognizes this shape and source generation omits
`yield` from public inputs. Receiver methods work with `NewObject` too.

```go
//gobridge:export
func Count(ctx context.Context, n int, yield func(int64) error) error {
    for i := 0; i < n; i++ {
        if err := yield(int64(i)); err != nil { return err }
    }
    return nil
}
```

`RegisterStream[I, T](registry, name, description, producer)` provides the typed
registration path with a named input struct. Generated Python returns a typed
async iterator (or synchronous iterator from `SyncService`/`count_sync`), and
TypeScript returns `AsyncGenerator<T>`:

```python
from contextlib import aclosing

async with aclosing(client.count(n=100)) as items:
    async for item in items:
        print(item)
        if item == 5:
            break
```

```typescript
for await (const item of client.count({n: 100})) {
  console.log(item);
  if (item === 5n) break;
}
```

Use `contextlib.closing` for early exits from synchronous Python iterators.
Full exhaustion, explicit iterator closure, and session shutdown release the
producer. TypeScript `for await` closes on `break`; Python needs the closing scope.
Producers must honor context cancellation and stop on yield errors, including
while doing work between yields.

Streams use pull requests on the existing multiplexed transport: one item per
read, no growing item queue. Open streams are limited to the server's concurrency
limit; idle streams expire after 30 seconds. A single read is also limited to 30
seconds by that lease, even if its call timeout is longer. A canceled read closes
its stream. Each item retains the 1 MiB frame limit. This first implementation is
server-to-client streaming; it does not expose arbitrary channels, client streams,
or bidirectional streams. Errors can arrive after earlier items were delivered.

For a typed batch, construct descriptors through `client.calls` (also exported as
module-level `calls`). This uses the generated public names and model types:

```python
first = client.calls.lookup(id="first")
second = client.calls.lookup(id="second")
results = await client.abatch([first, second])
first_model = results.get(first)  # typed return value; raises this call's error
```

```typescript
const [first, second] = await client.batch([
  client.calls.lookup({id: "first"}),
  client.calls.lookup({id: "second"}),
]);
if (!first.error) console.log(first.result); // inferred generated model type
```

Descriptors snapshot their arguments immediately and do not start a subprocess.
Python's `BatchResults.get(descriptor)` preserves its result type; indexed entries
also expose `result` or a `BridgeError`. Create a separate descriptor for each
entry in a Python batch. Use `client.batch` in synchronous Python and `abatch` in
async Python; TypeScript `batch` returns a Promise. Streams have no batch descriptor.

Raw `{method, params}` dictionaries/objects remain available for dynamic callers;
these use stable wire names and return wire values. The Go equivalent is
`registry.Batch(ctx, calls)`. At most 128 unary calls execute in input order, in one
round trip. Individual errors allow subsequent calls to run. Cancellation prevents
remaining handlers from starting; response-budget overflow marks remaining calls
as unexecuted. Batches are not atomic and never roll back or replay effects.

## Errors and observability

Return `gobridge.Failure(code, message)` for intentional public errors. Use
`&gobridge.Error{Code: "invalid_argument", Message: "Unknown region",
Details: map[string]string{"field": "region"}}` when clients need structured
context. Wrapped bridge errors retain their code and details. Python and TypeScript
exceptions expose `.code`, `.message`, and `.details`; existing standard error
subclasses continue to work.

Ordinary Go errors become `internal: internal operation error` at the transport
boundary. Their original messages are available only to the host observer. Panic
responses are similarly generic. This is an intentional behavior change: explicitly
mark user-facing messages, including constructor/factory validation failures.

```go
registry, err := greeter.NewGobridge(
    gobridge.WithLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil))),
    gobridge.WithObserver(func(ctx context.Context, event gobridge.CallEvent) {
        // Update your metrics/traces here; event.Err is the original Go error.
    }),
)
```

Completion events include method, request ID, duration and status code. Unary
calls, constructor initialization and stream producer completion are observed.
`gobridge.RequestID(ctx)` lets handlers correlate their own logs. Stream duration
covers the producer lifetime; batch members share the enclosing request ID.
Observer callbacks must be concurrency-safe and nonblocking; panics are isolated.
No telemetry SDK dependency is required. Admission failures and malformed protocol
frames are not operation-completion events.

The logger emits successes at debug and failures at warning. It excludes arguments,
results, constructor data and raw errors. Enable debug on the handler when needed.
Keep logs on stderr: stdout is reserved for protocol frames. Client subprocesses
inherit stderr, so logs reach the parent application's normal logging destination.

## Types, validation, and runtime settings

Supported values are strings, booleans, signed/unsigned integers, finite floats,
named structs, fixed arrays, slices, string-keyed maps and pointers. Struct fields need explicit
`json` names. Pointer inputs are optional/nullable; slices/maps are required but
can be null. Bytes, timestamps, and durations have the explicit codecs described
below. TypeScript omits absent optional properties. Recursive types, embedded
struct fields, arbitrary interfaces, variadic functions and multiple non-error
results still need explicit wrappers. Custom marshalers can opt in using
`GobridgeWireType`, described below.

| Go type | Python | TypeScript |
| --- | --- | --- |
| `int8` / `int16` / `int32` | `int` within Go range | `number` within Go range |
| `int` | `int` within target Go range | Safe integer `number` |
| `int64` / `uint64` | Exact `int` | Exact `bigint`, even for small values |
| `uint8` / `uint16` / `uint32` | Nonnegative `int` within Go range | Nonnegative `number` within Go range |
| `uint` | Nonnegative `int` within target Go range | Nonnegative safe-integer `number` |
| `[N]T` | Non-null `list[T]`, exactly N items | Non-null `ReadonlyArray<T>`, runtime length checked |
| `float32` / `float64` | Finite `float` | Finite `number` |
| Named struct | Frozen dataclass | Readonly interface |

Use Go `int64` or `uint64` when Node needs the full range. `uintptr` remains
unsupported. Fixed byte arrays are numeric arrays; `[]byte` keeps its base64 codec. Unsafe Numbers fail explicitly.
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
or copy them. `memo.Delete(key)` and `memo.Clear()` invalidate cached results.
Invalidation detaches in-flight loaders: current waiters may finish, but their
results cannot repopulate invalidated entries. Cache keys must include all inputs
and identity boundaries; caches live in the Go process and are not shared across
client processes. Reuse clients and batch useful work to amortize IPC.

For test commands, protocol details, measurements and release work, see
[Contributing](CONTRIBUTING.md).


### Enums and custom wire types

Annotate a defined string or integer type with `//gobridge:enum`. Export explicitly
typed constants; omitted declarations in a typed `const` block inherit its type
and support `iota`. Arbitrary inferred constant expressions are not discovered.

```go
//gobridge:enum
type Mode string
const (
    Fast Mode = "fast"
    Careful Mode = "careful"
)
```

Generation adds `GobridgeEnum() map[string]Mode`. Python receives a `str, Enum`
subclass (or `IntEnum` for integer types); TypeScript receives an `as const` object
and a value-union type. Unknown values fail validation. Type-name overrides apply
to enums too. For manual registration, implement that method yourself; do not
combine a manual method with the annotation. Enum methods must be deterministic
and work on a zero value. Only enum constants are exported by this feature;
arbitrary package constants are not automatically exposed.

An existing type with JSON/text marshalers can explicitly declare its wire type:

```go
type Identifier string
func (Identifier) GobridgeWireType() reflect.Type { return reflect.TypeOf("") }
func (v Identifier) MarshalText() ([]byte, error) { return []byte(v), nil }
func (v *Identifier) UnmarshalText(data []byte) error {
    // Validate the representation here before assigning it.
    *v = Identifier(data)
    return nil
}
```

The wire type can be any supported type, including a struct. Go's JSON/text methods
perform conversion; generated clients expose the declared wire representation.
This works for wrappers around UUIDs, decimal strings, or custom JSON values,
without adding dependencies to GoBridge. Implement both encoding and decoding for
bidirectional types. `GobridgeWireType` must be deterministic, side-effect-free,
and safe on a zero value; nil/self-referential mappings are rejected. It does not
create a native Python UUID/Decimal class automatically.

### Required, nullable, and omitted fields

`required` and `nullable` tags control presence independently of pointer shape:

```go
type Patch struct {
    Region *string `json:"region" required:"true"` // must be present; null allowed
    Label *string `json:"label,omitempty" required:"false" nullable:"false"`
}
```

A required field cannot also use `omitempty`. `required:"false"` permits omission;
`nullable:"false"` rejects explicit null. Existing untagged pointers keep their
optional/nullable behavior. In Python, explicitly optional fields and optional
non-null fields default to `UNSET`; generated serializers omit that sentinel.
`None` still means explicit JSON null. Import `UNSET` directly from your generated module, or use the generated field
defaults.
TypeScript uses `undefined`/absent properties for omission and `null` for null.

This makes wire presence explicit; it does not give a Go pointer an additional
presence bit. Use a purpose-built request type if a Go handler must distinguish
omitted from explicitly null for the same optional, nullable field. A zero Go
pointer can represent either after ordinary JSON decoding.

### API snapshots for CI

Snapshot the registry through its binary (including its embedded prefix):

```sh
./host bridge api --class Greeter > api-after.json
gobridge api-diff --check api-before.json api-after.json
```

The snapshot contains both public language schemas and class names. Pass
`--python-names` and `--typescript-names` JSON when reproducing per-module naming
maps, including their `class` override; use the same configuration for both
snapshots. Direct Go callers can use `registry.API(class, options...)` and
`gobridge.DiffAPI(before, after)`.

Diff output is deterministic JSON with paths, old/new values and a `breaking`
flag. New operations and documentation-only edits are safe. Removals, renames,
type/nullability/enum/constraint changes are conservatively flagged for review,
even where a particular change may be compatible. `--check` fails if any such
changes exist. Wire hashes are ignored for this comparison; exact runtime
handshake checks remain unchanged. This is a schema-based compatibility check,
not a behavioral or whole-package source-code compatibility proof.

### Bytes and time values

| Go | Python | TypeScript | JSON wire value |
|---|---|---|---|
| `[]byte` | `bytes \| None` | `Uint8Array \| null` | Base64 string or null |
| `time.Time` | `str` | `string` | RFC 3339 timestamp with timezone |
| `time.Duration` | `int` | `bigint` | Signed 64-bit nanoseconds |

Empty bytes and null bytes remain distinct. Timestamp strings preserve Go's
nanosecond precision and numeric timezone offset; they are not automatically
converted to Python `datetime` or JavaScript `Date`, which have lower precision.
Go validates timestamp values. Monotonic clock readings and timezone location
names are not carried over JSON. Durations use nanoseconds, never floating-point
seconds. These codec kinds participate in the schema fingerprint.

Other types with custom JSON or text marshalers must declare `GobridgeWireType`
or use an explicit wrapper.

## Breaking API changes

The consolidated API removes overlapping entrypoints rather than keeping aliases:

| Previous interface | Replacement |
| --- | --- |
| `NewObjectOptions(r, fn, ...)` | `NewObject(r, fn, ...)` for both constructor styles. |
| Python runtime `AsyncClient` | `Client`, which supports sync calls and async `acall`/context management; generated `Greeter` and `SyncGreeter` remain distinct. |
| `init --dry-run`, `build --dry-run` | `--check` on both commands. |
| Flat manifest `name/source/command/class` | Entries in `modules`, with class names under the language settings. |
| `python_distribution`, `python_requires` | `python.distribution`, `python.requires`. |
| `npm_package`, `npm_dependencies` | `typescript.package`, `typescript.dependencies`. |
| `python_package`, `typescript_package` | Each module's `python.package`, `typescript.package`. |
| `build/dev --dir/--command/--name/--class` | Edit the module's manifest settings; dev selects with `--module`. |

Create a manifest with `gobridge init` or the configuration example above. Regenerate
Go adapters and rebuild Python/TypeScript packages together after upgrading.
