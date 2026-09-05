"""Module defaults behave like ordinary reusable library objects."""
import asyncio
import concurrent.futures
from functools import partial
import multiprocessing as mp
import os
from pathlib import Path
import sys
import textwrap
import threading

import pytest

from gobridge import Client, ClosedError, DaemonError, RuntimeOptions
from gobridge.defaults import DefaultControl
from textkit import SyncTextKit as TextKit

ROOT = Path(__file__).resolve().parents[2]
BINARY = ROOT / "bin" / ("textkit.exe" if os.name == "nt" else "textkit")


@pytest.fixture
def control():
    control = DefaultControl(partial(TextKit, BINARY))
    yield control
    control.close()


def test_default_is_lazy_and_explicit_reset_creates_fresh_session(control):
    assert control._default is None
    original = control.client()
    assert original._transport is None
    assert control.client() is original
    assert control.start() is original
    pid = original.health().process_id
    proc = original._transport.proc
    assert original.analyze(text="cached").computation == 1

    control.close()
    assert proc.poll() is not None
    with pytest.raises(ClosedError):
        original.health()
    replacement = control.client()
    assert replacement is not original
    assert replacement._transport is None
    assert replacement.health().process_id != pid
    assert replacement.analyze(text="cached").computation == 1


def test_configuration_is_explicit_and_survives_reset(control):
    control.configure(_runtime=RuntimeOptions(timeout=2, max_pending=5))
    original = control.client()
    assert (original.timeout, original.max_pending) == (2, 5)
    # Creating the lazy object already fixes its configuration.
    with pytest.raises(RuntimeError, match="already exists"):
        control.configure(_runtime=RuntimeOptions(timeout=4))
    assert control.client() is original
    control.close()
    assert control.client().max_pending == 5
    control.close()
    control.configure(_runtime=RuntimeOptions(timeout=4))
    assert control.client().timeout == 4
    assert control.client().max_pending == 128


def test_default_creation_and_cold_start_are_shared_across_threads():
    created = []

    def factory(**kwargs):
        client = TextKit(BINARY, **kwargs)
        created.append(client)
        return client

    control = DefaultControl(factory)
    barrier = threading.Barrier(12)

    def invoke(_):
        barrier.wait(timeout=5)
        client = control.client()
        return client, client.analyze(text="one computation")

    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            results = list(pool.map(invoke, range(12)))
        assert len(created) == 1
        assert all(client is created[0] for client, _ in results)
        assert len({result.process_id for _, result in results}) == 1
        assert {result.computation for _, result in results} == {1}
    finally:
        control.close()


def test_sync_and_repeated_async_loops_share_default(control):
    original = control.client().analyze(text="same cache")

    async def invoke():
        return await control.client().acall("analyze", {"text": "same cache"})

    assert asyncio.run(invoke())["process_id"] == original.process_id
    result = asyncio.run(invoke())
    assert result["computation"] == original.computation
    assert result["process_id"] == original.process_id


def test_nested_scopes_restore_clients_and_keep_default_separate(control):
    default = control.start()
    default_pid = default.health().process_id
    with control.scope(_runtime=RuntimeOptions(timeout=2)) as outer:
        assert control.client() is outer
        assert control.start() is outer
        assert outer.timeout == 2
        outer_pid = outer.health().process_id
        with control.scope() as inner:
            assert control.client() is inner
            inner_pid = inner.health().process_id
            assert len({default_pid, outer_pid, inner_pid}) == 3
        assert control.client() is outer
        with pytest.raises(ClosedError):
            inner.health()
        assert outer.health().process_id == outer_pid
    assert control.client() is default
    assert default.health().process_id == default_pid
    with pytest.raises(ClosedError):
        outer.health()


def test_exception_restores_scope_and_reaps_client(control):
    default = control.client()
    with pytest.raises(ValueError, match="body failed"):
        with control.scope() as scoped:
            proc = scoped._transport.proc
            raise ValueError("body failed")
    assert control.client() is default
    assert proc.poll() is not None


def test_reset_inside_scope_closes_only_default(control):
    default = control.start()
    with control.scope() as scoped:
        pid = scoped.health().process_id
        control.close()
        assert control.client() is scoped
        assert scoped.health().process_id == pid
        with pytest.raises(ClosedError):
            default.health()
    assert control.client() is not default


def test_scopes_do_not_inherit_default_configuration(control):
    control.configure(_runtime=RuntimeOptions(timeout=2, max_pending=3))
    with control.scope(_runtime=RuntimeOptions(timeout=4)) as scoped:
        assert (scoped.timeout, scoped.max_pending) == (4, 128)
    assert control.client().max_pending == 3


def test_scope_does_not_switch_an_unrelated_thread(control):
    default = control.start()
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        assert pool.submit(control.client).result(timeout=3) is default
        with control.scope() as scoped:
            assert control.client() is scoped
            assert pool.submit(control.client).result(timeout=3) is default


async def test_async_scopes_are_isolated_and_child_tasks_inherit(control):
    default = await asyncio.to_thread(control.start)
    default_pid = (await default.acall("health"))["process_id"]
    ready = [asyncio.Event(), asyncio.Event()]

    async def scoped_task(index):
        async with control.scope() as client:
            pid = (await control.client().acall("health"))["process_id"]
            ready[index].set()
            await asyncio.wait_for(ready[1-index].wait(), timeout=3)

            async def inherited():
                assert control.client() is client
                return (await control.client().acall("health"))["process_id"]

            assert await asyncio.create_task(inherited()) == pid
            async with control.scope() as nested:
                assert control.client() is nested
                assert (await nested.acall("health"))["process_id"] != pid
            assert control.client() is client
        assert control.client() is default
        with pytest.raises(ClosedError):
            await client.acall("health")
        return pid

    first, second = await asyncio.gather(scoped_task(0), scoped_task(1))
    assert len({default_pid, first, second}) == 3
    assert control.client() is default


async def test_cancelled_async_scope_reaps_its_client_and_restores_context(control):
    default = control.client()
    entered = asyncio.Event()
    owned = []

    async def invoke():
        try:
            async with control.scope() as client:
                owned.append(client._transport.proc)
                entered.set()
                await client.acall("wait", {"milliseconds": 10000})
        finally:
            assert control.client() is default

    task = asyncio.create_task(invoke())
    await asyncio.wait_for(entered.wait(), timeout=3)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert owned[0].poll() is not None
    assert control.client() is default


async def test_cancelled_scope_entry_reaps_a_daemon_still_starting(tmp_path):
    ready, release = tmp_path / "ready", tmp_path / "release"
    script = tmp_path / "slow_handshake.py"
    script.write_text(textwrap.dedent(f"""
        import json, pathlib, sys, time
        hello = json.loads(sys.stdin.readline())
        pathlib.Path({str(ready)!r}).touch()
        deadline = time.monotonic() + 10
        while not pathlib.Path({str(release)!r}).exists():
            if time.monotonic() >= deadline:
                sys.exit(2)
            time.sleep(0.005)
        print(json.dumps({{"id": hello["id"], "result": {{"protocol": 1}}}}), flush=True)
        sys.stdin.read()
    """), encoding="utf-8")
    processes = []

    class RecordingClient(Client):
        def start(self):
            super().start()
            processes.append(self._transport.proc)
            return self

    control = DefaultControl(partial(RecordingClient, [sys.executable, "-u", str(script)]))

    async def enter_scope():
        async with control.scope():
            pytest.fail("a cancelled scope must not enter its body")

    task = asyncio.create_task(enter_scope())
    try:
        async def wait_for_handshake():
            while not ready.exists():
                await asyncio.sleep(0.005)

        await asyncio.wait_for(wait_for_handshake(), timeout=3)
        task.cancel()
        release.touch()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert len(processes) == 1
        assert processes[0].poll() is not None
        assert control._default is None
        assert control._scope.get() is None
    finally:
        release.touch()
        if not task.done():
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
        control.close()


def test_dead_default_is_not_silently_replaced(control):
    default = control.start()
    proc = default._transport.proc
    proc.kill()
    proc.wait(timeout=3)
    with pytest.raises(DaemonError):
        control.client().health()
    assert control.client() is default
    control.close()
    assert control.client().health().process_id != proc.pid


def _fork_control_call(control, connection):
    try:
        with control.client() as client:
            result = client.analyze(text="fork cache")
            connection.send((result.process_id, result.computation))
    finally:
        connection.close()


@pytest.mark.skipif(not hasattr(os, "fork"), reason="fork lock reset is POSIX-only")
def test_fork_resets_control_lock_and_preserves_parent_default(control):
    parent = control.start()
    original = parent.analyze(text="fork cache")
    context = mp.get_context("fork")
    receiving, sending = context.Pipe(duplex=False)
    child = context.Process(target=_fork_control_call, args=(control, sending))
    locked, release = threading.Event(), threading.Event()

    def hold_lock():
        with control._lock:
            locked.set()
            release.wait(timeout=10)

    thread = threading.Thread(target=hold_lock)
    thread.start()
    assert locked.wait(timeout=3)
    try:
        child.start()
        sending.close()
        assert receiving.poll(5), "child inherited a locked default control"
        child_pid, computation = receiving.recv()
        assert child_pid != original.process_id
        assert computation == 1
        child.join(timeout=3)
        assert child.exitcode == 0
    finally:
        release.set()
        thread.join(timeout=3)
        if child.is_alive():
            child.kill()
            child.join(timeout=3)
        receiving.close()
        sending.close()
    assert control.client() is parent
    assert parent.analyze(text="fork cache") == original
