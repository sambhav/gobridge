"""Explicit author-owned package additions; no build hooks or implicit discovery."""
import json
from pathlib import Path
import re
import shutil


def settings(project):
    path = Path(project) / "gobridge.json"
    return json.loads(path.read_text()) if path.is_file() else {}


def copy_package(project, language, destination):
    config = settings(project)
    source = config.get(language + "_package")
    if not source:
        return False
    root = Path(project).resolve()
    source = root / source
    if source.is_symlink() or not source.is_dir() or not source.resolve().is_relative_to(root):
        raise ValueError(f"{language}_package must be a directory inside the project")
    reserved = {"_bin", "_gobridge", "py.typed", "_bindings.py", "generated.ts", "package.json", "node_modules", "compiled", "tsconfig.json"}
    paths = list(source.rglob("*"))
    for path in paths:
        relative = path.relative_to(source)
        if path.is_symlink() or any(part.startswith(".") or part in reserved or part.startswith("_gobridge_") for part in relative.parts):
            raise ValueError(f"reserved or unsafe package addition: {relative}")
    for path in paths:
        relative = path.relative_to(source)
        if "__pycache__" in relative.parts or path.suffix == ".pyc":
            continue
        target = destination / relative
        if path.is_dir(): target.mkdir(parents=True, exist_ok=True)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
    return True


def python_requirements(project):
    values = settings(project).get("python_requires", [])
    for value in values:
        if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*(?:\[[A-Za-z0-9_,.-]+\])?(?:\s*(?:~=|==|!=|<=|>=|<|>)\s*[A-Za-z0-9.*+!-]+(?:\s*,\s*(?:~=|==|!=|<=|>=|<|>)\s*[A-Za-z0-9.*+!-]+)*)?", value):
            raise ValueError("python_requires supports package names, extras, and version comparisons")
    return values
