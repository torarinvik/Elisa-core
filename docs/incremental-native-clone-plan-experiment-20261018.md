# Native clone-plan experiment: not enabled

A request-local C++ plan was prototyped against Core d1afc6f0. It hoisted the
unsupported-IR safety scan and the GlobalValue-to-owner DenseMap out of each
shard clone, holding them only while one optimized module remained immutable.
The plan owned its stable owner array and was disposed before the module/context.
Serial fallback and per-shard restriction, pruning and verification remained.

This differs from the earlier discarded admission experiment in also reusing
the DenseMap, rather than only sharing the safety scan and owner array.

The extended boundary fixture passed (5.597 s), covering ctor/dtor registration,
aliases, ifuncs, module/inline assembly, address-taken blocks, invalid parts/jobs,
multiple clones and source-module immutability. Planned raw LLVM IR was compared
to a frozen independent copy of the pre-plan implementation. Full native
execution and race qualification were not pursued after the performance result.

Three alternating baseline/candidate pairs used separate test binaries and five
requests per sample. Parsing/semantic analysis were untimed. The workload has
128 stable function names at O0, with either unchanged source or a novel work0
body on every request. Each run asserts 8 reused/0 emitted shards for unchanged
requests and 7 reused/1 emitted shard for edits.

| Median ms/request | Unchanged baseline | Unchanged plan | Body edit baseline | Body edit plan |
|---|---:|---:|---:|---:|
| Total native pipeline | 117.08 | 140.61 | 157.85 | 136.75 |
| Clone/restrict/prune/verify | 27.76 | 33.02 | 29.29 | 26.42 |
| Exact bitcode key work | 13.99 | 17.27 | 14.63 | 12.98 |
| Total partition preparation | 41.76 | 50.50 | 43.94 | 39.58 |
| New plan creation | 0 | 0.21 | 0 | 0.19 |

Timing swung sharply between pairs and unrelated phases changed too. The plan
has no demonstrated consistent throughput gain. No runtime changes were kept.
The new generic `BenchmarkIncrementalNativeRequests` preserves the controlled
no-op/edit workload and phase/locality checks for subsequent experiments.

The prototype separately measured complete plan preparation and owner hashing
(the latter is a subset, not an additional cost to sum). Existing OwnerMapTime
and OwnerMapBuilds semantics were preserved, including rejected plans after a
successful owner build. Clone and bitcode costs still happen on cache hits.

Raw runs, medians, source hashes, patch, prototype files and frozen clone oracle
are preserved in `build/verification/incremental-native-clone-plan-20261018/`.
The production baseline remains unchanged. Next work should profile or reduce
per-shard cloning and bitcode serialization, or avoid rebuilding full current IR;
these measurements do not support another guard-hoisting cache layer.
