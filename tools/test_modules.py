"""Install multi-module wheels/npm packages and exercise names and Go options."""
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
    goos = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}[platform.system()]
    arch = "arm64" if platform.machine().lower() in {"arm64", "aarch64"} else "amd64"
    with tempfile.TemporaryDirectory(prefix="gobridge-modules-") as temp:
        project = Path(temp)
        tool = project / ("gobridge.exe" if os.name == "nt" else "gobridge")
        run("go", "build", "-o", str(tool), "./cmd/gobridge")
        (project / "go.mod").write_text(f"module modulecheck\n\ngo 1.23\nrequire github.com/sambhav/gobridge v0.0.0\nreplace github.com/sambhav/gobridge => {ROOT.as_posix()}\n")
        run(str(tool), "generate", "--dir", str(ROOT / "examples/catalog"), "--check")
        config = {
            "version": "2.1.0",
            "python": {"distribution": "acme-sdk"},
            "typescript": {"package": "@acme/sdk"},
            "modules": [
                {"name": "catalog", "command": "github.com/sambhav/gobridge/examples/catalog/cmd/catalog",
                 "python": {"module": "acme.catalog", "rename": {"operations": {"status": "inspect"}, "types": {"Status": "Snapshot"}, "fields": {"Status.endpoint": "origin"}}},
                 "typescript": {"export": ".", "rename": {"operations": {"status": "inspect"}, "types": {"Status": "Snapshot"}, "fields": {"Status.endpoint": "origin"}}}},
                {"name": "other", "command": "github.com/sambhav/gobridge/examples/catalog/cmd/catalog",
                 "python": {"module": "acme.other", "class": "OtherCatalog"},
                 "typescript": {"export": "./other", "class": "OtherCatalog"}},
                {"name": "text", "command": "github.com/sambhav/gobridge/examples/greeter/cmd/greeter",
                 "python": {"module": "acme.text"}, "typescript": {"export": "./text"}},
            ],
        }
        (project / "gobridge.json").write_text(json.dumps(config))
        run(str(tool), "build", "--python", "--typescript", "--targets", f"{goos}-{arch}", cwd=project)
        wheels = list((project / "dist").glob("*.whl"))
        tarballs = list((project / "dist/npm").glob("*.tgz"))
        assert len(wheels) == len(tarballs) == 1
        site = project / "site"
        run(sys.executable, "-m", "pip", "install", "--no-index", "--no-deps", "--target", str(site), str(wheels[0]), cwd=project)
        python_test = '''
import asyncio
from acme import catalog, other, text
assert hasattr(catalog, "CatalogClient")
assert hasattr(other, "OtherCatalog")
with catalog.SyncCatalogClient(base_url="custom", retries=0) as client:
    value = client.inspect()
    assert value == catalog.Snapshot(origin="custom", retries=0), value
    assert client.echo(status=value) == value
with other.SyncOtherCatalog() as client:
    value = client.get_status()
    assert value.base_url == "https://example.test" and value.retries == 3
with catalog.session_sync(base_url="scoped") as client:
    assert client.inspect().origin == "scoped"
try:
    with catalog.SyncCatalogClient(retries=-1) as client:
        client.inspect()
except Exception as error:
    assert "non-negative" in str(error), error
else:
    raise AssertionError("factory error was lost")
with catalog.SyncCatalogClient(retry=catalog.RetryOptions(attempts=0, delay_ms=100)) as client:
    assert client.inspect().retries == 0
    assert client.retry_delay() == 100
try:
    catalog.RetryOptions(attempts=3)
except TypeError:
    pass
else:
    raise AssertionError("missing grouped parameter accepted")
async def check():
    async with catalog.CatalogClient(base_url="async") as client:
        assert (await client.inspect()).origin == "async"
    assert await text.greet(name="Sam") == "Hello, Sam!"
    await text.shutdown()
asyncio.run(check())
'''
        run(sys.executable, "-c", python_test, cwd=project, env=dict(os.environ, PYTHONPATH=str(site)))
        npm = shutil.which("npm")
        (project / "package.json").write_text('{"private":true,"type":"module"}')
        run(npm, "install", "--ignore-scripts", "--no-audit", "--no-fund", str(tarballs[0]), cwd=project)
        (project / "check.mjs").write_text('''
import assert from 'node:assert/strict';
import { CatalogApi } from '@acme/sdk';
import { OtherCatalog } from '@acme/sdk/other';
import { greet, shutdown } from '@acme/sdk/text';
const client = new CatalogApi({baseURL: 'custom', retries: 0});
try {
  const value = await client.inspect();
  assert.deepEqual(value, {origin: 'custom', retries: 0});
  assert.deepEqual(await client.echo({status:value}), value);
} finally {await client.close();}
const grouped = new CatalogApi({retry:{attempts:0,delayMs:100}});
try {assert.equal((await grouped.inspect()).retries,0);assert.equal(await grouped.retryDelay(),100);}
finally {await grouped.close();}
assert.throws(()=>new CatalogApi({retry:{attempts:3}}), /missing field/);
const other = new OtherCatalog();
try {assert.deepEqual(await other.getStatus(), {baseURL:'https://example.test',retries:3});}
finally {await other.close();}
assert.equal(await greet({name:'Sam'}), 'Hello, Sam!');
await shutdown();
''')
        run("node", "check.mjs", cwd=project)
        # Module selection must also carry naming controls into development.
        run(str(tool), "dev", "--module", "catalog", "--once", cwd=project)
        run(sys.executable, "-c", "from acme.catalog import SyncCatalogClient; c=SyncCatalogClient(retries=0); assert c.inspect().retries==0; c.close()",
            cwd=project, env=dict(os.environ, PYTHONPATH=str(project / "build")))
        shutil.rmtree(project / "node_modules/@acme/sdk")
        run(str(tool), "dev", "--module", "catalog", "--typescript", "--once", cwd=project)
        (project / "devcheck.mjs").write_text("import {CatalogApi} from '@acme/sdk'; const c=new CatalogApi({retries:0}); try {if((await c.inspect()).retries!==0)throw Error('bad retries');} finally {await c.close();}")
        run("node", "devcheck.mjs", cwd=project)
        print("Multi-module install, naming, functional options, and development checks passed")


if __name__ == "__main__":
    main()
