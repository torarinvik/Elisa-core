# Private constant locality in incremental native objects

Editing an address-insignificant private string literal used only by `left` invalidated both stable-owner shards: selective cloning copied the unused constant into `right`'s bitcode input too. After partition restriction, remove directly unused local globally-unnamed constants before stripping dead declarations. Referenced initializer dependencies and metadata roots remain; significant addresses, external ownership, sections, COMDAT, and externally initialized globals are excluded. Debug information and module flags are not stripped.

This is a single conservative sweep. Earlier dead initializer dependencies can remain when later dead users are removed; no complete dead-chain or cycle elimination is claimed. Cache keys still compare exact per-shard bitcode, and native merging still uses complete shard objects.

## Correctness

Frozen base `a78df5249480260615607249e6d2086e64bb38d2` plus identical tests: the new single-consumer literal test fails on the baseline with two emissions and zero hits after an edit. With the change it requires one emission and one hit, and two hits on reverting. It confirms the literal survives LLVM lowering, `left`/`right` have different native owners, and retained/fresh objects produce the expected executable status.

All six focused tests pass: that locality regression, shared private-constant edit/revert, significant-address identity, ordinary body-edit reuse/native parity, global/target invalidation, and C++ partition guards. The C++ fixture also verifies retained initializer closure, metadata-only roots, public and significant-address definitions, custom sections, externally initialized globals, and LLVM verification after pruning. The COMDAT guard is source-reviewed, without a dedicated COMDAT fixture.

Functional receipt: `/private/tmp/elisa-shard-constant-locality-20261019/functional-receipt.json`. Complete baseline/candidate source maps matched after both commands; no competing compiler was observed.

## Component measurement

Identical prebuilt test executables, ABBA order, four Go workers, 2 GiB soft Go heap limit, fixed eight iterations and three repetitions per command: six observations per arm/workload. Parsing and semantic checking are excluded. Novel edits use unique same-width numeric literals; they cannot hit an earlier complete revision. Timed `WriteObject` includes LLVM lowering, cloning, bitcode preparation, native emission and object merging.

| Workload | Baseline median | Candidate median | Change |
|---|---:|---:|---:|
| Novel constant edit | 8.989 ms | 8.485 ms | -5.61% |
| Unchanged input | 7.437 ms | 6.979 ms | -6.16% |

Every edited baseline sample reports two emitted/zero reused partitions; the candidate reports one emitted/one reused. Unchanged samples all report zero emitted/two reused. Unchanged latency ranges overlap; its lower median supports no observed hit-path regression rather than a precise guaranteed speedup.

This is a tiny two-shard O0 fixture with `minWeight=0`, bypassing the normal partition-size threshold. It does not measure project-scale rebuilds, frontend reuse, successful Stage1 compilation, or a machine-code improvement. No paired shard disassembly is retained. Go allocation counters exclude native LLVM allocations.

Attempt 01 produced no benchmark result because outside CPU prevented quiet startup. Attempt 02 passed a two-second quiet startup and all source/binary/runner identity checks, with no competing compiler, sustained external saturation, RSS cancellation, or deadline failure. Brief desktop load was recorded (maximum observed outside-process CPU 62.4%); no such load persisted for the monitor's two-second rejection interval. Observed owned-group RSS peaked at 60.8 MiB; this is sampled, not a peak-memory guarantee.

Attempt 02 artifacts: `/private/tmp/elisa-shard-constant-locality-20261019/timing-attempt-02/{timing-summary.json,timing-receipt.json,*.bench.log}`. Runner SHA-256: `44c7882e4be3f6c6cd632232d28a927dcf18c94820e6768192d2d5d86a2ce9b8`. Baseline test executable SHA-256: `699924c955c23e3c5b44c4a28c39214a6b99ce84d5945c898ee7baf160e7b98b`; candidate: `63010cbe6c46b3eda5f514b75e850dba1051b7796c1bc3967b626b33d9cbaeea`.
