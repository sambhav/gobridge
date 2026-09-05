"""Portable local/CI verification entrypoint: python tools/check.py."""
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def main():
    os.chdir(ROOT)
    subprocess.run(["go", "test", "-race", "./..."], check=True)
    subprocess.run(["go", "vet", "./..."], check=True)
    binary = ROOT / "bin" / ("textkit.exe" if os.name == "nt" else "textkit")
    binary.parent.mkdir(exist_ok=True)
    subprocess.run(["go", "build", "-o", str(binary), "./examples/textkit"], check=True)
    generated = subprocess.check_output([str(binary), "generate-python", "--class", "TextKit", "--binary", "textkit"])
    if generated != (ROOT / "examples/textkit/textkit.py").read_bytes():
        raise SystemExit("Generated bindings are stale: regenerate examples/textkit/textkit.py")
    env = dict(os.environ, PYTHONPATH=str(ROOT / "python/src"))
    subprocess.run([sys.executable, "-m", "pytest", "-v"], check=True, env=env)


if __name__ == "__main__":
    main()
