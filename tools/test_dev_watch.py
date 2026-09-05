"""Live TypeScript reload, failed-build retention, and config restart behavior."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="gobridge-watch-") as tmp:
        root = Path(tmp)
        cli = root / ("gobridge.exe" if os.name == "nt" else "gobridge")
        subprocess.run(["go", "build", "-o", str(cli), "./cmd/gobridge"], cwd=ROOT, check=True)
        project = root / "project"
        subprocess.run([str(cli), "init", "--dir", str(project), "--module", "example.test/watch", "--npm-package", "@acme/greeter"], check=True)
        with (project / "go.mod").open("a") as file:
            file.write('\nreplace github.com/sambhav/gobridge => ' + json.dumps(ROOT.as_posix()) + '\n')
        subprocess.run([str(cli), "generate", "--dir", "bridge"], cwd=project, check=True)
        subprocess.run(["go", "mod", "tidy"], cwd=project, check=True)
        source = project / "bridge/greeter.go"
        source.write_text('package bridge\nimport _ "embed"\n//go:embed greeting.txt\nvar greeting string\n//gobridge:export\nfunc Greet(name string)string{return greeting+name}\n')
        asset = project / "bridge/greeting.txt"
        asset.write_text("v1:")
        app = project / "app.mts"
        app.write_text('import {greet} from "@acme/greeter";\nimport {appendFileSync} from "node:fs";\nsetInterval(async()=>{appendFileSync("requests.txt", await greet({name:"Sam"})+"\\n");},100);\n')
        records = project / "requests.txt"
        logpath = root / "dev.log"
        def contents(path):
            return path.read_text(errors="replace") if path.exists() else ""
        def wait(predicate, message, timeout=100):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                if predicate(): return
                if process.poll() is not None: break
                time.sleep(.1)
            raise AssertionError(message + "\n" + contents(logpath))
        flags = subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0
        with logpath.open("w") as log:
            process = subprocess.Popen([str(cli), "dev", "--typescript", "--interval", "100ms", "--", "node", "app.mts"], cwd=project, stdout=log, stderr=log, stdin=subprocess.DEVNULL, creationflags=flags)
            try:
                wait(lambda: "v1:Sam" in contents(records), "initial app did not run")
                package = project / "node_modules/@acme/greeter/package.json"
                before = package.read_bytes()
                app.write_text(app.read_text().replace('name:"Sam"', 'name:"Bob"'))
                wait(lambda: "v1:Bob" in contents(records), "application edit did not restart")
                assert package.read_bytes() == before, "application-only edit rebuilt package"
                asset.write_text("v2:")
                wait(lambda: "v2:Bob" in contents(records), "embedded asset edit did not rebuild")
                source.write_text("invalid go source")
                wait(lambda: "Build failed" in contents(logpath), "missing build failure")
                size = len(contents(records))
                wait(lambda: len(contents(records)) > size, "failed build stopped app")
                assert contents(records).splitlines()[-1] == "v2:Bob"
                (project / "gobridge.json").write_text("invalid json")
                wait(lambda: "restart gobridge dev" in contents(logpath), "configuration edit not reported")
                size = len(contents(records))
                wait(lambda: len(contents(records)) > size, "invalid config stopped app")
            finally:
                if process.poll() is None:
                    process.send_signal(signal.CTRL_BREAK_EVENT if os.name == "nt" else signal.SIGINT)
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill(); process.wait()
                        raise AssertionError("dev failed to stop")
            assert process.returncode == 0, contents(logpath)
        print("TypeScript app reload, embed rebuild, and failure retention passed")


if __name__ == "__main__":
    main()
