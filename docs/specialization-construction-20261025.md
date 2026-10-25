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

## Typed expression-type map copy

The specialization constructor uses maps.Clone for exprTypes. Existing
deepCloneValue treats interface values as opaque, so this keeps exactly the
same value aliases (including nil and typed nil) and a separate writable outer
map while avoiding reflective copying of every entry. Ordinary body workers
retain their previous policy. This still copies the whole map; it is not the
final solution to program-size-dependent construction.

Reference-policy/alias/nil controls plus private generic, snapshot and parallel
semantic controls pass under race in 1.859 seconds. Generic bindings, private
context restoration, retained generic disk IR/ABI and linked runtime controls
pass under race in 21.470 seconds after a separately recorded compilation-time
external timeout. Logs `/tmp/elisa-specialization-typed-expr-clone-race.log`,
`/tmp/elisa-specialization-clone-cuts-backend-race.log` and its `-retry.log`.

The isolated 65,536-row map-copy benchmark drops from 3.141 ms to 0.485 ms at
30 iterations with equivalent allocation (about 5.26 MB); 8,192 rows drop from
0.391 ms to 0.098 ms. These are map-copy microbenchmarks. The 50-iteration real
eight-product/512-unrelated-function ABBA results overlap: baseline 6.20–6.61 ms,
candidate 5.63–6.73 ms. No whole-compiler latency improvement is inferred.
Evidence `/tmp/elisa-specialization-typed-expr-clone-benchmark.log` and
`/tmp/elisa-expr-types-typed-construction-benchmark-v1`. Fresh Stage1 measurement
remains required.

## Generated typed copies under the existing opaque-value policy

The specialization constructor now has generated typed map copies immediately
before its reflective fallback. Existing memo/rebuild, declaration sharing and
owned-row policies keep precedence. The helper uses exactly the existing
!typeHoldsMapInline(elementType) rule: opaque pointers/interfaces/scalar structs
retain their previous aliases, while inline maps/slices and unknown fields keep
reflective cloning. No fact rows or checks are omitted. The generator covers
279 direct Analyzer maps, excludes the separately owned expression-type and
function-analysis policies, and has deterministic schema/source controls.

Actual generation and generator controls pass (0.182 seconds); private generic,
canonical snapshot/parallel and all Retained semantic controls pass under race
(7.453 seconds). Generic bindings/context and retained disk IR/ABI/linked-runtime
backend controls pass on the bounded retry after a separately preserved initial
compilation-time external timeout. Logs `/tmp/elisa-specialization-opaque-map-*.log`.

The isolated policy-branch ABBA benchmark, eight real products with 512 unrelated
functions at 50 iterations, takes baseline 5.615/5.607 ms and candidate 4.478/4.412
ms: approximately 20% less construction time in this fixture. Allocation rises
from about 5.906 MB to 6.012 MB per operation (about 1.8%); this is a measured
tradeoff, not an allocation improvement. Full logs and exact comparison overlay
are in `/tmp/elisa-opaque-map-construction-benchmark-v1`. Stage1 speed is not
inferred from this fixture.

The preceding main045280 producer still fails the real fresh baseline's final
30-second process. Its 22-process admission totals 312.698 seconds and saves
complete analysis but no object. A same-identity recovery also times out; its
24-second diagnostic profile samples 14.37 GB allocation, 10.12 CPU seconds in
analyzer cloning, and 3.23 GB under publication.walk. The latter includes
canonical membership discovery shared once per codegen session, not necessarily
per-product publication. Evidence `/tmp/elisa-stage1-clone-cuts-root`. This is
why the larger overlay and validation traversal work remains necessary.
