"""Build and install alpha/beta/rc wheels and npm tarballs without publishing."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import venv
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="gobridge-prereleases-") as tmp:
        root = Path(tmp)
        def run(*args, cwd=root, success=True):
            result = subprocess.run([str(a) for a in args], cwd=cwd, capture_output=True, text=True, timeout=240)
            assert (result.returncode == 0) == success, result.stdout + result.stderr
            return result.stdout
        cli = root / ("gobridge.exe" if os.name == "nt" else "gobridge")
        run("go", "build", "-o", cli, "./cmd/gobridge", cwd=ROOT)
        project = root / "author"
        run(cli, "init", "--dir", project, "--module", "example.test/prerelease", "--name", "acme.greeter", "--npm-package", "@acme/greeter")
        with (project / "go.mod").open("a") as file:
            file.write('\nreplace github.com/sambhav/gobridge => ' + json.dumps(ROOT.as_posix()) + '\n')
        run(cli, "generate", "--dir", "bridge", cwd=project)
        run("go", "mod", "tidy", cwd=project)
        target = run("go", "env", "GOOS").strip() + "-" + run("go", "env", "GOARCH").strip()
        config_path = project / "gobridge.json"
        config = json.loads(config_path.read_text())
        config["version"] = "1.2.3-alpha.0"
        config_path.write_text(json.dumps(config))
        for canonical, python_version in [("1.2.3-alpha.0", "1.2.3a0"), ("1.2.3-beta.1", "1.2.3b1"), ("1.2.3-rc.1", "1.2.3rc1")]:
            # Alpha exercises the manifest; beta/rc exercise the CLI override.
            args = ["--python", "--typescript", "--targets", target, "--output", str(root / canonical)]
            if "alpha" not in canonical:
                args += ["--version", canonical]
            plan = json.loads(run(cli, "build", *args, "--check", cwd=project))
            run(cli, "build", *args, cwd=project)
            dist = root / canonical
            assert plan["project"]["version"] == canonical
            assert all((dist / name).is_file() for name in plan["artifacts"])
            wheel = next(dist.glob("*.whl"))
            with zipfile.ZipFile(wheel) as archive:
                metadata = archive.read(f"acme_greeter-{python_version}.dist-info/METADATA").decode()
                assert f"Version: {python_version}\n" in metadata
            tgz = next((dist / "npm").glob("*.tgz"))
            with tarfile.open(tgz) as archive:
                assert json.load(archive.extractfile("package/package.json"))["version"] == canonical
            consumer = dist / "consumer"
            venv.EnvBuilder(with_pip=True).create(consumer)
            python = consumer / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
            run(python, "-m", "pip", "install", "--no-index", "--no-deps", wheel)
            assert run(python, "-c", 'from importlib.metadata import version; print(version("acme-greeter"))').strip() == python_version
            assert "Hello, Sam!" in run(python, "-c", 'from acme.greeter import greet_sync; print(greet_sync(name="Sam"))')
            node = dist / "node"
            node.mkdir()
            (node / "package.json").write_text('{"private":true,"type":"module"}')
            run(shutil.which("npm"), "install", "--offline", "--ignore-scripts", "--no-audit", "--no-fund", tgz, cwd=node)
            installed = json.loads((node / "node_modules/@acme/greeter/package.json").read_text())
            assert installed["version"] == canonical
            assert "Hello, Sam!" in run("node", "--input-type=module", "-e", 'import {greet} from "@acme/greeter"; console.log(await greet({name:"Sam"}));', cwd=node)
            print("Built and installed", canonical, "as Python", python_version, "and npm", canonical, flush=True)
        assert json.loads(config_path.read_text())["version"] == "1.2.3-alpha.0"
        # Let pip select among all three actual wheel versions, independent of
        # lexical filename order; prereleases require an explicit opt-in.
        selection = [python, "-m", "pip", "install", "--no-index", "--no-deps", "--ignore-installed", "--pre", "--target", root / "selected"]
        for directory in root.glob("1.2.3-*"):
            selection += ["--find-links", directory]
        run(*selection, "acme-greeter")
        assert (root / "selected/acme_greeter-1.2.3rc1.dist-info").is_dir()


if __name__ == "__main__":
    main()
