# SMT proof fixes on the committed incremental Core baseline

This isolated source integration combines proof parent `d0ebcd10aa1d1ef78255c4c2ba88eaaee3c03eda` with Core main `caa0279527973de0dfb1c996d2683c06739eb9d8`. Git merged the discharge changes without conflicts. The original proof branch remains at its tested commit; shared Core/source and installed binaries were untouched.

The combined tree passes 81 selected top-level tests with 0 skips: array/view/frame and quantified proof controls, complete annotation and associated lookup proofs, char conversion/XOR controls, persistent/in-memory cache identity and no-solver-start checks, raw-SAT counterexample preservation, and ordered incremental dependency invalidation. Package times were 16.711 seconds for semantic tests and 0.217 seconds for incremental tests. These are test timings, not compiler rebuild benchmarks.

Raw results and compiler-source hashes are retained under `build/verification/smt-main-integration-20261018/`. No standalone Stage0 binary or Stage1 product was built. Full compiler source qualification, independent proof-product replay and actual edit/rebuild timing remain pending the build coordinator's gated run.
