# Mutable-borrow traversal visited set

The fallback mutable-borrow capability query now keeps its first eight visited type identities in an operation-local inline set, spilling once to a map for larger graphs. It retains the caller-map compatibility entry point and preserves nil handling, aggregate-state stripping, depth-limit diagnostics before cycle checks, terminal handling, and generic-instance recursion state. No result is retained across operations or edits.

A historical Stage1 profile attributed 3.86 GB of its 45.18 GB allocated space to visited-map insertion; the relevant semantic source files exactly match the profiling snapshot. This identified the atom; it does not establish current whole-build savings.

Validation: 82 focused Global/capability tests pass, including cycles across the spill boundary, mutation between calls, seeded true/false caller-map entries and updates, and a shared tail reached beyond the depth limit. Sources match frozen candidate v4. Two matched ABBA benchmark series use prebuilt binaries, six observations per arm/case, GOMAXPROCS=2 and GOMEMLIMIT=1GiB on darwin/arm64.

| Aggregate fields | Baseline ns/op | Candidate ns/op | Change |
|---|---:|---:|---:|
| 1 | 64.445 | 35.980 | -44.2% |
| 7 | 174.100 | 98.295 | -43.5% |
| 8 | 324.550 | 286.100 | -11.8% |
| 9 | 380.600 | 338.600 | -11.0% |
| 32 | 1706.000 | 1429.500 | -16.2% |
| 64 | 3466.000 | 3219.000 | -7.1% |

Scalar/type-parameter/mutable-reference/immutable-reference checks improve 30.2%/52.2%/64.8%/62.5%, with zero heap allocations in both arms. The 8/9-field spill cases trade 528 more bytes and one allocation for faster execution; 32/64-field cases save 400 bytes and one allocation. These are traversal-level results, not incremental compiler or Stage1 rebuild timings. Deep/generic shapes remain functional test coverage, not separately timed claims.

Evidence directory: `/private/tmp/elisa-borrow-visitset-20261019`; functional receipt `candidate-v4-receipt.json`, timing receipts and summaries in `timing-01/` and `timing-terminal-01/`. The earlier CGO-disabled attempt failed setup because existing package helpers require CGO; no tests ran in that attempt. No new full Stage1 gate was run for this atom.
