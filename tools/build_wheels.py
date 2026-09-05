"""Build the runtime and example binary wheels without requiring target hosts.

This is a reference packaging recipe. Adapt PACKAGE, CLASS and GO_PACKAGE to
wrap your own Go library. No package is uploaded by this script.
"""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = {
    "linux-amd64": ("linux", "amd64", "linux_x86_64"),
    "linux-arm64": ("linux", "arm64", "linux_aarch64"),
    "darwin-amd64": ("darwin", "amd64", "macosx_12_0_x86_64"),
    "darwin-arm64": ("darwin", "arm64", "macosx_12_0_arm64"),
    "windows-amd64": ("windows", "amd64", "win_amd64"),
    "windows-arm64": ("windows", "arm64", "win_arm64"),
}


def run(*args, **kwargs):
    subprocess.run(args, check=True, cwd=ROOT, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    args = parser.parse_args()
    output = ROOT / "dist"
    output.mkdir(exist_ok=True)
    run(sys.executable, "-m", "pip", "wheel", "--disable-pip-version-check", "--no-index", "--no-deps", "--no-build-isolation", "-w", str(output), str(ROOT / "python"))
    with tempfile.TemporaryDirectory(prefix="gobridge-wheels-") as tmp:
        temp = Path(tmp)
        host = temp / ("textkit.exe" if os.name == "nt" else "textkit")
        run("go", "build", "-o", str(host), "./examples/textkit")
        bindings = subprocess.check_output([str(host), "generate-python", "--class", "TextKit", "--binary", "textkit"])
        for target in args.targets:
            goos, goarch, tag = TARGETS[target]
            stage = temp / target
            package = stage / "textkit"
            binary_dir = package / "_bin"
            binary_dir.mkdir(parents=True)
            (package / "__init__.py").write_bytes(bindings)
            (package / "py.typed").write_text("")
            binary = binary_dir / ("textkit.exe" if goos == "windows" else "textkit")
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
            run("go", "build", "-trimpath", "-o", str(binary), "./examples/textkit", env=env)
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
setup(name="gobridge-textkit-example", version="0.1.0", packages=["textkit"],
      package_data={{"textkit": ["py.typed", "_bin/*"]}},
      license="Apache-2.0", license_files=["LICENSE"], python_requires=">=3.10", install_requires=["gobridge-runtime==0.1.0"],
      cmdclass={{"bdist_wheel": PlatformWheel}})
''')
            subprocess.run([sys.executable, "-m", "pip", "wheel", "--disable-pip-version-check", "--no-index", "--no-deps", "--no-build-isolation", "-w", str(output), str(stage)], check=True)
            print("Built", target, flush=True)


if __name__ == "__main__":
    main()
