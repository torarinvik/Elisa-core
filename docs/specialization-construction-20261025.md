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

## Set inactive bucket storage

The parallel SetBucket initialization/removal paths now use the same checked
raw-storage protocol as dictionary buckets, retaining occupied-state key guards
and explicit Abort.Panic at clear. Actual raw SetBucket[sview] specialization
passes without broadening public supported set keys or relaxing zeroed validity.
The native control exercises that raw sview storage plus public cstr-set
add/duplicate/contains/remove/tombstone/reinsert/rehash/clear/zero-capacity
behavior and overflow-before-write. The identical native control against old
production fails the exact arena_set_zero_buckets zeroed-sview diagnostic.
Semantic race tests pass1.484 seconds; native/overflow controls pass1.617 seconds.
Logs: /tmp/elisa-set-inactive-storage-{semantic-race,native,unpatched-native}.log.
Fresh full Stage1 producer/source qualification remains pending.

## Corrected-runtime Stage1 baseline and new body miss

Fresh main02a74693 (binary6ad4a24f) and an independent7043-file47be-derived
input differ from the original source only in collections.elisa and the four
concurrency owner rewraps. Twenty bounded checkpoints saved analysis; step21
completed/stored analysis and reached the external30-second limit in codegen.
A fresh normal-process resume produced object
ed1449433613bf7aa0b5c774dc73b46e4ce788412b15764912bc2594d20506f5
in21.998139 seconds. The object links; that Stage1 compiler emitted a smoke
object whose linked executable returns42. An unchanged rebuild takes0.273746
seconds and reproduces the exact baseline object bytes.

A genuinely new parser.expression body comparison edit takes23.689778 seconds,
missing2 seconds. A second novel, equivalent arithmetic condition was profiled
separately (27.149687 seconds with observers): retained preparation remains
fast=true; specialization construction accounts for12.8GB allocation, analyzer
cloning10.6GB and its reflective fallback8.2GB (inclusive, overlapping totals).
The profile samples20.5GB total allocation and14.48CPU seconds clearing memory.
Read-only API diagnostics restore the original authenticated state without
publishing/modifying/rekeying it and apply the existing field clone policy:
iterBinderElementStates costs46.8MB/25.4ms and visibleCandidates9.95MB/6.04ms
per canonical field copy. Counts alone were not used as allocation evidence.
All measurements/manifests, exact edit bytes and restoration records reside at
the path in /tmp/elisa-stage1-bucket-baseline-root. The source parser is restored
to its recorded2556c882 baseline SHA. Full cold/strict-IR edited parity, O3,
varied edits and generation2 remain unqualified.

## Rebuild private name-candidate memos on demand

Private generic construction now starts an empty visibleCandidates cache rather
than reflecting over every cached lexical-context/name table. This cache is a
pure function of namespace/usings/alias context; ordinary worker-copy policy,
alias authority, candidate order and actual lookup facts remain unchanged.
Focused tests compare fresh private candidate lists against the existing
uncached builder across namespaces, using/import/module aliases and misses,
check canonical isolation and append capacity, and preserve ordinary workers.
Semantic retained/specialization/parallel/snapshot race selection passes7.639s.
All39 selected backend generic/specialization/alias tests pass in four bounded
groups; the all-in-one25s run expired and was retained as failed timing evidence.

The same construction benchmark source with4096names across three contexts
changes from1.976MB/16712allocations and1.31–1.95ms to8160bytes/235allocations
and32–50us per construction. Logs: /tmp/elisa-visible-candidates-*.log and
/tmp/elisa-visible-candidates-backend-groups.json. This fixture gain is verified;
full Stage1 timing for this producer is pending.

## Construction-local proof events

A private specialization now starts an empty flat proof-event sequence. Concrete
producers still record their actual obligations; public specialization publication
starts from the complete canonical Result and discards the construction analyzer,
so its full report/summary and foreign/nested query fallback are preserved.
Ordinary worker inheritance remains unchanged. The independent real-source test
fails without the correction, and verifies nonempty concrete observations and
complete canonical report/summary publication with the correction.

Focused race passes1.480s; retained/private/parallel/snapshot/overlay race passes
9.197s. The39 selected backend generic/specialization/alias tests pass in four
externally30s-bounded groups. Logs: /tmp/elisa-private-proof-events-*.log and
/tmp/elisa-private-proof-events-backend-groups.json. A separate prefix-copy
microbenchmark uses the measured Stage1 row count22893, without claiming a
semantic workload: constructor+first-append allocation changes from6004033bytes
to7672bytes, and0.607–0.647ms to0.075–0.079ms. Full Stage1 timing for this
producer remains pending. No proof authority, budgets or public event sequence
is removed.

## Lazy private iteration-binder provenance

Specialization now borrows the idle canonical iteration-binder table as a private
construction resource, materializing each requested value with the exact former
deepCloneValue policy once per key. Local writes, present-zero/missing/nil keys,
tombstones, scalar bits, nested map isolation and prior pointer/slice alias policy
are preserved. Ordinary canonical workers retain the previous full-copy policy.
The two production accesses use a closed accessor API; a Go-AST source check
rejects new raw access and the generated opaque-map switch excludes this table.

The new resource is rejected by canonical publication, scheduling, merges, public
fact closures, AnalysisBuild, MarshalState and independently encoded Restore.
Actual generators pass and add only the named resource type while removing its
old opaque-map case. Focused race tests pass3.087s; broader retained/private/
parallel/snapshot/zeroed/abstract-state race selection passes10.901s. The initial
source fixture was invalid and used a field source that could never trigger the
tracked writer; the corrected valid bare-local fixture requires zero errors and
actual rows, then compares every value with the old clone. An additional concrete
generic loop test passes1.526s and native actual-runtime execution passes1.631s,
checking copied string views retain their live backing. All50 selected backend
generic/specialization/alias/borrow/region tests pass within individual bounded
groups; one cumulative25s group timeout was preserved and split without omitting
any test. Logs: /tmp/elisa-iter-binder-*.log and backend JSON coverage records.

A512-row nested-state constructor fixture changes from24960545bytes/128748
allocations and14.5–15.6ms to7536bytes/233allocations and66–87us. The real
Stage1 source attribution was46.8MB per full table copy; neither result alone
is a full Stage1 speed claim. Fresh-producer/new-edit qualification follows.

## Workload provenance correction and preserved-spec rerun

The earlier bucket/iter workload copied the entire Go collections version into
the47be snapshot. It changed FNV specifications, named bucket constants and
effect-grant declarations beyond the six intended storage methods. Historical
manifests/hashes remain intact, with workload-provenance-amendment.json beside
each affected experiment. Their same-workload before/after timings are engineering
evidence, not qualification against the self-host repository's preserved library
specifications. No completion or proof-strength claim follows from that workload.

Within that exact older-library workload, mainfb341977 produces the final cold
object in29.534s, a first normal object-cache prime in14.866280s and the first
same depth-comparison edit in16.235027s (prior23.689778s). The actual subsequent
exact hit is0.272836s; diagnostic bootstrap bypassed object-cache publication,
so the first normal prime must not be called an exact-hit measurement. Baseline,
first-edit and arithmetic-profile objects are byte-identical to their respective
prior-producer counterparts. The complete new profile takes19.114264s, samples
15.55GB allocation, and still identifies8.10GB specialization/5.95GB analyzer
clone allocation; retained preparation remains2.640s.

The self-host primary now has exactly the four owner rewraps and six storage
method changes, preserving every other original collection function byte-for-byte,
its FNV specification include, bucket constants and unrelated effect grants.
Commits4bdcd7cde then corrective32113afd5 leave a net two-file30insert/19delete
delta. An independently copied7043-file preserved-spec input is recorded at
/tmp/elisa-stage1-preserved-spec-source-root. Its complete original-library
cold/IR/runtime qualification must run next. This correction preserves the full
original goal; O3, varied edits, normal CLI defaults and generation2 remain open.

## Preserved-specification Stage1 baseline

The corrected7043-file47be input preserves original FNV specifications, named
bucket states and unrelated effect grants, changing only six storage methods and
four owner rewraps. Main673183d8 (fb341977 executablefe718c05) completes final
O0 cold step24 in27.892s. Step16's external timeout left step15's atomic
checkpoint intact; the compiler's saved slow-declaration hint let step17 save
at12175/14768 and resume without changing any proof or administrative budget.
Normal state-backed cache prime takes15.996298s and reproduces the cold object's
exact035337b9ae6251ba60cebd4d884900f0a3ca2fef1c12ecd2728b55786d7646ef
bytes. That object links to Stage1, which compiles a native smoke returning42.

The first new depth-comparison edit takes17.935174s, still missing2s. Exact
baseline and edited object hits take0.287850s and0.422048s respectively. The
second new body profile completes22.221040s, showing16113.98MB allocation,
8294.57MB specialization and5938.19MB analyzer cloning (inclusive totals),
plus2147.33MB permission replay and870.60MB inventory allocation. Preparation
remainsfast=true at2.616s; restore overlaps primary parse, so those times are
not independent additive costs. Dataset/command/source/cache/runtime records:
/tmp/elisa-stage1-preserved-spec-root. Edited parser bytes and all edit records
are preserved, with the original parser baseline restored. Full edited
independent-cold/strict-IR parity, O3, varied matrix, defaults and generation2
remain open.

## Settled bound-name cache sharing

Private specializations copy the outer bound-name memo and share settled inner
sets. The reviewed producer builds a fresh set completely before publication;
its two consumers only read membership. Source checks bind those complete
functions and reject new consumers/raw accesses, preserving that closed contract.
New rows and outer deletions are private; ordinary workers retain deep copies.
An independent source review found no rekey/publication/mutation blocker.
Focused race passes2.664s, broad retained/private/snapshot/parallel/iterator race
passes12.410s, generation has no registry/helper diff, and all50 selected backend
generic/specialization/alias/borrow/region tests pass in bounded groups. The fair
direct-map benchmark changes638417bytes/8203allocations/0.73–1.10ms to
79312bytes/11allocations/4.3–4.8us. Logs: /tmp/elisa-settled-bound-*.log and
/tmp/elisa-settled-bound-backend.json. No end-to-end gain is inferred from this
component benchmark; fresh-producer qualification remains necessary.

## Exact typed parameter-fact memo copy

Private specialization copies storeScanParamFactsCache with a typed loop after
an exact four-field/name/type schema guard. Scalars, declaration keys and opaque
interface pointers are unchanged; reach keeps its prior backing with cap=len,
including nil versus non-nil empty shapes. Both constructor and helper fall back
to the former reflective policy for unknown schemas. Ordinary workers and the
mutable binder cache remain unchanged. Closed source controls bind actual
producer/consumer functions and reject new unreviewed accesses.

Focused race passes2.772s; broader retained/private/parallel/snapshot/iterator
race passes13.055s. Actual generation has no registry/helper diff. All50 selected
backend generic/specialization/alias/borrow/region tests pass in bounded groups.
Direct-map benchmark changes1770312bytes/8229allocations/1.35–2.11ms to
1573552bytes/34allocations/0.49–0.55ms. An independent source audit found no
alias/rekey/publication blocker. Logs: /tmp/elisa-param-facts-*.log and
/tmp/elisa-param-facts-backend.json. Full fresh-producer edited Stage1 measurement
remains necessary; the component benchmark is not a target qualification.

## Invocation-local permission inventory reuse

A stable retained update constructs the permission function inventory once after
structural changes, preserving executable source order and the first FuncDecl
ordinal. It passes that local value through body/summary components, cycles and
callers. All live type lookups, dynamic masks, complete row scans, declared/initial
selection, shallow snapshots and nested/current-type restoration remain unchanged.
No inventory instance is stored in Analyzer, RetainedAnalysis, Result or a global.
Generation adds its unused named type to the general registry; no persisted graph
field or resource is added. Independent source review confirmed phase stability.

The verbatim old-path oracle covers duplicate/nil/missing declarations, aliased
FuncTypes, dynamic rebinding/masks and five full slice values/identities. Actual
restored body/summary/add/remove cases compare cold results and every permission
row. Focused tests pass0.421s; broad semantic race passes13.101s after the first
external30s run expired during compilation. All50 selected backend regressions
pass in bounded groups. At2048functions the per-caller benchmark changes
1508686bytes/42allocations/0.46–0.65ms to1125134bytes/29allocations/0.37–0.41ms;
build-once costs457520bytes/24allocations/0.091–0.096ms per update. No-summary
updates pay this fixed cost, explicitly measured rather than hidden. Logs:
/tmp/elisa-permission-inventory-*.log and backend JSON coverage. Fresh-producer
Stage1 timing remains necessary; no sparse-row authority or proof work is skipped.

## Preserved-specification Stage1 comparison at 695c49fb

Fresh empty disk namespace, independent processes, server disabled, identical
47be source with only the six inactive-storage methods and four owner helpers
changed; all original specification includes/effect grants remain. Compiler
695c49fb binary SHA256 ee39ba035a8e874a57db1efe54f69dd472418cf2387ace0865b038d7a59e8660.
Cold analysis completed in22 externally bounded invocations; every invocation
finished within30s, final object step29.703s. Normal cache priming15.464849s,
exact unchanged0.285675s. The first unseen depth-comparison body edit took
16.600622s versus17.935174s on673183d8 (same source/workload), about7.4% lower
in this single controlled comparison. Exact edited repeat0.290104s. These are
separate measurements, not repeat-hit substitution for edit latency.

Baseline and edited objects respectively retain exact SHA256
035337b9ae6251ba60cebd4d884900f0a3ca2fef1c12ecd2728b55786d7646ef and
9eca96bcb50e0007f0e7ad4060e22919d617bc21d6950a422b2b51ec391fd669,
matching the previous producer byte for byte. Source restored to its exact
original hash after measurement. Evidence pointer
/tmp/elisa-stage1-settled-inventory-root, JSON/settings/objects and source edit
receipts alongside it. This run measures the three latest allocation changes
together; it does not isolate their individual end-to-end effects. Body latency
still exceeds2s; O3, varied edit matrix, strict current-producer IR and full
latest-source self-host/gen2 qualification remain open. Cache flags remain opt-in.

## Read-only publication struct traversal

The publication walker now reuses existing addressable struct storage rather
than copying it solely to expose fields. Nonaddressable map/interface values
retain exactly the prior reflect.New+Set fallback. The walk does not write input
storage; validation, identity classification, canonical pruning, pointer-role
tracking and span ownership remain unchanged. Immediate pre-cut oracle controls
compare graphs/errors/roles and actual syntax immutability across private,
unexported, pointer, slice, map, interface and cyclic inputs.

Focused tests pass0.554s; broader publication/specialization/sparse/canonical/
overlay race tests pass6.781s. Actual semantic generation produces no diff.
Targeted8192-row benchmark changes1246424–1246429bytes/24589allocations/
1.279–1.294ms to721977–721984bytes/16396allocations/1.104–1.115ms.
Only addressable temporary struct copies are removed; this does not eliminate
the whole1.8GB cumulative publication cost in the earlier profile, nor establish
an end-to-end Stage1 gain. Evidence /tmp/elisa-publication-addressable-*.log;
source/oracle artifact /tmp/elisa-publication-addressable-struct-walk-v1.

All50 selected backend generic/specialization/ABI/layout/alias/region regressions
pass in10 externally bounded groups; complete test names and timing records are
in /tmp/elisa-publication-addressable-backend.json.

## Invocation-local permission snapshot frames

The explicit update inventory now owns one saved-row map per active nesting
depth. Every scope still scans the same complete ordered rows, reads live types
and masks, saves complete five-slice headers, and performs the original normal
restoration loops. Deferred release clears references and releases only its own
frame on return, panic or Goexit. Prior exceptional semantic restoration behavior
is unchanged. No global/persisted/Analyzer/Result reference owns this scratch.
Nil-context compatibility keeps private one-shot allocation. Actual generation
adds the unreachable stack-only frame type to the broad registry.

The verbatim previous-path oracle compares complete row values, pointer/len/cap/
nil headers across nested calls, exceptional unwinds, aliases, masks and later
reuse, including idle key/value clearing. Focused permission/inventory race
passes1.633s. At2048functions warmed single caller changes1125082bytes/29allocs/
243.6–244.0us to0bytes/0allocs/130.2–131.5us; nested two scopes change2250166–
2250167bytes/58allocs/437.8–443.0us to0bytes/0allocs/218.3–219.3us. The separate
fresh invocation includes inventory and first-frame cost1583850bytes/57allocs/
310.3–315.4us. This is a many-callers scratch reuse gain, not free initial setup
or a complete Stage1 timing. Logs /tmp/elisa-permission-snapshot-*.log; frozen
source and audit /tmp/elisa-permission-snapshot-frames-v1.

Broader retained/permission/parallel/specialization/build/snapshot/overlay race
passes14.486s. Initial external30s run expired with empty output during compilation;
after confirmed process termination, retry completed. No budget was raised.

All50 selected backend regressions pass in10 bounded groups; exact coverage and
measurements: /tmp/elisa-permission-snapshot-backend.json.

## Fresh Stage1 comparison at 61557a73

Same preserved-spec source, new empty disk namespace, fresh processes and server
disabled. Producer binary d5b7fdc119b1587643d3a8cecf87949b6e1f436f6ca8191a2c1ca87266bd637a.
Cold completion22 bounded invocations, final26.806s. Normal prime14.458006s;
unchanged0.287050s; first unseen comparison body edit15.947435s; exact edited
repeat0.291867s. The first-edit measurement is about3.9% lower than695c49fb's
16.600622s in the previous single controlled run (and11.1% below673183d8's
17.935174s); no statistical consistency claim is made from one sample each.
Cold, normal baseline, edited and separately profiled arithmetic-edit objects
all remain byte-identical to their corresponding previous-producer objects.

A separate novel arithmetic-body profile with observers enabled takes17.945267s
versus22.221040s on673183d8. The allocation profile reports10696.50MB versus
16113.98MB, but these are GC-lagging sampled snapshots, not process TotalAlloc;
the previously stated33.6% process-allocation reduction is withdrawn below.
Inclusive SpecializeFunction4956.57MB and cloneAnalyzer3230.19MB identify observed
hotspots, overlap, and are not separate totals. Publication walker1.33GB cumulative,
disk decoder2.19GB and tokenization0.63GB are similarly sampled. Proof, optimization
and check budgets remain unchanged. Source restored
exactly after both edits. Evidence /tmp/elisa-stage1-snapshot-publication-root,
complete per-run JSON/settings/objects, source receipts and CPU/allocation profiles.
Targets still require substantial work: body2s, contract/signature3s, major10s,
O3 and full self-host/latest-source gates remain unqualified.

## Exact no-storage cache overlay for private construction

An authenticated current61557a73 snapshot exported through the normal disk
reader attributes3502576bytes/490allocations per private construction to the
57673-entry storeScanNoStorageCache. Current full owned construction measures
54054760bytes/270760allocations once; field attribution is a diagnostic including
fixed constructor overhead, not an additive end-to-end estimate. All-field scan
passes29.043s, with repeated top-field controls5.284s. Test-only overlays leave
main source clean; artifact /tmp/elisa-owned-clone-field-attribution-v1, ranked
measurements /tmp/elisa-owned-clone-attribution-all-ranked.json and raw receipt
under /tmp/elisa-stage1-snapshot-publication-root. Existing clone policies are
strictly pinned by source hashes in that diagnostic.

Private construction now borrows completed bool entries through a local/base
lookup and writes only local misses. Membership and both true/false answers are
preserved; no inherited entry is recomputed, cleared or materialized. Ordinary
workers retain their old full-map copy. Canonical invalidation and every
publication/scheduling/merge/build/Marshal/independently decoded Restore boundary
reject the construction resource. Closed raw-access controls retain the existing
Type-keyed cache's lifetime; no cross-edit authority is added. Actual generation
adds the named overlay resource, and its opaque-copy policy remains unchanged.

Focused race passes2.685s; broad retained/private/parallel/snapshot/permission race
passes15.555s after one confirmed external30s compilation timeout and retry.
Targeted32768-entry map-copy fixture changes1752880–1752881bytes/131allocations/
242.5–244.1us to5432bytes/3allocations/0.804–0.832us. Both sides include the same
Analyzer shell; the new side includes the actual non-nil empty local map. This
fixture does not include complete specialization/publication/IR costs or establish
a Stage1 timing. Logs /tmp/elisa-no-storage-overlay-*.log; frozen source artifact
/tmp/elisa-specialization-no-storage-overlay-v1. Current-producer fresh Stage1
qualification remains necessary.

All50 selected backend generic/alias/ABI/layout/region regressions pass in10
externally bounded groups: /tmp/elisa-no-storage-overlay-backend.json.

## Private optimization-symbol identity lookup overlay

Actual145742-row Stage1 attribution identified6997616bytes/746allocations per
owned constructor for optimizationSymbolIDs. Private construction now borrows
those immutable string rows and allocates only local misses. Local-first comma-ok
lookup preserves present-empty IDs; nil-symbol API behavior and exact shared
atomic UID allocation on a true miss remain unchanged. Ordinary worker map copy
and counter policies remain unchanged. Sparse public OptimizationFacts retain
complete IDs, extents and flags; the construction cache/resource is discarded.
Every canonical scheduling/merge/publication/build/Marshal/independently decoded
Restore and nested/pre-memo boundary rejects the active resource. Existing
no-storage/ExprTypes/ExprFacts/iterator resource checks remain intact.

Closed raw-access policy migrates the only production getter/writer and excludes
this map from generated owned opaque-copy dispatch. Actual generation confirms
that exact removal plus one state registry resource row. Independent source
review found no concrete counter, lifetime or alias defect. Controls cover known
and empty IDs, counter77->78 exactly once on miss, nil/empty map shapes, foreign
copy decline/rekey isolation, nested publication and full real global-array facts.
Focused combined identity/no-storage race passes2.643s; broader retained/private/
parallel/permission/snapshot/overlay race passes17.023s.

Actual owned constructor benchmark over otherwise empty Analyzer input is7568
bytes/237allocations/24.4–25.1us at0,1024 and145742rows, with the required empty
local map and inherited membership retained. This establishes no growth with
inherited table size in this fixture; it does not measure complete Stage1 output
or add field timings together. Logs /tmp/elisa-symbol-id-overlay-*.log; source
artifact /tmp/elisa-optimization-symbol-identity-overlay-v2-6de48dea. Fresh
producer end-to-end/cache/native/IR qualification remains pending for both new
overlays.

All50 selected backend generic/specialization/ABI/layout/alias/region tests pass
in10 externally bounded groups: /tmp/elisa-symbol-id-overlay-backend.json.

## Fresh Stage1 comparison at 446ed7be

Same exact preserved-spec source, new empty namespace, fresh processes and no
server. Producer binary291d9cb1a514c208ddaabac284aab70cae1e77352429338372ddf2082c37f309.
Cold completion22 bounded invocations, final26.841s. Normal prime14.035722s;
unchanged0.292181s; first unseen comparison body edit15.271819s; exact edited
repeat0.296520s. First edit is4.2% below61557a73's15.947435s in the previous
controlled single run,14.9% below673183d8's17.935174s. This is not yet evidence
of consistency across the required varied edit matrix. All corresponding cold,
normal, edited and profiled arithmetic objects keep their prior exact hashes.
The complete stdout/stderr streams also match61557a73 byte for byte for all four
normal measurements, including10379708-byte diagnostic streams.

Separate novel arithmetic-body observer profile takes17.522101s. Allocation total
10732.46MB versus10696.50MB at61557a73 is essentially flat/slightly higher;
cloneAnalyzer3161.04MB versus3230.19MB and SpecializeFunction4954.09MB versus
4956.57MB remain substantial. Thus component map-copy wins do not justify a
claim of another total-allocation reduction. The earlier final-restored-state
field census is a diagnostic input, not proof that every hot construction uses
those same complete maps. Actual callsite/input census is needed before projecting
field savings across all specializations. Source restored exactly after both edits.
Evidence /tmp/elisa-stage1-borrowed-cache-root, complete JSON/settings/profiles,
normal-streams-comparison.json and source receipts. Body2s, signature/contract3s,
major10s, O3/defaultCLI/latest-source/strictIR/full-gen2 gates remain unqualified.

## Bounded eager store-flow summary pooling

Owned construction keeps the exact existing summary copy policy: shared summary
pointer groups remain shared, distinct summaries and mutable retKind/reachKind
cells stay private, and completed connectivity fields keep their old shallow
sharing. It pools eagerly copied structs, row headers and kind cells using
same-runtime slices.Clone capacity probes and disjoint full-slice partitions.
Zero-length slices use slices.Clone directly, including nil/empty/backing shape.
Bounds fall back to the unchanged old helper; no flow/proof budget is changed.
Ordinary workers always retain that helper, pinned by an exact function hash.
Maps below256entries use the old path because the expanded small-input benchmark
found overhead at0/1/128entries. No lazy/value authority or new state field/type
is introduced; actual generation produces no diff.

Old-helper oracle tests cover nil/empty/capacity/summary aliases, mutable rows,
reslicing/append isolation, initialization and bounded fallback. The actual parsed
258-function constructor/publication fixture ensures pooling is exercised and
private pool pointers do not enter published query state. Focused race after
threshold/fixture correction passes1.724s; broader retained/private/sparse/canonical/
parallel/permission/snapshot/overlay race passes17.433s. An initial incorrect test
name filter matched no tests and is not counted as verification.

At11308rows the targeted fixture changes67915allocations to74 while bytes change
3486009–3486075 to3507673 (~0.6% increase). CPU ranges overlap: old1.266–1.293ms,
pooled1.261–1.318ms; no CPU speedup or whole-Stage1 gain is inferred. Small-map
fallback retains exact byte/allocation counts and similar timings. This cut targets
object count/GC pressure, not payload removal. Original tests/source artifact
/tmp/elisa-store-flow-summary-pool-v1; root modifications add the256 threshold,
0/1/128/11308 benchmarks and real large constructor fixture. Logs:
/tmp/elisa-store-flow-pool-*.log. End-to-end retained-pool lifetime/time qualification
remains necessary; actual hot constructor field inputs are still being measured.

All50 selected backend generic/specialization/ABI/layout/alias/region regressions
pass in10 externally bounded groups: /tmp/elisa-store-flow-pool-backend.json.

## Allocation-profile accounting correction

Current runtime source documents that MemProfile data may be up to two GC cycles
old. CLI startMemProfile writes pprof.Lookup("allocs").WriteTo(f,0), without a
final collection or current runtime.MemStats snapshot. Thus the published profiles
can lag recent constructors substantially at the default Darwin GOGC200. Reported
10696/10732/16114MB profile totals and per-function sums are observations from that
sampled epoch, not complete process allocation totals. The earlier33.6% total
allocation-reduction claim is withdrawn; the apparent flat latest total likewise
cannot establish flat real allocations. This is separate from normal CLI timings,
exact object/diagnostic comparisons and testing.B byte/allocation counts, which
remain valid. Use an explicit final MemStats TotalAlloc/Mallocs observer or a
properly synchronized complete profile for future process-wide allocation claims.
Do not force GC or change GOGC in qualification timings to make profiles comparable.

The isolated actual-input observer at /tmp/elisa-owned-clone-hot-debug-root records
187 cold and187 fresh-disk-baseline constructors from one actual Analyzer each;
all match the labeled Result and contain the complete measured145742symbol-ID,
5013binder,11308store-flow and57673no-storage rows. This confirms actual inputs in
those phases, not a shortcut for measuring process allocation totals. Debug object
matches the normal baseline exactly. Observer timings are never gain evidence.
