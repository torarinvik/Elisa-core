# Bounded frame-catalog publication for tracked name resolution

Design proposal, inspected against Go Core `ff7656f4` on 2026-10-18.
This is not an implemented optimization or a measured speedup. Self-hosted
Elisa query handles have a separate integration path.

## Current evidence

`semantic/incremental_lexical_reads.go` performs ordinary scope lookup before
observing it. Moving `prepareLexicalSymbol` before that lookup does not avoid
resolver work: it probes the requested name in each current scope itself.
`incremental/session.go` flattens all admitted publications into new input,
authority and query maps on every Publish, scans retired inputs and queries,
and passes the complete inventory to Database.applySnapshot. sessionQuery
retains reservations, not input ownership. `Database.Apply` already supports
an atomic changed-input batch, but bypassing Session would bypass admission,
negative-input authority, ownership accounting and retirement limits.

Publishing a complete world after each of N definitions therefore takes
quadratic publication work. Replacing a whole growing frame after every
Define has the same problem, even if unrelated frames are excluded.

## Required transaction boundary

Use one retained world with invocation-local bindings. BeginRevision invalidates
old handles before preparing any current identities. A builder accepts bounded
owner deltas; Commit makes the coherent world evaluable. No evaluation may
observe partially constructed frames. Each successful commit returns a new
handle; a failed preparation cannot resurrect the previous handle.

A proposed delta contains owner identity, removed input keys, upserted input
cells, query admissions/removals and a frame-completeness transition. Explicit
owner removal retires that owner's remaining keys. A delta is a patch, not an
implicit replacement: omitted keys remain owned. Full Publish remains a separate
replacement operation; switching modes must reset or construct ownership
metadata explicitly rather than pretending old publications had owners.

Retain an owner-to-key index and per-input ownership assertions. Shared cells
must agree after applying the whole transaction, including explicit negative
cells. Removing one owner's reference does not remove a still-shared input.
Changing a shared value requires all remaining owners to agree in the same
transaction; a single reference count cannot prove this. Prefer a single
canonical frame owner for name cells so most updates avoid this shared case.

Validate conflicts, cancellation, arithmetic overflow, authority and projected
budgets before mutating retained maps or the database. Stage only touched keys
and affected owner assertions. Clone admitted strings; do not retain caller
maps, ASTs, Scopes, Types, Symbols or source views. Apply all accepted changes
under the session gate and advance the database revision once. Use explicit
negative inputs for admitted absence. Unpublished absence remains an error.

## Deterministic admission policy

For the first delta API, admission is all-or-nothing: explicit removals are
accounted before upserts, then the complete projected transaction must fit every
reservation. If it does not, reject the entire transaction and leave the new
revision unavailable; the caller runs cold or constructs a smaller explicit
transaction. Do not silently omit touched queries, evict untouched owners or
choose winners by map iteration. Already-live touched queries do not outrank
new cells because both must fit together. Reordering equivalent patches must
produce identical admission and current-input authority. A future partial
admission policy requires a separately specified stable total ordering and
explicit skipped ownership units; it cannot split a frame's completeness proof.

## Frame and binding contract

Frame identity contains canonical unit identity, a supported source anchor,
scope role and stable path. Binding identity contains its frame, declaration
anchor/role/name and parameter ordinal where applicable. Generated, ambiguous
or unsupported anchors decline caching. Current FrameId-to-Scope and
BindingId-to-Symbol maps exist only during the invocation.

Publish parent edges and exact name cells while constructing scopes. A complete
frame catalog certifies absence; a previously observed miss does not. Every
accepted Define, replacement, deletion and late global insertion must update
the catalog and current binding registry before lookup becomes possible.
Invocation-local symbolsVersion detects local mutation but cannot establish
cross-revision equality. Parent changes must invalidate the parent-edge input.

Do not manufacture every possible absent-name cell during frame construction.
For a newly requested name, consult the complete current catalog once and admit
its exact positive or negative cell in a bounded transaction before evaluation.
Maintain a reverse index of admitted name cells so changes update only affected
cells. Previously admitted negatives must be refreshed against each new complete
frame before any retained result can be used. First admission is real resolver
work and must not count as a skipped probe.

A lexical query reads each frame's selected name cell and parent edge until the
first present cell, preserving all nearer negatives. Full failure depends on
the entire visited chain and terminal parent. On a hit, rebind the selected
stable binding to the current Symbol. Rebinding failure falls back cold;
never return a retained pointer. Budget failure or incomplete catalogs also
fall back cold, without using stale handles.

Global selection additionally depends on ordered namespace/usings/alias candidate
recipes and private-symbol/private-module visibility. Generation counters from
one invocation are not persistent content authority. Keep this separate from
lexical selection rather than broadly memoizing name_known or flow-sensitive
resolution under an incomplete key.

## Complexity and acceptance criteria

For U touched cells and A affected ownership assertions, ordinary publication
must be O(U + A), excluding bounded maintenance. It must not enumerate all
unrelated owners or all names in the growing frame. Whole-owner retirement is
O(keys owned). Explicit replacement may scan that owner's old inventory once.
Retired authority and DB tombstones need byte and key debt budgets; compaction
may be occasional O(world), reported separately from ordinary update latency.
Query reservation changes evict affected memos; eviction and input changes
should share one DB transaction, avoiding repeated revision changes.

Before claiming a gain, compare fresh and retained edit histories including
insertion, deletion, shadow introduction/removal, parent changes, alias and
visibility changes, negative lookups, cancellation and budget fallback.
Check current binding identity and diagnostics, not merely query hit counts.
Measure catalog construction/publication, lookup time, edit-to-artifact time,
retained bytes and allocations separately. Count scope probes skipped only
after a successful current binding rebind or certified negative result.

## Implementation ownership

Analyzer/Scope and parser/AST are actively edited by other owners. This proposal
does not change those files or Session. Coordinate the session API owner and
final Core source freeze before implementation. The next implementable slice is
owner/input-delta accounting with atomic failure semantics; production lookup
integration follows only once current catalog completeness is authoritative.

## Measured baseline

`BenchmarkSessionOneCellEditPublication` alternates one existing input value in
otherwise unchanged snapshots, after initial admission. Three 200 ms samples,
one CPU, Apple M5 Darwin arm64, Go 1.27.2, runtime source ff7656f4:

| Admitted queries/inputs | Median publication time | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| 64 | 65.692 us | 163,400 | 123 |
| 1,024 | 1.498940 ms | 2,711,343 | 1,140 |
| 8,192 | 10.432087 ms | 21,707,120 | 8,662 |

The benchmark checks complete admission and final retained inventory. It excludes
initial setup, query evaluation, scope construction, parsing and native output.
It demonstrates world-size publication cost for a one-cell edit, not a compiler
speedup or production resolver workload. Retain this baseline for comparison with
the future delta API. Raw local output:
`build/verification/incremental-publication-scaling-20261018/benchmark.txt`.

Command: `go -C compiler test ./src/incremental -run '^$' -bench
'^BenchmarkSessionOneCellEditPublication$' -benchmem -benchtime=200ms -count=3
-cpu=1` (shown across lines for readability).

A separate two-second 8,192-query profile attributes approximately 99.8% of
sampled allocated bytes directly to PublishPrepared (6,820.51 MiB across the
profiled benchmark process, not per operation). Growing selected/planned maps
and rebuilding final inventory/authority maps dominate; database snapshot
application accounts for approximately 4.26 MiB cumulatively. This supports
changing publication ownership/update structure before optimizing evaluator
lookup. CPU samples include substantial runtime kevent/madvise activity, so
this profile does not establish a precise mutator CPU breakdown. CPU, memory,
benchmark log and matching test executable are retained with the local evidence.
