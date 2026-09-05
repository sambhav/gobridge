"""Install local npm tarballs in a clean project and exercise bundled Go code."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, default=ROOT / "dist" / "npm", help="Local npm tarball directory")
    args = parser.parse_args()
    version = json.loads((ROOT / "typescript" / "package.json").read_text())["version"]
    example = args.directory.resolve() / f"gobridge-greeter-example-{version}.tgz"
    for package in (example,):
        if not package.is_file():
            parser.error(f"missing {package}; run tools/build_npm.py first")
    with tempfile.TemporaryDirectory(prefix="gobridge-node-install-") as temp:
        project = Path(temp)
        (project / "package.json").write_text(json.dumps({"name": "gobridge-install-check", "private": True, "type": "module"}) + "\n")
        executable = shutil.which("npm")
        if executable is None:
            parser.error("npm is required; install Node.js 24 or newer")
        subprocess.run([
            executable, "install", "--ignore-scripts", "--offline", "--no-audit", "--no-fund",
            str(example),
        ], cwd=project, check=True)
        (project / "check.mjs").write_text('''
import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import { constants } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { Greeter, configure, session, shutdown, greet, welcome, stats } from "gobridge-greeter-example";

const moduleURL = import.meta.resolve("gobridge-greeter-example");
const packageRoot = dirname(fileURLToPath(moduleURL));
const binaryName = process.platform === "win32" ? "greeter.exe" : "greeter";
const packagedBinary = join(packageRoot, "_bin", `${process.platform}-${process.arch}`, binaryName);
await access(packagedBinary, process.platform === "win32" ? constants.F_OK : constants.X_OK);
await access(join(packageRoot, "_gobridge", "LICENSE"));
const manifest = JSON.parse(await readFile(join(packageRoot, "package.json"), "utf8"));
assert.equal(manifest.dependencies, undefined);

try {
  assert.equal(await greet({name: "World"}), "Hello, World!");
  assert.equal(await welcome({name: "Default"}), "Welcome, Default");
  assert.equal((await stats()).calls, 1n);
  const greeter = new Greeter({prefix: "Packaged: "});
  try {
    assert.equal(await greeter.welcome({name: "Sam"}), "Packaged: Sam");
    const result = await greeter.stats();
    assert.equal(result.calls, 1n);
    assert.equal(typeof result.calls, "bigint");
    assert.notEqual(result.processId, process.pid);
    assert.equal(await greeter.reset(), undefined);
    assert.equal((await greeter.stats()).calls, 0n);
  } finally {
    await greeter.close();
  }
  await session({prefix: "Scoped: "}, async (client) => {
    assert.equal(await welcome({name: "Sam"}), "Scoped: Sam");
    assert.equal((await client.stats()).calls, 1n);
  });
  assert.equal((await stats()).calls, 1n);
} finally {
  await shutdown();
}
console.log("Clean npm install: bundled binary, functions, classes, scopes and bigint passed");
''')
        # There is no source checkout in this project's import path, and neither
        # package uses install scripts to download or build its executable.
        env = {key: value for key, value in os.environ.items() if key not in {"NODE_PATH", "NODE_OPTIONS", "GOBRIDGE_BINARY"}}
        subprocess.run(["node", "check.mjs"], cwd=project, env=env, check=True, timeout=60)


if __name__ == "__main__":
    main()
