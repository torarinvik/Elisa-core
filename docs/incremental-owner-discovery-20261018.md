# Reuse owner discovery across retained admission passes

IncrementalAnalysisSession.Analyze preserves retained-owner priority by admitting
retained owners before filling free slots with new owners. Previously both
passes walked declarations, reconstructed qualified owner names and hashed the
same identities, including functions whose bodies could not be cached.

The invocation-local preflight prepares every candidate identity and retained
classification once, then reuses that occurrence-ordered list for both
retained-first and new-owner admission. It preserves previous-owner authority,
duplicate-key first-wins behavior, 4,096 tracked-body admission and subsequent
retirement. Owner identity is intentionally versioned: new keys use the
`primitive-v2:` name prefix and v3 hash domain with ordered parameter-type tags,
so the former arity-only function-source key bytes are not preserved. The
collector still checks current admission state on each pass. No AST pointer or
candidate record is retained in the persistent Session.

The production list stores an approximately 80-byte candidate header per entry
on 64-bit targets, plus its precomputed owner-key string and, during lexical-read
auditing, its qualified name. The preflight caps candidate discovery at 16,384
candidate occurrences and 8 MiB of charged preparation work for cold sessions
and warm sessions with retained local names. The charge covers namespace and
qualified-name construction and retention, signature validation and tags, key
construction, retained-local classification, and candidate storage. It does
not cover the later `IndexFunctionSource` construction during Analyze collection.
Lexical indexing has separate limits of 4,096 functions and
`lookupTraceMaxEntries * 4` total source nodes (262,144); when those limits are
reached, the affected functions receive no index and their lexical observations
are dropped. If either owner-preflight bound is exceeded, Analyze uses only the
fully prepared prefix's retained owners and admits no new owner candidates,
because an unseen suffix may contain additional retained owners. The semantic
analysis still walks the full file, and the normal `current`-set retirement
removes prior owner/query/product entries omitted from that prefix; those bodies
are checked without session reuse and can be admitted again on a later analysis.
If the prefix has no retained candidate, or none survives normal admission,
Analyze calls `AnalyzeWithOptions` before changing the session database,
prior-owner map, or retained products. Invalid pointer aliases and exhaustion
of the separate 65,536-node pointer-alias preflight still force whole-request
cold analysis. These are work and temporary-record limits, not a hard RSS
guarantee. The partial-prefix policy trades reuse for bounded discovery work
when a file crosses either owner-preflight cap.

The isolated candidate-discovery qualification below does not add support for
aliased source ASTs throughout Analyze: its pre-existing pointer-keyed invocation
maps and AST-tree expectations remain unchanged. The separate shared-tree
follow-up described below adds a bounded preflight for the pointer-keyed owner and
direct-local maps; that guard is not part of the isolated candidate benchmark.

## Qualification

The change was isolated on branch codex/incremental-owner-admission-20261018 from
Core a28de4c3 so concurrent semantic edits could not affect the tested sources.
Only incremental_analysis_session.go, the new helper and its test/benchmark file
were changed in that executable source snapshot. The broad incremental/lexical
semantic race selection passed. It covers existing capacity/admission,
retained-local, replay and diagnostic parity controls, plus bounded-prefix,
namespace occurrence and large-name/overflow helper controls.

Component benchmark, Go 1.27.2, Apple M5 Darwin arm64, one CPU:

| Owner occurrences | Streaming median | Buffered median | Streaming bytes/op | Buffered bytes/op |
| --- | ---: | ---: | ---: | ---: |
| 512 | 431.205 us | 317.909 us | 409,752 | 376,336 |
| 16,383 | 14.002594 ms | 12.098610 ms | 13,106,554 | 13,941,682 |
| 16,385 (longer check) | 18.010087 ms | 9.619191 ms | 13,108,153 | 13,943,154 |

The first two rows use three 200 ms samples. A short over-cap run was noisy and
inconclusive, so the last row uses three one-second samples to check the fallback
cliff. This isolated iterator benchmark includes candidate-inventory construction,
both admission iterations, pass filtering and owner-name/key materialization,
while excluding semantic body work and the separate raw-AST pointer guard. It
predates the complete Analyze preflight described above and does not qualify that
path's cold-fallback threshold or latency. At large counts it trades more
allocation bytes for less identity preparation; fewer allocator calls are made
in all these cases.

A separate 512-function retained Analyze benchmark alternates one body edit and
requires exactly 511 reused bodies and one checked body each iteration. Parsing
is outside timing; real Analyze admission, validation and checking are inside.
Baseline/optimized/optimized/baseline runs used the same isolated source and an
overlay restoring only the original Analyze file for the baseline.

Allocation ranges were 10.906–10.918 MB per edit in baseline and 10.644–10.647 MB
with the candidate inventory, about 260 KiB (2.4%) less. Allocation calls fell
by roughly 1,500 per edit. Runtime ranges overlapped substantially: 11.1–20.5 ms
baseline versus 13.3–13.9 ms optimized. Host load was variable; this does not
establish a stable whole-analysis latency improvement. No edit-to-native-artifact
speedup is claimed.

Raw component/longer-cap/ABBA/race logs, the baseline overlay, exact source copies,
and a SHA-256 source manifest are retained at
build/verification/incremental-owner-discovery-20261018 in the shared Core checkout.
The final qualification verifies source hashes remain unchanged during its race
run. Earlier runs from concurrently edited shared files are excluded.

## Shared-tree pointer-alias preflight follow-up

After the isolated qualification, the shared checkout added a fail-closed
`primitiveOwnerPointerInventory` before candidate collection or session
publication. It walks namespace declarations, every static-if branch, impl and
interface function members, and direct function-body statements. Repeated
function pointers and repeated direct `VarDeclStmt` pointers decline incremental
admission; typed-nil declaration containers, depth beyond 128, or more than
65,536 visited declaration/body-statement nodes also use cold analysis. This
protects the invocation-local pointer-keyed maps without claiming general support
for aliased ASTs. The walker inspects all static-if branches conservatively, so
aliases across mutually exclusive branches may cause a cold fallback.

The shared-tree tests add top-level aliases into static-if then/elif/else branches,
impl/interface members, an overlong namespace, shared direct local declarations,
and an integration assertion that an impl alias falls back before retained keys
or database state change. Earlier boundary tests covered the pointer-check node
limit and the then-prefix-based candidate budget; those tests do not exercise the
complete preflight described above. This total-budget change has received source
review only; no tests were added or run for it.

The separate `BenchmarkPrimitiveOwnerPointerInventory` measures only this
unconditional guard, not a before/after speedup. Two short Apple M5 Darwin arm64
runs of 20 iterations × 3 samples varied materially. The first measured 512
functions at about 46–59 μs, 20,000 functions with two direct statements each
(near the guard limit) at about 3.95–4.05 ms, and 21,800 functions (over the
limit, hence cold fallback) at about 4.16–4.45 ms. The later run measured those
cases at about 82–87 μs, 5.60–7.87 ms, and 6.58–7.56 ms respectively. Both runs
reported 75 KB/op for the 512-function case and 3.55 MB/op for the large cases.
The over-limit measurements cover guard detection only; they exclude the
subsequent cold analysis. Run-to-run timing variance is substantial, so these are
overhead observations, not evidence of an incremental speedup. The shared tree
remains dirty, so this follow-up is not source-provenance-clean for a Stage0
build.

The pointer audit covers the current invocation maps keyed by `*ast.FuncDecl`
and `*ast.VarDeclStmt`: local-initializer admission only accepts direct
`fn.Body` declaration statements, and nested statements without an admitted
direct entry fall back before a query hit. Analyze still makes two declaration
walks: the raw-tree pointer-alias scan, then the capped owner-candidate preflight.
The latter now prepares all root/namespace candidates once, while the pointer
scan continues through skipped contexts such as overlong namespace owners and
static-if/impl/interface members. A future fused preflight could share these
walks, but must keep scanning skipped contexts and fail closed on duplicate
pointers or scan-limit exhaustion. The current pointer scan also treats aliases
in mutually exclusive static-if branches as unsafe and falls back conservatively.

The retained Analyze timing earlier in this note predates both the pointer scan
and the total owner-candidate preflight, so it does not include their per-request
cost. The isolated guard cost reaches several milliseconds near the node cap and
allocates megabytes in large cases; current end-to-end retained-edit latency with
these guards is unknown. Re-run the paired Analyze workload with both in place
before making latency claims.

## Remaining work

Discovery remains a full declaration walk, and session.previous retirement still
scans its retained set. This removes repeated work; it does not establish compiler
edit cost proportional to changed owners. Parser-produced changed/removed-owner
inventory and current semantic catalog publication/rebinding remain necessary for
professional incremental compilation.
