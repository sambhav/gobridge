# Performance and lifecycle verification

Run from a checkout with Go 1.23+, Python 3.10+, Node 24+, and the TypeScript
compiler dependencies installed:

```sh
npm ci --ignore-scripts --prefix typescript
python tools/bench.py --output benchmark-results.json
```

The runner builds matching fixtures, checks the generated TypeScript, then runs
each scenario in a separate process. It compares native Go, generated Python
sync/async APIs, and generated TypeScript APIs using the same Go handler.
Cases cover small calls, nested models, 1 KiB and 64 KiB bytes, and sequential
SHA-256 work. Concurrency is 1, 8, 32, and 128. The fixture explicitly allows
128 concurrent handlers; production's default of 64 is unchanged.

Each client uses one persistent daemon and 50 warm-up calls. Concurrent clients
use fixed workers issuing the next call after the previous result: this is
**closed-loop load**, not an open-loop capacity/SLO test. p50/p95/p99 include
waiting behind concurrent calls. The sync Python case uses threads. The native
Go case excludes serialization, generated-client conversion, transport, and
daemon startup; very small native timings approach timer/scheduling overhead.

Cold-first-call timing spans the first call on a fresh client, including daemon
startup and handshake. Ten cold samples are recorded per client process. It
does not include interpreter startup or imports. Every result is checked;
failed calls fail the run rather than being counted as throughput.

JSON reports contain individual latency samples, per-repeat percentiles,
throughput, cold timings, environment/commit details, and observed process-tree
RSS. Markdown reports use medians across repeats. Linux RSS is sampled every
50 ms, may miss brief peaks, and counts shared pages once per process; other
platforms report null rather than fabricated zeroes. Sampling itself introduces
some overhead. For published comparisons, use an otherwise idle machine with
the same tool versions, CPU limits, and settings, and inspect run-to-run spread.

## Compare a change

```sh
python tools/bench.py --calls 2000 --repeats 5 --output /tmp/before.json
# Apply the change; rebuild fixtures (do not use --skip-build here).
python tools/bench.py --calls 2000 --repeats 5 \
  --output /tmp/after.json --compare /tmp/before.json
```

Use `--cases tiny,nested`, `--clients python-sync,typescript`, or
`--concurrency 1,32` to narrow an investigation. `--skip-build` is only for
reusing fixtures already built from the code being measured. Preserve the raw
JSON alongside any published summary. CI uploads a bounded smoke report but
does not fail on timing thresholds or attempt to prove performance gains on
shared runners.

## Initial optimization results

The initial Linux x86-64 comparison used Go 1.27.1, Python 3.12.13, Node
24.19.0, and an eight-CPU quota. These are local observations, not performance
guarantees. The full sweep used 500 calls and three repeats per scenario;
[repeat-level results](benchmarks/initial-comparison.json) retain both gains
and regressions. Full raw samples are produced by the runner; they are not
checked into the repository.

Go microbenchmarks (median of five 500 ms runs):

| Benchmark | Before | After | Allocations/call |
| --- | ---: | ---: | ---: |
| Call | 4,427 ns | 2,967 ns | 16 → 9 |
| BindCall | 6,536 ns | 4,469 ns | 20 → 13 |

Strict structural validation remains in place. Using `json.Unmarshal` for the
subsequent typed decode removes streaming-decoder overhead; differential fuzz
tests compare behavior against the previous decoder.

Python nested-model throughput improved 29–47% across sync/async clients and
the four concurrency levels in this sweep. At concurrency one, sync improved
from 988 to 1,452 calls/s and async from 972 to 1,310 calls/s. The changes avoid
recursive dataclass deep copies during encoding and cache bounded, type-specific
decoding plans. Tiny-call results were mixed: this is not a universal speedup.

TypeScript results were also mixed. An apparent nested-case slowdown in the
initial sweep did not reproduce in an alternating old/new daemon experiment
(2,000 calls, five repeats): median serial throughput was essentially flat
(1,547 → 1,546 calls/s), and concurrency 32 changed from 2,565 to 2,764 calls/s.
The spread is substantial; do not treat the latter as a guaranteed 8% gain.
[Paired results](benchmarks/typescript-paired.json) preserve every repeat.
`GOBRIDGE_BENCH_BINARY` selects a prebuilt daemon in `tools/bench_node.mjs`
for comparisons using the same generated client.

Native handler code is unchanged. Its tiny-case throughput varied heavily
with scheduling and timer overhead at this sample size, so those differences
are not evidence of an optimization. Use longer runs and alternate comparison
order before making release-level performance claims. Python sync throughput
also includes worker-pool startup, which matters for short concurrent runs.

## Fuzz and stress

```sh
go test -run '^$' -fuzz '^FuzzDecodeInput$' -fuzztime 30s -parallel 2
go test -run '^$' -fuzz '^FuzzProtocolFrames$' -fuzztime 30s -parallel 2
python tools/stress.py --rounds 100
python tools/check_typescript.py
```

Decoder fuzzing compares acceptance and decoded values against the original
strict decoder, including unknown/missing fields, nullable containers, integer
bounds, timestamps, bytes, duplicate keys, and trailing JSON. Protocol fuzzing
feeds bounded malformed/truncated frames into a real server with finite
handlers. Go's fuzz engine retains minimized regressions if a failure occurs.

Python stress repeatedly fills the client limit, cancels active calls, kills
daemons, and races close with work. It checks all waiters settle and processes,
threads, and pending entries are cleaned up. TypeScript integration includes
real-daemon repeated abort/close cycles in addition to its existing process and
worker-thread ownership tests. CI runs bounded versions of these checks.
