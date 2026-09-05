"""Source annotations to typed, process-owned Python objects end to end."""
import asyncio
from concurrent.futures import ThreadPoolExecutor
import inspect
import multiprocessing as mp
import os
from pathlib import Path
import pickle
import typing

import pytest

from gobridge import InvalidArgumentError, RuntimeOptions

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="module")
def greeter_api():
    import greeter
    binary = ROOT / "bin" / ("greeter.exe" if os.name == "nt" else "greeter")
    yield greeter, binary
    greeter.shutdown_sync()


def test_plain_functions_and_typed_constructors(greeter_api):
    api, binary = greeter_api
    assert typing.get_type_hints(api.greet)["return"] is str
    assert typing.get_type_hints(api.Greeter.reset)["return"] is type(None)
    sig = inspect.signature(api.Greeter)
    assert sig.parameters["prefix"].kind is inspect.Parameter.KEYWORD_ONLY
    assert sig.parameters["prefix"].default is None
    with api.SyncGreeter(binary, prefix="Hi, ") as greeter:
        assert greeter.greet(name="World") == "Hello, World!"
        assert greeter.welcome(name="Sam") == "Hi, Sam"
        assert isinstance(greeter.stats(), api.Stats)
        assert greeter.stats().calls == 1
        assert greeter.reset() is None
        assert greeter.stats().calls == 0
        with pytest.raises(TypeError):
            greeter.welcome("positional")
        with pytest.raises(InvalidArgumentError):
            greeter.welcome(name=42)


def test_runtime_options_and_go_options_are_separate(greeter_api):
    api, binary = greeter_api
    with api.SyncGreeter(prefix="Scoped, ", _runtime=RuntimeOptions(command=binary, timeout=2)) as greeter:
        assert greeter.welcome(name="Sam") == "Scoped, Sam"
        assert greeter.timeout == 2
    with api.SyncGreeter(binary) as greeter:
        assert greeter.welcome(name="Sam") == "Welcome, Sam"


def test_configured_clients_have_independent_concurrent_state(greeter_api):
    api, binary = greeter_api
    with api.SyncGreeter(binary, prefix="A: ") as first, api.SyncGreeter(binary, prefix="B: ") as second:
        with ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(lambda n: first.welcome(name=str(n)), range(80)))
        assert results == [f"A: {n}" for n in range(80)]
        assert first.stats().calls == 80
        assert second.stats().calls == 0
        assert first.stats().process_id != second.stats().process_id


async def test_module_functions_and_async_share_configured_default(greeter_api):
    api, binary = greeter_api
    await api.shutdown()
    api.configure(command=binary, prefix="Default, ")
    assert await api.greet(name="World") == "Hello, World!"
    parent_pid = (await api.stats()).process_id
    results = await asyncio.gather(*(api.welcome(name=str(i)) for i in range(20)))
    assert results == [f"Default, {i}" for i in range(20)]
    assert (await api.stats()).calls == 20
    async with api.session(command=binary, prefix="Scoped, ") as scoped:
        assert (await api.stats()).process_id != parent_pid
        assert await api.welcome(name="Sam") == "Scoped, Sam"
        assert (await scoped.stats()).calls == 1
    assert (await api.stats()).process_id == parent_pid
    assert (await api.stats()).calls == 20
    await api.shutdown()


def test_pickle_reconstructs_options_with_fresh_state(greeter_api):
    api, binary = greeter_api
    with api.SyncGreeter(binary, prefix="Restored, ") as original:
        original.welcome(name="one")
        clone = pickle.loads(pickle.dumps(original))
        with clone:
            assert clone.stats().calls == 0
            assert clone.stats().process_id != original.stats().process_id
            assert clone.welcome(name="two") == "Restored, two"
        assert original.stats().calls == 1


def _child_object(client, connection):
    try:
        with client:
            connection.send((client.welcome(name="child"), client.stats().calls, client.stats().process_id))
    finally:
        connection.close()


@pytest.mark.parametrize("method", [m for m in ("spawn", "fork") if m in mp.get_all_start_methods()])
def test_multiprocessing_owns_new_object_with_same_options(greeter_api, method):
    api, binary = greeter_api
    ctx = mp.get_context(method)
    with api.SyncGreeter(binary, prefix="Child, ") as parent:
        parent.welcome(name="parent")
        parent_pid = parent.stats().process_id
        receive, send = ctx.Pipe(duplex=False)
        child = ctx.Process(target=_child_object, args=(parent, send))
        child.start()
        send.close()
        try:
            assert receive.poll(10), "child did not respond"
            greeting, calls, pid = receive.recv()
            assert (greeting, calls) == ("Child, child", 1)
            assert pid != parent_pid
            child.join(5)
            assert child.exitcode == 0
        finally:
            receive.close()
            if child.is_alive():
                child.terminate()
                child.join(5)
        assert parent.stats().process_id == parent_pid
        assert parent.stats().calls == 1


async def test_async_first_api_and_sync_guard_do_not_spawn(greeter_api):
    api, binary = greeter_api
    await api.shutdown()
    api.configure(command=binary)
    assert inspect.iscoroutinefunction(api.greet)
    assert inspect.iscoroutinefunction(api.Greeter.welcome)
    assert not inspect.iscoroutinefunction(api.greet_sync)
    assert not hasattr(api, "aio") and not hasattr(api, "control")
    assert "greet" in api.__all__ and "greet_sync" in api.__all__
    for action in [lambda: api.greet_sync(name="x"), api.shutdown_sync, api.session_sync]:
        with pytest.raises(RuntimeError, match="event loop"):
            action()
    assert api._bridge_defaults._default is None
    sync_client = api.SyncGreeter(binary)
    try:
        with pytest.raises(RuntimeError, match="event loop"):
            sync_client.welcome(name="x")
        assert sync_client._transport is None
    finally:
        await sync_client.aclose()
    assert await api.greet(name="World") == "Hello, World!"
    await api.shutdown()


def test_session_context_form_is_explicit(greeter_api):
    api, binary = greeter_api
    with pytest.raises(TypeError, match="async with"):
        with api.session(command=binary):
            pytest.fail("wrong context form entered")
    with pytest.raises(TypeError, match="async with"):
        with api.Greeter(binary):
            pytest.fail("async client entered synchronously")
    scope = api.session_sync(command=binary)
    async def wrong_form():
        with pytest.raises(TypeError, match="with session_sync"):
            async with scope:
                pytest.fail("sync session entered asynchronously")
    asyncio.run(wrong_form())
