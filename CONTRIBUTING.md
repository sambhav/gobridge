# Development and internals

The [README](README.md) is the complete user guide. This document covers
maintainer workflows and implementation notes.

## Verify a change

```sh
python -m pip install -e "./python[dev]"
python tools/check.py
npm ci --ignore-scripts --prefix typescript
python tools/check_typescript.py
```

The first check runs Go race tests/vet, adapter drift checks, Cobra tests
and pytest with real daemons. Pytest builds the Go fixtures and generates current
Python bindings before test collection, so `python -m pytest` also works directly. Generated bindings live in
ignored output directories. `examples/greeter` is the public library example;
`internal/fixtures` holds programs for protocol, type, and lifecycle tests.
The second compiles Node runtime/generated types,
checks invalid type examples, and tests real Go binaries and malformed peers.
For changes to packaging or embedding, run `python tools/test_embedded.py`,
`python tools/test_modules.py` and `python tools/test_project_build.py`.
After building packages, `python tools/test_wheel.py` and
`python tools/test_npm.py` verify clean installs.
`python tools/test_project_build.py` checks the standalone author CLI from a
separate Go project, including an independent application package version.
Pytest also checks source reload, immutable binary revisions and failed builds.

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

The generated clients request a compact handshake with protocol, schema hash
and constructor presence; full schema inspection also includes operations. A constructor schema
adds one `$init` before calls. SHA-256 hashes include docs/constraints using Go's
sorted-schema JSON; bindings embed that hash. Invalid framing, duplicate IDs and
malformed envelopes terminate the session; ordinary application errors do not.
Streams use bounded pull cursors with cancellation and a 30-second idle/read
lease. Unary batches run in input order with at most 128 entries and a bounded
response budget. Neither batching nor transport recovery replays work. The wire
request/response structs are private implementation details; hosts use `Serve`.
Error codes include `invalid_argument`,
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

## Performance and lifecycle checks

Reuse a client to retain its Go process and amortize startup. Enter a Python
client context or call TypeScript `start()` during application startup to prewarm
it. Independent clients and forked workers own independent Go state. Typed
batches reduce round trips; a slice-taking Go operation can also batch work inside
one handler. Both remain subject to the frame limit.

Use one cross-language benchmark runner:

```sh
npm ci --ignore-scripts --prefix typescript
python tools/bench.py --calls 2000 --repeats 5 --output /tmp/before.json
# Apply the change, then rebuild and compare.
python tools/bench.py --calls 2000 --repeats 5 --output /tmp/after.json --compare /tmp/before.json
go test -run '^$' -bench . -benchmem
```

The runner builds fixtures and checks generated TypeScript. It compares native
Go, Python sync/async and TypeScript using small calls, nested models, bytes and
CPU work. Select `--cases tiny,nested`, `--clients python-sync,typescript` or
`--concurrency 1,32` to narrow the run. `--skip-build` is only for fixtures already
built from the measured revision. CI uploads fresh JSON/Markdown reports; timings
are descriptive and never a pass/fail gate.

Each client reuses a daemon after 50 warm-up calls. Fixed workers generate
closed-loop load; percentiles include waiting behind concurrent calls. Native Go
excludes serialization, conversion and IPC. Cold-first-call samples include the
handshake, but exclude interpreter startup/imports. Linux process-tree RSS is
sampled every 50 ms and can miss peaks or count shared pages repeatedly; other
platforms report null. Preserve raw reports outside the source tree when comparing
revisions, record tool versions and alternate measurement order on an idle host.
Historical timings do not establish current throughput guarantees.

For targeted codec and build investigations:

```sh
node tools/bench_codecs.mjs /path/to/baseline/typescript/dist/codec.js
python tools/bench_decode.py --baseline /path/to/baseline/python/src/gobridge/runtime.py
python tools/bench_build.py
```

`bench_python.py` and `bench_node.mjs` are the main runner's worker programs.
Generated TypeScript compiles immutable codec plans without eval; Python caches
bounded decode plans. Packaging shares Go build output within staging and relies
on the Go compiler cache. These optimizations do not introduce a shared daemon
or automatic result cache. Application caching uses `Memo` explicitly.

```sh
go test -run '^$' -fuzz '^FuzzDecodeInput$' -fuzztime 30s -parallel 2
go test -run '^$' -fuzz '^FuzzProtocolFrames$' -fuzztime 30s -parallel 2
python tools/stress.py --rounds 100
```

Decoder fuzzing uses the retained test-only strict decoder as an oracle. Protocol
fuzzing exercises malformed frames against a real server. Stress checks cover
cancellation, daemon death, admission limits and cleanup. Keep these test oracles
and fixtures even when they are not referenced by production code.

## Releases from GitHub

[Release Please](https://github.com/googleapis/release-please-action) maintains one
version and changelog for the Go module, CLI and bundled runtimes. Generated
packages carry their runtime privately; no gobridge runtime or example package
is published to PyPI/npm. Authors may publish their own generated packages there.

1. Squash-merge with a title such as `fix: ...` or `feat: ...`. Merges to `main`
   create or refresh a release PR. Breaking changes (`feat!: ...`) bump the major
   version; features bump the minor version and fixes bump the patch.
2. Review and merge the release PR in GitHub. Automation creates `vX.Y.Z`, runs
   the full CI matrix on that exact tag, and publishes six CLI archives and their
   SHA-256 checksums to GitHub Releases. Ordinary merges accumulate in the release
   PR; merging that PR publishes the release.
3. If a release job failed, use **Actions → Release → Run workflow**, select
   `main`, and enter the existing tag in `retry_tag`. Automation tests that exact
   source revision. Existing release assets must match before a retry skips them;
   it never overwrites an asset or moves a published tag.

In **Settings → Actions → General**, allow GitHub Actions to create pull requests.
The workflow explicitly dispatches CI for bot-created release PRs, so no personal
GitHub token is required. It uses the repository's built-in token to create the
release and upload assets. No PyPI/npm accounts, tokens or package registrations
are needed to release gobridge.

Release Please updates `version.txt`, development manifests, the npm lockfile and
example settings together. CI rejects version drift and mismatched release tags.
The Go module tag makes `go get` and `go install ...@latest` available through the
normal Go toolchain. Archives also provide the CLI without building it locally.

Tests build and clean-install the example wheels/npm tarballs on multiple hosts,
but those artifacts remain CI fixtures. Linux wheel checks cover glibc and musl;
release artifacts contain only the gobridge tool, license and documentation.
