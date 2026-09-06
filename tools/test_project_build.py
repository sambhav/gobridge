"""Build and install versioned packages from a separate Go project using the CLI."""
import json
import re
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
    with tempfile.TemporaryDirectory(prefix="gobridge-project-") as temporary:
        root = Path(temporary)
        cli = root / ("gobridge.exe" if os.name == "nt" else "gobridge")
        subprocess.run(["go", "build", "-o", str(cli), "./cmd/gobridge"], cwd=ROOT, check=True)
        project = root / "author"
        project.mkdir()
        guide = (ROOT / "README.md").read_text()
        go_blocks = re.findall(r"```go\n(.*?)```", guide, re.S)
        library_source = next(b for b in go_blocks if b.startswith("package greeter\n"))
        command_source = next(b for b in go_blocks if b.startswith("package main\n"))
        options_source = next(b for b in go_blocks if b.startswith("type Options struct"))
        (project / "go.mod").write_text(
            'module example.com/greeter\n\ngo 1.23\n\nrequire github.com/sambhav/gobridge v0.0.0\n'
            + 'replace github.com/sambhav/gobridge => ' + json.dumps(ROOT.as_posix()) + '\n'
        )
        (project / "greeter.go").write_text(library_source + "\n" + options_source)
        command = project / "cmd" / "greeter"
        command.mkdir(parents=True)
        (command / "main.go").write_text(command_source)
        (project / "gobridge.json").write_text(json.dumps({"modules":[{"name": "greeter", "source": ".", "command": "./cmd/greeter", "typescript":{"export":"."}}]}))
        goos = subprocess.check_output(["go", "env", "GOOS"], text=True).strip()
        goarch = subprocess.check_output(["go", "env", "GOARCH"], text=True).strip()
        subprocess.run([str(cli), "build", "--python", "--typescript", "--targets", goos + "-" + goarch,
                        "--version", "0.2.3"], cwd=project, check=True)
        dist = project / "dist"
        application = next(dist.glob("greeter-0.2.3-*.whl"))
        with zipfile.ZipFile(application) as archive:
            metadata = archive.read("greeter-0.2.3.dist-info/METADATA").decode()
            assert "Version: 0.2.3" in metadata
            assert "Requires-Dist:" not in metadata
            assert "greeter/_gobridge/runtime.py" in archive.namelist()
            assert "greeter/_gobridge/LICENSE" in archive.namelist()
        env = root / "consumer"
        venv.EnvBuilder(with_pip=True).create(env)
        python = env / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        # The README program also runs from generated source in a clean venv.
        app_source = next(b for b in re.findall(r"```python\n(.*?)```", guide, re.S) if b.startswith("import asyncio\n"))
        (project / "app.py").write_text(app_source)
        subprocess.run([str(cli), "dev", "--once"], cwd=project, check=True)
        result = subprocess.check_output([str(python), "app.py"], cwd=project, text=True,
                                         env=dict(os.environ, PYTHONPATH=str(project / "build")))
        assert result.strip() == "Hello, World!"
        subprocess.run([str(python), "-m", "pip", "install", "--no-index", "--find-links", str(dist), "greeter==0.2.3"], check=True)
        subprocess.run([str(python), "-c", 'import asyncio; from greeter import greet, greet_sync; assert asyncio.run(greet(name="World")) == "Hello, World!"; assert greet_sync(name="Sam") == "Hello, Sam!"'],
                       cwd=root, env={k:v for k,v in os.environ.items() if k!="PYTHONPATH"}, check=True)
        node = root / "node-consumer"
        node.mkdir()
        (node / "package.json").write_text('{"private":true,"type":"module"}')
        application = dist / "npm" / "greeter-0.2.3.tgz"
        with tarfile.open(application) as archive:
            manifest = json.load(archive.extractfile("package/package.json"))
            assert manifest["version"] == "0.2.3"
            assert not manifest.get("dependencies")
            assert "package/_gobridge/runtime.js" in archive.getnames()
        subprocess.run([shutil.which("npm"), "install", "--offline", "--ignore-scripts", "--no-audit", "--no-fund", str(application)], cwd=node, check=True)
        subprocess.run(["node", "--input-type=module", "-e", 'import {greet} from "greeter"; if(await greet({name:"World"}) !== "Hello, World!") throw Error("bad greeting");'], cwd=node, check=True)
        subprocess.run([str(cli), "build", "--python", "--targets", goos + "-" + goarch,
                        "--output", "app-only"], cwd=project, check=True)
        assert not list((project / "app-only").glob("gobridge_runtime*"))
        assert len(list((project / "app-only").glob("*.whl"))) == 1
        # Separate distributions share native namespace parents, including after uninstall.
        for leaf in ("greeter", "farewell"):
            manifest = {"typescript":{"package":"@acme/"+leaf}, "modules":[{"name": "acme.tools." + leaf, "source": ".", "command": "./cmd/greeter", "typescript":{"export":"."}}]}
            (project / "gobridge.json").write_text(json.dumps(manifest))
            languages = ["--python", "--typescript"] if leaf == "greeter" else ["--python"]
            subprocess.run([str(cli), "build", *languages, "--targets", goos + "-" + goarch,
                            "--output", "namespaced"], cwd=project, check=True)
            wheel = next((project / "namespaced").glob("acme_tools_" + leaf + "-*.whl"))
            with zipfile.ZipFile(wheel) as archive:
                paths = archive.namelist()
                assert "acme/__init__.py" not in paths and "acme/tools/__init__.py" not in paths
                assert "acme/py.typed" not in paths and "acme/tools/py.typed" not in paths
                assert f"acme/tools/{leaf}/py.typed" in paths
                assert f"acme/tools/{leaf}/_gobridge/runtime.py" in paths
                assert f"acme/tools/{leaf}/_bin/acme_tools_{leaf}" + (".exe" if goos == "windows" else "") in paths
            subprocess.run([str(python), "-m", "pip", "install", "--no-index", str(wheel)], check=True)
        clean_env = {k:v for k,v in os.environ.items() if k != "PYTHONPATH"}
        subprocess.run([str(python), "-c", '''
import asyncio, pickle
import acme, acme.tools
from acme.tools import greeter, farewell
assert acme.__file__ is None and acme.tools.__file__ is None
assert greeter.greet_sync(name="Sam") == "Hello, Sam!"
assert asyncio.run(farewell.greet(name="Sam")) == "Hello, Sam!"
with greeter.SyncGreeter(prefix="Hi, ") as client:
    with pickle.loads(pickle.dumps(client)) as restored:
        assert restored.welcome(name="Sam") == "Hi, Sam"
greeter.shutdown_sync()
farewell.shutdown_sync()
'''], cwd=root, env=clean_env, check=True)
        subprocess.run([str(python), "-m", "pip", "uninstall", "-y", "acme-tools-greeter"], check=True)
        subprocess.run([str(python), "-c", 'from acme.tools.farewell import greet_sync; assert greet_sync(name="Sam") == "Hello, Sam!"'],
                       cwd=root, env=clean_env, check=True)
        subprocess.run([shutil.which("npm"), "install", "--offline", "--ignore-scripts", "--no-audit", "--no-fund",
                        str(project / "namespaced/npm/acme-greeter-0.1.0.tgz")], cwd=node, check=True)
        subprocess.run(["node", "--input-type=module", "-e", 'import {greet} from "@acme/greeter"; if(await greet({name:"Sam"}) !== "Hello, Sam!") throw Error("bad greeting");'], cwd=node, check=True)
        print("External project: versioned wheels and npm packages build and install through the CLI")


if __name__ == "__main__":
    main()
