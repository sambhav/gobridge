"""Explicit lifecycle controls for generated module-level functions."""
from __future__ import annotations

import asyncio
from contextvars import ContextVar, Token
import os
import threading
from typing import Callable, Generic, TypeVar
import weakref

from .runtime import Client

T = TypeVar("T", bound=Client)
_controls: weakref.WeakSet = weakref.WeakSet()


def _after_fork():
    # A vanished parent thread may have held a control's creation lock. Clients
    # independently detach inherited daemon pipes in runtime's fork hook.
    for control in list(_controls):
        control._lock = threading.Lock()


if hasattr(os, "register_at_fork"):
    os.register_at_fork(after_in_child=_after_fork)


class DefaultControl(Generic[T]):
    """Own a module's lazy default client and context-local overrides.

    ``factory`` must construct a lazy client without starting its subprocess.
    Creation is serialized; startup and shutdown happen outside this control's
    lock. Sync and async module functions both resolve ``client()`` and therefore
    share the same default daemon, independently of threads and event loops.
    """

    def __init__(self, factory: Callable[..., T]):
        self._factory = factory
        self._kwargs: dict = {}
        self._default: T | None = None
        self._lock = threading.Lock()
        self._scope: ContextVar[T | None] = ContextVar("gobridge.scope", default=None)
        _controls.add(self)

    def client(self) -> T:
        """Return the scoped client or create the module default lazily."""
        scoped = self._scope.get()
        if scoped is not None:
            return scoped
        with self._lock:
            if self._default is None:
                self._default = self._factory(**self._kwargs)
            return self._default

    def configure(self, **kwargs) -> None:
        """Replace default constructor arguments before an instance exists.

        Call ``close()`` explicitly before changing an already-created default.
        Configured arguments survive a close/reset. Scope arguments are separate
        and are passed directly to the factory without inheriting this config.
        """
        with self._lock:
            if self._default is not None:
                raise RuntimeError("default client already exists; call control.close() before configuring it")
            self._kwargs = dict(kwargs)

    def start(self) -> T:
        """Start and return the currently resolved client."""
        return self.client().start()

    def close(self) -> None:
        """Close/reset only the module default; scope-owned clients stay open.

        The next default call creates a fresh instance. In-flight calls on the
        previous instance can fail, as with an explicit client's ``close()``.
        """
        with self._lock:
            client, self._default = self._default, None
        if client is not None:
            client.close()

    def scope(self, **kwargs) -> _Scope[T]:
        """Create an isolated sync/async context using factory(**kwargs).

        Nested scopes restore the previous client. Async child tasks inherit
        their context; new threads should receive an explicit client when they
        need to share scoped state.
        """
        return _Scope(self, kwargs)


class _Scope(Generic[T]):
    def __init__(self, control: DefaultControl[T], kwargs: dict):
        self._control, self._kwargs = control, kwargs
        self._client: T | None = None
        self._token: Token | None = None
        self._entered = False

    def _create(self) -> T:
        if self._entered:
            raise RuntimeError("a scope context cannot be entered while already active")
        self._entered = True
        try:
            self._client = self._control._factory(**self._kwargs)
        except BaseException:
            self._entered = False
            raise
        return self._client

    def _restore(self) -> T:
        client = self._client
        if client is None:
            raise RuntimeError("scope context has not been entered")
        if self._token is not None:
            self._control._scope.reset(self._token)
        self._token = None
        self._client = None
        self._entered = False
        return client

    def __enter__(self) -> T:
        client = self._create()
        try:
            client.start()
            self._token = self._control._scope.set(client)
            return client
        except BaseException:
            self._restore().close()
            raise

    def __exit__(self, *args) -> None:
        self._restore().close()

    async def __aenter__(self) -> T:
        client = self._create()
        try:
            await asyncio.to_thread(client.start)
            self._token = self._control._scope.set(client)
            return client
        except BaseException:
            await self._restore().aclose()
            raise

    async def __aexit__(self, *args) -> None:
        await self._restore().aclose()
