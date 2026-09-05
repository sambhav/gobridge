"""Reproducible, descriptive benchmarks; no flaky CI timing thresholds."""
import argparse
import asyncio
import json
import os
from pathlib import Path
import platform
import statistics
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / "python/src"), str(ROOT / "examples/textkit")]
from textkit import TextKit as AsyncTextKit, SyncTextKit as TextKit


def latency(values):
    values = sorted(values)
    return {"median_us": round(statistics.median(values) / 1000, 2),
            "p95_us": round(values[int((len(values)-1)*.95)] / 1000, 2)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--calls", type=int, default=1000)
    args = parser.parse_args()
    if args.calls < 20:
        parser.error("use at least 20 calls")
    binary = ROOT / "bin" / ("textkit.exe" if os.name == "nt" else "textkit")
    report = {"python": platform.python_version(), "platform": platform.platform(), "calls": args.calls}
    start = time.perf_counter_ns()
    with TextKit(binary) as client:
        report["cold_start_ms"] = round((time.perf_counter_ns()-start)/1e6, 2)
        for _ in range(50):
            client.analyze(text="warm cache")
        for name, call in [("sync_raw", lambda: client.call("analyze", {"text":"warm cache"})),
                           ("sync_typed", lambda: client.analyze(text="warm cache"))]:
            samples = []
            for _ in range(args.calls):
                start = time.perf_counter_ns()
                call()
                samples.append(time.perf_counter_ns()-start)
            report[name] = latency(samples)

    async def async_bench():
        async with AsyncTextKit(binary) as client:
            for _ in range(50):
                await client.analyze(text="warm cache")
            samples = []
            for _ in range(args.calls):
                start = time.perf_counter_ns()
                await client.analyze(text="warm cache")
                samples.append(time.perf_counter_ns()-start)
            report["async_typed"] = latency(samples)
            start = time.perf_counter()
            for offset in range(0,args.calls,16):
                await asyncio.gather(*(client.analyze(text="warm cache") for _ in range(min(16,args.calls-offset))))
            report["async_concurrency_16_calls_per_second"] = round(args.calls/(time.perf_counter()-start))
    asyncio.run(async_bench())
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
