"""Build test daemons and generate bindings from their current schemas."""
import importlib
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = {
    "greeter": ("./examples/greeter/cmd/greeter", "Greeter"),
    "hello": ("./internal/fixtures/hello", "Hello"),
    "textkit": ("./internal/fixtures/textkit", "TextKit"),
    "wiretypes": ("./internal/fixtures/wiretypes", "WireTypes"),
    "metadata": ("./internal/fixtures/metadata", "Store"),
}


def generate_python(names=("greeter", "hello", "textkit")):
    output = ROOT / ".generated/python"
    output.mkdir(parents=True, exist_ok=True)
    (ROOT / "bin").mkdir(exist_ok=True)
    for name in names:
        package, client_class = FIXTURES[name]
        binary = ROOT / "bin" / (name + (".exe" if os.name == "nt" else ""))
        subprocess.run(["go", "build", "-o", str(binary), package], cwd=ROOT, check=True)
        source = subprocess.check_output([
            str(binary), "generate-python", "--class", client_class, "--binary", name,
        ])
        (output / (name + ".py")).write_bytes(source)
    # Python may have cached this sys.path entry as missing before generation.
    # Refresh both missing-directory and existing-directory finders.
    importlib.invalidate_caches()
