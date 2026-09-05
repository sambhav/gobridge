"""Dependency-free codec experiment, NOT a supported wire protocol.

Fixed schema: bytes data and a list of {name: str, values: list[int64]}.
Binary uses big-endian uint32 lengths/counts and signed int64 values. It omits
RPC envelopes, nullable values, schema evolution and hostile-input validation;
its timings are an optimistic bound, not a production replacement for JSON.
"""
import base64
import json
import platform
import struct
import time


def binary_encode(value):
    data, nodes = value["data"], value["nodes"]
    chunks = [struct.pack(">I", len(data)), data, struct.pack(">I", len(nodes))]
    for node in nodes:
        name, values = node["name"].encode(), node["values"]
        chunks.extend((struct.pack(">I", len(name)), name, struct.pack(">I", len(values)),
                       struct.pack(">" + "q" * len(values), *values)))
    return b"".join(chunks)


def binary_decode(data):
    offset = 0
    def size():
        nonlocal offset
        value, = struct.unpack_from(">I", data, offset)
        offset += 4
        return value
    length = size()
    payload = data[offset:offset+length]
    offset += length
    nodes = []
    for _ in range(size()):
        length = size()
        name = data[offset:offset+length].decode()
        offset += length
        count = size()
        values = list(struct.unpack_from(">" + "q" * count, data, offset))
        offset += count * 8
        nodes.append({"name":name,"values":values})
    if offset != len(data):
        raise ValueError("trailing bytes")
    return {"data":payload,"nodes":nodes}


def json_encode(value):
    return json.dumps(value, default=lambda v:base64.b64encode(v).decode("ascii"),
                      allow_nan=False, separators=(",", ":")).encode()


def json_decode(data):
    value = json.loads(data)
    value["data"] = base64.b64decode(value["data"], validate=True)
    return value


def main():
    results = []
    for case, value in [
        ("tiny", {"data":b"", "nodes":[]}),
        ("nested", {"data":b"", "nodes":[{"name":"entry", "values":[1,2,3,4]} for _ in range(16)]}),
        ("bytes64k", {"data":bytes(65536), "nodes":[]}),
    ]:
        for repeat in range(5):
            codecs = [("json",json_encode,json_decode),("fixed_binary",binary_encode,binary_decode)]
            if repeat % 2: codecs.reverse()
            for name, encode, decode in codecs:
                wire = encode(value)
                assert decode(wire) == value
                for _ in range(100): decode(encode(value))
                start = time.perf_counter()
                for _ in range(1000): decode(encode(value))
                results.append({"case":case,"codec":name,"repeat":repeat,"wire_bytes":len(wire),
                                "roundtrip_us":(time.perf_counter()-start)*1000})
    print(json.dumps({"python":platform.python_version(),"calls":1000,"results":results},indent=2))


if __name__ == "__main__":
    main()
