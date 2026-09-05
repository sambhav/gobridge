"""Build and attach immutable gobridge CLI releases. Never publish examples."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import time
import zipfile

from check_versions import check

ROOT = Path(__file__).resolve().parents[1]
TARGETS = [(os_name, arch) for os_name in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require_match(name, expected, actual):
    if expected != actual:
        raise ValueError(f"Refusing to replace {name}: published bytes differ from this build")


def prepare(tag, output):
    version = check(tag=tag)
    output.mkdir(parents=True, exist_ok=True)
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    epoch = int(subprocess.check_output(["git", "log", "-1", "--format=%ct"], cwd=ROOT, text=True))
    manifest = {"version": version, "commit": commit, "files": {}}
    with tempfile.TemporaryDirectory(prefix="gobridge-release-") as temporary:
        for goos, goarch in TARGETS:
            binary_name = "gobridge.exe" if goos == "windows" else "gobridge"
            binary = Path(temporary) / binary_name
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-X main.version={version}",
                            "-o", str(binary), "./cmd/gobridge"], cwd=ROOT,
                           env=dict(os.environ, CGO_ENABLED="0", GOOS=goos, GOARCH=goarch), check=True)
            contents = [(binary_name, binary.read_bytes(), 0o755),
                        ("LICENSE", (ROOT / "LICENSE").read_bytes(), 0o644),
                        ("README.md", (ROOT / "README.md").read_bytes(), 0o644)]
            filename = f"gobridge_{version}_{goos}_{goarch}" + (".zip" if goos == "windows" else ".tar.gz")
            path = output / filename
            if goos == "windows":
                with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
                    for name, data, mode in contents:
                        entry = zipfile.ZipInfo(name, time.gmtime(max(315532800, epoch))[:6])
                        entry.create_system = 3
                        entry.external_attr = (0o100000 | mode) << 16
                        entry.compress_type = zipfile.ZIP_DEFLATED
                        archive.writestr(entry, data)
            else:
                with path.open("wb") as file, gzip.GzipFile(filename="", fileobj=file, mode="wb", mtime=0) as compressed:
                    with tarfile.open(fileobj=compressed, mode="w") as archive:
                        for name, data, mode in contents:
                            entry = tarfile.TarInfo(name)
                            entry.size, entry.mode, entry.mtime = len(data), mode, epoch
                            archive.addfile(entry, io.BytesIO(data))
            manifest["files"][filename] = digest(path)
            print("Built", filename, flush=True)
    (output / "release.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    hashes = dict(manifest["files"], **{"release.json": digest(output / "release.json")})
    (output / "SHA256SUMS").write_text("".join(f"{value}  {name}\n" for name, value in sorted(hashes.items())))
    return manifest


def verified_files(output, tag):
    manifest = json.loads((output / "release.json").read_text())
    if tag != "v" + manifest["version"]:
        raise ValueError("Artifact version does not match release tag")
    version = manifest["version"]
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("Invalid release version")
    expected = {f"gobridge_{version}_{goos}_{arch}" + (".zip" if goos == "windows" else ".tar.gz") for goos, arch in TARGETS}
    if set(manifest["files"]) != expected:
        raise ValueError("Release must contain only the six gobridge CLI archives")
    for name, value in manifest["files"].items():
        require_match(name, value, digest(output / name))
    hashes = dict(manifest["files"], **{"release.json": digest(output / "release.json")})
    checksums = "".join(f"{value}  {name}\n" for name, value in sorted(hashes.items()))
    if (output / "SHA256SUMS").read_text() != checksums:
        raise ValueError("Checksum manifest does not match release files")
    return [output / name for name in sorted([*expected, "release.json", "SHA256SUMS"])]


def github(tag, output):
    files = verified_files(output, tag)
    repo = os.environ["GITHUB_REPOSITORY"]
    release = json.loads(subprocess.check_output(["gh", "api", f"repos/{repo}/releases/tags/{tag}"]))
    assets = {asset["name"]: asset for asset in release["assets"]}
    # Verify every collision before writing anything. Old releases are immutable.
    with tempfile.TemporaryDirectory(prefix="gobridge-existing-") as temporary:
        for path in files:
            if path.name not in assets: continue
            existing = assets[path.name].get("digest")
            if existing and existing.startswith("sha256:"):
                actual = existing.removeprefix("sha256:")
            else:
                subprocess.run(["gh", "release", "download", tag, "--repo", repo, "--pattern", path.name,
                                "--dir", temporary], check=True)
                actual = digest(Path(temporary) / path.name)
            require_match(path.name, digest(path), actual)
    for path in files:
        if path.name not in assets:
            subprocess.run(["gh", "release", "upload", tag, str(path), "--repo", repo], check=True)
    print(f"Verified and attached {len(files)} release assets to {tag}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "github"))
    parser.add_argument("--tag", required=True)
    parser.add_argument("--output", type=Path, default=ROOT / "release-artifacts")
    args = parser.parse_args()
    if args.action == "prepare": prepare(args.tag, args.output)
    else: github(args.tag, args.output)
