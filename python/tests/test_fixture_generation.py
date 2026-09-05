"""Bindings must be importable on the first run of a fresh checkout."""
from importlib.machinery import PathFinder

import generate_fixtures


def test_generation_refreshes_a_previously_missing_import_directory(tmp_path, monkeypatch):
    output = str(tmp_path / ".generated/python")
    assert PathFinder.find_spec("greeter", [output]) is None
    monkeypatch.setattr(generate_fixtures, "ROOT", tmp_path)
    monkeypatch.setattr(generate_fixtures.subprocess, "run", lambda *args, **kwargs: None)
    monkeypatch.setattr(generate_fixtures.subprocess, "check_output", lambda *args, **kwargs: b"answer = 42\n")

    generate_fixtures.generate_python(["greeter"])

    spec = PathFinder.find_spec("greeter", [output])
    assert spec is not None
    assert spec.loader.get_source("greeter") == "answer = 42\n"
