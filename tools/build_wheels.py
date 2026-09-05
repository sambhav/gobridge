"""Build the runtime and example binary wheels without requiring target hosts.

Pass --go-package, --package, --class, --binary and --distribution to wrap your
own Go library. Defaults build the textkit example. No package is uploaded.
"""
import argparse
import keyword
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
PROJECT = ROOT
TARGETS = {
    "linux-amd64": ("linux", "amd64", "linux_x86_64"),
    "linux-arm64": ("linux", "arm64", "linux_aarch64"),
    "darwin-amd64": ("darwin", "amd64", "macosx_12_0_x86_64"),
    "darwin-arm64": ("darwin", "arm64", "macosx_12_0_arm64"),
    "windows-amd64": ("windows", "amd64", "win_amd64"),
    "windows-arm64": ("windows", "arm64", "win_arm64"),
}


def run(*args, **kwargs):
    subprocess.run(args, check=True, cwd=PROJECT, **kwargs)


def main():
    global PROJECT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    parser.add_argument("--go-package", default="./examples/textkit", help="Go command package to build")
    parser.add_argument("--package", default="textkit", help="Top-level Python import package")
    parser.add_argument("--class", dest="client_class", default="TextKit", help="Generated Python client class")
    parser.add_argument("--binary", default="textkit", help="Executable filename without .exe")
    parser.add_argument("--distribution", default="gobridge-textkit-example", help="Python wheel distribution name")
    parser.add_argument("--project", type=Path, default=ROOT, help="Go project directory")
    parser.add_argument("--output", type=Path, default=ROOT / "dist", help="Wheel output directory")
    parser.add_argument("--version", default="0.1.0", help="Application package version")
    args = parser.parse_args()
    PROJECT = args.project.resolve()
    if not args.package.isidentifier() or keyword.iskeyword(args.package):
        parser.error("--package must be one Python package identifier")
    if not re.fullmatch(r"[A-Z][A-Za-z0-9]*", args.client_class):
        parser.error("--class must be a capitalized Python class identifier")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", args.binary):
        parser.error("--binary must be a filename stem using letters, digits, _ or -")
    if not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?", args.distribution):
        parser.error("--distribution must be a Python distribution name")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    run(sys.executable, "-m", "pip", "wheel", "--disable-pip-version-check", "--no-index", "--no-deps", "--no-build-isolation", "-w", str(output), str(ROOT / "python"))
    with tempfile.TemporaryDirectory(prefix="gobridge-wheels-") as tmp:
        temp = Path(tmp)
        host = temp / (args.binary + (".exe" if os.name == "nt" else ""))
        host_env = {key: value for key, value in os.environ.items() if key not in {"GOOS", "GOARCH"}}
        run("go", "build", "-o", str(host), args.go_package, env=host_env)
        bindings = subprocess.check_output([str(host), "generate-python", "--class", args.client_class, "--binary", args.binary])
        for target in args.targets:
            goos, goarch, tag = TARGETS[target]
            stage = temp / target
            package = stage / args.package
            binary_dir = package / "_bin"
            binary_dir.mkdir(parents=True)
            (package / "__init__.py").write_bytes(bindings)
            (package / "py.typed").write_text("")
            binary = binary_dir / (args.binary + (".exe" if goos == "windows" else ""))
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
            run("go", "build", "-trimpath", "-o", str(binary), args.go_package, env=env)
            binary.chmod(0o755)
            # Standard setuptools/wheel machinery writes metadata, hashes and
            # executable attributes. Python is ABI-independent; the Go binary
            # makes this a platform-specific (non-pure) wheel.
            (stage / "LICENSE").write_text((ROOT / "LICENSE").read_text())
            (stage / "setup.py").write_text(f'''
from setuptools import setup
from wheel.bdist_wheel import bdist_wheel
class PlatformWheel(bdist_wheel):
    def finalize_options(self):
        super().finalize_options()
        self.root_is_pure = False
    def get_tag(self):
        return "py3", "none", {tag!r}
setup(name={args.distribution!r}, version={args.version!r}, packages=[{args.package!r}],
      package_data={{{args.package!r}: ["py.typed", "_bin/*"]}},
      license="Apache-2.0", license_files=["LICENSE"], python_requires=">=3.10", install_requires=["gobridge-runtime==0.1.0"],
      cmdclass={{"bdist_wheel": PlatformWheel}})
''')
            subprocess.run([sys.executable, "-m", "pip", "wheel", "--disable-pip-version-check", "--no-index", "--no-deps", "--no-build-isolation", "-w", str(output), str(stage)], check=True)
            print("Built", target, flush=True)


if __name__ == "__main__":
    main()
