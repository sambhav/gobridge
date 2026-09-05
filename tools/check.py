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
    subprocess.run(["go", "test", "-race", "./..."], cwd=ROOT / "examples/cobra", check=True)
    subprocess.run(["go", "run", "./cmd/gobridge", "generate", "--dir", "./examples/greeter", "--check"], check=True)
    env = dict(os.environ, PYTHONPATH=str(ROOT / "python/src"))
    subprocess.run([sys.executable, "-m", "pytest", "-v"], check=True, env=env)


if __name__ == "__main__":
    main()
