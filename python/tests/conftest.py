"""Generate current bindings before collection, including direct pytest runs."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "tools"))
from generate_fixtures import generate_python


def pytest_configure(config):
    generate_python()
