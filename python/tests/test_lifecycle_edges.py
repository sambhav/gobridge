"""Adversarial ownership and wire tests against real child processes.

The small Python peers deliberately violate protocol boundaries that the Go
daemon normally guarantees. Ownership tests use the actual Go example daemon.
"""
import asyncio
import concurrent.futures
import dataclasses
import json
import multiprocessing as mp
import os
from pathlib import Path
import pickle
import subprocess
import sys
import textwrap
import threading
import time

import pytest

from gobridge import Client, ClosedError, DaemonError, InvalidArgumentError, RequestTimeout, RuntimeOptions
from textkit import SyncTextKit as TextKit

ROOT = Path(__file__).resolve().parents[2]
BINARY = ROOT / "bin" / ("textkit.exe" if os.name == "nt" else "textkit")


def _peer(tmp_path, body):
    path = tmp_path / "peer.py"
    path.write_text(textwrap.dedent(body), encoding="utf-8")
    return [sys.executable, "-u", str(path)]


async def _eventually(predicate, timeout=3):
    deadline = asyncio.get_running_loop().time() + timeout
    while not predicate():
        if asyncio.get_running_loop().time() >= deadline:
            pytest.fail("timed out waiting for subprocess state")
        await asyncio.sleep(0.005)


async def test_cancelled_cold_start_keeps_daemon_owned_and_skips_operation(tmp_path):
    ready, release, calls = (tmp_path / name for name in ("ready", "release", "calls"))
    command = _peer(tmp_path, f"""
        import json, os, pathlib, sys, time
        for line in sys.stdin:
            request = json.loads(line)
            with open({str(calls)!r}, "a") as log:
                log.write(request["method"] + "\\n")
            if request["method"] == "$hello":
                pathlib.Path({str(ready)!r}).touch()
                deadline = time.monotonic() + 10
                while not pathlib.Path({str(release)!r}).exists():
                    if time.monotonic() >= deadline:
                        sys.exit(2)
                    time.sleep(0.005)
                result = {{"protocol": 1}}
            else:
                result = {{"pid": os.getpid()}}
            print(json.dumps({{"id": request["id"], "result": result}}), flush=True)
    """)
    client = Client(command)
    proc = None
    operation = asyncio.create_task(client.acall("must-not-run"))
    try:
        await _eventually(ready.exists)
        operation.cancel()
        with pytest.raises(asyncio.CancelledError):
            await operation
        release.touch()
        await _eventually(lambda: client._transport is not None)
        proc = client._transport.proc
        result = await client.acall("ping")
        assert result["pid"] == proc.pid
        assert calls.read_text().splitlines() == ["$hello", "ping"]
    finally:
        release.touch()
        if not operation.done():
            operation.cancel()
            await asyncio.gather(operation, return_exceptions=True)
        await client.aclose()
    assert proc is not None and proc.poll() is not None


def test_event_loop_shutdown_cancels_pending_and_allows_next_loop(tmp_path):
    requests = tmp_path / "requests"
    command = _peer(tmp_path, f"""
        import json, os, pathlib, sys
        for line in sys.stdin:
            request = json.loads(line)
            if request["method"] == "wait":
                pathlib.Path({str(requests)!r}).touch()
                continue
            if request["method"] == "$cancel":
                continue
            result = {{"protocol": 1}} if request["method"] == "$hello" else {{"pid": os.getpid()}}
            print(json.dumps({{"id": request["id"], "result": result}}), flush=True)
    """)
    errors = []
    with Client(command) as client:
        proc = client._transport.proc

        async def leave_task_pending():
            asyncio.get_running_loop().set_exception_handler(lambda loop, context: errors.append(context))
            task = asyncio.create_task(client.acall("wait"))
            await _eventually(requests.exists)
            assert not task.done()
            # asyncio.run cancels this task before closing its event loop.
            return task

        cancelled = asyncio.run(leave_task_pending())
        assert cancelled.cancelled()
        assert not client._transport.pending
        assert asyncio.run(client.acall("ping"))["pid"] == proc.pid
        assert errors == []


@pytest.mark.parametrize("response", [
    "[]",
    '{"id": request["id"], "result": {}, "error": {"code": "x", "message": "bad"}}',
    '{"id": request["id"]}',
    '{"id": request["id"], "error": None}',
    '{"id": request["id"], "error": {}}',
    '{"id": request["id"], "error": {"code": [], "message": "bad"}}',
    '{"id": request["id"], "error": {"code": 123, "message": "bad"}}',
    '{"id": request["id"], "error": {"code": "bad", "message": []}}',
], ids=["array", "result-and-error", "missing-outcome", "null-error", "missing-error-fields",
        "unhashable-code", "non-string-code", "non-string-message"])
def test_invalid_response_completes_waiter_with_transport_error(tmp_path, response):
    command = _peer(tmp_path, f"""
        import json, sys
        for line in sys.stdin:
            request = json.loads(line)
            if request["method"] == "$hello":
                response = {{"id": request["id"], "result": {{"protocol": 1}}}}
            else:
                response = {response}
            print(json.dumps(response), flush=True)
    """)
    with Client(command) as client:
        with pytest.raises(DaemonError, match="transport"):
            client.call("ping", timeout=0.5)
        # Corruption invalidates the session; no later call is replayed.
        with pytest.raises(DaemonError, match="transport"):
            client.call("ping", timeout=0.5)


@pytest.mark.parametrize("frame", [b"not-json\n", b"x" * (1024 * 1024 + 1)],
                         ids=["non-json-stdout", "oversized-unfinished-frame"])
def test_stdout_corruption_fails_without_waiting_for_newline(tmp_path, frame):
    payload = tmp_path / "payload"
    payload.write_bytes(frame)
    command = _peer(tmp_path, f"""
        import json, pathlib, sys
        request = json.loads(sys.stdin.readline())
        print(json.dumps({{"id": request["id"], "result": {{"protocol": 1}}}}), flush=True)
        sys.stdin.readline()
        sys.stdout.buffer.write(pathlib.Path({str(payload)!r}).read_bytes())
        sys.stdout.buffer.flush()
        sys.stdin.read()
    """)
    with Client(command) as client:
        with pytest.raises(DaemonError, match="transport"):
            client.call("ping", timeout=1)


@pytest.mark.parametrize("result", ["None", "[]", "7"], ids=["null", "array", "integer"])
def test_malformed_handshake_is_a_typed_daemon_error(tmp_path, result):
    command = _peer(tmp_path, f"""
        import json, sys
        request = json.loads(sys.stdin.readline())
        print(json.dumps({{"id": request["id"], "result": {result}}}), flush=True)
        sys.stdin.read()
    """)
    with pytest.raises(DaemonError):
        with Client(command):
            pass


def test_eof_fails_all_pending_requests_and_reaps_daemon(tmp_path):
    command = _peer(tmp_path, """
        import json, sys
        hello = json.loads(sys.stdin.readline())
        print(json.dumps({"id": hello["id"], "result": {"protocol": 1}}), flush=True)
        for _ in range(4):
            sys.stdin.readline()
        # Exit without responding to any of the four in-flight requests.
    """)
    with Client(command) as client:
        transport = client._transport
        submitted = [transport.submit("wait", {}, 2)[1] for _ in range(4)]
        for pending in submitted:
            with pytest.raises(DaemonError, match="daemon exited"):
                pending.result(timeout=2)
        assert transport.pending == {}
        with pytest.raises(DaemonError):
            client.call("ping")
        assert transport.proc.wait(timeout=2) == 0


def test_concurrent_close_reaps_once_and_resolves_all_callers():
    client = TextKit(BINARY).start()
    transport = client._transport
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
            pending = [pool.submit(client.wait, milliseconds=10000) for _ in range(8)]
            deadline = time.monotonic() + 3
            while client.health().active != len(pending):
                if time.monotonic() >= deadline:
                    pytest.fail("Go handlers did not all start")
                time.sleep(0.005)
            closers = [pool.submit(client.close) for _ in range(8)]
            for closing in closers:
                closing.result(timeout=5)
            for result in pending:
                with pytest.raises((ClosedError, DaemonError)):
                    result.result(timeout=2)
        assert transport.proc.poll() is not None
        assert not transport.reader.is_alive()
        assert not transport.writer.is_alive()
    finally:
        client.close()


def _idle_fork_child(connection):
    connection.send("ready")
    if connection.poll(10):
        connection.recv()
    connection.close()


@pytest.mark.skipif(not hasattr(os, "fork"), reason="fork descriptor ownership is POSIX-only")
def test_idle_fork_child_does_not_keep_parent_daemon_stdin_alive():
    context = mp.get_context("fork")
    parent_connection, child_connection = context.Pipe()
    with TextKit(BINARY) as client:
        proc = client._transport.proc
        child = context.Process(target=_idle_fork_child, args=(child_connection,))
        try:
            child.start()
            child_connection.close()
            assert parent_connection.poll(3), "fork child did not start"
            assert parent_connection.recv() == "ready"
            # The child never touches the client. Its inherited copy of the
            # writer must already be detached for the daemon to receive EOF.
            proc.stdin.close()
            assert proc.wait(timeout=3) == 0
            assert child.is_alive()
        finally:
            if child.is_alive():
                parent_connection.send("stop")
            if child.pid is not None:
                child.join(timeout=3)
                if child.is_alive():
                    child.kill()
                    child.join(timeout=3)
            parent_connection.close()
            child_connection.close()


def test_runtime_options_are_frozen_keyword_only_and_snapshot_command():
    command = [str(BINARY)]
    options = RuntimeOptions(command=command, timeout=2, startup_timeout=3, max_pending=4)
    command.append("unexpected")
    assert options.command == (str(BINARY),)
    with pytest.raises(dataclasses.FrozenInstanceError):
        options.timeout = 9
    with pytest.raises(TypeError):
        RuntimeOptions(str(BINARY))
    with pytest.raises(ValueError):
        RuntimeOptions(startup_timeout=0)
    with pytest.raises(ValueError):
        RuntimeOptions(max_pending=1.5)


def test_constructor_snapshot_is_initialized_once_and_preserved_by_pickle(tmp_path):
    calls = tmp_path / "calls"
    command = _peer(tmp_path, f"""
        import json, os, sys
        config = None
        for line in sys.stdin:
            request = json.loads(line)
            with open({str(calls)!r}, "a") as log:
                log.write(json.dumps({{"pid": os.getpid(), "method": request["method"]}}) + "\\n")
            if request["method"] == "$hello":
                result = {{"protocol": 1, "constructor": {{}}}}
            elif request["method"] == "$init":
                config = request["params"]
                result = None
            else:
                result = {{"pid": os.getpid(), "config": config}}
            print(json.dumps({{"id": request["id"], "result": result}}), flush=True)
    """)
    config = {"items": [{"name": "original"}]}
    client = Client(command, init=config, startup_timeout=2)
    config["items"][0]["name"] = "mutated"
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(lambda _: client.call("ping"), range(8)))
        assert all(result["config"] == {"items": [{"name": "original"}]} for result in results)
        assert len({result["pid"] for result in results}) == 1
        with pickle.loads(pickle.dumps(client)) as cloned:
            clone_result = cloned.call("ping")
            assert cloned.startup_timeout == 2
            assert clone_result["pid"] != results[0]["pid"]
            assert clone_result["config"] == results[0]["config"]
        events = [json.loads(line) for line in calls.read_text().splitlines()]
        for pid in {event["pid"] for event in events}:
            methods = [event["method"] for event in events if event["pid"] == pid]
            assert methods[:2] == ["$hello", "$init"]
            assert methods.count("$init") == 1
    finally:
        client.close()


def test_constructor_failure_is_sticky_and_reaps_daemon(tmp_path, monkeypatch):
    command = _peer(tmp_path, """
        import json, sys
        for line in sys.stdin:
            request = json.loads(line)
            if request["method"] == "$hello":
                response = {"id": request["id"], "result": {"protocol": 1, "constructor": {}}}
            else:
                response = {"id": request["id"], "error": {"code": "invalid_argument", "message": "constructor failed"}}
            print(json.dumps(response), flush=True)
    """)
    popen, children = subprocess.Popen, []

    def record_process(*args, **kwargs):
        child = popen(*args, **kwargs)
        children.append(child)
        return child

    monkeypatch.setattr("gobridge.runtime.subprocess.Popen", record_process)
    client = Client(command, init={})
    try:
        for invoke in (client.start, lambda: client.call("ping"), lambda: asyncio.run(client.acall("ping"))):
            with pytest.raises(InvalidArgumentError, match="constructor failed"):
                invoke()
        assert len(children) == 1
        assert children[0].poll() is not None
        assert client._transport is None
    finally:
        client.close()


def test_hello_and_constructor_share_one_startup_budget(tmp_path):
    calls = tmp_path / "calls"
    command = _peer(tmp_path, f"""
        import json, sys, time
        for line in sys.stdin:
            request = json.loads(line)
            with open({str(calls)!r}, "a") as log:
                log.write(json.dumps(request) + "\\n")
            if request["method"] == "$hello":
                time.sleep(0.15)
                result = {{"protocol": 1, "constructor": {{}}}}
            else:
                time.sleep(0.3)
                result = None
            print(json.dumps({{"id": request["id"], "result": result}}), flush=True)
    """)
    client = Client(command, startup_timeout=0.35)
    try:
        with pytest.raises(RequestTimeout, match="startup"):
            client.start()
        events = [json.loads(line) for line in calls.read_text().splitlines()]
        assert [event["method"] for event in events] == ["$hello", "$init"]
        assert events[1]["timeout_ms"] < events[0]["timeout_ms"]
    finally:
        client.close()


def _gated_peer(tmp_path):
    ready, release = tmp_path / "ready", tmp_path / "release"
    command = _peer(tmp_path, f"""
        import json, os, pathlib, sys, time
        first = not pathlib.Path({str(ready)!r}).exists()
        for line in sys.stdin:
            request = json.loads(line)
            if request["method"] == "$hello":
                if first:
                    pathlib.Path({str(ready)!r}).touch()
                    deadline = time.monotonic() + 15
                    while not pathlib.Path({str(release)!r}).exists():
                        if time.monotonic() >= deadline:
                            sys.exit(2)
                        time.sleep(0.005)
                result = {{"protocol": 1}}
            else:
                result = {{"pid": os.getpid()}}
            print(json.dumps({{"id": request["id"], "result": result}}), flush=True)
    """)
    return command, ready, release


def _wait_for_file(path):
    deadline = time.monotonic() + 3
    while not path.exists():
        if time.monotonic() >= deadline:
            pytest.fail("daemon did not reach handshake")
        time.sleep(0.005)


def test_slow_startup_does_not_block_an_unrelated_sync_client(tmp_path):
    command, ready, release = _gated_peer(tmp_path)
    slow = Client(command)
    with TextKit(BINARY) as running, concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        startup = pool.submit(slow.start)
        try:
            _wait_for_file(ready)
            result = pool.submit(running.health).result(timeout=1)
            assert result.process_id == running._transport.proc.pid
            assert not startup.done()
        finally:
            release.touch()
            slow.close()
        startup.result(timeout=3)


def _fork_during_startup(client, connection):
    try:
        with client:
            connection.send(client.call("ping")["pid"])
            if connection.poll(10):
                connection.recv()
    finally:
        connection.close()


@pytest.mark.skipif(not hasattr(os, "fork"), reason="fork startup ownership is POSIX-only")
def test_fork_during_startup_detaches_unpublished_transport_and_resets_locks(tmp_path):
    command, ready, release = _gated_peer(tmp_path)
    client = Client(command, startup_timeout=10)
    context = mp.get_context("fork")
    parent_connection, child_connection = context.Pipe()
    child = context.Process(target=_fork_during_startup, args=(client, child_connection))
    # Bound regressions where fork incorrectly waits for a full handshake.
    unblock = threading.Timer(5, release.touch)
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        startup = pool.submit(client.start)
        try:
            _wait_for_file(ready)
            assert client._transport is None
            unblock.start()
            child.start()
            child_connection.close()
            assert parent_connection.poll(3), "fork child retained a startup lifecycle lock"
            child_pid = parent_connection.recv()
            assert not release.exists(), "fork waited for the parent's handshake"
            unblock.cancel()
            release.touch()
            assert startup.result(timeout=3) is client
            proc = client._transport.proc
            assert proc.pid != child_pid
            # The child's inherited *unpublished* stdin copy must be closed.
            proc.stdin.close()
            assert proc.wait(timeout=3) == 0
            assert child.is_alive()
        finally:
            unblock.cancel()
            release.touch()
            client.close()
            if child.is_alive():
                parent_connection.send("stop")
            if child.pid is not None:
                child.join(timeout=3)
                if child.is_alive():
                    child.kill()
                    child.join(timeout=3)
            parent_connection.close()
            child_connection.close()


async def test_cancelled_async_context_entry_reaps_starting_daemon(tmp_path, monkeypatch):
    command, ready, release = _gated_peer(tmp_path)
    popen, children = subprocess.Popen, []

    def record_process(*args, **kwargs):
        child = popen(*args, **kwargs)
        children.append(child)
        return child

    monkeypatch.setattr("gobridge.runtime.subprocess.Popen", record_process)
    client = Client(command)

    async def enter():
        async with client:
            pytest.fail("cancelled async context must not enter its body")

    task = asyncio.create_task(enter())
    try:
        await _eventually(ready.exists)
        task.cancel()
        release.touch()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert len(children) == 1
        assert children[0].poll() is not None
        with pytest.raises(ClosedError):
            client.start()
    finally:
        release.touch()
        if not task.done():
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
        await client.aclose()
