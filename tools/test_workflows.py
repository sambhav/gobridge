"""Exercise scaffold -> dev -> customized wheel/npm installs in a clean project."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import venv
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="gobridge-workflows-") as tmp:
        root = Path(tmp)
        cli = root / ("gobridge.exe" if os.name == "nt" else "gobridge")
        subprocess.run(["go", "build", "-o", str(cli), "./cmd/gobridge"], cwd=ROOT, check=True)
        project = root / "author"
        def run(*args, cwd=project, success=True, env=None):
            result = subprocess.run([str(a) for a in args], cwd=cwd, capture_output=True, text=True, env=env, timeout=180)
            assert (result.returncode == 0) == success, result.stdout + result.stderr
            return result
        run(cli, "init", "--dir", project, "--module", "example.test/author", "--name", "acme.tools.greeter", "--npm-package", "@acme/greeter", cwd=root)
        with (project / "go.mod").open("a") as file:
            file.write('\nreplace github.com/sambhav/gobridge => ' + json.dumps(ROOT.as_posix()) + '\n')
        target = subprocess.check_output(["go", "env", "GOOS"], text=True).strip() + "-" + subprocess.check_output(["go", "env", "GOARCH"], text=True).strip()
        # Inspection does not generate adapters or dist even in a fresh scaffold.
        plan = json.loads(run(cli, "build", "--check", "--targets", target).stderr)
        assert plan["project"]["name"] == "acme.tools.greeter"
        assert not (project / "bridge/zz_gobridge.gen.go").exists()
        assert not (project / "dist").exists()
        run(cli, "generate", "--dir", "bridge")
        run("go", "mod", "tidy")
        run(cli, "dev", "--once")
        assert "Hello, World!" in run(sys.executable, "app.py", env=dict(os.environ, PYTHONPATH=str(project / "build"))).stdout
        run(cli, "dev", "--typescript", "--once")
        assert "Hello, World!" in run("node", "app.mts").stdout
        package = project / "node_modules/@acme/greeter"
        previous = (package / "package.json").read_bytes()
        source = project / "bridge/greeter.go"
        original = source.read_text()
        source.write_text("invalid go source")
        run(cli, "dev", "--typescript", "--once", success=False)
        assert (package / "package.json").read_bytes() == previous
        assert "Hello, World!" in run("node", "app.mts").stdout
        source.write_text(original)
        config = json.loads((project / "gobridge.json").read_text())
        config.update(python_package="python-package", typescript_package="typescript-package", python_requires=["typing-extensions>=4"], npm_dependencies={"escape-string-regexp": "5.0.0"})
        (project / "gobridge.json").write_text(json.dumps(config))
        py = project / "python-package"
        ts = project / "typescript-package"
        py.mkdir(); ts.mkdir()
        (py / "__init__.py").write_text('from ._bindings import *\nfrom importlib.resources import files\ndef friendly(name):\n    return greet_sync(name=name) + files(__package__).joinpath("suffix.txt").read_text()\n')
        (py / "suffix.txt").write_text(" wrapped")
        (ts / "index.ts").write_text('import escapeStringRegexp from "escape-string-regexp";\nexport * from "./generated.js";\nimport {greet} from "./generated.js";\nimport {readFileSync} from "node:fs";\nexport async function friendly(name: string) { return await greet({name: escapeStringRegexp(name)}) + readFileSync(new URL("./suffix.txt", import.meta.url), "utf8"); }\n')
        (ts / "suffix.txt").write_text(" wrapped")
        run(cli, "dev", "--once")
        assert "Hello, Sam! wrapped" in run(sys.executable, "-c", 'from acme.tools.greeter import friendly; print(friendly("Sam"))', env=dict(os.environ, PYTHONPATH=str(project / "build"))).stdout
        run(shutil.which("npm"), "install", "--ignore-scripts", "--no-audit", "--no-fund", "escape-string-regexp@5.0.0")
        run(cli, "dev", "--typescript", "--once")
        assert "Hello, Sam! wrapped" in run("node", "--input-type=module", "-e", 'import {friendly} from "@acme/greeter"; console.log(await friendly("Sam"));').stdout
        run(cli, "build", "--python", "--typescript", "--targets", target)
        dist = project / "dist"
        manifest = json.loads((dist / "gobridge-build.json").read_text())
        assert len(manifest["artifacts"]) == 2
        for item in manifest["artifacts"]:
            assert hashlib.sha256((dist / item["path"]).read_bytes()).hexdigest() == item["sha256"]
        wheel = next(dist.glob("*.whl"))
        with zipfile.ZipFile(wheel) as archive:
            assert "acme/tools/greeter/suffix.txt" in archive.namelist()
            assert "Requires-Dist: typing-extensions>=4" in archive.read("acme_tools_greeter-0.1.0.dist-info/METADATA").decode()
        env = root / "consumer"
        venv.EnvBuilder(with_pip=True).create(env)
        python = env / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        run(python, "-m", "pip", "install", "--no-index", "--no-deps", wheel, cwd=root)
        clean = {k:v for k,v in os.environ.items() if k != "PYTHONPATH"}
        assert "Hello, Sam! wrapped" in run(python, "-c", 'from acme.tools.greeter import friendly; print(friendly("Sam"))', cwd=root, env=clean).stdout
        node = root / "node"
        node.mkdir()
        (node / "package.json").write_text('{"private":true,"type":"module"}')
        run(shutil.which("npm"), "install", "--offline", "--ignore-scripts", "--no-audit", "--no-fund", next((dist / "npm").glob("*.tgz")), cwd=node)
        assert "Hello, Sam! wrapped" in run("node", "--input-type=module", "-e", 'import {friendly} from "@acme/greeter"; console.log(await friendly("Sam"));', cwd=node).stdout
        # Same-version content changes cannot overwrite an existing release implicitly.
        before = wheel.read_bytes()
        (py / "suffix.txt").write_text(" changed")
        result = run(cli, "build", "--python", "--targets", target, success=False)
        assert "--replace" in result.stderr
        assert wheel.read_bytes() == before
        run(cli, "build", "--python", "--targets", target, "--replace")
        assert wheel.read_bytes() != before
        print("Scaffolding, dev revisions, customized packages, and overwrite protection passed")


if __name__ == "__main__":
    main()
