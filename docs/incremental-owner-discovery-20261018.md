# Reuse owner discovery across retained admission passes

IncrementalAnalysisSession.Analyze preserves retained-owner priority by admitting
retained owners before filling free slots with new owners. Previously both
passes walked declarations, reconstructed qualified owner names and hashed the
same identities, including functions whose bodies could not be cached.

The new invocation-local candidate inventory prepares that identity and retained
classification once. It preserves declaration occurrence order and the existing
function-source key bytes, previous-owner authority, duplicate-key first-wins
behavior, 4,096 tracked-body admission and subsequent retirement. The collector
still checks current admission state on each pass. No AST pointer or candidate
record is retained in the persistent Session.

Records contain a function pointer, a 32-byte identity and a retained flag
(approximately 48 bytes per record on this target). They retain no qualified
name strings. The inventory stops at 16,384 records or an 8 MiB preparation-work
estimate covering namespace/owner-key construction and retained-local name
classification. A saturated inventory reuses its prepared prefix and streams the
unprepared suffix on both passes, skipping prefix occurrences without rehashing
them. Cold worlds stream; lexical auditing also streams to preserve its required
qualified source context. Streamed records reuse their already encoded name;
buffered names are encoded only after matching the current admission pass.
These are bounded temporary records/work estimates, not a hard RSS guarantee.

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
cliff. This benchmark includes candidate-inventory construction, both admission
iterations, pass filtering and production owner-name/key materialization, while
excluding semantic body work and the separate raw-AST pointer guard. At large
counts it trades more allocation bytes for less identity preparation; fewer
allocator calls are made in all these cases.

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
or database state change. Boundary tests cover the exact pointer-check node limit
and aggregate retained-local classification work crossing the 8 MiB candidate
budget while preserving retained-owner order across both passes. The full
`go test ./src/semantic -count=1` passed on the shared dirty worktree (54.667 s);
the focused owner/session suite passed again after the final test additions.

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
direct entry fall back before a query hit. The main optimization opportunity is
to share work with owner candidate preparation: Analyze currently scans the raw
declaration tree for pointer aliases, then candidate preparation traverses it
again (and retained-prefix/suffix preparation can add another walk). A shared
preflight can carry namespace/candidate eligibility while validating every
function and direct local pointer, preparing candidate records only for the
existing root/namespace candidates. It must still scan skipped declaration
contexts, including overlong namespace owners and static-if/impl/interface
members, after candidate count or preparation-work limits are reached; stopping
with candidate admission would lose the guard's fail-closed guarantee. Return
separate scan validity and prefix completeness: duplicate pointers or scan-limit
failure require cold fallback before collection/publication, while a valid but
incomplete prefix should keep the current occurrence-ordered suffix replay.
Suffix replay may still traverse again to avoid retaining unbounded candidate
records. The current scan also treats aliases in mutually exclusive static-if
branches as unsafe and falls back conservatively. These are performance and
admission-coverage tradeoffs; no fused traversal has been implemented or
qualified.

The retained Analyze timing earlier in this note predates this shared-tree
preflight, so it does not include the new per-request scan. The isolated guard
cost reaches several milliseconds near the node cap and allocates megabytes in
large cases; current end-to-end retained-edit latency with this guard is
unknown. Re-run the paired Analyze workload with the guard in place and compare
it against the same revision with only the guard disabled before deciding that
the correctness protection is affordable or that owner discovery improved.

## Remaining work

Discovery remains a full declaration walk, and session.previous retirement still
scans its retained set. This removes repeated work; it does not establish compiler
edit cost proportional to changed owners. Parser-produced changed/removed-owner
inventory and current semantic catalog publication/rebinding remain necessary for
professional incremental compilation.
