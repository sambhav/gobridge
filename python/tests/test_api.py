"""Public API and protocol boundary tests, using native pytest fixtures."""
import asyncio
import dataclasses
import importlib.util
import inspect
import os
from pathlib import Path
import subprocess
import sys
import typing

import pytest

from gobridge import BridgeError, Client, DaemonError, InvalidArgumentError
from textkit import Analysis, TextKit as AsyncTextKit, SyncTextKit as TextKit

ROOT = Path(__file__).resolve().parents[2]
BINARY = ROOT / "bin" / ("textkit.exe" if os.name == "nt" else "textkit")


@pytest.fixture
def kit():
    with TextKit(BINARY) as client:
        yield client


@pytest.fixture(scope="session")
def wiretypes(tmp_path_factory):
    folder = tmp_path_factory.mktemp("wiretypes")
    binary = folder / ("wiretypes.exe" if os.name == "nt" else "wiretypes")
    subprocess.run(["go", "build", "-o", str(binary), "./internal/fixtures/wiretypes"], cwd=ROOT, check=True)
    source = subprocess.check_output([str(binary), "generate-python", "--class", "WireTypes", "--binary", "wiretypes"])
    module_file = folder / "wiretypes_generated.py"
    module_file.write_bytes(source)
    spec = importlib.util.spec_from_file_location("wiretypes_generated", module_file)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module, binary


def test_signatures_and_named_results(kit):
    signature = inspect.signature(TextKit.analyze)
    assert signature.parameters["text"].kind is inspect.Parameter.KEYWORD_ONLY
    assert typing.get_type_hints(TextKit.analyze)["return"] is Analysis
    assert inspect.iscoroutinefunction(AsyncTextKit.analyze)
    result = kit.analyze(text="hello world")
    assert dataclasses.asdict(result)["words"] == 2
    with pytest.raises(dataclasses.FrozenInstanceError):
        result.words = 5


@pytest.mark.parametrize("text", [None, 42, True, ["x"]])
def test_bad_input_has_typed_error(kit, text):
    with pytest.raises(InvalidArgumentError) as error:
        kit.analyze(text=text)
    assert error.value.code == "invalid_argument"


def test_schema_mismatch_fails_before_operations():
    with pytest.raises(DaemonError, match="schema_mismatch"):
        with Client(BINARY, expected_schema="stale"):
            pass


def test_nested_models_and_full_width_integers(wiretypes):
    module, binary = wiretypes
    child = module.Child(name="nested")
    with module.SyncWireTypes(binary) as client:
        result = client.echo(child=child, items=[child], labels={"one": child}, big=2**63-1)
        assert isinstance(result.child, module.Child)
        assert result.items[0] == child
        assert result.labels["one"] == child
        assert result.optional is None
        assert result.big == 2**63-1
        assert client.echo(child=child, optional=child, items=None, labels=None, big=0).optional == child
        with pytest.raises(InvalidArgumentError):
            client.echo(child=child, items=[], labels={}, big=2**63)


def test_panic_and_oversized_response_do_not_kill_daemon(wiretypes):
    module, binary = wiretypes
    with module.SyncWireTypes(binary) as client:
        with pytest.raises(BridgeError, match="operation panicked"):
            client.explode()
        with pytest.raises(BridgeError) as error:
            client.large()
        assert error.value.code == "resource_exhausted"
        result = client.echo(child=module.Child(name="ok"), items=[], labels={}, big=7)
        assert result.big == 7


async def test_async_fixture_and_cancellation_cleanup():
    async with AsyncTextKit(BINARY) as client:
        requests = [asyncio.create_task(client.wait(milliseconds=10000)) for _ in range(20)]
        for _ in range(100):
            if (await client.health()).active == 20:
                break
            await asyncio.sleep(0.01)
        for task in requests:
            task.cancel()
        results = await asyncio.gather(*requests, return_exceptions=True)
        assert all(isinstance(result, asyncio.CancelledError) for result in results)
        for _ in range(100):
            if (await client.health()).active == 0:
                break
            await asyncio.sleep(0.01)
        assert (await client.health()).active == 0
        assert (await client.analyze(text="still working")).words == 2
