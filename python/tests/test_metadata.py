"""Shared Go constraints remain discoverable without Python validation overhead."""
import dataclasses
import importlib.util
import inspect
import json
import os
from pathlib import Path
import subprocess
import sys

import pytest

from gobridge import InvalidArgumentError

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="module")
def metadata_api(tmp_path_factory):
    folder = tmp_path_factory.mktemp("metadata")
    binary = folder / ("metadata.exe" if os.name == "nt" else "metadata")
    subprocess.run(["go", "build", "-o", str(binary), "./internal/fixtures/metadata"], cwd=ROOT, check=True)
    source = subprocess.check_output([str(binary), "generate-python", "--class", "Store", "--binary", "metadata"])
    file = folder / "metadata_generated.py"
    file.write_bytes(source)
    spec = importlib.util.spec_from_file_location("metadata_generated", file)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module, binary


def request(api, **changes):
    values = dict(name="世界🌍", big=9007199254740993, tags=[], labels={}, fraction=1.5)
    values.update(changes)
    return api.Request(**values)


def test_metadata_uses_standard_dataclasses_and_exact_integers(metadata_api):
    api, binary = metadata_api
    fields = {f.name: f for f in dataclasses.fields(api.Request)}
    assert fields["name"].metadata["description"] == "One to four Unicode code points."
    assert fields["name"].metadata["constraints"] == {"min_length": 1, "max_length": 4}
    assert fields["big"].metadata["constraints"] == {"minimum": 9007199254740993, "maximum": 9007199254740995}
    assert request(api).age is None
    assert inspect.signature(api.Store).parameters["capacity"].default is inspect.Parameter.empty
    assert dataclasses.fields(api.Config)[0].metadata["description"] == "Maximum stored items."
    with api.Store(binary, capacity=2) as store:
        original = request(api)
        assert store.echo(request=original) == original
        assert store.flattened(**dataclasses.asdict(original)) == original
        assert store.echo(request=request(api, age=120, big=9007199254740995, tags=None, labels=None)).age == 120


@pytest.mark.parametrize("changes,path", [
    ({"name": ""}, "name"),
    ({"name": "世界🌍ab"}, "name"),
    ({"age": -1}, "age"),
    ({"age": 121}, "age"),
    ({"big": 9007199254740992}, "big"),
    ({"big": 9007199254740996}, "big"),
    ({"tags": ["a", "b", "c"]}, "tags"),
    ({"labels": {"a": "1", "b": "2"}}, "labels"),
    ({"fraction": 0.49}, "fraction"),
])
def test_all_calls_apply_the_same_go_constraints(metadata_api, changes, path):
    api, binary = metadata_api
    # Dataclass construction remains lightweight. The daemon owns enforcement.
    value = request(api, **changes)
    with api.Store(binary, capacity=2) as store:
        with pytest.raises(InvalidArgumentError, match=path):
            store.echo(request=value)
        with pytest.raises(InvalidArgumentError, match=path):
            store.flattened(**dataclasses.asdict(value))


def test_constructor_constraints_and_help_are_shared(metadata_api):
    api, binary = metadata_api
    with pytest.raises(InvalidArgumentError, match="capacity"):
        with api.Store(binary, capacity=0):
            pass
    help_text = subprocess.check_output([str(binary), "echo", "--help"], text=True, encoding="utf-8")
    assert "Maximum stored items." in help_text
    assert "One to four Unicode code points." in help_text
    assert "request.name" in help_text
    cli = subprocess.run([str(binary), "--config", '{"capacity":2}', "flattened", "--json", json.dumps(dataclasses.asdict(request(api, age=121)))], capture_output=True)
    assert cli.returncode != 0
    assert json.loads(cli.stderr)["code"] == "invalid_argument"


def test_lowercase_go_model_is_not_shadowed_by_field_or_parameter(metadata_api):
    api, binary = metadata_api
    value = api.Holder(record=api.record(name="nested"))
    with api.Store(binary, capacity=2) as store:
        result = store.lowercase(record=value)
        assert isinstance(result.record, api.record)
        assert result == value
        assert store.lowercase(record=api.Holder()).record is None
