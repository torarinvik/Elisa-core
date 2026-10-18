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

Traversal tests include repeated function pointers in different namespaces to
check occurrence order and fallback only. This does not add support for aliased
source ASTs throughout Analyze: its pre-existing pointer-keyed invocation maps
and AST-tree expectations remain unchanged. No new whole-tree pointer scan or
large-AST cold-fallback rule is part of this change.

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
cliff. This benchmark includes inventory construction, both admission iterations,
pass filtering and the production owner-name/key materialization, while excluding
semantic body work. At large counts it trades more allocation bytes for less
identity preparation; fewer allocator calls are made in all these cases.

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

## Remaining work

Discovery remains a full declaration walk, and session.previous retirement still
scans its retained set. This removes repeated work; it does not establish compiler
edit cost proportional to changed owners. Parser-produced changed/removed-owner
inventory and current semantic catalog publication/rebinding remain necessary for
professional incremental compilation.
