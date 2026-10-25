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

## Interval validation without quadratic pointer scans

Syntax ownership validation still checks every current backing range and pointer,
but uses predecessor binary search within proven-disjoint ranges instead of
comparing every pointer with every range. The earliest overlap bounds the
searchable prefix, preserving exact hidden-tail-versus-overlap diagnostic
precedence. There is no cached validation epoch or skipped discovery.

Existing private/syntax controls and 2,048 deterministic cases compare against
the exact previous quadratic oracle, including error strings. Explicit mixed
invalid, wrong-type, zero-size, end, alias/hidden-tail and newly discovered
metadata controls pass under race in 1.644 seconds. On identical 2,048-slice/
2,048-pointer metadata, the 20-iteration benchmark falls from 41.97 ms to
0.236 ms with identical measured allocation (48,304 bytes, 15 allocations/op).
This is an interval-validation microbenchmark, not a Stage1 rebuild result.
Logs `/tmp/elisa-specialization-syntax-interval-v2-race.log` and
`/tmp/elisa-specialization-syntax-interval-v2-benchmark.log`.

## Construction-only expression-type overlay

Private specialization construction now starts with an empty writable exprTypes
map over the canonical table for reads. Local membership, present nil/typed nil,
tombstones, subsequent assignments and logical iteration preserve the previous
full-copy semantics. Shared-codegen locking protects that base during each
constructor. Canonical publication and snapshots reject active construction
resources; published products contain only their current owned facts.

The closed Go-AST source policy covers 461 scalar reads, 30 membership reads,
152 retained local writes, two deletes and the logical range consumer. Actual
state/schema generation passes. Overlay membership/resource/foreign-query tests
and all private, Retained, snapshot and parallel semantic controls pass under
race in 8.828 seconds. Backend binding/context, retained disk IR/ABI and linked
runtime controls pass under race in 21.746 seconds after a separately preserved
compilation-time external timeout. Independent source review found no actionable
lookup/locking/publication/snapshot issue; it did not replace execution tests.

The 50-iteration ABBA construction comparison restores every changed Go source
to exact predecessor8124891c for the baseline and disables newly added Go files.
Eight products with 512 unrelated functions change from baseline 4.349/4.380 ms
and about 6.012 MB to candidate 4.203/4.203 ms and about 4.701 MB: approximately
4% less time and 22% less allocation in this fixture. No Stage1 speed claim is
made yet. Evidence `/tmp/elisa-expr-type-overlay-construction-benchmark-v1`,
`/tmp/elisa-expr-type-overlay-generate.log`,
`/tmp/elisa-expr-type-overlay-semantic-race.log`, and
`/tmp/elisa-expr-type-overlay-backend-race{-retry,}.log`.

## Prune statically identity-free canonical catalog values

Only canonical membership scans now skip static type closures proved unable to
carry AST nodes, Symbols or Scopes. Interfaces, semantic Type implementations,
AST leaf implementations, cycles and unknown kinds remain on the original walk.
Private fact/schema/resource validation is unchanged. The cache contains only
reflection types and booleans, never program graphs.

Exact old-walker index comparisons cover nodes, symbols, scopes and relevant
seen roles for real generic/default/module facts, detached analyzer AST, aliases,
interfaces and cycles. Private unknown-fact rejection and existing specialization/
overlay controls pass under race in 3.014 seconds. Backend generic bindings and
private context restoration pass under race in 1.631 seconds.

The first-catalog benchmark never warms the AST catalog. With 512 unrelated
functions, ten iterations change reference 9.363 ms / 13.573 MB to cold-type-cache
pruned 5.322 ms / 7.430 MB (about 43% less time and 45% less allocation); warm
type-cache cost is 5.195 ms / 7.414 MB. With zero unrelated functions, cold
classifier setup increases time from 0.152 ms to 0.214 ms while allocation drops.
These fixture costs do not establish full Stage1 performance. Logs
`/tmp/elisa-canonical-membership-pruning-{race,benchmark,backend-race}.log`.

## Reject private semantic failures before codegen publication

Private concrete analysis previously emitted ordinary semantic errors into a
construction analyzer, then published only canonical diagnostics and memoized
the product. Both a new concrete bool-reference arithmetic error and a repeated
canonical error on the same target reproduced successful invalid products before
the fix. Source/API failures are preserved in
`/tmp/elisa-specialization-diagnostic-{unpatched-regression,v3-unpatched}.log`.

Specialization workers now start with fresh diagnostics AND deduplication before
cloning inherited storage. They reject empty/error-severity diagnostics before
publication, matching Result.Errors. This prevents inherited reporter state from
hiding errors or suppressing region-dependent checks. Warnings remain nonfatal,
canonical diagnostics remain unchanged, failures never enter the successful
memo, and ordinary worker diagnostic policy remains unchanged. No proof budget
or check is weakened.

Focused real-source regressions pass under race in 1.445 seconds; all private,
overlay, canonical, Retained, snapshot and parallel semantic controls pass in
8.713 seconds. Backend binding/context/disk-IR/ABI/runtime controls pass on the
bounded retry after preserved compilation-time timeouts. Records
`/tmp/elisa-specialization-diagnostic-v3-{focused-race,semantic-race-retry,backend-race-final}.log`.

The preceding acbfaf producer still timed out in both final cold object generation
and a normal same-cache recovery. Those runs remain engineering performance
evidence, not correctness qualification, because they predate this error gate.
Their immutable source/cache/producer records are under
`/tmp/elisa-stage1-specialization-catalog-root`; no state has been rekeyed.

## Private expression-fact overlay

The constructor now overlays exprFacts as well as exprTypes instead of copying
all canonical facts. Whole OptimizationFacts values, Extent pointer aliases,
present-zero membership, tombstones and local rewrites retain the old semantics.
The constructor base is stable under shared-codegen locking. Future inline
resource-bearing OptimizationFacts schemas fall back to reflective cloning.

Actual generation, generator/source-policy and logical/reference controls pass.
Semantic/optimization/Retained/private/parallel/snapshot race controls pass before
and after an independently reviewed restore guard: raw registry encoding of an
active overlay must not restore as canonical. Marshal, Restore, AnalysisBuild,
public closure and worker/merge boundaries reject active construction resources.
Backend generic/context/disk-IR/ABI/native controls pass on the bounded retry
after a preserved compilation-time timeout. Logs
`/tmp/elisa-expr-facts-overlay-{semantic-race,restored-resource-race,policy-benchmark,generator-test,backend-race-retry}.log`.

Exact predecessor1f344e9e Go sources are restored in the 50-iteration ABBA
construction baseline. Eight products with 512 unrelated functions change from
4.232/4.254 ms and 4.701 MB to 4.213/4.098 ms and 3.657 MB: approximately22%
less allocation, with only a small timing improvement in this fixture.
`/tmp/elisa-expr-facts-overlay-construction-benchmark-v1` preserves the exact
overlay and logs. The isolated65536-row constructor benchmark changes from
4.151 ms / 8.401 MB in the ordinary reflective worker to 0.0267 ms / 7.6 KB in
the specialization overlay; it is not a full-construction or Stage1 comparison.

The correctly error-gated main1f344e Stage1 recovery completed26.761 seconds
with exit1 rather than producing an object: concrete ctx_task_to_raw reported
an unconsumed Pending handle. The old helper rebuilt handle/state fields without
moving its complete source. This newly visible runtime source bug remains open
until explicit whole-owner transfer is validated; the error gate is retained.

## Whole-owner concurrency handle rewrap

The four runtime thread/task from/to-raw helpers now use existing whole-owner
move-as destructuring before rebuilding the same-state carrier from its exact
handle/state fields. Their previous field copies left concrete Pending/Joinable
inputs live, exposed by the private semantic error gate. No compiler rule, cast
authority, must-consume check, payload layout, or diagnostic is relaxed.

Tests extract the actual runtime helpers, concretely specialize all four, and
check renamed helpers, old-body rejection/no memo, duplicate/use-after rejection,
returned Pending obligations and invalid state casts. Native LLVM/clang execution
roundtrips distinct handle bits through all four helpers with unchanged states.
Both semantic and backend race selections pass; logs
`/tmp/elisa-concurrency-owner-rewrap-{semantic,backend}-race.log`.
The prior frozen47be runtime source is historical evidence; next Stage1 input
must explicitly record these four ownership-source changes.

## Dictionary inactive bucket storage

The next source-owner-corrected Stage1 recovery failed in25.636 seconds with
no object: arena_dict_zero_buckets constructed a typed zeroed generic bucket
containing sview. Initialization and removal now clear inactive raw storage
through the existing explicit unsafe pointer/extern boundaries. Bulk clearing
returns before writes at capacity0 and checks size multiplication for overflow.
Payload reads remain behind occupied-state checks; insertion initializes both
fields before publishing occupied. Compiler validity checks are unchanged.

Actual-helper semantic specialization for sview plus a payload containing a
non-null reference and sview passes under race; ordinary zeroed sview still
rejects. Native actual-runtime controls use supported cstr keys and that same
nonzero payload, covering zero capacity, insertion, lookup, deletion, tombstones,
reinsertion, rehash, iteration and clear; checked overflow rejects before writes.
Restoring the old production source makes the native test fail at the exact
arena_dict_zero_buckets zeroed-sview diagnostic. Logs are
/tmp/elisa-dict-inactive-storage-{semantic-race,native-v2,overflow,unpatched-native}.log.
These are correctness controls; no new full Stage1 timing gain is claimed.

## Unchanged named contracts in retained body edits

Named contracts deliberately have no callable symbol/classifier row. They now
receive a distinct non-callable classification marker; complete contract/header
fingerprints and edit invalidation remain authoritative, and missing classifier
authority for actual callables still rejects. Before this correction an ordinary
body-only update in a program with two unchanged named contracts declined after
a disk roundtrip with declaration classification unavailable. That control now
passes full cold/live published-result parity and unchanged diagnostics. The
parity oracle also explicitly requires both paired non-callable contracts to
have no symbols, while preserving its missing-callable rejection.

Focused race tests pass1.504 seconds and the retained/session race selection
passes7.005 seconds, including existing contract/caller invalidation controls.
Logs: /tmp/elisa-contract-classification-{unpatched,focused-race-v2,retained-race}.log.
No full Stage1 timing is claimed for this admission correction.
