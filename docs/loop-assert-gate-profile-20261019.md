# Loop assertion scope-map reuse

The qualified ea4e Stage0 checking frozen Stage1 source `358ee26e7a354ddb192d4e37a26664c1a0a6a2fb48c2937f0fd0169e7faabf73` took 212.273 seconds with 69 diagnostics and no object. Diagnostic identity matched the unprofiled baseline. Profiles are in `/private/tmp/elisa-stage1-profile-ea4e-20261019-r1`.

The CPU profile sampled 328.26 core-seconds, including 182.82 seconds cumulative in gcDrain. Allocation sampling estimated 50.36 GiB total allocation, including 5.88 GiB flat in loopAssertOperatorGate. Solver check time summed to 69.583 seconds over 4,420 checks. Allocation totals are not peak memory; cumulative CPU entries overlap.

The condition walker rebuilt every visible scope binding at every recursive expression node. It now builds one gate at the root and passes it through recursion. The condition walk does not change the scope or the gate type map; nearest-scope shadowing and conservative unknown-operator rejection remain intact. No cross-condition cache is introduced.

Focused existing sequential-loop assertion/frame tests and the new unresolved inner-shadow control pass in an isolated HEAD export plus this change. Benchmark: 1,001 visible bindings, logical tree of 65 nodes and arithmetic/comparison tree of 35 nodes, three repetitions, Go4 workers/2 GiB soft heap limit:

| Case | Original bytes/op | Reused gate bytes/op | Original allocs/op | Reused gate allocs/op |
|---|---:|---:|---:|---:|
| Logical | 10,399,512 | 159,992 | 1,300 | 20 |
| Arithmetic/comparison | about 5,599,740 | 159,992 | 700 | 20 |

Candidate timings were 67–73 microseconds/op; baseline timings were 3.0–6.9 milliseconds/op. A separate Stage1 process started during the baseline command, so these elapsed comparisons are exploratory and must not be presented as isolated speedup evidence. The allocation-count reduction is independent of scheduling. Full paired Stage1 wall-time/diagnostic validation remains required. Benchmark/test logs: `/private/tmp/elisa-core-loop-gate-candidate-20261019/tests-{candidate,baseline}.log`.

## Full frozen-source validation

The clean committed `46b36a092484a906e9e19ae77afeb1014334532a` build (SHA256 `3bf03a3b901060890935e4c664adfa96a4ff8b71cb1b8884315a23b95de37f1e`) completed the same failed Stage1 check in 191.355 seconds, versus 212.273 seconds with ea4e: 20.918 seconds / 9.85% less wall time in this matched pair. All 69 diagnostic records match exactly, not merely their count. Source and binary were unchanged across the run. Both runs produce no object or executable. This is a failing-check speed comparison, not a successful compiler rebuild time.

Both runs used CPU/allocation/solver profiling, four Go workers and a 2 GiB soft Go memory limit. Allocation estimates fell from 50.36 GiB to 45.18 GiB (about 5.18 GiB less). Sampled CPU fell from 328.26 to 281.56 core-seconds; gcDrain cumulative from 182.82 to 152.29 core-seconds. These are one-run measurements with sampling/scheduling variation, not a distribution or a guarantee for current evolving source.

Candidate artifacts: `/private/tmp/elisa-stage1-profile-loop-gate-46b36a09-20261019-r1`, including gate-result.json, diagnostic-equivalence.json, cpu.pprof and allocs.pprof. A separate 6 GiB heap experiment uses this same binary/source; do not attribute its results to the code change alone.

## Current main requalification

On 2026-10-19, the exact runtime and regression-test bytes from `46b36a09` were applied to clean main `c2dd0810` in an isolated source export. All 30 focused loop-invariant, sequential-model, loop-assert gate and SMT view-frame tests passed, followed by the same 30 tests under the Go race detector. The complete candidate source map matched before and after each command. Artifacts: `/private/tmp/elisa-loop-gate-current-main-20261019/{qualified-receipt.json,sources-before.json,focused.log,race.log}`.

These checks establish focused correctness on the current main base. They do not establish a current full-check latency or a successful Stage1 build. The historical matched pair above remains the elapsed-speed evidence; aborted closed-scalar replay measurements concern a separate candidate and are excluded.
