"""Typed library failures survive ordinary Python process boundaries."""
import multiprocessing as mp
import pickle

import pytest

from gobridge import (
    BridgeError, BusyError, ClosedError, DaemonError, InvalidArgumentError,
    RequestTimeout,
)

ERROR_TYPES = (BridgeError, BusyError, ClosedError, DaemonError, InvalidArgumentError, RequestTimeout)


@pytest.mark.parametrize("error_type", ERROR_TYPES)
@pytest.mark.parametrize("protocol", [0, pickle.HIGHEST_PROTOCOL])
def test_exception_pickle_preserves_type_code_message_and_attributes(error_type, protocol):
    original = error_type("ошибка_λ", "操作 failed — café 🌍\nsecond line")
    original.operation = "greet"
    restored = pickle.loads(pickle.dumps(original, protocol=protocol))
    assert type(restored) is error_type
    assert restored.code == original.code
    assert restored.message == original.message
    assert restored.args == original.args
    assert str(restored) == str(original)
    assert restored.operation == original.operation
    if error_type is RequestTimeout:
        assert isinstance(restored, TimeoutError)


def _return_errors(connection, errors):
    try:
        connection.send(errors)
    finally:
        connection.close()


def test_all_errors_round_trip_through_spawn_arguments_and_pipe():
    context = mp.get_context("spawn")
    receiving, sending = context.Pipe(duplex=False)
    errors = [cls(f"code_{index}_λ", f"操作 {index}: café 🌍") for index, cls in enumerate(ERROR_TYPES)]
    child = context.Process(target=_return_errors, args=(sending, errors))
    try:
        child.start()
        sending.close()
        assert receiving.poll(10), "spawn child did not return its exceptions"
        restored = receiving.recv()
        child.join(timeout=5)
        assert child.exitcode == 0
        assert len(restored) == len(errors)
        for original, result in zip(errors, restored):
            assert type(result) is type(original)
            assert (result.code, result.message, result.args) == (original.code, original.message, original.args)
    finally:
        if child.is_alive():
            child.kill()
            child.join(timeout=5)
        receiving.close()
        sending.close()
