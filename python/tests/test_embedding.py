"""An existing Cobra executable can expose only a nested daemon command."""
import asyncio
import os
from pathlib import Path
import subprocess

import pytest

from annotated import Greeter
from gobridge import RuntimeOptions

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="module")
def host_binary(tmp_path_factory):
    folder = tmp_path_factory.mktemp("cobra-host")
    binary = folder / ("host.exe" if os.name == "nt" else "host")
    subprocess.run(["go", "build", "-o", str(binary), "."], cwd=ROOT / "examples/cobra", check=True)
    return binary


def test_host_owns_its_commands_and_exposes_only_daemon(host_binary):
    root_help = subprocess.check_output([str(host_binary), "--help"], text=True, encoding="utf-8")
    bridge_help = subprocess.check_output([str(host_binary), "bridge", "--help"], text=True, encoding="utf-8")
    assert "bridge" in root_help
    assert "serve" in bridge_help
    # Embedded applications choose their own CLI; operations aren't installed.
    for command in ("greet", "schema", "generate-python"):
        result = subprocess.run([str(host_binary), "bridge", command], capture_output=True)
        assert result.returncode != 0


def test_python_launches_nested_daemon_with_constructor_options(host_binary):
    options = RuntimeOptions(command=[host_binary, "bridge"])
    with Greeter(prefix="Embedded, ", _runtime=options) as client:
        assert client.greet(name="World") == "Hello, World!"
        assert client.welcome(name="Sam") == "Embedded, Sam"
        assert client.stats().calls == 1
        assert client.command == (str(host_binary), "bridge")


async def test_async_calls_share_embedded_daemon_and_reap_on_close(host_binary):
    client = Greeter(_runtime=RuntimeOptions(command=[host_binary, "bridge"]))
    try:
        results = await asyncio.gather(*(client.acall("welcome", {"name": str(i)}) for i in range(10)))
        assert results == [f"Welcome, {i}" for i in range(10)]
        process = client._transport.proc
        assert (await client.acall("stats"))["calls"] == 10
    finally:
        await client.aclose()
    assert process.poll() is not None
