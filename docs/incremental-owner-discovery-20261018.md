# Reuse owner discovery across retained admission passes

IncrementalAnalysisSession.Analyze preserves retained-owner priority by admitting
retained owners before filling free slots with new owners. Previously both
passes walked declarations, reconstructed qualified owner names and hashed the
same identities, including functions whose bodies could not be cached.

For warm sessions, an invocation-local preflight prepares owner identities and
retained classification once, then reuses that occurrence-ordered list for
retained-first and new-owner admission. A cold session has no retained owners to
prioritize, so it prepares and admits candidates directly from one declaration
walk without building a candidate list. Both paths preserve duplicate-key
first-wins behavior, 4,096 tracked-body admission and subsequent retirement.
Owner identity is intentionally versioned: new keys use the `primitive-v2:`
name prefix and v3 hash domain with ordered parameter-type tags, so the former
arity-only function-source key bytes are not preserved. No AST pointer or
candidate record is retained in the persistent Session.

The production list stores an approximately 80-byte candidate header per entry
on 64-bit targets, plus its precomputed owner-key string and, during lexical-read
auditing, its qualified name. The owner record cap is 16,384 supported,
cacheable candidate occurrences (including duplicate owner keys), with up to
4,096 additional bounded lexical-audit contexts for
unsupported signatures. Cold sessions use the same owner and 8 MiB preparation
work caps but do not retain a candidate list. The charge covers namespace and
qualified-name construction and retention, signature validation and tags, key
construction, retained-local classification, and a per-candidate storage
allowance for warm sessions. It does not cover the later `IndexFunctionSource`
construction during Analyze collection. Return screening and local-initializer
expression planning share a cumulative
cap of 262,144 expression-node visits per Analyze call, including preflight,
reuse-boundary validation, and post-analysis publication checks. It counts
expression-node visits per bounded traversal, not distinct AST nodes across the
whole Analyze call. A warm owner can be screened during admission and again at
the analyzer reuse boundary; a cold-analyzed body can be screened again before
product publication. Each pass charges its visits to the same counter.
The per-expression 4,096-node and depth-128 limits remain. This counter does not
include `IndexFunctionSource` construction. Candidates that cannot fit the
remaining owner/local slots skip local planning; lexical-audit candidates still
receive their lexical observation before this gate. If owner preflight screening
exhausts the counter, Analyze falls back before changing the database,
prior-owner map, or retained products. If screening exhausts after the analyzer
has begun a body, that body is analyzed normally; incomplete screening does not
evict its old product or publish a new one, so no cached return-body typing is
used or published for that body on that request. Local initializer typing may
still be reused after its own validation. Each later Analyze call starts with a
fresh visit budget.
In a cold session, once tracked-body capacity fills, owner identity preparation
stops; when lexical auditing is enabled, the walk continues with lexical-only
candidates through its 4,096-function prefix. These work limits are not a peak
heap memory guarantee.
Unsupported signatures do not consume the owner-record cap, but validating them
still consumes the work budget. A sufficiently large unsupported prefix can
exhaust that budget before a later retained owner is discovered; a warm request
then uses only any retained candidates already prepared or falls back cold if
there are none.
Lexical indexing has separate limits of 4,096 functions and
`lookupTraceMaxEntries * 4` retained source ordinals (262,144); Analyze builds
an index before checking whether that aggregate ordinal cap would be exceeded.
The ordinal cap therefore limits retained indexes, not temporary construction
work or peak memory for one index. Each syntax fingerprint has its own limits of
1,000,000 visits, 16 MiB encoded bytes, and depth 2,048, but an index can still
temporarily allocate its full node list and ordinal map before the aggregate
check. When the function-count or retained-ordinal limit is reached, affected
functions receive no index and their lexical observations are dropped. When a
warm-session owner preflight reaches a cap, Analyze uses only its fully prepared
prefix and admits retained candidates from it because
the unseen suffix may contain additional retained owners. Prior owner, query,
and product entries for owners outside that prefix are preserved in the session
but are not admitted or consulted by that partial request. Entries for scanned
prefix owners with the same origin, qualified name, and parameter signature
are handled by normal admission and retirement, including removed local keys
when local classification completes. The local-name classifier skips functions
with more than 257 top-level statements, so an identity-matched owner on that
path can lose prior local keys without enumerating the full local set; this
reduces possible reuse but those keys are not admitted by the partial request.
Renames, signature changes, and deletions do not match the old owner identity,
and unsupported signatures have no matching prepared identity, so their prior
key groups can be indistinguishable from truly undiscovered suffix entries.
Those groups remain dormant until a complete census or session reset, and they
count toward the tracked-key cap; repeated partial requests can therefore leave
less room for new prefix admission. A cold session instead prepares and admits
candidates directly in declaration occurrence order until a cap stops it. In
both cases, normal semantic analysis still walks the full file. For a stable
over-cap file in fixed declaration order, suffix owners are not newly admitted
on partial requests; new suffix entries become reusable only when preflight
completes under the cap, or after a session reset or an explicit rotation
policy is added.

Functions beyond the admitted owner prefix have no owner/session reuse, and
lexical-read auditing creates no `IndexFunctionSource` entry or observations for
them during that request. A warm partial request also skips lexical indexing for
non-retained candidates inside the prepared prefix because it runs only the
retained pass. If a warm prefix has no retained candidate or none survives
normal admission, or if a cold path admits no owner, Analyze calls
`AnalyzeWithOptions` before changing the session database, prior-owner map, or
retained products. Invalid pointer aliases and exhaustion of the separate
65,536-node pointer-alias preflight still force whole-request cold analysis.
These are work and temporary-record limits, not a hard RSS guarantee. The
capped-prefix policy trades reuse and lexical-read coverage for bounded discovery
work when a file crosses either owner cap.

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
