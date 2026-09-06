"""Bounded, multiplexed stdio transport with explicit process ownership.

Clients are lazy and pickle by configuration. A client owns one daemon per
Python process, shared by all of that client's threads and asyncio callers.
"""
from __future__ import annotations

import atexit
import base64
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
import sys
import threading
import time
import types
import typing
import weakref
from pathlib import Path

MAX_FRAME = 1024 * 1024


def _command_args(command):
    if isinstance(command, (str, os.PathLike)):
        command = (command,)
    command = tuple(os.fspath(part) for part in command)
    if not command:
        raise ValueError("a command is required")
    return command


def _validate_options(timeout, max_pending, startup_timeout):
    if not isinstance(max_pending, int) or isinstance(max_pending, bool) or max_pending < 1:
        raise ValueError("max_pending must be a positive integer")
    for name, value in (("timeout", timeout), ("startup_timeout", startup_timeout)):
        if not math.isfinite(value) or not 0 < value <= 86400:
            raise ValueError(f"{name} must be finite and in (0, 86400] seconds")


@dataclasses.dataclass(frozen=True, kw_only=True)
class RuntimeOptions:
    """Advanced process options, separate from a library's constructor inputs."""

    command: str | os.PathLike | typing.Sequence[str] | None = None
    timeout: float = 30
    max_pending: int = 128
    startup_timeout: float = 5

    def __post_init__(self):
        _validate_options(self.timeout, self.max_pending, self.startup_timeout)
        if self.command is not None:
            object.__setattr__(self, "command", _command_args(self.command))


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

    def __reduce__(self):
        # Exception.args contains the formatted display string, whereas our
        # constructor needs the two original wire fields. Preserve subclasses
        # and ordinary exception attributes across multiprocessing/pickle.
        return type(self), (self.code, self.message), self.__dict__


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
    if (not isinstance(data, dict) or not isinstance(data.get("code"), str)
            or not isinstance(data.get("message"), str)):
        raise ValueError("invalid daemon error envelope")
    cls = {"invalid_argument": InvalidArgumentError, "busy": BusyError,
           "deadline_exceeded": RequestTimeout}.get(data["code"], BridgeError)
    return cls(data["code"], data["message"])


def _json_default(value):
    if isinstance(value, bytes):
        return base64.b64encode(value).decode("ascii")
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        # JSON visits nested values itself. asdict would recursively copy the
        # entire graph before the encoder traverses that same graph again.
        return {field.metadata.get("wire_name", field.name): getattr(value, field.name) for field in dataclasses.fields(value)}
    raise TypeError(f"Cannot encode {type(value).__name__}")


T = typing.TypeVar("T")


@lru_cache(maxsize=512)
def _type_hints(cls):
    # Generated fields may share a name with a private/lowercase model type.
    # Class locals then contain the field's default (often None), not the type.
    namespace = vars(sys.modules[cls.__module__])
    return typing.get_type_hints(cls, globalns=namespace, localns=namespace)


@lru_cache(maxsize=512)
def _decoder(cls):
    """Compile a bounded, reusable conversion graph without per-item dispatch."""
    identity = lambda value: value
    memo = {}

    def compile_type(cls):
        if cls in (str, int, float, bool, typing.Any):
            return identity
        if cls in memo:
            return memo[cls]
        # A forward closure supports recursive dataclasses. The graph is local
        # to this cache entry and is collected when the bounded cache evicts it.
        convert = identity
        def nullable(value):
            return None if value is None else convert(value)
        memo[cls] = nullable
        origin, args = typing.get_origin(cls), typing.get_args(cls)
        if cls is bytes:
            convert = lambda value: base64.b64decode(value, validate=True)
        elif origin in (types.UnionType, typing.Union):
            convert = compile_type(next(t for t in args if t is not type(None)))
        elif dataclasses.is_dataclass(cls):
            hints = _type_hints(cls)
            fields = {f.metadata.get("wire_name", f.name): (f.name, compile_type(hints[f.name])) for f in dataclasses.fields(cls)}
            convert = lambda value: cls(**{fields[k][0]: fields[k][1](v) for k, v in value.items()})
        elif origin is list:
            child = compile_type(args[0])
            convert = list if child is identity else lambda value: [child(v) for v in value]
        elif origin is dict:
            child = compile_type(args[1])
            convert = dict if child is identity else lambda value: {k: child(v) for k, v in value.items()}
        return nullable

    return compile_type(cls)


def decode(cls: type[T], value: typing.Any) -> T:
    """Reconstruct generated dataclasses, recursively including containers."""
    if value is None or cls in (str, int, float, bool):
        return value
    return _decoder(cls)(value)


class _Transport:
    def __init__(self, command, max_pending, expected_schema, init_json, startup_timeout):
        self.owner_pid = os.getpid()
        self.lock = threading.Lock()
        self._close_lock = threading.Lock()
        self.pending = {}
        self.seq = 0
        self.max_pending = max_pending
        self.failure = None
        self._direct_writes = False
        self._queued_writes = 0
        self.outbox = queue.Queue(maxsize=max_pending * 2 + 4)
        self.proc = self.reader = self.writer = None
        deadline = time.monotonic() + startup_timeout
        try:
            # Register even an in-flight startup before fork can copy its pipe
            # descriptors. The handshake/constructor never holds this gate.
            with _fork_lock:
                self.proc = subprocess.Popen(
                    [*command, "serve"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                    stderr=None, bufsize=0, close_fds=True,
                )
                if os.name == "posix":
                    os.set_blocking(self.proc.stdin.fileno(), False)
                    self._direct_writes = True
                self.reader = threading.Thread(target=self._read, daemon=True, name="gobridge-reader")
                self.writer = threading.Thread(target=self._write, daemon=True, name="gobridge-writer")
                _transports.add(self)
                self.reader.start()
                self.writer.start()
            hello = self._startup_call("$hello", {"compact": True}, deadline)
            if not isinstance(hello, dict) or hello.get("protocol") != 1:
                raise DaemonError("protocol", "unsupported daemon protocol version")
            if expected_schema is not None and hello.get("schema_hash") != expected_schema:
                raise DaemonError("schema_mismatch", "bindings and daemon differ; regenerate bindings or install the matching binary")
            if hello.get("constructor") is not None:
                self._startup_call("$init", {} if init_json is None else json.loads(init_json), deadline)
            elif init_json is not None:
                raise DaemonError("schema_mismatch", "initialization options supplied for a daemon without a constructor")
        except BaseException:
            self.close()
            raise

    def _startup_call(self, method, params, deadline):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise RequestTimeout("deadline_exceeded", "daemon startup deadline exceeded")
        _, future = self.submit(method, params, remaining)
        try:
            return future.result(timeout=remaining)
        except futures.TimeoutError:
            raise RequestTimeout("deadline_exceeded", "daemon startup deadline exceeded") from None

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
                self._enqueue(data)
            except queue.Full:
                del self.pending[request_id]
                raise BusyError("busy", "client outbound queue is full") from None
            return request_id, f

    def _enqueue(self, data):
        # Caller holds self.lock. Never overtake a queued or partially written
        # frame. Small Unix writes normally complete without a writer wakeup.
        if self._direct_writes and self._queued_writes == 0 and len(data) <= 4096:
            try:
                written = os.write(self.proc.stdin.fileno(), data)
            except OSError:
                # The writer handles EAGAIN and sticky transport failures using
                # the normal asynchronous failure path for pending futures.
                written = 0
            if written == len(data):
                return
            data = data[written:]
        self.outbox.put_nowait(data)
        self._queued_writes += 1

    def cancel(self, request_id):
        with self.lock:
            f = self.pending.pop(request_id, None)
            if self.failure or f is None:
                return
        f.cancel()
        full = False
        with self.lock:
            if self.failure:
                return
            try:
                self._enqueue(json.dumps({"method": "$cancel", "params": {"id": request_id}}).encode() + b"\n")
            except queue.Full:
                full = True
        if full:
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
                # A queued frame keeps _queued_writes > 0, excluding direct
                # caller writes. Let the dedicated writer block normally for
                # large frames instead of adding readiness waits per pipe chunk.
                if self._direct_writes:
                    os.set_blocking(self.proc.stdin.fileno(), True)
                try:
                    while view:
                        if self.failure:
                            return
                        n = self.proc.stdin.write(view)
                        if not n:
                            raise BrokenPipeError("daemon stopped reading")
                        view = view[n:]
                finally:
                    if self._direct_writes:
                        os.set_blocking(self.proc.stdin.fileno(), False)
                # Restore nonblocking mode before allowing a caller fast path.
                with self.lock:
                    self._queued_writes -= 1
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
                    # Decode before removing its future: malformed envelopes
                    # must fail every waiter, including this response's owner.
                    error = _error(msg["error"]) if "error" in msg else None
                    with self.lock:
                        f = self.pending.pop(msg["id"], None)
                    # A late result for a cancelled/timed-out request is ignored.
                    if f is not None and not f.done():
                        if error is not None:
                            f.set_exception(error)
                        else:
                            f.set_result(msg["result"])
                if len(buffer) > MAX_FRAME:
                    raise ValueError("response exceeds frame limit")
        except Exception as e:
            self._fail(DaemonError("transport", str(e)))

    def detach_after_fork(self):
        # Only close this child's copies. Never signal or wait for the parent's
        # daemon. FileIO is unbuffered; Popen must not try to reap it here.
        if self.proc is not None:
            for stream in (self.proc.stdin, self.proc.stdout):
                stream.close()
            self.proc.returncode = 0

    def close(self):
        if os.getpid() != self.owner_pid:
            self.detach_after_fork()
            return
        with self._close_lock:
            self._fail(ClosedError("closed", "client has been closed"))
            if self.proc is None:
                return
            # Kill first if a writer is blocked on a stopped/non-reading daemon.
            if self.proc.poll() is None:
                self.proc.terminate()
            try:
                self.proc.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=2)
            for thread in (self.reader, self.writer):
                if thread is not None and thread.ident is not None and thread is not threading.current_thread():
                    thread.join(timeout=2)
            for stream in (self.proc.stdin, self.proc.stdout):
                stream.close()


_clients = weakref.WeakSet()
_transports = weakref.WeakSet()
_fork_lock = threading.RLock()


def _before_fork():
    _fork_lock.acquire()


def _after_parent():
    _fork_lock.release()


def _after_child():
    global _fork_lock, _transports
    # Startup transports may not have been published on their Client yet.
    for transport in list(_transports):
        transport.detach_after_fork()
    _transports = weakref.WeakSet()
    for client in list(_clients):
        if client._finalizer is not None:
            client._finalizer.detach()
        client._finalizer = None
        client._transport = None
        client._startup_error = None
        client._lifecycle = threading.RLock()
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
                 timeout: float = 30, max_pending: int = 128, expected_schema: str | None = None,
                 init: dict | None = None, startup_timeout: float = 5):
        self.command = _command_args(command)
        _validate_options(timeout, max_pending, startup_timeout)
        self.timeout, self.max_pending = timeout, max_pending
        self.startup_timeout = startup_timeout
        self.expected_schema = expected_schema
        if init is not None and not isinstance(init, dict):
            raise TypeError("init must be a dictionary or None")
        # Snapshot nested data now: mutating caller-owned lists/dicts later must
        # not change a lazy client's constructor or a multiprocessing clone.
        self._init_json = None if init is None else json.dumps(
            init, default=_json_default, allow_nan=False, separators=(",", ":"))
        self._transport = None
        self._finalizer = None
        self._startup_error = None
        self._lifecycle = threading.RLock()
        self._closed = False
        with _fork_lock:
            _clients.add(self)

    def __getstate__(self):
        return (self.command, self.timeout, self.max_pending, self.expected_schema,
                self._init_json, self.startup_timeout, self._closed)

    def __setstate__(self, state):
        command, timeout, max_pending, expected_schema, init_json, startup_timeout, closed = state
        Client.__init__(self, command, timeout=timeout, max_pending=max_pending, expected_schema=expected_schema,
                        init=None if init_json is None else json.loads(init_json), startup_timeout=startup_timeout)
        self._closed = closed

    def _ensure(self):
        with self._lifecycle:
            if self._closed:
                raise ClosedError("closed", "client has been closed")
            if self._startup_error is not None:
                raise self._startup_error
            if self._transport is None:
                try:
                    self._transport = _Transport(self.command, self.max_pending, self.expected_schema,
                                                 self._init_json, self.startup_timeout)
                except BaseException as error:
                    # Constructor side effects can outlive a failed response.
                    # Retry requires an explicit new client/session.
                    self._startup_error = error
                    raise
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
        with self._lifecycle:
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
        try:
            await asyncio.to_thread(self.start)
        except BaseException:
            # A cancelled context entry has no body/exit to own its daemon.
            # Close waits for a bounded startup and reaps any process it made.
            await self.aclose()
            raise
        return self

    async def __aexit__(self, *args):
        await self.aclose()


def require_sync() -> None:
    """Reject blocking facade calls before creating or touching a transport."""
    try:
        asyncio.get_running_loop()
    except RuntimeError:
        return
    raise RuntimeError("synchronous bridge calls cannot run inside an event loop; use the async API with await")


class AsyncClient(Client):
    """Base for generated async clients; supports async context management."""


def _shutdown():
    for client in list(_clients):
        client.close()


atexit.register(_shutdown)
