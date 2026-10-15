#!/usr/bin/env python3
"""rlrank.py <rlimit.log> [--top N] [--budget 500000]

Ranks ELISA_SMT_RLIMIT_LOG lines ("delta elapsed_us verdict\\tfile:line | func | kind | goal")
by rlimit, and groups them by function, by file, by kind and by normalized goal shape.
Unlabeled lines (older binaries) are grouped under "?".
"""
import re, sys, collections

args = sys.argv[1:]
path = args[0]
top = int(args[args.index('--top') + 1]) if '--top' in args else 25
budget = int(args[args.index('--budget') + 1]) if '--budget' in args else 500000

def shape(goal):
    if goal.startswith('(assert (not '):
        goal = goal[len('(assert (not '):]            # dead-path labels carry the goal assert
    g = re.sub(r'\|[^|]*(\||$)', 'S', goal)         # quoted symbols (sview_length_…), maybe truncated
    g = re.sub(r'\b(aux|bpred|svarg\$|v_svarg\$)\w*', 'A', g)
    g = re.sub(r'\bv_\w+', 'V', g)
    g = re.sub(r'\b[bq]_\w+', 'V', g)
    g = re.sub(r'-?\b\d+\b', 'N', g)
    g = re.sub(r'\s+', ' ', g)
    return g.rstrip(') .')[:120]                   # truncation leaves unbalanced closers

rows = []
for line in open(path, errors='replace'):
    head, _, label = line.rstrip('\n').partition('\t')
    parts = head.split()
    if len(parts) < 3:
        continue
    delta, us, verdict = int(parts[0]), int(parts[1]), parts[2]
    where = func = kind = goal = '?'
    if label:
        f = [x.strip() for x in label.split(' | ', 3)]
        where, func, kind, goal = (f + ['?'] * 4)[:4]
    rows.append((delta, us, verdict, where, func, kind, goal))

total = sum(r[0] for r in rows)
total_us = sum(r[1] for r in rows)
at = [r for r in rows if r[0] >= budget]
print(f'queries={len(rows)} rlimit={total} solver_ms={total_us/1000:.0f} at_budget={len(at)} '
      f'({sum(r[0] for r in at)} rlimit, {100*sum(r[0] for r in at)/max(total,1):.1f}%)')
print('verdicts:', dict(collections.Counter(r[2] for r in rows)))

def group(title, key):
    agg = collections.defaultdict(lambda: [0, 0, 0, 0])
    for r in rows:
        a = agg[key(r)]
        a[0] += r[0]; a[1] += r[1]; a[2] += 1; a[3] += r[0] >= budget
    print(f'\n== by {title} (top {top} by rlimit): rlimit  ms  queries  at_budget  key')
    for k, (s, us, n, b) in sorted(agg.items(), key=lambda kv: -kv[1][0])[:top]:
        print(f'{s:>11} {us/1000:>8.0f} {n:>6} {b:>4}  {str(k)[:160]}')

group('function', lambda r: (r[3].rsplit('/', 1)[-1], r[4]))
group('file', lambda r: r[3].rsplit('/', 1)[-1].split(':')[0])
def feats(kind):
    m = re.search(r'\[([^\]]*)\]', kind)
    return m.group(1) if m else '?'

group('kind/verdict', lambda r: (r[5].split(' [')[0], r[2]))
group('theory features/verdict', lambda r: (feats(r[5]), r[2]))
# Budget hits only: which theories, and were they ever going to prove? (unknown = no verdict)
saved = rows
rows = [r for r in saved if r[0] >= budget]
group('AT-BUDGET theory features', lambda r: feats(r[5]))
rows = saved
# Proofs by cost: how much rlimit do real proofs need? (decides whether a staged budget is safe)
unsat = sorted(r[0] for r in rows if r[2] == 'unsat')
if unsat:
    q = lambda p: unsat[min(len(unsat) - 1, int(p * len(unsat)))]
    print(f'\n== rlimit of PROVEN queries: n={len(unsat)} p50={q(.5)} p90={q(.9)} p99={q(.99)} max={unsat[-1]}')
    for cut in (20000, 50000, 100000, 200000):
        print(f'   proofs needing > {cut}: {sum(1 for x in unsat if x > cut)}')
group('goal shape', lambda r: shape(r[6]))

print(f'\n== top {top} single queries')
for r in sorted(rows, key=lambda r: -r[0])[:top]:
    print(f'{r[0]:>8} {r[1]/1000:>7.0f}ms {r[2]:<7} {r[3].rsplit("/", 1)[-1]} {r[4]} [{r[5]}] {r[6][:140]}')
