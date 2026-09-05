"""Deterministic checks for benchmark reporting; never assert speed."""
import unittest
from bench import summarize


class Reporting(unittest.TestCase):
    def test_comparison(self):
        report = dict(environment=dict(commit='abc',python='3.12',go='go1.23',node='v24'), parameters={},
                      results=[dict(case='tiny',client='go',concurrency=1,calls_per_second=200,
                                    p50_us=1,p95_us=2,p99_us=3)])
        before = dict(report, results=[dict(report['results'][0], calls_per_second=100)])
        text = summarize(report,before)
        self.assertIn('+100.0%',text)
        self.assertIn('Native Go excludes serialization and IPC',text)
        self.assertIn('null metric means unavailable',text)


if __name__ == '__main__':
    unittest.main()
