"""One isolated Python benchmark process; invoked by bench.py."""
import argparse
import asyncio
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / "python/src"), str(ROOT / ".generated/python")]
from perf import Perf, SyncPerf, Node


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=["sync", "async"], required=True)
    parser.add_argument("--calls", type=int, required=True)
    parser.add_argument("--concurrency", type=int, required=True)
    parser.add_argument("--size", type=int, default=0)
    parser.add_argument("--rounds", type=int, default=0)
    parser.add_argument("--nested", action="store_true")
    args = parser.parse_args()
    binary = str(ROOT / "bin" / ("perf.exe" if os.name == "nt" else "perf"))
    params = dict(data=bytes(args.size), rounds=args.rounds,
                  nodes=[Node(name="entry", values=[1, 2, 3, 4]) for _ in range(16)] if args.nested else [])
    samples = [0.] * args.calls
    cold = []

    def check(result):
        assert result.data == params["data"] and result.nodes == params["nodes"] and len(result.digest) == 64

    def sync_run():
        for _ in range(10):
            client = SyncPerf(binary)
            start = time.perf_counter_ns()
            try:
                client.work(data=b"", rounds=0, nodes=[])
                cold.append((time.perf_counter_ns() - start) / 1e6)
            finally:
                client.close()
        with SyncPerf(binary) as client:
            for _ in range(50): check(client.work(**params))
            def worker(offset):
                for i in range(offset, args.calls, args.concurrency):
                    start = time.perf_counter_ns()
                    result = client.work(**params)
                    samples[i] = (time.perf_counter_ns() - start) / 1000
                    check(result)
            with ThreadPoolExecutor(max_workers=args.concurrency) as pool:
                start = time.perf_counter()
                list(pool.map(worker, range(args.concurrency)))
                return time.perf_counter() - start

    async def async_run():
        for _ in range(10):
            client = Perf(binary)
            start = time.perf_counter_ns()
            try:
                await client.work(data=b"", rounds=0, nodes=[])
                cold.append((time.perf_counter_ns() - start) / 1e6)
            finally:
                await client.aclose()
        async with Perf(binary) as client:
            for _ in range(50): check(await client.work(**params))
            async def worker(offset):
                for i in range(offset, args.calls, args.concurrency):
                    start = time.perf_counter_ns()
                    result = await client.work(**params)
                    samples[i] = (time.perf_counter_ns() - start) / 1000
                    check(result)
            start = time.perf_counter()
            await asyncio.gather(*(worker(i) for i in range(args.concurrency)))
            return time.perf_counter() - start

    elapsed = sync_run() if args.mode == "sync" else asyncio.run(async_run())
    ordered = sorted(samples)
    print(json.dumps(dict(calls_per_second=args.calls / elapsed, samples_us=samples,
                         p50_us=ordered[(len(ordered)-1)//2], p95_us=ordered[int((len(ordered)-1)*.95)],
                         p99_us=ordered[int((len(ordered)-1)*.99)], cold_first_call_ms=cold)))


if __name__ == "__main__":
    main()
