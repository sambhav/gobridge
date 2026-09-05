"""Build local npm tarballs containing the runtime and generated Go bindings.

The example tarball includes binaries for six Node platform/architecture pairs.
Use --targets to shorten a local build. This script never publishes packages.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT / "typescript"
TARGETS = {
    "linux-amd64": ("linux", "amd64", "linux-x64"),
    "linux-arm64": ("linux", "arm64", "linux-arm64"),
    "darwin-amd64": ("darwin", "amd64", "darwin-x64"),
    "darwin-arm64": ("darwin", "arm64", "darwin-arm64"),
    "windows-amd64": ("windows", "amd64", "win32-x64"),
    "windows-arm64": ("windows", "arm64", "win32-arm64"),
}


def npm(*args, cwd, capture=False):
    executable = shutil.which("npm")
    if executable is None:
        raise SystemExit("npm is required; install Node.js 24 or newer")
    return subprocess.run(
        [executable, *args], cwd=cwd, check=True,
        stdout=subprocess.PIPE if capture else None, text=True,
    )


def pack(stage, output):
    result = npm("pack", "--ignore-scripts", "--json", "--pack-destination", str(output), cwd=stage, capture=True)
    metadata = json.loads(result.stdout)
    if len(metadata) != 1:
        raise RuntimeError("npm pack did not return exactly one tarball")
    path = output / metadata[0]["filename"]
    print("Packed", path, flush=True)
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    parser.add_argument("--go-package", default="./examples/greeter/cmd/greeter", help="Go command package to build")
    parser.add_argument("--class", dest="client_class", default="Greeter", help="Generated TypeScript client class")
    parser.add_argument("--binary", default="greeter", help="Executable filename without .exe")
    parser.add_argument("--package", default="gobridge-greeter-example", help="Generated npm package name")
    parser.add_argument("--output", type=Path, default=ROOT / "dist" / "npm", help="Tarball output directory")
    parser.add_argument("--project", type=Path, default=ROOT, help="Go project directory")
    parser.add_argument("--version", help="Application package version")
    parser.add_argument("--repository", default="", help="Application source repository URL")
    parser.add_argument("--license", default="", help="Application license identifier")
    args = parser.parse_args()
    project = args.project.resolve()
    if not re.fullmatch(r"[A-Z][A-Za-z0-9]*", args.client_class):
        parser.error("--class must be a capitalized class identifier")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", args.binary):
        parser.error("--binary must be a filename stem using letters, digits, _ or -")
    if not re.fullmatch(r"(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*", args.package):
        parser.error("--package must be a lowercase npm package name")
    if args.package == "gobridge-runtime":
        parser.error("--package must differ from gobridge-runtime")
    compiler = RUNTIME / "node_modules" / "typescript" / "bin" / "tsc"
    if not compiler.is_file():
        parser.error("install compiler dependencies first: npm ci --ignore-scripts --prefix typescript")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    runtime_manifest = json.loads((RUNTIME / "package.json").read_text())
    runtime_version = runtime_manifest["version"]
    version = args.version or runtime_version
    with tempfile.TemporaryDirectory(prefix="gobridge-npm-") as temp:
        temporary = Path(temp)
        host = temporary / (args.binary + (".exe" if os.name == "nt" else ""))
        host_env = {key: value for key, value in os.environ.items() if key not in {"GOOS", "GOARCH"}}
        host_env["CGO_ENABLED"] = "0"
        subprocess.run(["go", "build", "-o", str(host), args.go_package], cwd=project, env=host_env, check=True)
        bindings = subprocess.check_output([
            str(host), "generate-typescript", "--class", args.client_class, "--binary", args.binary,
        ])
        stage = temporary / "package"
        source = stage / "src"
        source.mkdir(parents=True)
        shutil.copytree(RUNTIME / "src", source / "_gobridge")
        (source / "index.ts").write_bytes(bindings.replace(b'from "gobridge-runtime"', b'from "./_gobridge/index.js"'))
        manifest = {
            "name": args.package,
            "version": version,
            "description": "Generated TypeScript bindings with a bundled Go library daemon",
            "type": "module",
            "main": "./index.js",
            "types": "./index.d.ts",
            "exports": {".": {"types": "./index.d.ts", "import": "./index.js"}},
            "files": ["index.js", "index.d.ts", "_bin", "_gobridge", "LICENSE", "README.md"],
            "license": args.license or "UNLICENSED",
            "engines": {"node": ">=24"},
        }
        if args.repository:
            manifest["repository"] = {"type": "git", "url": args.repository}
        (stage / "package.json").write_text(json.dumps(manifest, indent=2) + "\n")
        if (project / "LICENSE").is_file():
            shutil.copyfile(project / "LICENSE", stage / "LICENSE")
        (stage / "README.md").write_text(
            f"# {args.package}\n\nGenerated bindings and bundled Go executables. "
            f"Import `{args.client_class}` to create an isolated client, or import "
            "an operation directly to use the lazy default client.\n\n"
            "Requires Node.js 24 or newer. No Go toolchain is needed by consumers.\n"
        )
        config = {
            "compilerOptions": {
                "target": "ES2022", "lib": ["ESNext"],
                "module": "NodeNext", "moduleResolution": "NodeNext",
                "strict": True, "declaration": True, "noEmitOnError": True,
                "rootDir": "src", "outDir": "compiled",
                "types": ["node"], "typeRoots": [str(RUNTIME / "node_modules" / "@types")],
            },
            "include": ["src/**/*.ts"],
        }
        (stage / "tsconfig.json").write_text(json.dumps(config, indent=2) + "\n")
        subprocess.run(["node", str(compiler), "-p", str(stage / "tsconfig.json")], cwd=stage, check=True)
        for filename in ("index.js", "index.d.ts"):
            shutil.copyfile(stage / "compiled" / filename, stage / filename)
        shutil.copytree(stage / "compiled" / "_gobridge", stage / "_gobridge")
        shutil.copyfile(ROOT / "LICENSE", stage / "_gobridge" / "LICENSE")
        for target in args.targets:
            goos, goarch, node_target = TARGETS[target]
            binary_dir = stage / "_bin" / node_target
            binary_dir.mkdir(parents=True)
            binary = binary_dir / (args.binary + (".exe" if goos == "windows" else ""))
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
            subprocess.run(["go", "build", "-trimpath", "-o", str(binary), args.go_package], cwd=project, env=env, check=True)
            binary.chmod(0o755)
            print("Built", node_target, flush=True)
        pack(stage, output)


if __name__ == "__main__":
    main()
