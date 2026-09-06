from pathlib import Path
import os
import sys
import pytest
from generate_fixtures import generate_python
from gobridge import UNSET, InvalidArgumentError

generate_python(("typesplus",))
ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/".generated/python"))
from typesplus import SyncTypesPlus, TypesPlus, Payload, Mode, Level
BINARY=ROOT/"bin"/("typesplus.exe" if os.name=="nt" else "typesplus")

def payload(**changes):
    return Payload(**dict(dict(id=2**64-1,pair=[0,65535],mode=Mode.Fast,level=Level.Huge,key="id-test",region=None),**changes))

def test_extended_types_and_typed_batches():
    with SyncTypesPlus(str(BINARY)) as client:
        value=payload()
        assert value.label is UNSET
        result=client.round_trip(value=value)
        assert result==value
        assert result.mode is Mode.Fast and result.level is Level.Huge
        assert client.empty(value=[])==[]
        first=client.calls.round_trip(value=value)
        second=client.calls.signed(value=-(2**63))
        value.pair[0]=12
        batch=client.batch([first,second])
        assert batch.get(first).pair[0]==0
        assert batch.get(second)==-(2**63)
        assert batch[0]["result"].id==2**64-1
        with pytest.raises(TypeError):
            Payload(id=1,pair=[1,2],mode=Mode.Fast,level=Level.Small,key="id-x")

@pytest.mark.parametrize("changes",[{"id":2**64},{"id":-1},{"pair":[1]},{"pair":[1,2,3]},{"pair":[1,65536]},{"mode":"invalid"},{"level":2},{"key":"bad"},{"label":None}])
def test_invalid_types(changes):
    with SyncTypesPlus(str(BINARY)) as client:
        with pytest.raises(InvalidArgumentError):client.round_trip(value=payload(**changes))

@pytest.mark.asyncio
async def test_async_typed_batch():
    async with TypesPlus(str(BINARY)) as client:
        call=client.calls.round_trip(value=payload())
        assert (await client.abatch([call])).get(call).mode is Mode.Fast
