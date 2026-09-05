"""Bounded, multiplexed stdio transport with explicit process ownership.

Clients are lazy and pickle by configuration. A client owns one daemon per
Python process, shared by all of that client's threads and asyncio callers.
"""
from __future__ import annotations

import atexit
import asyncio
import concurrent.futures as futures
import dataclasses
from functools import lru_cache
import json
import math
import os
import queue
import shutil
import subprocess
import threading
import types
import typing
import weakref
from pathlib import Path

MAX_FRAME = 1024 * 1024


def resolve_binary(module_file: str, name: str) -> str:
    """Resolve a wheel-bundled executable first, then the user's PATH."""
    filename = name + (".exe" if os.name == "nt" else "")
    bundled = Path(module_file).resolve().parent / "_bin" / filename
    if bundled.is_file():
        return str(bundled)
    found = shutil.which(filename)
    if found:
        return found
    raise FileNotFoundError(f"{filename} is not bundled or on PATH; pass command='/path/to/{filename}'")


class BridgeError(Exception):
    def __init__(self, code: str, message: str):
        self.code, self.message = code, message
        super().__init__(f"{code}: {message}")


class InvalidArgumentError(BridgeError):
    pass


class BusyError(BridgeError):
    pass


class RequestTimeout(BridgeError, TimeoutError):
    pass


class DaemonError(BridgeError):
    pass


class ClosedError(BridgeError):
    pass


def _error(data):
    cls = {"invalid_argument": InvalidArgumentError, "busy": BusyError,
           "deadline_exceeded": RequestTimeout}.get(data["code"], BridgeError)
    return cls(data["code"], data["message"])


def _json_default(value):
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        return dataclasses.asdict(value)
    raise TypeError(f"Cannot encode {type(value).__name__}")


T = typing.TypeVar("T")


@lru_cache(maxsize=512)
def _type_hints(cls):
    return typing.get_type_hints(cls)


def decode(cls: type[T], value: typing.Any) -> T:
    """Reconstruct generated dataclasses, recursively including containers."""
    if value is None:
        return value
    origin, args = typing.get_origin(cls), typing.get_args(cls)
    if origin in (types.UnionType, typing.Union):
        return decode(next(t for t in args if t is not type(None)), value)
    if dataclasses.is_dataclass(cls):
        hints = _type_hints(cls)
        return cls(**{k: decode(hints[k], v) for k, v in value.items()})
    if origin is list:
        return [decode(args[0], v) for v in value]
    if origin is dict:
        return {k: decode(args[1], v) for k, v in value.items()}
    return value


class _Transport:
    def __init__(self, command, max_pending, expected_schema):
        self.owner_pid = os.getpid()
        self.lock = threading.Lock()
        self.pending = {}
        self.seq = 0
        self.max_pending = max_pending
        self.failure = None
        self.outbox = queue.Queue(maxsize=max_pending * 2 + 4)
        # Unbuffered FileIO can be detached in a fork child without acquiring
        # locks owned by a vanished Python reader/writer thread.
        self.proc = subprocess.Popen(
            [*command, "serve"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=None, bufsize=0, close_fds=True,
        )
        self.reader = threading.Thread(target=self._read, daemon=True, name="gobridge-reader")
        self.writer = threading.Thread(target=self._write, daemon=True, name="gobridge-writer")
        self.reader.start()
        self.writer.start()
        try:
            _, f = self.submit("$hello", {}, 5)
            hello = f.result(timeout=5)
            if hello.get("protocol") != 1:
                raise DaemonError("protocol", "unsupported daemon protocol version")
            if expected_schema is not None and hello.get("schema_hash") != expected_schema:
                raise DaemonError("schema_mismatch", "bindings and daemon differ; regenerate bindings or install the matching binary")
        except BaseException:
            self.close()
            raise

    def submit(self, method, params, timeout):
        if timeout is not None and (not math.isfinite(timeout) or not 0 < timeout <= 86400):
            raise ValueError("timeout must be finite and in (0, 86400] seconds")
        with self.lock:
            if self.failure:
                raise self.failure
            if len(self.pending) >= self.max_pending:
                raise BusyError("busy", "client pending-request limit reached")
            self.seq += 1
            request_id = str(self.seq)
            message = {"id": request_id, "method": method, "params": params}
            if timeout is not None:
                message["timeout_ms"] = max(1, math.ceil(timeout * 1000))
            data = json.dumps(message, default=_json_default, allow_nan=False,
                              separators=(",", ":")).encode() + b"\n"
            if len(data) > MAX_FRAME:
                raise ValueError("request exceeds frame limit")
            f = futures.Future()
            self.pending[request_id] = f
            try:
                self.outbox.put_nowait(data)
            except queue.Full:
                del self.pending[request_id]
                raise BusyError("busy", "client outbound queue is full") from None
            return request_id, f

    def cancel(self, request_id):
        with self.lock:
            f = self.pending.pop(request_id, None)
            if self.failure or f is None:
                return
        f.cancel()
        try:
            self.outbox.put_nowait(json.dumps({"method": "$cancel", "params": {"id": request_id}}).encode() + b"\n")
        except queue.Full:
            # Without room to deliver cancellation, fail the transport rather
            # than silently leaving an operation running without an owner.
            self.close()

    def _fail(self, error):
        with self.lock:
            if self.failure:
                return
            self.failure = error
            pending, self.pending = self.pending, {}
        for f in pending.values():
            if not f.done():
                f.set_exception(error)
        try:
            self.outbox.put_nowait(None)
        except queue.Full:
            pass

    def _write(self):
        try:
            while True:
                data = self.outbox.get()
                if data is None or self.failure:
                    return
                view = memoryview(data)
                while view:
                    n = self.proc.stdin.write(view)
                    if not n:
                        raise BrokenPipeError("daemon stopped reading")
                    view = view[n:]
        except (OSError, ValueError) as e:
            self._fail(DaemonError("transport", str(e)))

    def _read(self):
        try:
            # BufferedReader is deliberately avoided for fork detachment.
            buffer = bytearray()
            while True:
                chunk = self.proc.stdout.read(65536)
                if not chunk:
                    raise EOFError("daemon exited; in-flight outcomes may be unknown")
                buffer.extend(chunk)
                while b"\n" in buffer:
                    line, _, rest = buffer.partition(b"\n")
                    buffer = bytearray(rest)
                    if len(line) > MAX_FRAME:
                        raise ValueError("response exceeds frame limit")
                    msg = json.loads(line)
                    if not isinstance(msg, dict) or not isinstance(msg.get("id"), str):
                        raise ValueError("invalid daemon response")
                    if ("error" in msg) == ("result" in msg):
                        raise ValueError("response needs exactly one of result or error")
                    with self.lock:
                        f = self.pending.pop(msg["id"], None)
                    # A late result for a cancelled/timed-out request is ignored.
                    if f is not None and not f.done():
                        if "error" in msg:
                            f.set_exception(_error(msg["error"]))
                        else:
                            f.set_result(msg["result"])
                if len(buffer) > MAX_FRAME:
                    raise ValueError("response exceeds frame limit")
        except Exception as e:
            self._fail(DaemonError("transport", str(e)))

    def detach_after_fork(self):
        # Only close this child's copies. Never signal or wait for the parent's
        # daemon. FileIO is unbuffered; Popen must not try to reap it here.
        for stream in (self.proc.stdin, self.proc.stdout):
            stream.close()
        self.proc.returncode = 0

    def close(self):
        if os.getpid() != self.owner_pid:
            self.detach_after_fork()
            return
        self._fail(ClosedError("closed", "client has been closed"))
        # Kill first if a writer is blocked on a stopped/non-reading daemon.
        if self.proc.poll() is None:
            self.proc.terminate()
        try:
            self.proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=2)
        for thread in (self.reader, self.writer):
            if thread is not threading.current_thread():
                thread.join(timeout=2)
        for stream in (self.proc.stdin, self.proc.stdout):
            stream.close()


_clients = weakref.WeakSet()
_fork_lock = threading.RLock()


def _before_fork():
    _fork_lock.acquire()


def _after_parent():
    _fork_lock.release()


def _after_child():
    global _fork_lock
    for client in list(_clients):
        if client._transport is not None:
            client._transport.detach_after_fork()
        if client._finalizer is not None:
            client._finalizer.detach()
        client._finalizer = None
        client._transport = None
    _fork_lock = threading.RLock()


if hasattr(os, "register_at_fork"):
    os.register_at_fork(before=_before_fork, after_in_parent=_after_parent,
                        after_in_child=_after_child)


class Client:
    """Lazy process-owned client. Use a context manager or call close().

    command is an executable path or argv prefix; no shell is involved.
    max_pending bounds local memory independently of daemon concurrency.
    Failures never automatically replay operations or restart a stateful daemon.
    """

    def __init__(self, command: str | os.PathLike | typing.Sequence[str], *,
                 timeout: float = 30, max_pending: int = 128, expected_schema: str | None = None):
        if isinstance(command, (str, os.PathLike)):
            command = [os.fspath(command)]
        self.command = tuple(command)
        if not self.command or max_pending < 1:
            raise ValueError("a command and positive max_pending are required")
        if not math.isfinite(timeout) or not 0 < timeout <= 86400:
            raise ValueError("default timeout must be finite and in (0, 86400]")
        self.timeout, self.max_pending = timeout, max_pending
        self.expected_schema = expected_schema
        self._transport = None
        self._finalizer = None
        self._closed = False
        with _fork_lock:
            _clients.add(self)

    def __getstate__(self):
        return self.command, self.timeout, self.max_pending, self.expected_schema, self._closed

    def __setstate__(self, state):
        command, timeout, max_pending, expected_schema, closed = state
        Client.__init__(self, command, timeout=timeout, max_pending=max_pending, expected_schema=expected_schema)
        self._closed = closed

    def _ensure(self):
        with _fork_lock:
            if self._closed:
                raise ClosedError("closed", "client has been closed")
            if self._transport is None:
                self._transport = _Transport(self.command, self.max_pending, self.expected_schema)
                self._finalizer = weakref.finalize(self, self._transport.close)
            return self._transport

    def start(self):
        self._ensure()
        return self

    def call(self, method: str, params: dict | None = None, *, timeout: float | None = None):
        t = self._ensure()
        timeout = self.timeout if timeout is None else timeout
        request_id, f = t.submit(method, {} if params is None else params, timeout)
        try:
            return f.result(timeout=timeout)
        except futures.TimeoutError:
            t.cancel(request_id)
            raise RequestTimeout("deadline_exceeded", "request deadline exceeded") from None
        except BaseException:
            t.cancel(request_id)
            raise

    async def acall(self, method: str, params: dict | None = None, *, timeout: float | None = None):
        # Cold process startup/handshake runs off the event loop. Shield it so
        # cancellation cannot abandon an untracked daemon under construction.
        t = self._transport
        if t is None:
            startup = asyncio.create_task(asyncio.to_thread(self._ensure))
            try:
                t = await asyncio.shield(startup)
            except asyncio.CancelledError:
                # Retrieve errors if startup finishes after caller cancellation.
                startup.add_done_callback(lambda done: None if done.cancelled() else done.exception())
                raise
        elif self._closed:
            raise ClosedError("closed", "client has been closed")
        timeout = self.timeout if timeout is None else timeout
        request_id, f = t.submit(method, {} if params is None else params, timeout)
        af = asyncio.wrap_future(f)
        try:
            return await asyncio.wait_for(asyncio.shield(af), timeout)
        except asyncio.TimeoutError:
            af.add_done_callback(lambda done: None if done.cancelled() else done.exception())
            t.cancel(request_id)
            raise RequestTimeout("deadline_exceeded", "request deadline exceeded") from None
        except BaseException:
            af.add_done_callback(lambda done: None if done.cancelled() else done.exception())
            t.cancel(request_id)
            raise

    def close(self):
        with _fork_lock:
            self._closed = True
            if self._transport is not None:
                self._transport.close()
                self._transport = None
            if self._finalizer is not None:
                self._finalizer.detach()
                self._finalizer = None

    async def aclose(self):
        await asyncio.to_thread(self.close)

    def __enter__(self):
        return self.start()

    def __exit__(self, *args):
        self.close()

    async def __aenter__(self):
        await asyncio.to_thread(self.start)
        return self

    async def __aexit__(self, *args):
        await self.aclose()


class AsyncClient(Client):
    """Base for generated async clients; supports async context management."""


def _shutdown():
    for client in list(_clients):
        client.close()


atexit.register(_shutdown)
