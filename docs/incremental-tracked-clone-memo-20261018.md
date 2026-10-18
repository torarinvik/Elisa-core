# Operation-local tracked-type clone memo

Tracked type cloning now holds up to eight type-to-clone bindings in inline
storage. A larger graph spills once to the ordinary identity map, copying every
in-progress binding before proceeding. The memo belongs to one clone operation
or one root-map clone; it is never retained in an Analyzer or incremental session.
Explicit caller-provided maps remain authoritative and are populated as before.

The default root clone also directly returns nil and immutable identity leaves.
CStrType, SViewType and mutable composite wrappers still clone. Recursive calls
retain the original depth checks, invalid-type propagation, sharing and cycle
behavior. Root-map clones defer the old 4*root-count map capacity hint until a
spill, avoiding an oversized map for scalar roots or shared small graphs.

## Qualification

The complete semantic package suite passed (52.248 s). New tests cover cycles
before/at/after the eight-entry boundary, 32-node cyclic graphs, source/clone
mutation isolation and explicit-map replacements. Existing recursive sharing and
depth-limit tests remain applicable. Final race and native-output checks are
recorded with the evidence before integration.

Three 200 ms samples on one Apple M5 CPU compare matched source overlays with
the candidate. Host timing variance was high; these are component measurements.
A separate simple-leaf-only overlay isolates that smaller optimization.

| Shape | Original bytes / allocations | Candidate bytes / allocations |
|---|---:|---:|
| 64 scalar roots | 22016 / 7 | 3544 / 4 |
| 64 roots sharing an optional | 22032 / 8 | 3560 / 5 |
| 64 unique optional roots | 23040 / 71 | 23088 / 72 |
| Struct with 7 optional fields | 1312 / 10 | 1312 / 10 |
| Struct with 64 optional fields | 20128 / 78 | 19568 / 77 |

The unique-root spill case adds one allocation and 48 bytes. The scalar/shared
root cases save 18472 bytes and three allocations. Candidate optional cloning
measured 35.7–43.2 ns versus 99.8–103 ns originally. A seven-field optional struct
measured 0.99–1.22 us versus 2.12–2.73 us with only the simple leaf fast path.
Do not generalize these component timings to whole-compilation latency.

## Frozen project edits

The 2635701-byte frozen project snapshot has SHA-256
86e7b5e6520848f6083217b40ac7ede1b09336dfe232f64c31d544e8bda94bd1.
The retained benchmark appends a novel scalar-body edit, excludes parsing and
prewarm equally, and disables SMT. It does not reconstruct all CLI loader options.

| Pair | Original bytes / allocations | Candidate bytes / allocations | Original / candidate elapsed |
|---|---:|---:|---:|
| 1 | 4416157184 / 21405351 | 4409218096 / 21413292 | 15.62 / 9.01 s |
| 2 | 4412735000 / 21403325 | 4408590248 / 21419684 | 8.84 / 16.39 s |

Both pairs allocate fewer bytes (about 6.62 and 3.95 MiB), with more allocations
(7941 and 16359). This is a modest roughly 0.1–0.16% reduction on this workload.
The timing reverses between pairs, so there is no whole-project speedup claim.

All runs preserve 72 body reuses, 1886 body checks, 2837 reused expression nodes,
999 errors and 2298 total diagnostic entries. The complete sorted diagnostic
multiset SHA-256 remains
429bdc001b9c7b0f4dda795e0228231c363baac74555e31a8e6a9f57004cefa6.
This proves parity on this failing frozen input, not compiler error-freedom.

Final targeted race checks passed: semantic clone/sharing/cycle/depth tests in
2.995 s, and compile-server backend parity plus native/concurrent native object
checks in 7.832 s. Native tests build and execute linked objects with clang.
