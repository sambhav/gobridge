"""Run from the repository root: python examples/hello/demo.py."""
from __future__ import annotations

import argparse
import asyncio
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path

from hello import (Hello as AsyncHello, SyncHello as Hello, cached_greet as cached_greet_async, cached_greet_sync as cached_greet, greet_sync as greet, configure, session_sync, shutdown_sync)


async def module_async_demo(original) -> None:
    assert await cached_greet_async(name="default") == original
    print("Sync and async functions share one default Go cache.")


async def async_demo(binary: str) -> None:
    async with AsyncHello(binary) as hello:
        results = await asyncio.gather(
            *(hello.cached_greet(name="async") for _ in range(16))
        )
        assert {result.computation for result in results} == {1}
        assert len({result.process_id for result in results}) == 1
        print(results[0].message)
        print("Async tasks share one Go cache.")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    default = Path(__file__).resolve().parents[2] / "bin" / (
        "hello.exe" if os.name == "nt" else "hello"
    )
    parser.add_argument("--binary", default=str(default))
    args = parser.parse_args()

    # Installed wheels need no configuration: import greet and call it.
    configure(command=args.binary)
    try:
        print(greet(name="world").message)
        original = cached_greet(name="default")
        asyncio.run(module_async_demo(original))

        with session_sync(command=args.binary) as isolated:
            scoped = cached_greet(name="default")
            assert scoped.process_id != original.process_id
            assert scoped == isolated.cached_greet(name="default")
        assert cached_greet(name="default") == original
        print("A scope restores the previous default after isolating state.")

        with Hello(args.binary) as hello:
            with ThreadPoolExecutor(max_workers=8) as pool:
                results = list(pool.map(lambda _: hello.cached_greet(name="threads"), range(32)))
            assert {result.computation for result in results} == {1}
            assert len({result.process_id for result in results}) == 1
            print("Threads share one Go cache.")

            with Hello(args.binary) as separate:
                other = separate.cached_greet(name="threads")
                assert other.process_id != results[0].process_id
                assert other.computation == 1
                print("Separate clients have separate Go state.")

        asyncio.run(async_demo(args.binary))
    finally:
        shutdown_sync()


if __name__ == "__main__":
    main()
