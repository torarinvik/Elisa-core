# Current semantic edit profile

The integer edit benchmark now includes fresh and retained analysis modes on
identical freshly parsed ASTs. Parsing, prewarm and parity checks stay outside
benchmark timing. CPU labels `phase=int-body-edit-analysis` and
`analysis-mode=fresh|retained` isolate analysis stack samples from setup. Runtime
and GC system-stack samples can inherit labels; do not interpret all filtered
CPU as pure useful semantic work.

Every retained iteration asserts exactly one checked body and 255 reused bodies;
preflight compares cold/warm semantic results. This prevents an exact-repeat or
non-admitted fixture from silently standing in for a small edited analysis.

One current-state 1s/count1 profile on main b9118503 plus this benchmark change:

| Mode | Time/edit | Bytes/edit | Allocations/edit |
|---|---:|---:|---:|
| Fresh | 46.358 ms | 42,560,970 | 356,632 |
| Retained | 43.290 ms | 39,547,075 | 210,444 |

This single profile is diagnostic, not repeated comparative acceptance evidence,
not a new runtime optimization, and not full Stage1 latency. Typing reuse avoids
many allocations while repeated common passes dominate much of the remaining
latency. Profile any next atom before changing it.

Validation passed in 19.991s wall / 5.849s package, no competing compiler observed,
source hashes match before/after, both shared locks held. Frozen source map
`c8bbc64bd81c98b3f0516f2f586313d4275565ae9374a33c85f16a92c82f88de`.
Sources, CPU profile and receipts: `/private/tmp/elisa-semantic-labeled-main-profile-20261019/`.
