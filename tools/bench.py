"""Cross-language, closed-loop benchmarks. Timings are descriptive, not CI gates."""
import argparse
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
CASES = {"tiny": (0, 0, False), "nested": (0, 0, True),
         "bytes1k": (1024, 0, False), "bytes64k": (65536, 0, False),
         "cpu": (64, 2000, False)}


def command_text(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def process_tree(pid):
    """Linux-only observed RSS/process count; unavailable is never reported as zero."""
    directory = Path('/proc') / str(pid)
    try:
        pages = int((directory / 'statm').read_text().split()[1])
        children = (directory / 'task' / str(pid) / 'children').read_text().split()
        rss, count = pages * os.sysconf('SC_PAGE_SIZE'), 1
        for child in children:
            result = process_tree(int(child))
            if result:
                rss += result[0]
                count += result[1]
        return rss, count
    except (OSError, ValueError, IndexError, AttributeError):
        return None


def measure(command):
    peak_rss = peak_processes = None
    process = subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            usage = process_tree(process.pid)
            if usage:
                peak_rss = max(peak_rss or 0, usage[0])
                peak_processes = max(peak_processes or 0, usage[1])
            try:
                stdout, stderr = process.communicate(timeout=.05)
                if process.returncode:
                    raise RuntimeError(f"{command}: {stderr}")
                result = json.loads(stdout)
                result.update(observed_peak_rss_bytes=peak_rss, observed_peak_processes=peak_processes)
                return result
            except subprocess.TimeoutExpired:
                continue
        raise TimeoutError(f"benchmark exceeded 120 seconds: {command}")
    finally:
        if process.poll() is None:
            process.kill()
            process.communicate()


def summarize(report, previous=None):
    lines = ['# Benchmark results', '',
             'Closed-loop load; latency includes time waiting behind other in-flight calls. '
             'Native Go excludes serialization and IPC. No timing thresholds are enforced.', '',
             f"Commit: `{report['environment']['commit']}`; Python {report['environment']['python']}; "
             f"{report['environment']['go']}; Node {report['environment']['node']}.", '',
             '| Case | Client | Concurrency | Calls/s | p50 µs | p95 µs | p99 µs | Throughput change |',
             '|---|---|---:|---:|---:|---:|---:|---:|']
    baseline = {} if previous is None else {(r['case'], r['client'], r['concurrency']): r for r in previous['results']}
    for row in report['results']:
        before = baseline.get((row['case'], row['client'], row['concurrency']))
        change = '—' if before is None else f"{(row['calls_per_second']/before['calls_per_second']-1)*100:+.1f}%"
        lines.append(f"| {row['case']} | {row['client']} | {row['concurrency']} | {row['calls_per_second']:.0f} | "
                     f"{row['p50_us']:.1f} | {row['p95_us']:.1f} | {row['p99_us']:.1f} | {change} |")
    lines += ['', 'Values are medians across repeats. Raw samples, cold-first-call timings, and observed '
              'process-tree RSS are in the JSON report. RSS sampling is Linux-only, every 50 ms, and can '
              'miss short-lived peaks. A null metric means unavailable, not zero. The RSS sum includes '
              'shared pages once per process. These are host-specific results, not universal guarantees.']
    stable = lambda env: {k:v for k,v in env.items() if k not in ('commit','dirty','tree')}
    if previous and (stable(previous['environment']) != stable(report['environment']) or previous['parameters'] != report['parameters']):
        lines += ['', '**Comparison warning:** environment, commit, or parameters differ. Inspect both JSON reports before attributing changes to code.']
    return '\n'.join(lines) + '\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--calls', type=int, default=1000)
    parser.add_argument('--repeats', type=int, default=3)
    parser.add_argument('--concurrency', default='1,8,32,128')
    parser.add_argument('--cases', default=','.join(CASES))
    parser.add_argument('--clients', default='go,python-sync,python-async,typescript')
    parser.add_argument('--output', type=Path, default=ROOT / 'benchmark-results.json')
    parser.add_argument('--compare', type=Path)
    parser.add_argument('--skip-build', action='store_true', help='reuse fixtures after the initial run')
    args = parser.parse_args()
    concurrency = [int(n) for n in args.concurrency.split(',')]
    cases, clients = args.cases.split(','), args.clients.split(',')
    if not 20 <= args.calls <= 100000 or not 1 <= args.repeats <= 20 or any(n<1 or n>128 for n in concurrency):
        parser.error('calls: 20..100000; repeats: 1..20; concurrency: 1..128')
    if any(case not in CASES for case in cases) or any(c not in ['go','python-sync','python-async','typescript'] for c in clients):
        parser.error('unknown case or client')
    if not args.skip_build:
        from generate_fixtures import generate_python
        generate_python(['perf'])
        if 'typescript' in clients:
            # Reuse the same generated-binding/type-check path as integration CI.
            subprocess.run([sys.executable, str(ROOT/'tools/check_typescript.py')], cwd=ROOT, check=True, stdout=sys.stderr)
    environment = dict(commit=command_text('git','rev-parse','HEAD'), tree=command_text('git','rev-parse','HEAD^{tree}'), dirty=bool(command_text('git','status','--porcelain')), daemon_max_concurrency=128,
                       go=command_text('go','version'), python=platform.python_version(), node=command_text('node','--version') if 'typescript' in clients else None,
                       platform=platform.platform(), cpu_count=os.cpu_count(), machine=platform.machine(),
                       gomaxprocs=os.environ.get('GOMAXPROCS'), cpu_quota=Path('/sys/fs/cgroup/cpu.max').read_text().strip() if Path('/sys/fs/cgroup/cpu.max').exists() else None)
    report = dict(environment=environment, parameters=dict(calls=args.calls,repeats=args.repeats,concurrency=concurrency,cases=cases,clients=clients), results=[])
    binary = str(ROOT/'bin'/('perf.exe' if os.name=='nt' else 'perf'))
    for case in cases:
        size, rounds, nested = CASES[case]
        for count in concurrency:
            for client in clients:
                common = ['--calls',str(args.calls),'--concurrency',str(count),'--size',str(size),'--rounds',str(rounds)] + (['--nested'] if nested else [])
                command = [binary,'native'] if client=='go' else ['node',str(ROOT/'tools/bench_node.mjs')] if client=='typescript' else [sys.executable,str(ROOT/'tools/bench_python.py'),'--mode',client.removeprefix('python-')]
                print(f'{case}: {client}, concurrency={count}', file=sys.stderr, flush=True)
                runs = [measure(command + common) for _ in range(args.repeats)]
                row = dict(case=case,client=client,concurrency=count,runs=runs)
                for metric in ('calls_per_second','p50_us','p95_us','p99_us'):
                    row[metric] = statistics.median(run[metric] for run in runs)
                report['results'].append(row)
                args.output.parent.mkdir(parents=True,exist_ok=True)
                args.output.write_text(json.dumps(report,indent=2)+'\n')
    previous = json.loads(args.compare.read_text()) if args.compare else None
    args.output.with_suffix('.md').write_text(summarize(report,previous))
    print(args.output)


if __name__ == '__main__':
    main()
