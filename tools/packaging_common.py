"""Shared, dependency-free packaging metadata and static Linux validation."""
from pathlib import Path
import re
import struct
import shutil
import subprocess


def application_version(version):
    """Validate canonical application SemVer; return its Python distribution form."""
    parts = re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)\.(0|[1-9][0-9]*))?", version)
    if parts is None:
        raise ValueError("version must be X.Y.Z or X.Y.Z-{alpha,beta,rc}.N (for example 1.2.3-rc.1); no leading zeros or build metadata")
    for index in (1, 2, 3, 5):
        value = parts[index]
        if value is not None and (len(value) > 16 or int(value) > 9007199254740991):
            raise ValueError("version components must be at most 9007199254740991 (npm's safe integer limit)")
    result = ".".join(parts.group(1, 2, 3))
    if parts[4]:
        result += {"alpha": "a", "beta": "b", "rc": "rc"}[parts[4]] + parts[5]
    return result


def build_go_binary(destination, package, project, env, *, cache=None, trimpath=False):
    """Reuse link outputs, while Go still validates source/flags on every build."""
    destination = Path(destination)
    output = destination
    if cache is not None:
        target = (env.get("GOOS") or "host") + "-" + (env.get("GOARCH") or "host")
        output = Path(cache).resolve() / (target + ("-trimpath" if trimpath else "")) / destination.name
        output.parent.mkdir(parents=True, exist_ok=True)
    command = ["go", "build"]
    if trimpath:
        command.append("-trimpath")
    subprocess.run([*command, "-o", str(output), package], cwd=project, env=env, check=True)
    if output != destination:
        shutil.copyfile(output, destination)


def python_version(root):
    # The selected Go module contains this exact runtime manifest. Keep this
    # usable on Python 3.10 without requiring a TOML package just to build.
    text = (Path(root) / "python/pyproject.toml").read_text()
    project = re.search(r"(?ms)^\[project\]\s*\n(.*?)(?=^\[|\Z)", text)
    version = re.search(r'^version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"\s*$', project[1], re.M) if project else None
    if not version:
        raise ValueError("Python runtime manifest must declare a stable X.Y.Z version")
    return version[1]


def validate_static_linux(path, arch):
    """Only static ELF files can use our glibc/musl-independent wheel recipe.

    Go's default CGO_ENABLED=0 executable has no interpreter or dynamic segment.
    Reject build-mode/environment overrides that introduce a libc dependency.
    Both libc families are also exercised by native package-install CI.
    """
    data = Path(path).read_bytes()
    if len(data) < 64 or data[:6] != b"\x7fELF\x02\x01":
        raise ValueError("Linux wheel requires a little-endian 64-bit ELF executable")
    kind, machine = struct.unpack_from("<HH", data, 16)
    if kind != 2 or machine != {"amd64": 62, "arm64": 183}[arch]:
        raise ValueError("Linux executable type or architecture does not match wheel target")
    offset = struct.unpack_from("<Q", data, 32)[0]
    size, count = struct.unpack_from("<HH", data, 54)
    if size < 56 or count == 0 or offset < 64 or offset + size * count > len(data):
        raise ValueError("Invalid ELF program header table")
    for index in range(count):
        segment = struct.unpack_from("<I", data, offset + index * size)[0]
        if segment in (2, 3):  # PT_DYNAMIC / PT_INTERP
            raise ValueError("Linux wheel requires static linkage; remove dynamic linking/build-mode overrides")
