# gobridge

Write operations in Go. Expose them as a CLI and ordinary-looking Python
classes, backed by a private Go daemon. TypeScript bindings are planned.

This is an initial implementation, not a released package. Neither the Go
module nor the Python package has been tagged or published to a registry.

## Python usage

The example bindings are generated from Go types. Results are dataclasses,
methods have real signatures, and errors are Python exceptions.

```python
from textkit import TextKit, AsyncTextKit

# A packaged wheel includes the right Go binary for the host.
with TextKit() as kit:
    result = kit.analyze(text="Hello from Go")
    print(result.words)  # 3

async def example():
    async with AsyncTextKit() as kit:
        result = await kit.analyze(text="Hello asynchronously")
        await kit.wait(milliseconds=50, _timeout=1)
        return result.words
```

For development, pass `TextKit("./bin/textkit")`. Construction is lazy;
entering the context or making the first call starts the daemon. Keep a client
alive to reuse the Go cache. Use context managers or `close()` / `aclose()`.
Garbage collection and normal interpreter exit also attempt cleanup.

## Define an operation once

```go
type AnalyzeInput struct {
    Text string `json:"text"`
}
type Analysis struct {
    Words int `json:"words"`
}

func main() {
    r := gobridge.New()
    err := gobridge.Register(r, "analyze", "Count words.",
        func(ctx context.Context, in AnalyzeInput) (Analysis, error) {
            return Analysis{Words: len(strings.Fields(in.Text))}, nil
        })
    if err != nil { panic(err) }
    r.Main()
}
```

See [`examples/textkit/main.go`](examples/textkit/main.go) for the complete
program, including an optional Go TTL/LRU cache. Wrapping an existing Go
library requires a small typed adapter; it does not rewrite that library.

The same binary exposes:

```sh
textkit analyze --text "hello world"
textkit analyze --json '{"text":"hello world"}'
textkit analyze --json - < input.json
textkit schema
textkit generate-python --class TextKit --binary textkit > textkit.py
textkit serve --max-concurrency 64
```

CLI string flags accept plain text; numbers, booleans, nullable pointers and
containers accept JSON literals. The initial CLI supports `--field value`
pairs; help lists operation descriptions and `schema` describes their fields.

## Ownership and concurrency

| Caller | Connection and daemon | Go state/cache |
| --- | --- | --- |
| Threads sharing one client | One multiplexed stdio connection | Shared |
| Async tasks sharing one client | Same connection; no event-loop-owned transport | Shared |
| Different clients in one process | Separate connections and daemons | Isolated |
| Pickled client sent with spawn/forkserver | New connection and daemon on first use | Fresh |
| Inherited client after fork | Child closes inherited pipe copies, resets local transport | Fresh; parent remains usable |

There is no global daemon and no socket path. Stdio means **two anonymous
pipes**, not a Unix-domain socket. That avoids port selection, Unix socket
availability, Windows named-pipe differences, authentication tokens and stale
socket files. Requests carry IDs, so responses can arrive out of order.

Python's `spawn` and `forkserver` are preferable to forking an application
with active threads. The runtime resets its own inherited state; it cannot
make arbitrary third-party locks or native runtimes fork-safe. It never
changes your multiprocessing start method. See the [Python multiprocessing
documentation](https://docs.python.org/3/library/multiprocessing.html).

`Memo` in Go offers bounded TTL/LRU caching and per-key request coalescing.
One cancelled caller does not cancel other callers waiting on the same work;
the last departing waiter cancels the loader. Cache keys must capture all
inputs and identity boundaries. Values must be immutable or copied by callers.
Only use caching for appropriate pure operations; it is never automatic.

## Failure behavior

- Individual timeouts and async cancellation send cancellation to Go contexts.
  Handlers must cooperate. `close()` stops the private daemon and fails pending
  calls; it is an abort, not a drain.
- Requests are bounded by a local pending limit, a bounded writer queue,
  a daemon concurrency limit, and a 1 MiB frame limit. Overload fails promptly.
- A dedicated writer thread keeps pipe backpressure off caller/event-loop
  threads. Cold startup runs off the event loop for async calls.
- EOF cancels Go work and ends the daemon. Handler panics become errors.
- A crashed daemon fails pending calls. It is **not restarted automatically**;
  create a fresh client to explicitly accept new Go state. Operations are
  never replayed automatically; an interrupted mutation may have completed.
- Bindings verify protocol version and an exact schema fingerprint on startup.
- Stdout is reserved for protocol frames. Go logging belongs on stderr.

## Develop and verify

Go 1.23+ and Python 3.10+ are required. Both runtime implementations use only
their standard libraries. Packaging uses setuptools and wheel.

```sh
go build -o bin/textkit ./examples/textkit
./bin/textkit generate-python --class TextKit --binary textkit > examples/textkit/textkit.py
python -m pip install -e "./python[dev]"
PYTHONPATH=examples/textkit python -c 'from textkit import TextKit; k=TextKit("./bin/textkit"); print(k.analyze(text="hello")); k.close()'
python tools/check.py
```

`tools/check.py` is portable, builds the example, checks generated-file drift,
runs Go tests with the race detector and vet, and runs pytest/pytest-asyncio integration
tests against actual subprocesses. CI covers Linux, macOS and Windows and
Python 3.10, 3.12 and 3.14, plus the minimum Go version.

## Package binaries with Python

```sh
python -m pip install setuptools wheel
python tools/build_wheels.py --targets linux-amd64
python -m pip install --no-index --find-links dist gobridge-textkit-example
python -c 'from textkit import TextKit; k=TextKit(); print(k.analyze(text="no Go installation needed")); k.close()'
```

The packaging recipe produces a pure-Python runtime wheel and platform wheels
for the generated example package. Users need Python, not a Go toolchain.
Adapt the recipe to your library. It does not upload to PyPI. Supported build
targets are Linux, macOS and Windows on amd64 and arm64. Linux wheels currently
use `linux_*` tags for direct/private distribution, **not manylinux/musllinux
certification for public PyPI**. Public distribution is a follow-up gate.

The recipe uses `CGO_ENABLED=0`. Cross-compilation works for compatible Go
dependencies; libraries requiring cgo need target C toolchains and additional
packaging work. See [Go's target/build environment
documentation](https://go.dev/doc/install/source#environment).

## Wire types and current scope

Supported: named structs with explicit JSON tags; strings, booleans, signed
integers, floats, nested structs, pointers, slices and string-keyed maps.
Pointers map to `T | None`. Nil maps/slices map to `None`. Other fields are
required. Unknown fields and invalid scalar types are rejected in Go.
Names must be Python-safe. Recursive structs, embedded fields, unsigned
integers, `[]byte`, interfaces and custom JSON encoders need explicit adapters.

This version supports unary operation methods and per-client Go state.
Streaming, persistent remote object handles, shared daemons, disk caches,
TypeScript generation and public release automation are documented in
[`docs/PLAN.md`](docs/PLAN.md). See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
for the transport and lifecycle decisions.
