"""Bounded real-daemon lifecycle stress: cancellation, overload, death, cleanup."""
import argparse
import asyncio
import base64
import json
import os
from pathlib import Path
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'python/src'))
from gobridge import Client, BusyError, ClosedError, DaemonError


async def exercise(binary, rounds):
    started = time.monotonic()
    for iteration in range(rounds):
        client = Client(binary, max_pending=8, timeout=10)
        await client.acall('work',dict(data='',rounds=0,nodes=[]))
        transport = client._transport  # Test-only inspection of resource ownership.
        process = transport.proc
        tasks = [asyncio.create_task(client.acall('work',dict(data='',rounds=50_000_000,nodes=[]))) for _ in range(8)]
        try:
            # Wait for all eight requests to actually own pending slots.
            deadline = time.monotonic()+5
            while len(transport.pending)<8:
                if time.monotonic()>deadline: raise AssertionError('requests failed to become pending')
                await asyncio.sleep(.001)
            try:
                await client.acall('work',dict(data='',rounds=0,nodes=[]))
                raise AssertionError('pending limit was not enforced')
            except BusyError:
                pass
            if iteration % 3 == 0:
                for task in tasks: task.cancel()
            elif iteration % 3 == 1:
                process.kill()
            else:
                await client.aclose()
            results = await asyncio.wait_for(asyncio.gather(*tasks,return_exceptions=True),5)
            assert all(isinstance(result,(asyncio.CancelledError,DaemonError,ClosedError)) for result in results), results
            if iteration % 3 == 0:
                # Cancellation leaves the same daemon usable after its handlers drain.
                result = await client.acall('work',dict(data=base64.b64encode(b'ok').decode(),rounds=0,nodes=[]))
                assert result['data']=='b2s='
        finally:
            for task in tasks: task.cancel()
            await client.aclose()
            await asyncio.gather(*tasks,return_exceptions=True)
        assert process.poll() is not None, 'daemon was not reaped'
        assert not transport.pending, 'pending requests leaked'
        assert not transport.reader.is_alive() and not transport.writer.is_alive(), 'transport threads leaked'
    return dict(rounds=rounds,calls=rounds*8,seconds=time.monotonic()-started,
                cancellations=(rounds+2)//3,process_deaths=(rounds+1)//3,close_races=rounds//3)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rounds',type=int,default=60)
    args=parser.parse_args()
    if not 3<=args.rounds<=1000: parser.error('rounds must be 3..1000')
    from generate_fixtures import generate_python
    generate_python(['perf'])
    binary=str(ROOT/'bin'/('perf.exe' if os.name=='nt' else 'perf'))
    print(json.dumps(asyncio.run(exercise(binary,args.rounds)),indent=2))


if __name__=='__main__':
    main()
