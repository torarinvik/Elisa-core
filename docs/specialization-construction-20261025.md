# Specialization construction measurements

## Owned function-analysis rows

Specialization construction now starts with an empty writable functionAnalyses
map. The typed owned-key import copies each needed function contribution before
concrete analysis; public foreign queries retain canonical Result fallback.
Ordinary body-worker clone behavior is unchanged. The skip happens before the
reflective clone, avoiding a full-program outer-map traversal per specialization.
Existing clone semantics preserve pointer values: no whole-CFG deep-copy removal
is claimed. Other full-program construction maps still need work.

Private/nested generic queries, canonical snapshot/parallel controls and the
unrelated-row allocation/isolation controls pass under race in 1.699 seconds:
`/tmp/elisa-specialization-sparse-function-analyses-full-race.log`. All commands
have external 30-second and internal 25-second bounds.

An ABBA construction benchmark uses eight real products and 512 unrelated
functions, bypasses memo hits, and compares only this policy branch through a
Go source overlay. At 20 iterations, baseline allocations are 6,053,814 and
6,053,454 bytes/op; candidate allocations are 5,906,662 and 5,906,796 bytes/op.
This is approximately 147 KB less allocation per eight-product construction.
Elapsed results overlap (baseline 6.33–7.39 ms, candidate 6.14–8.59 ms), so
these samples do not establish a latency improvement. Full evidence and the
exact comparison overlay are in `/tmp/elisa-function-analysis-sparse-benchmark-v1`.
The real Stage1 timeout and all rebuild targets remain open.
