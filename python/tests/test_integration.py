"""Exercise actual Go child processes, not a mocked transport."""
import asyncio
import concurrent.futures
import gc
import multiprocessing as mp
import os
from pathlib import Path
import pickle
import socket
import subprocess
import sys
import threading
import time
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "examples/textkit"))
from textkit import Analysis, AsyncTextKit, TextKit
from gobridge import BusyError, Client, ClosedError, DaemonError, InvalidArgumentError, RequestTimeout

BINARY = str(ROOT / "bin" / ("textkit.exe" if os.name == "nt" else "textkit"))


def process_call(client, output):
    try:
        with client:
            result = client.analyze(text="same key")
            output.put((os.getpid(), result.process_id, result.computation))
    except BaseException as e:
        output.put(("error", str(e)))


class Integration(unittest.TestCase):
    def test_typed_results_and_cache(self):
        with TextKit(BINARY) as client:
            result = client.analyze(text="hello 🌍")
            self.assertIsInstance(result, Analysis)
            self.assertEqual((result.words, result.characters), (2, 7))
            self.assertEqual(result, client.analyze(text="hello 🌍"))
            self.assertEqual(client.analyze(text="different").computation, 2)

    def test_threads_share_cache_and_correlate_responses(self):
        with TextKit(BINARY) as client, concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
            results = list(pool.map(lambda _: client.analyze(text="one key"), range(80)))
            self.assertEqual(len({r.process_id for r in results}), 1)
            self.assertEqual({r.computation for r in results}, {1})
            results = list(pool.map(lambda i: client.analyze(text="x " * i), range(80)))
            self.assertEqual([r.words for r in results], list(range(80)))

    def test_clients_are_isolated(self):
        with TextKit(BINARY) as a, TextKit(BINARY) as b:
            self.assertNotEqual(a.health().process_id, b.health().process_id)
            self.assertEqual(a.analyze(text="a").computation, 1)
            self.assertEqual(b.analyze(text="b").computation, 1)

    def test_errors_and_timeout_leave_client_usable(self):
        with TextKit(BINARY) as client:
            with self.assertRaises(InvalidArgumentError):
                client.wait(milliseconds=-1)
            with self.assertRaises(RequestTimeout):
                client.wait(milliseconds=10000, _timeout=0.03)
            for _ in range(100):
                if client.health().active == 0:
                    break
                time.sleep(0.01)
            self.assertEqual(client.health().active, 0)
            self.assertEqual(client.analyze(text="still alive").words, 2)

    def test_close_reaps_child_and_fails_pending(self):
        client = TextKit(BINARY).start()
        proc = client._transport.proc
        with concurrent.futures.ThreadPoolExecutor() as pool:
            pending = pool.submit(client.wait, milliseconds=10000)
            for _ in range(100):
                if client.health().active:
                    break
                time.sleep(0.01)
            client.close()
            with self.assertRaises((ClosedError, DaemonError)):
                pending.result(timeout=3)
        self.assertIsNotNone(proc.poll())
        with self.assertRaises(ClosedError):
            client.health()
        client.close()

    def test_crash_fails_pending_without_replay(self):
        with TextKit(BINARY) as client, concurrent.futures.ThreadPoolExecutor() as pool:
            pending = pool.submit(client.wait, milliseconds=10000)
            for _ in range(100):
                if client.health().active:
                    break
                time.sleep(0.01)
            client._transport.proc.kill()
            with self.assertRaises(DaemonError):
                pending.result(timeout=3)
            with self.assertRaises(DaemonError):
                client.health()

    def test_bounded_pending(self):
        with TextKit(BINARY, max_pending=1) as client:
            request_id, future = client._transport.submit("wait", {"milliseconds": 10000}, 20)
            with self.assertRaises(BusyError):
                client.health()
            client._transport.cancel(request_id)
            self.assertTrue(future.cancelled())

    def test_pickle_creates_fresh_session(self):
        with TextKit(BINARY) as a, pickle.loads(pickle.dumps(a)) as b:
            self.assertNotEqual(a.health().process_id, b.health().process_id)

    def test_garbage_collection_reaps_child(self):
        client = TextKit(BINARY).start()
        proc = client._transport.proc
        del client
        gc.collect()
        self.assertIsNotNone(proc.poll())

    def test_all_multiprocessing_start_methods(self):
        with TextKit(BINARY) as client:
            original = client.analyze(text="same key")
            for method in mp.get_all_start_methods():
                with self.subTest(method=method):
                    if method == "forkserver":
                        try:
                            with socket.socket(socket.AF_UNIX):
                                pass
                        except PermissionError:
                            self.skipTest("sandbox prohibits AF_UNIX sockets required by Python forkserver")
                    ctx = mp.get_context(method)
                    output = ctx.Queue()
                    child = ctx.Process(target=process_call, args=(client, output))
                    child.start()
                    try:
                        result = output.get(timeout=15)
                        child.join(timeout=10)
                        self.assertEqual(child.exitcode, 0)
                        self.assertNotEqual(result[0], "error", result)
                        self.assertNotEqual(result[1], original.process_id)
                        self.assertEqual(result[2], 1)
                        self.assertEqual(client.analyze(text="same key"), original)
                    finally:
                        if child.is_alive():
                            child.kill()
                            child.join()
                        output.close()
                        output.join_thread()

    @unittest.skipUnless(hasattr(os, "fork"), "POSIX only")
    def test_fork_does_not_inherit_locked_transport(self):
        with TextKit(BINARY) as client:
            ready, release = threading.Event(), threading.Event()
            def hold():
                with client._transport.lock:
                    ready.set()
                    release.wait(10)
            thread = threading.Thread(target=hold)
            thread.start()
            ready.wait(5)
            ctx = mp.get_context("fork")
            output = ctx.Queue()
            child = ctx.Process(target=process_call, args=(client, output))
            try:
                child.start()
                result = output.get(timeout=10)
                self.assertNotEqual(result[0], "error", result)
            finally:
                release.set()
                thread.join()
                child.join(5)
                if child.is_alive():
                    child.kill()
                    child.join()
                output.close()
                output.join_thread()

    def test_frame_limit_and_nan(self):
        with TextKit(BINARY) as client:
            with self.assertRaises(ValueError):
                client.analyze(text="x" * (1024 * 1024))
            with self.assertRaises(ValueError):
                client.call("wait", {"milliseconds": float("nan")})
            self.assertGreater(client.health().process_id, 0)

    def test_malformed_daemon_and_handshake_version(self):
        for response in ['not json', '{"id":"1","result":{"protocol":99}}']:
            script = "import sys,time;sys.stdin.readline();print(" + repr(response) + ",flush=True);time.sleep(20)"
            with self.assertRaises(DaemonError):
                with Client([sys.executable, "-c", script]):
                    pass

    def test_cli_matches_binding(self):
        result = subprocess.run([BINARY, "analyze", "--text", "two words"], capture_output=True, text=True, check=True)
        self.assertIn('"words":2', result.stdout)


class AsyncIntegration(unittest.IsolatedAsyncioTestCase):
    async def test_async_concurrency_and_loop_responsiveness(self):
        async with AsyncTextKit(BINARY) as client:
            ticks = []
            async def ticker():
                for _ in range(10):
                    ticks.append(1)
                    await asyncio.sleep(0.01)
            results, _ = await asyncio.gather(
                asyncio.gather(*(client.wait(milliseconds=120) for _ in range(16))), ticker())
            self.assertEqual(len({r.process_id for r in results}), 1)
            self.assertEqual(len(ticks), 10)
            self.assertEqual((await client.analyze(text="hi async")).words, 2)

    async def test_cancellation_only_cancels_one_request(self):
        async with AsyncTextKit(BINARY) as client:
            a = asyncio.create_task(client.wait(milliseconds=10000))
            b = asyncio.create_task(client.wait(milliseconds=150))
            for _ in range(100):
                if (await client.health()).active == 2:
                    break
                await asyncio.sleep(0.01)
            a.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await a
            await b
            self.assertEqual((await client.health()).active, 0)

    async def test_timeout_and_repeated_event_loops(self):
        async with AsyncTextKit(BINARY) as client:
            with self.assertRaises(RequestTimeout):
                await client.wait(milliseconds=10000, _timeout=0.02)
            self.assertEqual((await client.analyze(text="ok")).words, 1)


class MultipleLoops(unittest.TestCase):
    def test_same_client_across_event_loops_and_threads(self):
        with TextKit(BINARY) as client:
            async def invoke():
                return await client.acall("health")
            first = asyncio.run(invoke())
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                results = list(pool.map(lambda _: asyncio.run(invoke()), range(8)))
            self.assertEqual({r["process_id"] for r in results}, {first["process_id"]})


if __name__ == "__main__":
    unittest.main()
