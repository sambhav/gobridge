import asyncio
from contextlib import closing, aclosing
from pathlib import Path
import os
import sys

import pytest
from generate_fixtures import generate_python
from gobridge import BridgeError

generate_python(("streaming",))
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / ".generated/python"))
from streaming import Streaming, SyncStreaming
BINARY = ROOT / "bin" / ("streaming.exe" if os.name == "nt" else "streaming")


def test_stream_and_batch():
    with SyncStreaming([str(BINARY), "bridge"]) as client:
        assert list(client.numbers(count=3)) == [0, 1, 2]
        with pytest.raises(BridgeError) as caught:
            list(client.numbers(count=1, fail=True))
        assert caught.value.details == {"field": "fail"}
        with closing(client.numbers(count=100000)) as items:
            assert next(items) == 0
        results = client.batch([{"method": "active"}, {"method": "missing"}, {"method": "explode"}])
        assert results[1]["error"].code == "not_found"
        assert results[2]["error"].message == "internal operation error"


@pytest.mark.asyncio
async def test_async_stream_cleanup():
    async with Streaming([str(BINARY), "bridge"]) as client:
        assert [n async for n in client.numbers(count=3)] == [0, 1, 2]
        async with aclosing(client.numbers(count=100000)) as items:
            assert await anext(items) == 0
        for _ in range(100):
            if await client.active() == 0:
                break
            await asyncio.sleep(.01)
        assert await client.active() == 0
        assert (await client.abatch([{"method":"active"}]))[0]["result"] == 0
