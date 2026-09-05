"""Build self-contained binary wheels without requiring target hosts.

Pass --go-package, --package, --class, --binary and --distribution to wrap your
own Go library. Defaults build the greeter example. No package is uploaded.
"""
import argparse
import keyword
import os
from pathlib import Path
import re
import shutil
import csv
import io
import base64
import hashlib
import time
import zipfile
import subprocess
import tempfile

from packaging_common import python_version, validate_static_linux

ROOT = Path(__file__).resolve().parents[1]
PROJECT = ROOT
TARGETS = {
    "linux-amd64": ("linux", "amd64", "manylinux_2_17_x86_64.musllinux_1_2_x86_64"),
    "linux-arm64": ("linux", "arm64", "manylinux_2_17_aarch64.musllinux_1_2_aarch64"),
    "darwin-amd64": ("darwin", "amd64", "macosx_12_0_x86_64"),
    "darwin-arm64": ("darwin", "arm64", "macosx_12_0_arm64"),
    "windows-amd64": ("windows", "amd64", "win_amd64"),
    "windows-arm64": ("windows", "arm64", "win_arm64"),
}


def run(*args, **kwargs):
    subprocess.run(args, check=True, cwd=PROJECT, **kwargs)


def write_wheel(stage, package, distribution, version, tag, output, repository, license_id, project):
    """Write a standard wheel using only Python's standard library."""
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", version):
        raise ValueError("version must be a stable X.Y.Z version")
    for value in (repository, license_id):
        if "\n" in value or "\r" in value:
            raise ValueError("package metadata must not contain newlines")
    normalized = re.sub(r"[-_.]+", "_", distribution)
    dist_info = stage / f"{normalized}-{version}.dist-info"
    dist_info.mkdir()
    readme = project / "README.md"
    description = readme.read_text(encoding="utf-8") if readme.is_file() else f"Typed Python bindings for {package}, with a bundled Go executable."
    headers = ["Metadata-Version: 2.1", f"Name: {distribution}", f"Version: {version}",
               f"Summary: Typed Python bindings for {package}", "Requires-Python: >=3.10",
               "Description-Content-Type: text/markdown"]
    if repository: headers.append(f"Home-page: {repository}")
    if license_id: headers.append(f"License: {license_id}")
    (dist_info / "METADATA").write_text("\n".join(headers) + "\n\n" + description + "\n", encoding="utf-8")
    if (project / "LICENSE").is_file():
        shutil.copyfile(project / "LICENSE", dist_info / "LICENSE")
    (dist_info / "WHEEL").write_text("Wheel-Version: 1.0\nGenerator: gobridge\nRoot-Is-Purelib: false\n" +
                                    "".join(f"Tag: py3-none-{platform}\n" for platform in tag.split(".")))
    destination = output / f"{normalized}-{version}-py3-none-{tag}.whl"
    timestamp = time.gmtime(max(315532800, int(os.environ.get("SOURCE_DATE_EPOCH", "315532800"))))[:6]
    records = []
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as wheel:
        def add(name, data, mode=0o644):
            info = zipfile.ZipInfo(name, timestamp)
            info.create_system = 3
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            wheel.writestr(info, data)
        for path in sorted(stage.rglob("*")):
            if not path.is_file(): continue
            name, data = path.relative_to(stage).as_posix(), path.read_bytes()
            add(name, data, path.stat().st_mode & 0o777)
            digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
            records.append((name, "sha256=" + digest, str(len(data))))
        record = dist_info.name + "/RECORD"
        buffer = io.StringIO(newline="")
        writer = csv.writer(buffer, lineterminator="\n")
        writer.writerows([*records, (record, "", "")])
        add(record, buffer.getvalue().encode())
    return destination


def main():
    global PROJECT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    parser.add_argument("--go-package", default="./examples/greeter/cmd/greeter", help="Go command package to build")
    parser.add_argument("--package", default="greeter", help="Top-level Python import package")
    parser.add_argument("--class", dest="client_class", default="Greeter", help="Generated Python client class")
    parser.add_argument("--binary", default="greeter", help="Executable filename without .exe")
    parser.add_argument("--distribution", default="gobridge-greeter-example", help="Python wheel distribution name")
    parser.add_argument("--project", type=Path, default=ROOT, help="Go project directory")
    parser.add_argument("--output", type=Path, default=ROOT / "dist", help="Wheel output directory")
    parser.add_argument("--version", help="Application package version (default: runtime version)")
    parser.add_argument("--repository", default="", help="Application source repository URL")
    parser.add_argument("--license", default="", help="Application license identifier")
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
    if re.sub(r"[-_.]+", "-", args.distribution).lower() == "gobridge-runtime" or args.package == "gobridge":
        parser.error("application package must not shadow the gobridge runtime")
    runtime_version = python_version(ROOT)
    version = args.version or runtime_version
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
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
            private = package / "_gobridge"
            shutil.copytree(ROOT / "python/src/gobridge", private, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
            shutil.copyfile(ROOT / "LICENSE", private / "LICENSE")
            source = bindings.decode().replace("\nfrom gobridge", "\nfrom ._gobridge")
            (package / "__init__.py").write_text(source, encoding="utf-8")
            (package / "py.typed").write_text("")
            binary = binary_dir / (args.binary + (".exe" if goos == "windows" else ""))
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
            run("go", "build", "-trimpath", "-o", str(binary), args.go_package, env=env)
            binary.chmod(0o755)
            if goos == "linux":
                validate_static_linux(binary, goarch)
            write_wheel(stage, args.package, args.distribution, version, tag, output,
                        args.repository, args.license, PROJECT)
            print("Built", target, flush=True)


if __name__ == "__main__":
    main()
