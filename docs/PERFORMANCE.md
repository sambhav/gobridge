# Performance priorities and measurements

The target is low overhead for useful Go library operations, with a familiar
Python API. IPC will not beat an in-process function call for trivial scalar
work. Batch small operations when possible; keep expensive reusable state in Go.

## First measurements (2026-09-05)

Linux x86_64 hosted sandbox, Python 3.12.13, Go 1.27.1. Each Python run made
1,000 calls after 50 warmups against a tiny cached text-analysis operation.
These are individual development samples, not hardware-independent guarantees
or statistically controlled comparisons. Shared-host scheduling affects them.

| Measurement | Initial sample | After hot-path patch |
| --- | ---: | ---: |
| Sync raw median round trip | 184.53 µs | 171.88 µs |
| Sync typed median round trip | 328.16 µs | 169.81 µs |
| Async typed median round trip | 583.58 µs | 334.32 µs |
| Async typed p95 | 909.95 µs | 554.35 µs |
| Async throughput, concurrency 16 | 3,227 calls/s | 7,033 calls/s |

The patch caches resolved dataclass type hints and skips the thread-pool hop
for already-started async clients. Cold startup remains off the event loop.
Cold-start samples were 21–32 ms; process creation is deliberately amortized by
keeping clients alive. The measurements include transport, JSON and result
construction, not just Go handler time.

Go-only samples: typed registry dispatch approximately 4.7 µs/op, 1,153 B and
18 allocations; a cached `Memo.Get` approximately 103 ns/op with zero allocations.
These are separate microbenchmarks and are not IPC throughput figures.

## Reproduce

```sh
go build -o bin/textkit ./examples/textkit
python tools/benchmark.py --calls 1000
go test -run '^$' -bench . -benchmem
```

CI uploads descriptive benchmark artifacts for each PR revision. Timing is
not a pass/fail gate on shared runners. Compare repeated samples on consistent
hardware before accepting performance claims or regression thresholds.

## Next work, in priority order

1. Benchmark representative libraries: CPU work, cached I/O, 1 KiB–1 MiB data,
   cancellation and load saturation. Measure memory, allocations and tail latency.
2. Add a batching primitive before optimizing tiny individual RPCs; an API-level
   batch can amortize JSON, context switches and schema checks.
3. Generate specialized result decoders if cached generic decoding still matters.
   Evaluate optional JSON accelerators behind an extra, without requiring them.
4. Measure framing/serialization before considering a binary codec. Preserve
   wire compatibility and TypeScript integer correctness.
5. Tune cache policy and concurrency per workload rather than treating more
   goroutines, threads or daemons as automatically faster.
6. Keep dependency-free dataclasses as the default. Optional Pydantic support
   should have an explicit use case and measured validation/startup costs.
