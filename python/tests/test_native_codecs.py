import importlib.util
import os
from pathlib import Path
import sys

from gobridge.runtime import decode, _json_default


def test_native_codecs_round_trip(tmp_path):
    root = Path(__file__).resolve().parents[2]
    from tools.generate_fixtures import generate_python
    generate_python(("wiretypes",))
    spec = importlib.util.spec_from_file_location("native_wiretypes", root / ".generated/python/wiretypes.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    module.configure(command=str(root / "bin" / ("wiretypes.exe" if os.name == "nt" else "wiretypes")))
    try:
        for data in (None, b"", b"\x00\x01\xff"):
            for delay in (-(2**63), 2**63-1):
                result = module.native_values_sync(data=data, at="2026-09-05T12:34:56.123456789+02:00", delay=delay)
                assert result.data == data
                assert result.at == "2026-09-05T12:34:56.123456789+02:00"
                assert result.delay == delay
    finally:
        module.shutdown_sync()
        sys.modules.pop(spec.name, None)
    assert decode(bytes, _json_default(b"\xff")) == b"\xff"
