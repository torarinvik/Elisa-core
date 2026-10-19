# Return-element traversal atom

Baseline `182e100d`. Return-element discovery now uses cached exported composite
field indices, including maps. It preserves pointer cycles, map-value traversal,
return callback order and lambda/function boundaries. Indices live in existing
immutable reflection metadata; this adds a small per-type retained index slice.
Metadata creation is outside these warm traversal measurements, so no cold-start
gain is claimed. Other reflection metadata callers retain their original fields.

Same-process ABBA leaves, 3 repetitions each (6 observations per implementation),
100ms, GOMAXPROCS=4, GOMEMLIMIT=2GiB:

| Shape | Original µs | Candidate µs | Reduction |
|---|---:|---:|---:|
| body/depth16 | 4.686 | 2.583 | 44.9% |
| map-cycle/depth16 | 4.428 | 2.530 | 42.9% |
| body/depth128 | 33.228 | 20.529 | 38.2% |
| map-cycle/depth128 | 35.355 | 22.828 | 35.4% |

Allocations match exactly in all cases. Fixtures check original/current return
callbacks with actual returns, a map field, a pointer cycle and excluded lambda
returns. Existing return-element, reflection and syntax-plan tests pass, including
focused race. Pair wall22.52s; race23.13s including compilation. Both shared locks
held and no competing compiler observed. All source hashes checked before and
after each command. No whole-analysis or Stage1 speedup is claimed.

Frozen sources, baseline walker, ABBA fixture, logs, manifests and post-command
hash receipts: `/private/tmp/elisa-semantic-return-atom-20261019/`.
Source-map `e149d8ca11231c8da56f2b709ef39184bf36ff1efef73098435bdae1952a9737`.
