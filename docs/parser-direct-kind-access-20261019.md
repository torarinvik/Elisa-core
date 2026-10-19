# Direct token-kind access in the parser

Baseline: `4b667beea789ce84d6405a3db642c44341bc9a3b`.

`peek` now reads the current token Kind directly rather than returning a full
Token through `cur` and then extracting Kind. The EOF check is unchanged.
Cursor initialization/movement still records incremental syntax observations;
offset probes retain their own observation calls. The static token-access guard
includes the new accessor in its centralized file.

The frozen same-process comparison used A-B-B-A leaves, three observations per
leaf (six per implementation), 200ms benchmarks, GOMAXPROCS=4 and GOMEMLIMIT=2GiB.
Both shared locks were held and a process monitor found no competing compiler.

| Functions | Body | Baseline ms | Candidate ms | Time reduction |
|---|---|---:|---:|---:|
| 256 | scalar | 0.500 | 0.365 | 27.1% |
| 256 | container | 0.993 | 0.697 | 29.8% |
| 256 | exprblock | 0.727 | 0.536 | 26.2% |
| 256 | forwarding | 0.790 | 0.486 | 38.5% |
| 2048 | scalar | 3.913 | 2.892 | 26.1% |
| 2048 | container | 7.845 | 5.565 | 29.1% |
| 2048 | exprblock | 5.848 | 4.161 | 28.8% |
| 2048 | forwarding | 6.758 | 4.036 | 40.3% |

Allocation was essentially unchanged; minor container/forwarding differences
reflect parser map allocation variability. Full parser tests, benchmark AST and
notice parity, incremental trace tests, EOF boundary parity, and the full parser
race suite passed. Pair wall time: 33.08s; race wall time: 14.74s.

These are parser-only results on synthetic 256/2048-function workloads, not a
measured full Stage1 compilation improvement. Desktop/background effects remain
possible despite compiler process isolation.

Artifacts: `/private/tmp/elisa-parser-peek-kind-20261019/` contains
`clean-qualified-manifest.json`, `runs-clean.json`, `paired-clean.log`,
`race-clean.log`, and `medians.json`, with exact candidate hashes.
