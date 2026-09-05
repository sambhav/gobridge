# TypeScript: the same Go library, with a native Node API

Design checkpoint: this document is pushed before implementation. The complete
Go/CLI/Python/Cobra integration has passed its cross-platform CI gate. TypeScript
is the next increment, built on the same schema and private stdio protocol.

## Import a function, or create an instance

```ts
import { greet, Greeter, control } from "gobridge-greeter-example";

console.log(await greet({ name: "World" }));

await using greeter = new Greeter({ prefix: "Hi, " });
console.log(await greeter.welcome({ name: "Sam" }));
console.log((await greeter.stats()).calls); // bigint for Go int64
```

Generated functions return promises. Constructors remain synchronous and lazy;
importing a module or constructing a client starts no subprocess. The first
call performs the handshake and initializes the Go object exactly once. Classes
also expose `start()`, `close()`, and `Symbol.asyncDispose` for explicit lifetime
control. A `try/finally` with `await client.close()` works without `await using`.

Go operation names and JSON field names become camelCase in TypeScript, including
nested models. The codec maps them back to the original wire names. Generation
rejects collisions rather than silently renaming two fields to the same name.
Results are plain typed objects with readonly interface properties, scalar
values, or `void`. Readonly is a TypeScript contract, not a runtime deep freeze.

## Application options and transport controls are separate

```ts
const greeter = new Greeter({
  prefix: "Embedded, ",
  _runtime: {
    command: ["/path/to/host", "bridge"],
    timeoutMs: 5_000,
    startupTimeoutMs: 5_000,
    maxPending: 64,
  },
});
```

The runtime appends `serve` to the executable/argument prefix, so this example
launches `host bridge serve`, including the Cobra embedding case. No shell is
involved. Published example packages include the native binary as package data;
normal consumers need no executable configuration.

Go constructor fields become typed properties on the generated options object.
Required Go fields remain required; pointer fields are optional and nullable.
The same Go field documentation and constraints appear in generated JSDoc and
the exported schema. Go remains the authoritative constraint validator.

## Cancellation and scopes

```ts
const abort = new AbortController();
const pending = greeter.welcome({ name: "Sam" }, { signal: abort.signal });
abort.abort();
await pending; // rejects with AbortError; unrelated calls remain usable

await control.scope({ prefix: "Scoped, " }, async (client) => {
  console.log(await greet({ name: "World" }));
  console.log(await client.welcome({ name: "Sam" }));
});
```

Per-call controls use a separate final `{ signal, timeoutMs }` argument. An
already-aborted signal must not start the daemon or invoke an operation. If a
call is cancelled during cold startup, startup remains owned and the operation
is never sent; explicit close still cleans up that client.

Module functions share one lazy default client. `control.configure(options)`
sets its constructor arguments before creation; `control.close()` deliberately
resets it. A dead default is never silently restarted. `control.start()` can
warm the currently selected client.

`control.scope(options, callback)` creates and closes an isolated client around
the callback. AsyncLocalStorage keeps simultaneous async tasks separate, and
nested scopes restore the prior client. Child tasks inherit their async context;
finish them before leaving the scope. Scope options do not inherit default
configuration. Explicit clients are the clearest choice for long-lived instances.

## Integers retain their meaning

| Go type | TypeScript type | Rule |
| --- | --- | --- |
| `int8`, `int16`, `int32` | `number` | Integral and within the declared Go range. |
| `int` | `number` | Safe JavaScript integers only; out-of-range values fail explicitly. |
| `int64` | `bigint` | Full signed 64-bit range, including values beyond Number precision. |
| `float32`, `float64` | `number` | Finite numbers; Go handles declared type/range constraints. |

Use explicit `int64` in the Go API when its full range must be usable in
TypeScript. A Go `int` is architecture-dependent, and the TypeScript facade does
not pretend every possible 64-bit Go int fits a Number.

The runtime reads original JSON number text before converting large integer
values and writes bigint values as exact JSON numeric literals. The Go protocol
stays unchanged. Unsafe Numbers are rejected rather than rounded. Users pass
bigint literals such as `9_007_199_254_740_993n` for int64 fields and receive bigint
results consistently, even when the value is small. Ordinary `JSON.stringify`
of application objects containing bigint still needs an application serializer.

## Ownership, limits, and failures

Each explicit client owns its own Go subprocess. Node worker threads and child
processes create their own clients/defaults; live clients are not transferable.
Pass configuration data to a worker and construct a client there.

Concurrent calls share a bounded pending map and a bounded output queue. Frames
retain the Go protocol's 1 MiB limit. Writes respect pipe backpressure, and
cancellation cannot grow an unbounded queue behind a stalled daemon. Responses
are correlated by ID and may arrive out of order.

Startup uses one budget for handshake and constructor initialization. Failed
startup remains failed for that client, because constructor side effects cannot
be safely retried automatically. Crashes, malformed frames, schema mismatch,
request timeout, cancellation, and explicit close reject pending promises with
useful error types. No operation is automatically replayed.

Idle default clients must not keep an otherwise finished Node program alive.
Pending operations keep their transport referenced; idle transports release
those event-loop references. Normal exit closes/reaps tracked children; explicit
close or async disposal gives deterministic cleanup. Abrupt termination has the
same limits as other child-process-based libraries.

## Supported runtime and packaging

The first version targets Node.js 24 and newer and publishes ESM JavaScript plus
TypeScript declarations. The runtime has no third-party production dependencies.
A generated package contains its bindings and binaries for Linux, macOS, and
Windows on x64/arm64. Binary resolution selects the current platform and
architecture. These are local build artifacts until a release is explicitly made.

Browser runtimes cannot spawn the local Go binary and are outside this transport.
CommonJS, browser RPC transports, shared machine daemons, and streaming are
separate extensions rather than hidden changes to the first version.

Planned author commands:

```sh
go build -o bin/annotated ./examples/annotated/cmd/annotated
bin/annotated generate-typescript --class Greeter --binary annotated \
  > examples/annotated/annotated.ts
```

Generation emits real declarations and signatures, not a dynamic property proxy.
Generated code and package installation are checked in CI alongside Go/Python.

## Acceptance checks

- Import/startup laziness, natural functions/classes, nested types and camelCase.
- Actual Go daemons, constructor options, independent state, concurrent requests.
- Full int64 round trips, safe-int failures, Unicode, nullable containers and void.
- Timeout/AbortSignal cleanup, malformed protocol, crash/no replay, bounded writes.
- Async scopes, worker/child-process separation, idle process exit and cleanup.
- Type-check generated bindings and deliberate invalid-call examples.
- Pack/install into a clean Node project and exercise bundled binaries.
- Node 24/26 on Linux, macOS and Windows, while keeping Go/Python gates green.

The design uses Node's [child-process API](https://nodejs.org/api/child_process.html),
[AsyncLocalStorage](https://nodejs.org/api/async_context.html), and the standardized
[JSON source/raw-number facilities](https://tc39.es/proposal-json-parse-with-source/).
