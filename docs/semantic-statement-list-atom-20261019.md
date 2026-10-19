# Nested statement-list discovery atom

Baseline `9853794a8c0e72eb562aed71441deb025dbb15f0`.

`descendStmtLists` uses existing immutable `syntaxWalkFields` plans to avoid
scalar and source-position fields before its conservative statement-list shape
check. Pointer/interface/composite field order, pointer seen sets, recursive
shape handling, and nested-list reserve diagnostics are unchanged. No ownership
or semantic-query admission rule changed.

Same-process original and candidate recursive walkers, ABBA leaves, three
repetitions per leaf (six samples per implementation), 100ms benchmarks,
GOMAXPROCS=4 and GOMEMLIMIT=2GiB:

| Shape | Original µs | Candidate µs | Time reduction |
|---|---:|---:|---:|
| scalar/depth16 | 3.050 | 2.807 | 8.0% |
| lambda/depth16 | 4.370 | 3.688 | 15.6% |
| scalar/depth128 | 21.256 | 18.742 | 11.8% |
| lambda/depth128 | 23.538 | 20.050 | 14.8% |

Bytes/op and allocations/op match exactly in all four cases. Fixtures compare
complete diagnostics, pending lint state and reserve suppression. The lambda
fixture asserts that the nested loop's explicit reserve was actually found.
Existing statement-list, syntax-plan, reserve and iterator-fill regressions are
included in the focused normal/race commands. Runtime changes are one field-loop
replacement; copied baseline walkers stay in the frozen experiment artifact.

This is a component result, not measured whole-analysis or full Stage1 latency.
Artifact directory `/private/tmp/elisa-semantic-stmt-atom-20261019/` contains
sources, manifests, runs, pair/race logs, medians and per-command source hashes.
The runner verifies every recorded source file at startup and again after each
command before issuing its terminal receipt. Both shared locks and continuous
compiler process monitoring isolate the measurement.

Frozen source-map SHA-256:
`56b47ab8e9c4a0911cbc75ba6de5bfbee296e7e55f66938e9463736f534ed85a`.

Both commands passed: pair wall 23.74s, focused race wall 26.12s including compilation. No competing compiler was observed.
