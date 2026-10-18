# Pointer-free frame catalog API

This is a Go Core library foundation, not production resolver integration or a
compiler speedup. Analyzer/Scope construction and normal resolution are unchanged.
The existing lexical observer remains an audit path. See also
[incremental publication design](incremental-frame-catalog-publication-20261018.md).

`NewFrameCatalog` copies an invocation's supplied frame/parent/name inventory.
The caller must attest that enumeration is complete at the current read boundary.
The constructor validates structure, referenced-parent completeness, identity
encoding, cycles, depth and budgets; it cannot detect a live name/frame omitted
by a compiler collector. A partial scope or previous lookup trace cannot certify
negative name cells. Completeness means the names visible at the actual semantic
read boundary, not every declaration eventually found in that source block.
Publishing later local declarations before their original visibility point would
change shadowing/order semantics. The production adapter must mirror accepted
scope mutations and parent changes before evaluating against the new environment.
The copied catalog is immutable outside this package.

Frame and binding IDs are opaque exact UTF-8 strings. The semantic adapter must
supply stable IDs with source/module/declaration context and reject ambiguous
anchors. No pointer, traversal index or hash substitutes for exact identity.
Go Core's NewFunctionIdentity is body-independent; FunctionSourceAnchor.Tree
fingerprints full function syntax and therefore churns on body edits. Using that
anchor for frames is safe only with explicit function-wide retirement/rebuild,
and does not provide fine-grained stability within the edited function.

## Separate cell and query ownership

`PrepareLookup` returns a query publication with no owned inputs, plus independent
cell-owner publications. `NamePublication` derives positive, present-nil and
negative cells from the current complete catalog. `ParentPublication` derives
an explicit parent edge, including an empty terminal parent. Each cell has one
stable owner; lookup queries only read it. A cell edit can therefore invalidate
all dependent memos while leaving their query owners untouched.

`FrameCatalogEvaluators` registers selection and inert cell-owner evaluator kinds.
The latter are ownership units using the current Session API, and consume query
slots even though they have no result/dependency reservation. Catalog binding
limits and Session admission limits are separate: the catalog may describe many
names while only a bounded requested working set is published. Cell-owner count
plus lookup-query count must fit Session query limits. Do not chunk an oversized
world into publications and serve a partially current snapshot.

The selector records every absent name before selection, every traversed parent
edge, and the selected positive/nil cell. Present-nil stops traversal. Complete
failure also depends on the terminal parent edge. Products contain frame/binding
IDs and presence/nil flags only. Current Symbol rebinding, visibility checks,
diagnostics, types and mutable flow facts remain the semantic adapter's work.
This API does not implement global candidate ordering or binder-specific nil rules.

## Revision preparation and lifecycle

Prepare current input under PublishDeltaPrepared so construction failure first
invalidates old handles. `LookupDelta` bounds cumulative preparation work before
materializing additional plans, deduplicates cell/query owners and checks owner,
input, edge, dependency and logical byte reservations. Session still makes final
atomic admission against its existing world. A batch can fail even when each
individual lookup fits. Direct PrepareLookup is for individually bounded plans;
callers accumulating plans themselves must enforce an aggregate work budget.

Initially publish cells for requested names. Thereafter the compiler collector
must maintain an exact retained cell-owner manifest, or provide declaration
changed-name deltas. Refresh all affected previously published positives and
negatives even if a retained function performs no new lookup. Parent changes
must publish current edges and newly required name cells. Retire cell owners
when their authority ends, and query owners when their frame/source identity
ends. Removing one query does not retire globally owned cells. Neither this
catalog nor LookupDelta discovers changed/removed declarations automatically.

Reservations are sized from each prepared query's current chain/products. If a
later cell edit increases product size or dependency depth beyond its retained
reservation, evaluation returns the corresponding budget error. Refresh that
query reservation or fall back cold; never return a stale selection. The API
intentionally does not silently enlarge unreserved storage during evaluation.

## Bounds and current costs

Raw identities are limited to 4 KiB, encoded keys/results to 64 KiB, depth to 256
and a single prepared lookup to a conservative 1 MiB materialization estimate.
Preflight includes JSON escaping and temporary encoded copies. Aggregate lookup
preparation consumes its supplied logical byte/work allowance, including duplicate
requests. Session owner/edge/dependency/input limits still apply. These are logical
payload/work bounds, not an allocator-capacity or RSS guarantee.

NewFrameCatalog still scans/copies all supplied frames and names. LookupDelta
preparation still walks each supplied lookup's current ancestor chain through
the first selected binding. Parent cells are prepared only after an absent name. It belongs at coherent
revision preparation, never before each resolver hit. Production changed-owner
integration and preparation cost must be included in any future end-to-end
measurement. The previous Session one-cell benchmark does not qualify this
adapter or establish O(touched) compiler behavior.

## Verification

The incremental package passes race checks. Coverage includes caller-map ownership,
structural/byte/depth limits, missing parents, cycles, invalid and maximally escaped
identities, batch limits counting cell-owner slots, cumulative duplicate work,
negative/parent dependency inspection and 200 retained histories compared with an
independent ordinary scope walk (shadow insertion/removal, nil blockers and parent
changes). A focused regression updates one shared name-cell owner while omitting
both memoized lookup owners; both results match fresh evaluation. Retiring that
cell makes the omitted queries fail with unpublished-input authority rather than
reuse their old products.

Exact source copies/hashes and the raw race log are retained locally under
`build/verification/incremental-frame-catalog-api-20261018/`. No throughput claim
is made for this library slice; production discovery, publication, rebinding and
native-artifact validation remain outstanding.
