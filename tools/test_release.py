"""Offline release/packaging regressions; never writes to a registry or GitHub."""
import base64
import csv
import hashlib
import io
import json
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
import zipfile

from build_wheels import write_wheel
from check_versions import check, ROOT
from packaging_common import application_version, validate_static_linux
from release import digest, require_match, verified_files, TARGETS


class PackagingTests(unittest.TestCase):
    def test_application_versions(self):
        cases = json.loads((ROOT / "testdata/package_versions.json").read_text())
        for version, expected in cases["valid"].items():
            with self.subTest(version=version):
                self.assertEqual(application_version(version), expected)
        for version in cases["invalid"]:
            with self.subTest(version=version), self.assertRaises(ValueError):
                application_version(version)

    def test_invalid_versions_fail_before_builder_output(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / "dist"
            for script in ("build_wheels.py", "build_npm.py"):
                result = subprocess.run([sys.executable, str(ROOT / "tools" / script),
                                         "--version", "1.2.3-rc.01", "--output", str(output)],
                                        capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("version must be", result.stderr)
                self.assertFalse(output.exists())

    def test_wheel_record_tags_binary_mode_and_reproducibility(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            stage = root / "stage"
            package = stage / "greeter"
            package.mkdir(parents=True)
            (package / "__init__.py").write_text("answer = 42\n")
            binary = package / "daemon"
            binary.write_bytes(b"binary payload")
            binary.chmod(0o755)
            tag = "manylinux_2_17_x86_64.musllinux_1_2_x86_64"
            path = write_wheel(stage, "greeter", "acme-greeter", "1.2.3", tag, root, "", "", root)
            first = path.read_bytes()
            with zipfile.ZipFile(path) as archive:
                self.assertTrue(archive.getinfo("greeter/daemon").external_attr >> 16 & 0o111)
                self.assertNotIn("Requires-Dist:", archive.read("acme_greeter-1.2.3.dist-info/METADATA").decode())
                self.assertEqual(archive.read("acme_greeter-1.2.3.dist-info/WHEEL").count(b"Tag:"), 2)
                records = list(csv.reader(io.StringIO(archive.read("acme_greeter-1.2.3.dist-info/RECORD").decode())))
                for name, expected, size in records[:-1]:
                    data = archive.read(name)
                    actual = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
                    self.assertEqual(expected, "sha256=" + actual)
                    self.assertEqual(int(size), len(data))
                self.assertEqual({r[0] for r in records}, set(archive.namelist()))
            shutil.rmtree(stage / "acme_greeter-1.2.3.dist-info")
            self.assertEqual(first, write_wheel(stage, "greeter", "acme-greeter", "1.2.3", tag, root, "", "", root).read_bytes())

    def test_linux_rejects_dynamic_linkage_wrong_arch_and_truncated_headers(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "binary"
            data = bytearray(120)
            data[:6] = b"\x7fELF\x02\x01"
            struct.pack_into("<HH", data, 16, 2, 62)
            struct.pack_into("<Q", data, 32, 64)
            struct.pack_into("<HH", data, 54, 56, 1)
            struct.pack_into("<I", data, 64, 1)
            path.write_bytes(data)
            validate_static_linux(path, "amd64")
            with self.assertRaises(ValueError): validate_static_linux(path, "arm64")
            for segment in (2, 3):
                struct.pack_into("<I", data, 64, segment)
                path.write_bytes(data)
                with self.assertRaises(ValueError): validate_static_linux(path, "amd64")
            path.write_bytes(data[:80])
            with self.assertRaises(ValueError): validate_static_linux(path, "amd64")

    def test_versions_and_tag_must_agree(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name in ("version.txt", "python/pyproject.toml", "typescript/package.json", "typescript/package-lock.json", "gobridge.json"):
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, target)
            version = check(root)
            check(root, "v" + version)
            with self.assertRaises(ValueError): check(root, "v999.0.0")
            manifest = root / "gobridge.json"
            content = json.loads(manifest.read_text())
            content["version"] = "999.0.0"
            manifest.write_text(json.dumps(content))
            with self.assertRaises(ValueError): check(root)

    def test_releases_only_allow_tool_archives_and_reject_changed_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            files = {}
            for goos, arch in TARGETS:
                name = f"gobridge_1.2.3_{goos}_{arch}" + (".zip" if goos == "windows" else ".tar.gz")
                path = root / name
                path.write_bytes(name.encode())
                files[name] = digest(path)
            manifest = {"version": "1.2.3", "files": files}
            (root / "release.json").write_text(json.dumps(manifest))
            hashes = dict(files, **{"release.json": digest(root / "release.json")})
            (root / "SHA256SUMS").write_text("".join(f"{value}  {name}\n" for name, value in sorted(hashes.items())))
            self.assertEqual(len(verified_files(root, "v1.2.3")), 8)
            with self.assertRaises(ValueError): verified_files(root, "v1.2.4")
            with self.assertRaises(ValueError): require_match("archive", "old", "new")
            (root / next(iter(files))).write_bytes(b"changed")
            with self.assertRaises(ValueError): verified_files(root, "v1.2.3")
            manifest["files"]["greeter-example.whl"] = "unwanted"
            (root / "release.json").write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, "only the six"): verified_files(root, "v1.2.3")


if __name__ == "__main__":
    unittest.main()
