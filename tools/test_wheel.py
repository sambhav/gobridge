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
        subprocess.run([str(python), "-m", "pip", "install", "--disable-pip-version-check", "--no-index", "--find-links", str(ROOT / "dist"), "gobridge-textkit-example"], check=True)
        subprocess.run([str(python), "-c", '''
import asyncio, dataclasses, pathlib, textkit
from textkit import TextKit, AsyncTextKit
with TextKit() as kit:
    assert pathlib.Path(kit.command[0]).is_relative_to(pathlib.Path(textkit.__file__).parent)
    result = kit.analyze(text="bundled binary works")
    assert result.words == 3 and dataclasses.is_dataclass(result)
async def main():
    async with AsyncTextKit() as kit:
        assert (await kit.analyze(text="async works")).words == 2
asyncio.run(main())
print("Clean wheel install: bundled binary, sync and async all passed")
'''], cwd=temp, check=True, env={k:v for k,v in os.environ.items() if k!="PYTHONPATH"})


if __name__ == "__main__":
    main()
