"""Compare Python decoding with a saved baseline runtime.py, in alternating order."""
import argparse
from dataclasses import dataclass
import importlib.util
import json
from pathlib import Path
import platform
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "python/src"))
from gobridge.runtime import decode


@dataclass
class Node:
    name: str
    values: list[int]


@dataclass
class Result:
    nodes: list[Node]
    data: bytes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", type=Path, required=True)
    args = parser.parse_args()
    spec = importlib.util.spec_from_file_location("baseline_runtime", args.baseline)
    baseline = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = baseline
    spec.loader.exec_module(baseline)
    value = {"nodes": [{"name": "entry", "values": [1, 2, 3, 4]} for _ in range(16)], "data": "AP8="}
    assert decode(Result, value) == baseline.decode(Result, value)
    calls, results = 10000, []
    for repeat in range(5):
        versions = [("before", baseline.decode), ("after", decode)]
        for name, call in reversed(versions) if repeat % 2 else versions:
            for _ in range(1000):
                call(Result, value)
            start = time.perf_counter()
            for _ in range(calls):
                call(Result, value)
            results.append({"repeat": repeat, "name": name,
                            "us_per_decode": (time.perf_counter() - start) * 1e6 / calls})
    print(json.dumps({"python": platform.python_version(), "calls": calls, "results": results}, indent=2))


if __name__ == "__main__":
    main()
