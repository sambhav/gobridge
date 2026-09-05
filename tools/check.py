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
    subprocess.run(["go", "run", "./cmd/gobridge", "generate", "--dir", "./examples/annotated", "--check"], check=True)
    for name, client_class in [("textkit", "TextKit"), ("hello", "Hello"), ("annotated", "Greeter")]:
        binary = ROOT / "bin" / (name + (".exe" if os.name == "nt" else ""))
        binary.parent.mkdir(exist_ok=True)
        subprocess.run(["go", "build", "-o", str(binary), f"./examples/{name}"], check=True)
        generated = subprocess.check_output([
            str(binary), "generate-python", "--class", client_class, "--binary", name,
        ], text=True, encoding="utf-8")
        # Git may check out CRLF files on Windows; compare normalized text.
        if generated != (ROOT / f"examples/{name}/{name}.py").read_text(encoding="utf-8"):
            raise SystemExit(f"Generated bindings are stale: regenerate examples/{name}/{name}.py")
    env = dict(os.environ, PYTHONPATH=str(ROOT / "python/src"))
    subprocess.run([sys.executable, "-m", "pytest", "-v"], check=True, env=env)


if __name__ == "__main__":
    main()
