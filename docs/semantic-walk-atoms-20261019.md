# Semantic AST-walk atom qualification

Baseline: `576ce3b814493cfb8a645b82d53e011c17775ad0`.

The current retained integer-body edit profile checked one body and reused 255
at 49.994 ms/edit, 39.53 MB and 210,441 allocations. This is a single synthetic
analysis-only profile, not a full Stage1 rebuild or a before/after result.
Discarded-loop and synthesized-region discovery walkers each accounted for
70ms inclusive sampled CPU. Runtime/GC samples also inherited phase labels;
aggregate profile percentages must not be described as pure semantic work.

Both walkers now use existing immutable `syntaxWalkFields` plans for struct
fields. They still visit pointers, interfaces and composite containers, preserve
declaration order, shared-node/cycle seen sets, lowered links, expression-block
handling and all diagnostic rules. Skipped scalar/position fields cannot contain
a callback. No semantic products or AST nodes are newly cached.

Frozen same-process original/candidate ABBA leaves, three repetitions per leaf
(six observations per implementation), 100ms, GOMAXPROCS=4, GOMEMLIMIT=2GiB:

| Workload | Original µs | Candidate µs | Time reduction |
|---|---:|---:|---:|
| scalar/depth16/discard | 4.670 | 3.580 | 23.3% |
| scalar/depth16/regions | 3.991 | 2.766 | 30.7% |
| shared-block/depth16/discard | 7.687 | 5.676 | 26.2% |
| shared-block/depth16/regions | 6.304 | 4.005 | 36.5% |
| scalar/depth128/discard | 32.052 | 23.348 | 27.2% |
| scalar/depth128/regions | 31.738 | 22.095 | 30.4% |
| shared-block/depth128/discard | 51.529 | 36.563 | 29.0% |
| shared-block/depth128/regions | 45.614 | 31.329 | 31.3% |

Bytes and allocation counts match exactly in all eight cases. Shared-block
fixtures assert one real discarded-loop diagnostic and one generated region;
original/candidate diagnostic lists and region order/pointer identities match.
Existing discarded-loop rejection/acceptance, shared resolved-argument DAG,
region and syntax-field-plan regressions pass, including the focused race run.
Pair wall time 29.39s; race wall time 24.12s including first-use race compilation
(the race package itself took 1.470s). Both shared locks were held; continuous
compiler process monitoring observed no competitor.

No whole-analysis or whole-compiler speedup is claimed from these atom results.
The owner-pointer inventory and incremental admission rules remain unchanged.

Exact frozen sources, original walker copies, parity/ABBA fixture, manifests,
logs and medians are in `/private/tmp/elisa-semantic-walk-atoms-20261019/`.
Source-map SHA-256:
`2065e7c037505b971f5d49a762ae3f84c8f0fa2fe138356d828aef9a72e9f49f`.
