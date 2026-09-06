"""Package a nested bridge command and exercise its generated default launchers."""
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def run(*args, cwd=ROOT, **kwargs):
    return subprocess.run(args, cwd=cwd, check=True, **kwargs)

def main():
    with tempfile.TemporaryDirectory(prefix="gobridge-embedded-") as temporary:
        project=Path(temporary)
        cli=project / ("gobridge.exe" if os.name=="nt" else "gobridge")
        run("go","build","-o",str(cli),"./cmd/gobridge")
        (project/"go.mod").write_text('module embeddedtest\n\ngo 1.23\nrequire github.com/sambhav/gobridge v0.0.0\nreplace github.com/sambhav/gobridge => '+json.dumps(ROOT.as_posix())+'\n')
        # The binary intentionally has no generation command at its root.
        source=(ROOT/"internal/fixtures/streaming/main.go").read_text()
        source=source.replace('args := os.Args[1:]', 'args := os.Args[1:]\n if len(args)==0 || args[0]!="bridge" { panic("bridge prefix required") }')
        (project/"main.go").write_text(source)
        config={"modules":[{"name":"embedded","command":".","command_prefix":["bridge"],"typescript":{"export":"."}}]}
        (project/"gobridge.json").write_text(json.dumps(config))
        goos={"Linux":"linux","Darwin":"darwin","Windows":"windows"}[platform.system()]
        arch="arm64" if platform.machine().lower() in {"arm64","aarch64"} else "amd64"
        run(str(cli),"build","--python","--typescript","--targets",f"{goos}-{arch}",cwd=project)
        wheel=next((project/"dist").glob("*.whl"))
        site=project/"site"
        run(sys.executable,"-m","pip","install","--no-index","--no-deps","--target",str(site),str(wheel))
        test='from embedded import SyncEmbedded\nwith SyncEmbedded() as client:\n assert list(client.numbers(count=3)) == [0,1,2]\n'
        run(sys.executable,"-c",test,cwd=project,env=dict(os.environ,PYTHONPATH=str(site)))
        node=project/"node";node.mkdir()
        (node/"package.json").write_text('{"private":true,"type":"module"}')
        run(shutil.which("npm"),"install","--offline","--ignore-scripts","--no-audit","--no-fund",str(next((project/"dist/npm").glob("*.tgz"))),cwd=node)
        run("node","--input-type=module","-e",'import {Embedded} from "embedded"; await using c = new Embedded(); const v=[]; for await(const n of c.numbers({count:3}))v.push(n); if(v.join()!= "0,1,2")throw Error("stream");',cwd=node)
        run(str(cli),"dev","--once",cwd=project)
        run(sys.executable,"-c",test,cwd=project,env=dict(os.environ,PYTHONPATH=str(project/"build")))
        run(str(cli),"dev","--typescript","--once",cwd=project)
        print("Embedded generation: wheel/npm and dev launch nested commands")

if __name__=="__main__":main()
