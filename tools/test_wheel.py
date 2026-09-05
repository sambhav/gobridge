"""Install local platform wheels in a clean venv and run bundled binaries."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import venv

ROOT = Path(__file__).resolve().parents[1]

EXAMPLES = {
    "textkit": ("TextKit", "analyze", {"text": "bundled binary works"}, "words", 3),
    "hello": ("Hello", "greet", {"name": "packaged"}, "message", "Hello, packaged!"),
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--example", choices=EXAMPLES, default="textkit")
    args = parser.parse_args()
    client_class, operation, parameters, field, expected = EXAMPLES[args.example]
    with tempfile.TemporaryDirectory(prefix="gobridge-install-") as temp:
        env = Path(temp) / "venv"
        venv.EnvBuilder(with_pip=True).create(env)
        python = env / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        subprocess.run([str(python), "-m", "pip", "install", "--disable-pip-version-check", "--no-index", "--find-links", str(ROOT / "dist"), f"gobridge-{args.example}-example"], check=True)
        subprocess.run([str(python), "-c", f'''
import asyncio, dataclasses, importlib, pathlib
package = importlib.import_module({args.example!r})
SyncClient = getattr(package, {client_class!r})
AsyncClient = getattr(package, {("Async" + client_class)!r})
with SyncClient() as client:
    assert pathlib.Path(client.command[0]).resolve().is_relative_to(pathlib.Path(package.__file__).resolve().parent)
    result = getattr(client, {operation!r})(**{parameters!r})
    assert getattr(result, {field!r}) == {expected!r} and dataclasses.is_dataclass(result)
async def main():
    async with AsyncClient() as client:
        result = await getattr(client, {operation!r})(**{parameters!r})
        assert getattr(result, {field!r}) == {expected!r} and dataclasses.is_dataclass(result)
asyncio.run(main())
print("Clean {args.example} wheel install: bundled binary, sync and async all passed")
'''], cwd=temp, check=True, env={k:v for k,v in os.environ.items() if k!="PYTHONPATH"})


if __name__ == "__main__":
    main()
