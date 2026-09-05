"""Typed registration, cached state, and client ownership regressions."""
import asyncio
from concurrent.futures import ThreadPoolExecutor
import dataclasses
import json
import os
from pathlib import Path
import subprocess

import pytest

ROOT = Path(__file__).resolve().parents[2]
from hello import (Hello as AsyncHello, SyncHello as Hello, Greeting, cached_greet as cached_greet_async, cached_greet_sync as cached_greet, greet_sync as greet, configure, session, session_sync, shutdown_sync)

BINARY = ROOT / "bin" / ("hello.exe" if os.name == "nt" else "hello")


@pytest.fixture
def module_default():
    shutdown_sync()
    configure(command=str(BINARY))
    try:
        yield
    finally:
        shutdown_sync()


@pytest.mark.parametrize("arguments", [
    ["--name", "世界"],
    ["--json", '{"name":"世界"}'],
])
def test_hello_cli_and_binding_agree(arguments):
    output = subprocess.check_output([str(BINARY), "greet", *arguments], text=True, encoding="utf-8")
    with Hello(str(BINARY)) as hello:
        result = hello.greet(name="世界")
        assert isinstance(result, Greeting)
        assert result.message == "Hello, 世界!"
        assert dataclasses.asdict(result) == json.loads(output)


def test_hello_rejects_unknown_keyword_without_starting_a_daemon():
    hello = Hello(str(BINARY))
    try:
        with pytest.raises(TypeError):
            hello.greet(nmae="typo")
    finally:
        hello.close()


def test_module_function_has_real_signature(module_default):
    assert greet(name="world") == Greeting(message="Hello, world!")
    with pytest.raises(TypeError):
        greet(nmae="typo")


def test_threads_share_the_module_default_on_concurrent_first_use(module_default):
    with ThreadPoolExecutor(max_workers=8) as pool:
        results = list(pool.map(lambda _: cached_greet(name="thread default"), range(32)))
    assert len({result.process_id for result in results}) == 1
    assert {result.computation for result in results} == {1}


def test_module_sync_and_async_share_state_across_event_loops(module_default):
    original = cached_greet(name="shared default")
    assert asyncio.run(cached_greet_async(name="shared default")) == original
    assert asyncio.run(cached_greet_async(name="shared default")) == original


def test_nested_scopes_restore_the_previous_client_even_after_error(module_default):
    original = cached_greet(name="scope")
    with session_sync(command=str(BINARY)) as outer:
        scoped = cached_greet(name="scope")
        assert scoped == outer.cached_greet(name="scope")
        assert scoped.process_id != original.process_id
        with pytest.raises(RuntimeError, match="application error"):
            with session_sync(command=str(BINARY)) as inner:
                nested = cached_greet(name="scope")
                assert nested == inner.cached_greet(name="scope")
                assert nested.process_id not in {original.process_id, scoped.process_id}
                raise RuntimeError("application error")
        assert cached_greet(name="scope") == scoped
    assert cached_greet(name="scope") == original


@pytest.mark.asyncio
async def test_async_scopes_are_isolated_between_tasks_and_restore_default(module_default):
    original = await cached_greet_async(name="async scope")
    both_entered = asyncio.Event()
    arrivals = 0

    async def worker():
        nonlocal arrivals
        async with session(command=str(BINARY)) as scoped:
            first = await cached_greet_async(name="async scope")
            arrivals += 1
            if arrivals == 2:
                both_entered.set()
            await asyncio.wait_for(both_entered.wait(), timeout=5)
            again = await cached_greet_async(name="async scope")
            assert first == again
            assert dataclasses.asdict(first) == await scoped.acall(
                "cached_greet", {"name": "async scope"}
            )
        assert await cached_greet_async(name="async scope") == original
        return first

    left, right = await asyncio.gather(worker(), worker())
    assert len({original.process_id, left.process_id, right.process_id}) == 3
    assert await cached_greet_async(name="async scope") == original


@pytest.mark.asyncio
async def test_hello_async_result_is_typed():
    async with AsyncHello(str(BINARY)) as hello:
        assert await hello.greet(name="async") == Greeting(message="Hello, async!")
