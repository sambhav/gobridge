"""A reused link output must still follow source and build-flag changes."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from packaging_common import build_go_binary


class BuildCacheTest(unittest.TestCase):
    def test_go_invalidates_shared_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go.mod").write_text("module example.com/cachetest\n\ngo 1.23\n")
            source = root / "main.go"
            template = 'package main\nimport "fmt"\nvar value = "%s"\nfunc main() { fmt.Print(value) }\n'
            env = {key:value for key,value in os.environ.items() if key not in {"GOOS", "GOARCH", "GOFLAGS"}}
            env["CGO_ENABLED"] = "0"
            binary = root / ("app.exe" if os.name == "nt" else "app")
            def build():
                build_go_binary(binary, ".", root, env, cache=root / "cache", trimpath=True)
                binary.chmod(0o755)
                return subprocess.check_output([str(binary)], text=True)
            source.write_text(template % "first")
            self.assertEqual(build(), "first")
            self.assertEqual(build(), "first")
            source.write_text(template % "second")
            self.assertEqual(build(), "second")
            env["GOFLAGS"] = "-ldflags=-X=main.value=flag"
            self.assertEqual(build(), "flag")


if __name__ == "__main__":
    unittest.main()
