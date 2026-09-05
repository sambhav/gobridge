"""The installed-style author CLI publishes matched packages and reloads safely."""
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

import pytest

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="module")
def author_cli(tmp_path_factory):
    target = tmp_path_factory.mktemp("author-cli") / ("gobridge.exe" if os.name == "nt" else "gobridge")
    subprocess.run(["go", "build", "-o", str(target), "./cmd/gobridge"], cwd=ROOT, check=True)
    return target


@pytest.fixture
def project(tmp_path):
    (tmp_path / "go.mod").write_text(
        'module example.test/project\n\ngo 1.23\n\nrequire github.com/sambhav/gobridge v0.0.0\n'
        + 'replace github.com/sambhav/gobridge => ' + json.dumps(ROOT.as_posix()) + '\n'
    )
    (tmp_path / "greeter.go").write_text(
        'package greeter\n//gobridge:export\nfunc Greet(name string) string { return "v1:" + name }\n'
    )
    command = tmp_path / "cmd" / "service"
    command.mkdir(parents=True)
    (command / "main.go").write_text(
        'package main\nimport greeter "example.test/project"\n'
        'func main() { r,err := greeter.NewGobridge(); if err!=nil { panic(err) }; r.Main() }\n'
    )
    (tmp_path / "gobridge.json").write_text(json.dumps({"name": "greeter", "source": ".", "command": "./cmd/service"}))
    return tmp_path


def dev(cli, project, *args, success=True):
    result = subprocess.run([str(cli), "dev", *args], cwd=project, capture_output=True, text=True, timeout=60)
    if success:
        assert result.returncode == 0, result.stderr
    else:
        assert result.returncode != 0
    return result


def load_package(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def test_dev_rebuild_keeps_old_imports_matched_and_failure_preserves_package(author_cli, project):
    dev(author_cli, project, "--once")
    output = project / "build" / "greeter"
    entry = output / "__init__.py"
    old_source = entry.read_bytes()
    before = load_package(entry, "dev_before")
    after = None
    try:
        assert before.greet_sync(name="Sam") == "v1:Sam"
        old_command = before._bridge_defaults.client().command
        source = project / "greeter.go"
        source.write_text(source.read_text().replace('"v1:"', '"v2:"') +
                          '\n//gobridge:export\nfunc Farewell(name string) string { return "Bye:" + name }\n')
        dev(author_cli, project, "--once")
        assert entry.read_bytes() != old_source
        after = load_package(entry, "dev_after")
        assert after.greet_sync(name="Sam") == "v2:Sam"
        assert after.farewell_sync(name="Sam") == "Bye:Sam"
        assert before.greet_sync(name="Sam") == "v1:Sam"
        before.shutdown_sync()
        # An old import starting another daemon still resolves its original binary.
        assert before.greet_sync(name="Again") == "v1:Again"
        assert before._bridge_defaults.client().command == old_command
        published = entry.read_bytes()
        source.write_text("package greeter\ninvalid Go source\n")
        dev(author_cli, project, "--once", success=False)
        assert entry.read_bytes() == published
        assert after.greet_sync(name="Still") == "v2:Still"
        assert (output / "py.typed").exists()
        assert len(list((output / "_bin").iterdir())) == 2
    finally:
        before.shutdown_sync()
        if after is not None:
            after.shutdown_sync()
        sys.modules.pop("dev_before", None)
        sys.modules.pop("dev_after", None)


def test_dev_refuses_handwritten_packages_and_unknown_configuration(author_cli, project):
    output = project / "build" / "greeter"
    output.mkdir(parents=True)
    entry = output / "__init__.py"
    entry.write_text("handwritten = True\n")
    result = dev(author_cli, project, "--once", success=False)
    assert "refusing to overwrite" in result.stderr
    assert entry.read_text() == "handwritten = True\n"
    (project / "gobridge.json").write_text('{"name":"greeter","typo":true}')
    assert "unknown field" in dev(author_cli, project, "--once", success=False).stderr


def wait_for(predicate, message, timeout=40):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.05)
    raise AssertionError(message)


@pytest.mark.parametrize("package_name", ["greeter", "acme.tools.greeter"])
def test_dev_watch_reloads_python_and_go_and_keeps_app_on_build_failure(author_cli, project, package_name):
    manifest = json.loads((project / "gobridge.json").read_text())
    manifest["name"] = package_name
    (project / "gobridge.json").write_text(json.dumps(manifest))
    package_dir = project / "build" / Path(*package_name.split("."))
    app = project / "app.py"
    records = project / "requests.txt"
    log_path = project / "dev.log"
    app.write_text('''import time
from greeter import greet_sync
while True:
    with open("requests.txt", "a", encoding="utf-8") as output:
        output.write(greet_sync(name="Sam") + "\\n")
    time.sleep(0.1)
'''.replace('from greeter import', f'from {package_name} import'))
    def contents(path):
        return path.read_text(encoding="utf-8", errors="replace") if path.exists() else ""
    env = dict(os.environ, PYTHONPATH=str(ROOT / "python" / "src"))
    flags = subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0
    with log_path.open("w", encoding="utf-8") as log:
        process = subprocess.Popen([str(author_cli), "dev", "--interval", "100ms", "--", sys.executable, "app.py"],
                                   cwd=project, stdout=log, stderr=log, stdin=subprocess.DEVNULL,
                                   env=env, creationflags=flags)
        try:
            wait_for(lambda: "v1:Sam" in contents(records), "initial application did not start: " + contents(log_path))
            binaries = set((package_dir / "_bin").iterdir())
            app.write_text(app.read_text().replace('name="Sam"', 'name="Bob"'))
            wait_for(lambda: "v1:Bob" in contents(records), "Python edit did not reload")
            assert set((package_dir / "_bin").iterdir()) == binaries
            source = project / "greeter.go"
            source.write_text(source.read_text().replace('"v1:"', '"v2:"'))
            wait_for(lambda: "v2:Bob" in contents(records), "Go edit did not reload")
            source.write_text("package greeter\ninvalid Go source\n")
            wait_for(lambda: "Build failed" in contents(log_path), "failed build was not reported")
            size = len(contents(records))
            wait_for(lambda: len(contents(records)) > size, "build failure stopped the working application")
            assert contents(records).splitlines()[-1] == "v2:Bob"
        finally:
            if process.poll() is None:
                process.send_signal(signal.CTRL_BREAK_EVENT if os.name == "nt" else signal.SIGINT)
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
                    pytest.fail("watcher did not stop its application")
        assert process.returncode == 0, contents(log_path)


def test_dotted_dev_custom_output_and_namespace_ownership(author_cli, project):
    manifest = json.loads((project / "gobridge.json").read_text())
    manifest["name"] = "acme.tools.greeter"
    (project / "gobridge.json").write_text(json.dumps(manifest))
    output = project / "custom" / "acme" / "tools" / "greeter"
    dev(author_cli, project, "--once", "--python", str(output))
    assert not (output.parent / "__init__.py").exists()
    assert not (output.parent.parent / "__init__.py").exists()
    subprocess.run([sys.executable, "-c", 'from acme.tools.greeter import Greeter, greet_sync; assert greet_sync(name="Sam") == "v1:Sam"'],
                   cwd=project, env=dict(os.environ, PYTHONPATH=str(project / "custom")), check=True)
    invalid = dev(author_cli, project, "--once", "--python", str(project / "wrong" / "greeter"), success=False)
    assert "import package path" in invalid.stderr
    parent_init = output.parent / "__init__.py"
    parent_init.write_text("handwritten = True\n")
    blocked = dev(author_cli, project, "--once", "--python", str(output), success=False)
    assert "namespace parent" in blocked.stderr
    assert parent_init.read_text() == "handwritten = True\n"
