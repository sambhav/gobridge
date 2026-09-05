"""Fail before building or publishing when coordinated package versions drift."""
import argparse
import json
from pathlib import Path
import re

from packaging_common import python_version

ROOT = Path(__file__).resolve().parents[1]


def check(root=ROOT, tag=None):
    version = (root / "version.txt").read_text().strip()
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", version):
        raise ValueError("version.txt must contain a stable semantic version X.Y.Z")
    if tag is not None and tag != "v" + version:
        raise ValueError(f"release tag {tag!r} disagrees with version.txt ({version})")
    lock = json.loads((root / "typescript/package-lock.json").read_text())
    versions = {
        "Python": python_version(root),
        "npm": json.loads((root / "typescript/package.json").read_text())["version"],
        "npm lock": lock["version"],
        "npm lock root": lock["packages"][""]["version"],
        "example": json.loads((root / "gobridge.json").read_text())["version"],
    }
    for name, actual in versions.items():
        if actual != version:
            raise ValueError(f"{name} version {actual!r} disagrees with version.txt ({version})")
    return version


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag")
    print(check(tag=parser.parse_args().tag))
