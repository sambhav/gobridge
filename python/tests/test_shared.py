"""Shared annotated constructors over real subprocesses, sync and async."""
import asyncio
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import pickle
import subprocess

import pytest
from gobridge import InvalidArgumentError, BridgeError
import shared as api

ROOT = Path(__file__).resolve().parents[2]
BINARY = ROOT / 'bin' / ('shared.exe' if os.name == 'nt' else 'shared')


@pytest.fixture(autouse=True)
def default_transport():
    api.shutdown_sync()
    api.configure(command=BINARY)
    yield
    api.shutdown_sync()


def test_lazy_snapshot_concurrent_objects_and_shutdown():
    labels = ['original']
    first = api.SyncAuthClient(account_name='first', labels=labels)
    second = api.SyncAuthClient(account_name='second', labels=None)
    first = pickle.loads(pickle.dumps(first))
    labels.append('changed')
    assert api._bridge_defaults._default is None
    with ThreadPoolExecutor(max_workers=8) as pool:
        objects = [first, second] * 16
        results = list(pool.map(lambda obj: obj.identify(), objects))
    pid = results[0].process_id
    assert {r.process_id for r in results} == {pid}
    assert [r.account for r in results] == ['first', 'second'] * 16
    assert results[0].labels == ['original']
    assert {r.calls for r in results} == {1}  # fresh receiver for each request
    assert api.process_id_sync() == pid
    assert api.constructions_sync() == 32
    transport = api._bridge_defaults.client()
    results = transport.batch([first.calls.identify(), second.calls.identify()])
    assert [r["result"].account for r in results] == ['first', 'second']
    assert [r.calls for r in first.watch(count=3)] == [1, 2, 3]
    api.shutdown_sync()
    assert transport._closed
    assert first.identify().process_id != pid


async def test_async_sync_and_isolated_scopes_share_deliberately():
    first = api.AuthClient(account_name='first', labels=None)
    second = api.AuthClient(account_name='second', labels=None)
    results = await asyncio.gather(*(obj.identify() for obj in [first, second] * 12))
    pid = results[0].process_id
    assert {r.process_id for r in results} == {pid}
    assert (await asyncio.to_thread(api.SyncAuthClient(account_name='sync', labels=None).identify)).process_id == pid
    assert await api.process_id() == pid
    async with api.session(command=BINARY) as transport:
        scoped_pid = await transport.process_id()
        assert scoped_pid != pid
        assert (await first.identify()).process_id == scoped_pid
        pinned = api.AuthClient(account_name='pinned', labels=None, _client=transport)
        async with api.session(command=BINARY):
            assert (await first.identify()).process_id != scoped_pid
            assert (await pinned.identify()).process_id == scoped_pid
        assert [r.account async for r in second.watch(count=2)] == ['second', 'second']
        await api.shutdown()  # only closes the default
        assert (await pinned.identify()).process_id == scoped_pid
    assert (await first.identify()).process_id not in {pid, scoped_pid}


def test_sync_isolation_and_constructor_errors_do_not_poison_daemon():
    first = api.SyncAuthClient(account_name='first', labels=None)
    default_pid = first.identify().process_id
    with api.session_sync(command=BINARY) as transport:
        assert first.identify().process_id == transport.process_id()
        assert transport.process_id() != default_pid
    with api.SyncAuthClientSession(command=BINARY) as transport:
        pinned = api.SyncAuthClient(account_name='pinned', labels=None, _client=transport)
        assert pinned.identify().process_id == transport.process_id()
        assert first.identify().process_id == default_pid
    for account, error in [('bad', InvalidArgumentError), ('panic', BridgeError), ('nil', BridgeError)]:
        with pytest.raises(error):
            api.SyncAuthClient(account_name=account, labels=None).identify()
        assert first.identify().process_id == default_pid
    with pytest.raises(InvalidArgumentError):
        api.SyncAuthClient(account_name=42, labels=None).identify()


def test_cli_configuration_and_generated_adapter():
    result = subprocess.run([str(BINARY), '--config', '{"account":"cli","labels":null}', 'identify'], text=True, capture_output=True, check=True)
    assert '"account":"cli"' in result.stdout
    subprocess.run(['go', 'run', './cmd/gobridge', 'generate', '--dir', './internal/fixtures/shared', '--check'], cwd=ROOT, check=True)
