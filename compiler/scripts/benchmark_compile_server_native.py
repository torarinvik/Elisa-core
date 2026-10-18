"""Run fixed-count, order-reversed native server benchmark pairs.

Use an already-built test executable. This script never builds the compiler.
Prefer Homebrew Python on macOS: Apple's Python shim may inject SDKROOT, which
correctly causes conservative completed-object cache admission to decline.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import statistics
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--benchmark', choices=['Noop', 'BodyEdit'], default='Noop')
parser.add_argument('--iterations', type=int, default=10)
parser.add_argument('--pairs', type=int, default=3)
args = parser.parse_args()
if args.iterations < 1 or args.pairs < 1:
    parser.error('iterations and pairs must be positive')
root = Path(__file__).resolve().parents[2]
binary = args.binary.resolve(strict=True)
args.output.mkdir(parents=True, exist_ok=False)
sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
def source_snapshot():
    # Include parser/semantic/backend dependencies and untracked source files,
    # rather than attributing a binary to just its three benchmark entry files.
    listed = subprocess.check_output(
        ['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard', 'compiler'],
        cwd=root).split(b'\0')
    suffixes = {'.go', '.c', '.cc', '.cpp', '.h', '.hpp', '.elisa', '.mod', '.sum', '.s', '.S', '.txt'}
    paths = sorted({os.fsdecode(p) for p in listed if p and Path(os.fsdecode(p)).suffix in suffixes})
    return {path: sha(root / path) for path in paths}

manifest = {'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root,
             text=True).strip(), 'source_sha256': source_snapshot(),
            'binary_sha256': sha(binary), 'iterations': args.iterations,
            'benchmark': args.benchmark, 'status': 'incomplete', 'runs': []}
manifest['git_status'] = subprocess.check_output(
    ['git', 'status', '--porcelain'], cwd=root, text=True)
env = os.environ.copy()
env.update(GOMAXPROCS='1', GOMEMLIMIT='256MiB')
manifest['environment_sha256'] = hashlib.sha256(json.dumps(sorted(env.items())).encode()).hexdigest()
(args.output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
name = 'BenchmarkCompileServerNative' + args.benchmark
for pair in range(args.pairs):
    order = ['shards', 'completed'] if pair % 2 == 0 else ['completed', 'shards']
    for arm in order:
        inventory = subprocess.check_output(['ps', '-axo', 'pid,comm'], text=True)
        conflicts = [line for line in inventory.splitlines() if re.search(
            r'(?:^|/|\s)(?:elisac[^/ ]*|z3|clang(?:\+\+)?|go|src\.test)$', line)]
        if conflicts:
            raise SystemExit('benchmark window occupied: ' + str(conflicts))
        command = [str(binary), '-test.run=^$', '-test.bench=^' + name + '/' + arm + '$',
                   '-test.benchtime=' + str(args.iterations) + 'x', '-test.count=1', '-test.v']
        run = subprocess.run(command, cwd=root / 'compiler', env=env, text=True,
                             stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        (args.output / f'{pair + 1}-{arm}.txt').write_text(run.stdout)
        if run.returncode or 'PASS' not in run.stdout or 'SKIP' in run.stdout:
            raise SystemExit('failed or unsupported arm:\n' + run.stdout)
        row = next((line.split() for line in run.stdout.splitlines()
                    if line.startswith(name + '/' + arm) and 'ns/op' in line), None)
        if row is None or int(row[1]) != args.iterations:
            raise SystemExit('missing fixed-count benchmark row:\n' + run.stdout)
        metrics = {row[i + 1]: float(row[i]) for i in range(2, len(row), 2)}
        manifest['runs'].append({'pair': pair + 1, 'arm': arm, 'metrics': metrics})
        (args.output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        print(arm, metrics, flush=True)
if sha(binary) != manifest['binary_sha256'] or source_snapshot() != manifest['source_sha256'] or subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() != manifest['head']:
    raise SystemExit('binary/source changed during measurement')
manifest['status'] = 'complete'
manifest['medians'] = {arm: {metric: statistics.median(r['metrics'][metric]
    for r in manifest['runs'] if r['arm'] == arm)
    for metric in manifest['runs'][0]['metrics']} for arm in ['shards', 'completed']}
(args.output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(manifest['medians'], indent=2))
