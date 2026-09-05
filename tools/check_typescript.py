"""Compile generated APIs and test Node bindings against real Go daemons."""
import json
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
TYPESCRIPT = ROOT / "typescript"
EXAMPLES = {
    "textkit": ("./examples/textkit", "TextKit"),
    "hello": ("./examples/hello", "Hello"),
    "annotated": ("./examples/annotated/cmd/annotated", "Greeter"),
    "wiretypes": ("./internal/fixtures/wiretypes", "WireTypes"),
    "metadata": ("./internal/fixtures/metadata", "Store"),
}


def run(*args, cwd=ROOT):
    subprocess.run(args, cwd=cwd, check=True)


def main():
    compiler = TYPESCRIPT / "node_modules" / "typescript" / "bin" / "tsc"
    if not compiler.is_file():
        raise SystemExit("Install compiler dependencies: npm ci --ignore-scripts --prefix typescript")
    run("node", str(compiler), "-p", str(TYPESCRIPT / "tsconfig.json"))
    generated = TYPESCRIPT / ".generated"
    generated.mkdir(exist_ok=True)
    (generated / "package.json").write_text('{"private":true,"type":"module"}\n')
    runtime = generated / "node_modules" / "gobridge-runtime"
    runtime.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(TYPESCRIPT / "package.json", runtime / "package.json")
    shutil.copytree(TYPESCRIPT / "dist", runtime / "dist", dirs_exist_ok=True)
    (ROOT / "bin").mkdir(exist_ok=True)
    suffix = ".exe" if os.name == "nt" else ""
    for name, (package, client_class) in EXAMPLES.items():
        binary = ROOT / "bin" / (name + suffix)
        run("go", "build", "-o", str(binary), package)
        source = subprocess.check_output([
            str(binary), "generate-typescript", "--class", client_class, "--binary", name,
        ]).decode("utf-8")
        checked_in = ROOT / "examples" / name / (name + ".ts")
        if checked_in.is_file() and checked_in.read_text(encoding="utf-8") != source:
            raise SystemExit(f"Generated TypeScript is stale: {checked_in}")
        (generated / (name + ".ts")).write_text(source, encoding="utf-8", newline="\n")
    run("go", "build", "-o", str(ROOT / "bin" / ("cobra-host" + suffix)), ".", cwd=ROOT / "examples" / "cobra")
    shutil.copyfile(TYPESCRIPT / "test" / "api.types.ts", generated / "api.types.ts")
    config = {
        "compilerOptions": {
            "target": "ES2022", "lib": ["ESNext"],
            "module": "NodeNext", "moduleResolution": "NodeNext",
            "strict": True, "declaration": True, "noEmitOnError": True,
            "outDir": "dist", "types": ["node"],
        },
        "include": ["*.ts"],
    }
    (generated / "tsconfig.json").write_text(json.dumps(config, indent=2) + "\n")
    run("node", str(compiler), "-p", str(generated / "tsconfig.json"))
    tests = sorted(str(path) for path in (TYPESCRIPT / "test").glob("*.test.mjs"))
    run("node", "--test", *tests)


if __name__ == "__main__":
    main()
