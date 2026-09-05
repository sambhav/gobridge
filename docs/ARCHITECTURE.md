# Architecture decisions

## One client, one process-owned daemon

Default ownership is `(Python PID, client instance)`, not a machine-wide singleton
and not one daemon per thread. A daemon can hold existing Go library instances,
connection pools and caches captured by operation closures. Sharing a Python
client deliberately shares this state. A different client is a different session.

The Python object is a typed facade, not a mirror of arbitrary Go pointers.
Returning Go object identity across IPC requires a handle/lifetime protocol and
is deferred. Values are copied through JSON. Go authors can opt in ordinary functions/methods with source annotations, use
`Bind` directly, or use generic `Register` for explicit DTO adapters. A declared
constructor initializes one Go receiver per daemon; it never runs for schema
discovery. Generated module functions share a lazy process-local default client;
explicit instances and ContextVar scopes provide independent state.

## Why stdio first

"Stdio socket" combines two different transports. Version 1 chooses anonymous
stdin/stdout pipes to a private subprocess. There are no Unix socket paths,
listening ports, network listeners, named pipes or global service discovery.
This also works where local sockets are prohibited but subprocess pipes work.

A Unix socket would be useful for an independently launched shared daemon.
That is a future explicit option, with Windows named-pipe and session semantics.

## Protocol v1

Each frame is one UTF-8 JSON object followed by newline, limited to 1 MiB.
Request IDs are nonempty strings (at most 128 bytes), unique among active calls.
Responses may arrive out of order. This is a small custom protocol, not JSON-RPC
2.0 or MCP: there is no implicit compatibility claim.

```json
{"id":"1","method":"$hello","params":{},"timeout_ms":5000}
{"id":"2","method":"analyze","params":{"text":"hello"},"timeout_ms":30000}
{"method":"$cancel","params":{"id":"2"}}
```

The handshake returns `protocol: 1`, `schema_hash` and the operation schema.
When a constructor is declared, the handshake also includes its configuration
schema. Python then sends `$init` once, before publishing the started client.
The hash is SHA-256 of Go's JSON serialization of the sorted operation schema
and, where present, the constructor schema.
Generated clients embed the server-produced hash; clients do not independently
canonicalize JSON. Result/error responses have the request ID and exactly one
of `result` or `error`. Cancellation notifications have no response.

Errors include `invalid_argument`, `not_found`, `busy`, `cancelled`,
`deadline_exceeded`, `resource_exhausted`, `internal` and application-defined codes.
Python additionally reports transport, closed-client and schema-mismatch errors.

Unknown operation methods return errors. Invalid framing, duplicate active IDs
and malformed envelope shapes close the session. Application exceptions do not.
This is a trusted private-child transport, not an authenticated RPC listener for
untrusted clients. Never pipe unrelated stdout output into the protocol channel.

## Threading, asyncio and backpressure

Python owns a bounded pending-future map, a bounded outbound queue, one writer
thread and one reader thread per daemon. Thread-safe futures correlate requests;
async callers wrap them in their current event loop. No event loop owns the
transport. Writes do not hold up the caller on OS pipe backpressure.

The Go server admits a bounded number of concurrent calls and rejects excess
work promptly. Cancellation controls remain readable at capacity. Handlers get
contexts but are responsible for protecting any mutable Go library state.
`Memo` is one supplied concurrency primitive, not a universal lock around handlers.

Python's timeout bounds its wait after submission; a cold start has a separate
5-second startup timeout by default, configurable through `RuntimeOptions`.
That budget covers both the handshake and constructor initialization. The daemon's relative deadline begins when admitted.
Queued messages cannot run forever unnoticed: the caller sends cancellation
when its own wait expires. Cancellation is best effort; non-cooperative Go work
can occupy a slot until it finishes or the client closes the daemon.

## Multiprocessing

Spawn/forkserver pickle only the executable configuration, timeout, admission
limit, schema fingerprint, snapshotted constructor options, startup timeout and
closed state. Locks, pipes, futures and Go state
are never serialized. Unpickled live clients lazily start a fresh daemon.

For fork, callbacks synchronize transport creation against forking, close the
child's inherited unbuffered pipe copies, detach inherited finalizers, and reset
the transport reference and per-client lifecycle lock. Startup transports are
registered before the handshake, so an in-progress constructor cannot leave
untracked inherited pipes. Unrelated clients do not share a startup lock. The child does not terminate or wait for the parent's
daemon. This also runs for clients the child never subsequently uses, so they
do not keep the parent's pipe alive. The parent's transport continues unaffected.

The hook addresses gobridge's resources only. Python cautions against forking
multithreaded applications, and no library hook can fix arbitrary foreign locks.
Prefer spawn/forkserver; the library does not force a start method.

## State, caching and failures

The registry and schemas are fixed before serving. Caches are opt-in, in-memory,
bounded and scoped to the daemon. A cache load survives one waiter's cancellation
while other waiters remain. Last-waiter cancellation stops the loader context;
errors and abandoned results are not cached. Concurrent cache callers receive
the same value, so reference-containing values must be immutable or copied.

No operation is automatically replayed, and a crashed daemon is not silently
replaced. A caller must explicitly instantiate a fresh client and decide how
to recover from an operation whose outcome is unknown. Shutdown aborts work;
an explicit draining mode can be added later.

EOF causes `Serve` to cancel its contexts and return without waiting for handlers
that ignore cancellation. `Registry.Main` then exits the child process. Embedders
must close their reader to interrupt a blocked scan when their parent context is
cancelled; a generic `io.Reader` cannot be forcibly interrupted by context alone.

## Sources

- [Python multiprocessing](https://docs.python.org/3/library/multiprocessing.html):
  start methods, inherited resources and threaded-fork limitations.
- [Python fork callbacks](https://docs.python.org/3/library/os.html#os.register_at_fork):
  callbacks apply to forks made through Python; native extensions bypassing
  Python's fork hooks are outside this guarantee.
- [Go target environment](https://go.dev/doc/install/source#environment):
  GOOS/GOARCH and platform requirements.
