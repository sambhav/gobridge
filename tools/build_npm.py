"""Build local npm tarballs containing the runtime and generated Go bindings.

The example tarball includes binaries for six Node platform/architecture pairs.
Use --targets to shorten a local build. This script never publishes packages.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

from package_customization import copy_package, settings
from packaging_common import application_version, build_go_binary

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
    parser.add_argument("--modules", type=Path, help="resolved module manifest from gobridge build")
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    parser.add_argument("--build-cache", type=Path, help="reuse Go link outputs; Go still checks sources and flags")
    parser.add_argument("--go-package", default="./examples/greeter/cmd/greeter", help="Go command package to build")
    parser.add_argument("--class", dest="client_class", default="Greeter", help="Generated TypeScript client class")
    parser.add_argument("--binary", default="greeter", help="Executable filename without .exe")
    parser.add_argument("--package", default="gobridge-greeter-example", help="Generated npm package name")
    parser.add_argument("--output", type=Path, default=ROOT / "dist" / "npm", help="Tarball output directory")
    parser.add_argument("--project", type=Path, default=ROOT, help="Go project directory")
    parser.add_argument("--version", help="Application package version")
    parser.add_argument("--repository", default="", help="Application source repository URL")
    parser.add_argument("--license", default="", help="Application license identifier")
    parser.add_argument("--dev-output", type=Path, help="publish an immutable local development revision")
    parser.add_argument("--host-binary", type=Path, help="reuse a host executable in development mode")
    args = parser.parse_args()
    if args.host_binary and not args.dev_output:
        parser.error("--host-binary requires --dev-output")
    project = args.project.resolve()
    if not re.fullmatch(r"[A-Z][A-Za-z0-9_]*", args.client_class):
        parser.error("--class must be a capitalized class identifier")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", args.binary):
        parser.error("--binary must be a filename stem using letters, digits, _ or -")
    if not re.fullmatch(r"(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*", args.package):
        parser.error("--package must be a lowercase npm package name")
    if args.package == "gobridge-runtime":
        parser.error("--package must differ from gobridge-runtime")
    runtime_manifest = json.loads((RUNTIME / "package.json").read_text())
    version = args.version if args.version is not None else runtime_manifest["version"]
    application_version(version)  # Same input policy as wheels, before compilation.
    compiler = RUNTIME / "node_modules" / "typescript" / "bin" / "tsc"
    if not compiler.is_file():
        parser.error("install compiler dependencies first: npm ci --ignore-scripts --prefix typescript")
    output = args.output.resolve()
    if not args.dev_output:
        output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="gobridge-npm-") as temp:
        temporary = Path(temp)
        modules = json.loads(args.modules.read_text()) if args.modules else [{
            "command": args.go_package, "binary": args.binary,
            "typescript": {"export": ".", "class": args.client_class},
        }]
        stage = temporary / "package"
        source = stage / "src"
        source.mkdir(parents=True)
        exports = {}
        custom = False
        for index, module in enumerate(modules):
            config = module["typescript"]
            export = config["export"]
            relative = "" if export == "." else export[2:]
            module["directory"] = relative
            directory = source / relative
            directory.mkdir(parents=True, exist_ok=True)
            if (directory / "index.ts").exists() or (directory / "generated.ts").exists():
                raise ValueError(f"module collides with package additions: {export}")
            host = temporary / (f"host{index}" + (".exe" if os.name == "nt" else ""))
            host_env = {key: value for key, value in os.environ.items() if key not in {"GOOS", "GOARCH"}}
            host_env["CGO_ENABLED"] = "0"
            if args.host_binary:
                shutil.copyfile(args.host_binary, host)
            else:
                build_go_binary(host, module["command"], project, host_env, cache=args.build_cache)
            host.chmod(0o755)
            bindings = subprocess.check_output([str(host), "generate-typescript", "--class", config["class"],
                "--binary", module["binary"], "--names", json.dumps(config.get("rename", {}))])
            shutil.copytree(RUNTIME / "src", directory / "_gobridge")
            module_custom = copy_package(project, config.get("package", ""), directory)
            custom = custom or module_custom
            generated = "generated.ts" if module_custom else "index.ts"
            (directory / generated).write_bytes(bindings.replace(b'from "gobridge-runtime"', b'from "./_gobridge/index.js"'))
            if module_custom and not (directory / "index.ts").exists():
                (directory / "index.ts").write_text('export * from "./generated.js";\n')
            prefix = "./" + (relative + "/" if relative else "")
            exports[export] = {"types": prefix + "index.d.ts", "import": prefix + "index.js"}
        manifest = {
            "name": args.package,
            "version": version,
            "description": "Generated TypeScript bindings with a bundled Go library daemon",
            "type": "module",
            "main": "./index.js",
            "types": "./index.d.ts",
            "exports": exports,
            "files": ["index.js", "index.d.ts", "_bin", "_gobridge", "LICENSE", "README.md"],
            "license": args.license or "UNLICENSED",
            "engines": {"node": ">=24"},
        }
        if "." not in exports:
            manifest.pop("main"); manifest.pop("types")
        dependencies = settings(project).get("typescript", {}).get("dependencies", {})
        if dependencies:
            manifest["dependencies"] = dependencies
        if custom or args.modules:
            manifest["files"] = ["**/*", "!src", "!compiled", "!tsconfig.json"]
        if args.repository:
            manifest["repository"] = {"type": "git", "url": args.repository}
        (stage / "package.json").write_text(json.dumps(manifest, indent=2) + "\n")
        if dependencies:
            npm("install", "--ignore-scripts", "--package-lock=false", "--no-audit", "--no-fund", cwd=stage)
        if (project / "LICENSE").is_file():
            shutil.copyfile(project / "LICENSE", stage / "LICENSE")
        (stage / "README.md").write_text(
            f"# {args.package}\n\nGenerated bindings and bundled Go executables. "
            f"Import `{args.client_class}` to create an isolated client, or import "
            "an operation directly to use the lazy default client.\n\n"
            "Requires Node.js 24 or newer. No Go toolchain is needed by consumers.\n"
        )
        if (project / "README.md").is_file():
            shutil.copyfile(project / "README.md", stage / "README.md")
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
        shutil.copytree(stage / "compiled", stage, dirs_exist_ok=True)
        if custom:
            for asset in source.rglob("*"):
                if asset.is_file() and asset.suffix != ".ts":
                    destination = stage / asset.relative_to(source)
                    if destination.exists():
                        raise ValueError(f"package asset collides with generated output: {destination.name}")
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copyfile(asset, destination)
        for module in modules:
            module_stage = stage / module["directory"]
            shutil.copyfile(ROOT / "LICENSE", module_stage / "_gobridge" / "LICENSE")
            for target in args.targets:
                goos, goarch, node_target = TARGETS[target]
                binary_dir = module_stage / "_bin" / node_target
                binary_dir.mkdir(parents=True)
                binary = binary_dir / (module["binary"] + (".exe" if goos == "windows" else ""))
                env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
                if args.host_binary:
                    shutil.copyfile(host, binary)
                else:
                    build_go_binary(binary, module["command"], project, env, cache=args.build_cache, trimpath=True)
                binary.chmod(0o755)
                print("Built", node_target, flush=True)
        if args.dev_output:
            destination = args.dev_output.resolve()
            shutil.rmtree(stage / "node_modules", ignore_errors=True)
            shutil.rmtree(stage / "src")
            shutil.rmtree(stage / "compiled")
            (stage / "tsconfig.json").unlink()
            digest = hashlib.sha256()
            for path in sorted(stage.rglob("*")):
                if path.is_file():
                    digest.update(path.relative_to(stage).as_posix().encode() + b"\0")
                    digest.update(path.read_bytes())
            revision = "rev-" + digest.hexdigest()[:24]
            target = destination / revision
            if not target.exists():
                with tempfile.TemporaryDirectory(prefix=".revision-", dir=destination) as temporary_revision:
                    staged = Path(temporary_revision) / revision
                    shutil.copytree(stage, staged)
                    os.replace(staged, target)
            manifest["exports"] = {key: {kind: f"./{revision}/" + path[2:] for kind, path in value.items()} for key, value in exports.items()}
            if "." in manifest["exports"]:
                manifest["main"] = manifest["exports"]["."]["import"]
                manifest["types"] = manifest["exports"]["."]["types"]
            fd, name = tempfile.mkstemp(prefix=".manifest-", dir=destination)
            try:
                with os.fdopen(fd, "w") as file:
                    json.dump(manifest, file, indent=2)
                os.replace(name, destination / "package.json")
            finally:
                if os.path.exists(name): os.unlink(name)
            print("Updated", destination, "with", revision, flush=True)
        else:
            pack(stage, output)


if __name__ == "__main__":
    main()
