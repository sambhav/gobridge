# Startup, request cost, and transport choices

Keep a client alive for the lifetime of the application or worker. A client
already owns one persistent Go daemon and reuses its stdio handles across
requests. Generated module-level functions also reuse their default client.
Creating and closing a client per request pays for process startup, handshake,
and initialization every time. There is no per-request connection to cache.

Warm the client during application startup, before accepting requests:

```python
import asyncio
from greeter import Greeter

async def main():
    async with Greeter() as client:  # Starts and initializes once.
        print(await client.greet(name="Sam"))
        print(await client.greet(name="Chhavi"))  # Reuses the same daemon.

asyncio.run(main())
```

```typescript
import { Greeter } from "@acme/greeter";

const client = await new Greeter().start();
try {
  console.log(await client.greet({name: "Sam"}));
  console.log(await client.greet({name: "Chhavi"}));
} finally {
  await client.close();
}
```

Prewarming moves startup work out of the first business request; it does not
make process creation free. Keep distinct clients when you need distinct
constructor state. Forked workers create their own daemons; they do not reuse
the parent's transport.

## Compatible improvements

- Clients opt into a compact `$hello` response: protocol version, schema hash,
  and constructor presence. They still verify schema identity before `$init`.
  Older clients receive the full schema, and newer clients accept older daemon
  responses. Full schema inspection remains available.
- Go responses are encoded once, avoiding a second scan/copy of already valid
  JSON. Timed requests also avoid allocating and immediately cancelling an
  unnecessary parent context.
- On Unix, Python attempts a nonblocking direct write for frames up to 4 KiB
  when no writer backlog exists. A full pipe or larger request uses the bounded
  writer queue. While the dedicated writer owns queued frames, it can block
  normally; nonblocking mode is restored before callers may write directly.
  Windows retains the queued writer model. Cancellation frames obey the same
  ordering, and close can still terminate a daemon that stops reading.

Component benchmarks on Linux, Go 1.27.1, Python 3.12.13 and Node 24.19.0:

| Component | Before | After |
| --- | ---: | ---: |
| Encode a tiny response | 1.85 µs / 272 B allocated | 1.01 µs / 144 B |
| Encode a response containing 64 KiB bytes | 237 µs / 183 KB allocated | 109 µs / 90 KB |
| Build and encode hello for 200 operations | 1.41 ms / 55,620 wire bytes | 0.93 ms / 115 wire bytes |

These are component medians, not guarantees for complete requests. Compact
hello still computes the schema hash and does not eliminate process creation.
For the small benchmark service, cold-first-call medians remain around 20–27 ms;
do not interpret the handshake byte reduction as an equivalent startup speedup.

Complete-call comparisons alternate before/after order. Final Python results
use five repeats (2,000 tiny calls; 1,000 sync or 500 async large calls):

| Client / payload | Before calls/s | After calls/s | Change |
| --- | ---: | ---: | ---: |
| Python sync / tiny | 4,271 | 6,053 | +42% |
| Python async / tiny | 2,750 | 3,972 | +44% |
| Python sync / 64 KiB bytes | 350 | 359 | +3% |
| Python async / 64 KiB bytes | 308 | 309 | approximately flat |

The earlier TypeScript comparison measured 4,309 → 4,073 tiny calls/s (−6%)
and 298 → 304 large calls/s (+2%); its runtime does not use the Python write
fast path. Thus, lower server allocation cost is not a blanket cross-language
throughput claim. An initial Python large-frame regression led to the dedicated
blocking-writer adjustment; both the negative observations and follow-up results
remain in [all repeat data](benchmarks/startup-and-transport.json).

## Batch small units of work

Expose an operation taking a slice when callers can submit several items at
once. This amortizes framing, scheduling, deadlines, and process communication
without changing the transport or adding dependencies. For example, an annotated
`GreetMany(names []string) []string` operation can serve a whole list in one RPC.

The echo fixture measured the following median item rates over five repeats of
200 serial calls, alternating batch-size order:

| Items per call | TypeScript items/s | Python sync items/s |
| --- | ---: | ---: |
| 1 | 3,170 | 2,745 |
| 16 | 19,890 | 20,028 |
| 128 | 50,003 | 58,427 |

This is echo work, not a claim about business-handler throughput. Batches share
one deadline and response; design partial failures explicitly and remain within
the 1 MiB frame limit. The measurements precede the direct-write optimization.

## JSON, binary formats, and sockets

The protocol already uses JSON Lines (one JSON object followed by a newline).
Length-prefixed JSON still has the same payload conversion cost. In an
in-memory Go framing benchmark, newline scanning took about 22 ns per 64-byte
frame versus 26 ns for uint32 length framing. At 64 KiB, the times were about
10.2 µs versus 3.4 µs. These exclude JSON parsing and OS I/O.

Go 1.27's `encoding/json` already uses the newer v2 implementation while retaining
v1 behavior; no additional JSON dependency is needed. In this checkout, that
engine reduced the small Go Call benchmark from about 4.09 µs to 2.87 µs and
allocations from 19 to 9 compared with `GOEXPERIMENT=nojsonv2`. The supported
Go minimum remains 1.23. See the [Go 1.27 release notes](https://go.dev/doc/go1.27).
Python uses its standard C-backed JSON encoder/scanner; TypeScript uses Node's
built-in JSON functions with exact-integer handling. Third-party JSON libraries
would add dependencies and are not included.

A deliberately limited, fixed-schema Python binary experiment used only
`struct`, UTF-8 strings, uint32 lengths and int64 values:

| Payload | JSON encode + decode | Binary encode + decode | JSON / binary bytes |
| --- | ---: | ---: | ---: |
| Tiny | 6.07 µs | 1.36 µs | 22 / 8 |
| Nested small integers | 28.15 µs | 30.47 µs | 597 / 728 |
| 64 KiB bytes | 1,894 µs | 6.73 µs | 87,406 / 65,544 |

The binary prototype is **not a supported protocol**. It omits nullable types,
RPC envelopes, hostile-input validation, evolution, and the Go/TypeScript codec
implementations. It is an optimistic codec-only comparison, not an RPC speedup.
It supports investigating optional binary byte attachments; it does not justify
replacing every JSON request. Small integer arrays can be larger in fixed-width
binary than in decimal JSON.

A same-process, transport-only pipe echo measured about 3.3 µs per 64-byte round
trip. Unix sockets were unavailable in this environment (`operation not
permitted`), so no socket performance claim is made. A shared socket daemon
could amortize startup across independent clients, but needs explicit ownership,
per-session initialization/state, authorization, and crash recovery. A pool must
not silently share stateful objects. In-process FFI would remove IPC but needs
a separate Node native addon, ABI and lifetime handling, and different failure
isolation. Neither is implemented as an automatic replacement for stdio.

Reproduce the experiments:

```sh
go test -run '^$' -bench 'Benchmark(ResponseEncoding|Hello|TransportRoundTrip|FrameDecode)$' -benchmem -count 3
GOEXPERIMENT=nojsonv2 go test -run '^$' -bench '^BenchmarkCall$' -benchmem -count 3
python tools/bench_formats.py
python tools/bench_python.py --mode sync --calls 200 --concurrency 1 --size 0 --rounds 0 --nodes 128
node tools/bench_node.mjs --calls 200 --concurrency 1 --size 0 --rounds 0 --nodes 128
```

Build the fixtures first using the commands in [performance.md](performance.md).
