# Avoid cycle-map work for terminal capability types

The retained real-project profile on Core cb488ca0 identified type graph work as
an allocation hotspot. Whole-process allocation samples (prewarm, parsing and
edited analysis combined) attributed about 8% to cycle-map growth in
Analyzer.typeContainsMutableBorrowCapability, with tracked-type cloning larger.
Those samples do not attribute all those bytes to the timed edit.

Terminal types cannot lead back to an ancestor. The capability traversal now
returns their existing result without inserting them into the cycle map. False
terminal leaves also avoid a map lookup. Positive opaque type parameters and
store-row proxies retain the previous helper result for caller-supplied seen
entries; typed-nil proxies stay false. The existing depth guard remains before
all fast returns. Compound types retain the existing visited-map traversal and
cycle behavior. No query identity, persistent input, cache admission or effect
policy is changed.

The implementation was suggested by the coordinator, then adjusted and qualified
in the isolated Core worktree on branch
codex/incremental-capability-terminals-20261018, based on cb488ca0. Shared pending
alias-inventory edits were excluded from this source snapshot.

## Verification and measurements

The focused semantic race selection for mutable borrows, capability queries,
global effects and permission grants passed in 2.725 s. New cases cover terminal
results, typed nils, the diagnostic depth boundary, preseeded seen maps and
recursive aggregates both without and with a writable borrow. Existing query
projection, retained-history and global-grant checks are included.

Three 200 ms samples per mode, Go 1.27.2, Apple M5 Darwin arm64, one CPU; baseline
uses an overlay restoring only the original global_permissions.go:

| Traversal | Baseline median | Terminal fast path | Baseline bytes/allocs | Fast bytes/allocs |
| --- | ---: | ---: | ---: | ---: |
| Scalar | 132.0 ns | 19.66 ns | 0 / 0 | 0 / 0 |
| Seven optional scalar fields | 1,380 ns | 285.4 ns | 456 / 3 | 0 / 0 |
| 64 optional scalar fields | 10,846 ns | 6,610 ns | 6,696 / 9 | 6,696 / 9 |

Both modes construct fresh traversal maps per operation. The seven-field case
avoids crossing a map growth threshold when scalar leaves stop taking slots.
The wide graph still needs the same compound-node map storage, but does fewer
leaf probes. This is a capability-traversal component result, not an incremental
compiler or native-artifact speedup.

A frozen real-project retained-analysis check (one novel appended scalar-body
edit; parsing excluded, SMT disabled, identical profiling flags) reported:

| Metric | cb488ca0 baseline | Fast path |
| --- | ---: | ---: |
| Allocated bytes per edited analysis | 4,417,211,392 | 4,414,444,832 |
| Allocation calls | 21,429,067 | 21,413,054 |
| Reused / checked bodies | 72 / 1,886 | 72 / 1,886 |
| Reused expression nodes | 2,837 | 2,837 |

This is 2,766,560 fewer bytes (about 2.64 MiB) and 16,013 fewer allocations in the
single measured edit, a small whole-analysis allocation reduction. The fixture
is errorful: both modes report 999 errors and 2,298 total diagnostics. The complete
sorted diagnostic multiset is unchanged:
429bdc001b9c7b0f4dda795e0228231c363baac74555e31a8e6a9f57004cefa6.
The source snapshot SHA-256 is
86e7b5e6520848f6083217b40ac7ede1b09336dfe232f64c31d544e8bda94bd1.
This benchmark does not reconstruct every CLI/loader option and is not a clean
compiler build or native-output qualification.

Elapsed samples varied from 36.9 s in baseline to 7.35 s with the change while
host load changed. No whole-analysis speedup ratio is inferred from those single
samples. Most type-clone and nonterminal traversal allocations remain.

Raw race/component/project logs, matching profile binaries and CPU/memory
profiles, source copies/hashes and the exact commands are retained locally under
build/verification/incremental-capability-terminals-20261018 in the shared Core
checkout. CPU phase labels isolate edited analysis from prewarm/parsing; memory
profiles include the whole process and must not be interpreted as edited-only.
