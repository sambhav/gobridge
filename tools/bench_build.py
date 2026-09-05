"""Measure warm Go builds with fresh versus shared link outputs, including copies."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from packaging_common import build_go_binary

ROOT = Path(__file__).resolve().parents[1]


def main():
    env = {k:v for k,v in os.environ.items() if k not in {"GOOS", "GOARCH"}}
    env["CGO_ENABLED"] = "0"
    results = []
    with tempfile.TemporaryDirectory(prefix="gobridge-bench-build-") as directory:
        root = Path(directory)
        name = "perf.exe" if os.name == "nt" else "perf"
        def build(destination, cache):
            destination.parent.mkdir(parents=True, exist_ok=True)
            start = time.perf_counter()
            build_go_binary(destination, "./internal/fixtures/perf", ROOT, env, cache=cache, trimpath=True)
            elapsed = time.perf_counter() - start
            digest = hashlib.sha256(destination.read_bytes()).hexdigest()
            return elapsed, digest
        _, expected = build(root / name, root / "cache")
        for repeat in range(5):
            names = ["fresh", "shared"] if repeat % 2 == 0 else ["shared", "fresh"]
            for mode in names:
                elapsed, digest = build(root / f"{mode}-{repeat}" / name,
                                        root / "cache" if mode == "shared" else None)
                assert digest == expected
                results.append({"repeat":repeat,"mode":mode,"seconds":elapsed})
    print(json.dumps({"go":subprocess.check_output(["go","version"],text=True).strip(),
                      "cache":"warm compiler cache; shared link output prewarmed", "results":results},indent=2))


if __name__ == "__main__":
    main()
