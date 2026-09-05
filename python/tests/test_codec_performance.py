"""Optimization regressions: preserve wire values and lazy recursive decoding."""
from __future__ import annotations

import base64
import dataclasses
import json

from gobridge.runtime import _decoder, _json_default, decode


@dataclasses.dataclass
class Branch:
    name: str
    children: list[Branch]
    data: bytes | None = None


def test_shallow_dataclass_encoding_matches_recursive_asdict():
    value = Branch("root", [Branch("child", [], b"\x00\xff")])
    def legacy(item):
        if isinstance(item, bytes):
            return base64.b64encode(item).decode("ascii")
        return dataclasses.asdict(item)
    before = json.dumps(value, default=legacy)
    after = json.dumps(value, default=_json_default)
    assert before == after
    assert decode(Branch, json.loads(after)) == value
    assert decode(dict[str, list[Branch]], {"items": [json.loads(after)]}) == {"items": [value]}


def test_decoder_cache_is_type_specific_and_bounded():
    first = dataclasses.make_dataclass("SameName", [("value", int)])
    second = dataclasses.make_dataclass("SameName", [("value", bytes)])
    assert decode(first, {"value": 123}).value == 123
    assert decode(second, {"value": "AA=="}).value == b"\x00"
    assert decode(bytes | None, None) is None
    assert decode(bytes, "") == b""
    assert _decoder.cache_info().maxsize == 512
