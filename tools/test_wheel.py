"""Install local platform wheels in a clean venv and run bundled binaries."""
import os
from pathlib import Path
import subprocess
import tempfile
import venv

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="gobridge-install-") as temp:
        env = Path(temp) / "venv"
        venv.EnvBuilder(with_pip=True).create(env)
        python = env / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        subprocess.run([str(python), "-m", "pip", "install", "--disable-pip-version-check", "--no-index", "--find-links", str(ROOT / "dist"), "gobridge-greeter-example"], check=True)
        subprocess.run([str(python), "-c", '''
import asyncio, dataclasses, importlib.util, pathlib
assert importlib.util.find_spec("gobridge") is None, "consumer must not need a core install"
import greeter
with greeter.SyncGreeter() as client:
    assert pathlib.Path(client.command[0]).resolve().is_relative_to(pathlib.Path(greeter.__file__).resolve().parent)
    result = client.stats()
    assert result.calls == 0 and dataclasses.is_dataclass(result)
async def main():
    async with greeter.Greeter() as client:
        result = await client.stats()
        assert result.calls == 0 and dataclasses.is_dataclass(result)
    assert (await greeter.stats()).calls == 0
asyncio.run(main())
assert greeter.stats_sync().calls == 0
greeter.shutdown_sync()
print("Clean greeter wheel install: bundled binary, sync and async all passed")
'''], cwd=temp, check=True, env={k:v for k,v in os.environ.items() if k!="PYTHONPATH"})


if __name__ == "__main__":
    main()
