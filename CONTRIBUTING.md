# Development and internals

The [README](README.md) is the single user guide. Keep examples progressive and
put implementation notes here. Document APIs before implementing them, regenerate
examples, and keep draft PR increments reviewable. No packages are released yet.

## Verify a change

```sh
python -m pip install -e "./python[dev]"
python tools/check.py
npm ci --ignore-scripts --prefix typescript
python tools/check_typescript.py
```

The first check runs Go race tests/vet, adapter/Python drift checks, Cobra tests
and pytest with real daemons. The second compiles Node runtime/generated types,
checks invalid type examples, and tests real Go binaries and malformed peers.
After building packages, `python tools/test_wheel.py --example annotated` and
`python tools/test_npm.py` verify clean installs.

CI covers minimum Go 1.23; Python 3.10/3.12/3.14 and Node 24/26 on Linux, macOS
and Windows; six-target builds; and native wheel/npm installs. Retain ownership,
fork/startup, cancellation and bounded-queue regressions. Deliberate threaded-fork
tests can emit Python's deprecation warning.

## Protocol and ownership

Protocol v1 uses private UTF-8 JSON-lines over anonymous subprocess pipes. It is
neither MCP nor JSON-RPC and opens no listening port. Frames are at most 1 MiB;
IDs are nonempty strings up to 128 bytes, unique among active requests. Responses
can arrive out of order and contain exactly one result or error.

```json
{"id":"1","method":"$hello","params":{},"timeout_ms":5000}
{"id":"2","method":"greet","params":{"name":"World"},"timeout_ms":30000}
{"method":"$cancel","params":{"id":"2"}}
```

The handshake returns protocol, schema hash and operations. A constructor schema
adds one `$init` before calls. SHA-256 hashes include docs/constraints using Go's
sorted-schema JSON; bindings embed that hash. Invalid framing, duplicate IDs and
malformed envelopes terminate the session; ordinary application errors do not.
Crashes never implicitly replay work. Error codes include `invalid_argument`,
`not_found`, `busy`, `cancelled`, `deadline_exceeded`, `resource_exhausted`,
`internal`, and application codes.

Python uses reader/writer threads and thread-safe futures. Async callers wrap
futures in their current loop; no loop owns a daemon. Cold startup/shutdown run
off the loop; dataclass hints are cached. Fork hooks gate process creation, detach
all child-inherited pipes/finalizers and reset locks. Pickles contain configuration.
Native forks bypassing Python hooks remain outside that guarantee.

Node uses bounded pending/output queues, exact JSON numeric text and
AsyncLocalStorage. Pending calls retain transports; idle children allow Node to
exit. Finalization preserves pending operations before reaping dropped clients.
Explicit close gives deterministic cleanup. Go validates fixed schemas and
precompiled constraints; bounded admission keeps cancellation responsive. EOF
cancels and returns even for uncooperative handlers; the host owns final exit
and unblocking borrowed readers.

## Performance

```sh
go build -o bin/textkit ./examples/textkit
python tools/benchmark.py --calls 1000
go test -run '^$' -bench . -benchmem
```

One 2026-09-05 Linux x86_64 sample (Python 3.12.13, Go 1.27.1, 1,000 calls after
warmup) recorded typed sync/async medians of 159/314 microseconds, cold startup
19.57 ms, and async throughput 5,020 calls/s at concurrency 16. Separate Go
microbenchmarks measured Bind at 7.623 microseconds/op and Memo hits at 107.4 ns/op
with zero allocations. These are shared-host observations, not guarantees or
controlled comparisons. CI uploads benchmarks; timing is not a pass/fail gate.
Measure realistic payloads and saturation before adding specialized codecs or
optional dependencies. Reuse processes and batch useful work first.

## Before release

- Certify Linux wheel tags and compatibility; expand native arm64 host coverage.
- Add sustained load, protocol fuzzing, process-kill and free-threaded Python checks.
- Add checksums/signing and reproducible versioned releases.
- Define schema compatibility policy; exact fingerprints require regeneration.
- Keep native Go, Python/Node APIs, Cobra and clean artifact installation green.

Streaming, remote object handles, shared machine daemons and disk-backed caches
are future resource models needing explicit lifetime, backpressure and recovery.
The present API copies values and owns one object per private daemon.

Primary references: [Python multiprocessing](https://docs.python.org/3/library/multiprocessing.html),
[fork hooks](https://docs.python.org/3/library/os.html#os.register_at_fork),
[Node child processes](https://nodejs.org/api/child_process.html),
[AsyncLocalStorage](https://nodejs.org/api/async_context.html),
[exact JSON numbers](https://tc39.es/proposal-json-parse-with-source/),
[Go cross-compilation](https://go.dev/doc/install/source#environment).
