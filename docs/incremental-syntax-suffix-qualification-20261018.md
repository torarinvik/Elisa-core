# Qualification of exact-position syntax suffix replay

Implementation: `d1cc388f` (18 October 2026). A retained parse can now skip
unchanged declarations following an edited declaration, in addition to the
existing whole-file and prefix reuse. It copies a private pristine suffix;
semantic and lowering mutations cannot modify the retained snapshot.

The old and current lexical suffix must match every token field, including
positions and EOF, with equal token counts. The parser must reach the same
quiescent boundary and outgoing counters/visibility. Per-transition lower token
read bounds prove later parsing never inspected changed prefix tokens. Recovery,
notices, state differences, shifted locations and incomplete traces decline
replay. Token-span reads now record both endpoints. The parser field/read-channel
audit explicitly covers the new state and certified EOF jump. Replayed traces
respect the checkpoint bound and can authorize subsequent revisions.

The existing lazy snapshot-admission policy remains: a source revision must have
an admitted pristine snapshot (normally after its second request). One-off cold
revisions do not suddenly capture an entire AST. Hits refresh the retained
source's LRU position, so later equal-position edits can reuse it. Source
relocation, token insertion/deletion and finer-than-declaration syntax reuse
remain future work. This does not make arbitrary function semantics incremental.

AST replay now reserves its temporary identity tables from capture counts.
Partial ranges use declaration-weighted capacity hints; all nodes and aliases
still traverse the same typed copier. Snapshot metadata remains bounded and
charged with its retained payload.

## Checks

- Fresh AST and diagnostic parity over repeated edits, undo, shifted locations
  and parse errors; visibility keys bind to current private declarations.
- Snapshot mutations and eight concurrent edited parses retain private nodes.
- Certificate rejection for changed parser counters and backward token reads.
- Fresh/retained semantic, lowered and LLVM outputs preserve current defaults.
- Six fresh/retained native objects link and execute with expected values after
  default-argument edits and undo, using a partition-sized module.
- Full AST, lexer and parser suites pass. Selected ownership, cache, certificate
  and native integration suites pass under the race detector.

The final race log reports AST 1.213 s, parser 2.759 s and main-package integration
5.681 s; lexer tests reused a valid cached race result. The complete normal
frontend suites pass after the final snapshot capacity changes.

## Throughput and memory evidence

The workload has 513 scalar functions. Each novel edit changes an equal-width
six-digit literal in the first function; 512 unchanged suffix declarations are
replayed. Separate-mode samples varied enough to change the apparent result, so
an alternating paired benchmark was added. It executes both paths for each edit,
reversing their order on alternating iterations.

| Paired sample | Tokens plus fresh parse | Suffix replay | Reduction |
| --- | ---: | ---: | ---: |
| 1 | 5.248 ms/edit | 3.935 ms/edit | 25.0% |
| 2 | 6.625 ms/edit | 5.011 ms/edit | 24.4% |
| 3 | 3.717 ms/edit | 3.046 ms/edit | 18.0% |

Per-phase paired metrics include lexing, cache handling and parsing/copying;
changed-source construction is outside those phase timers. Overall benchmark
ns/op and allocation figures include source construction. Separate-mode
allocation samples are about 4.44 MB/7,765 allocations for fresh parsing from
tokens and 4.98–4.99 MB/6,774–6,775 allocations for suffix replay. The extra bytes
are an explicit throughput tradeoff.

These are short shared-host synthetic frontend measurements, not a stable
whole-compiler speedup or production-project qualification. All earlier mixed
samples are preserved alongside the final paired samples, rather than discarded.

Exact source hashes, committed compiler source archive, full frontend/race/native
logs and all benchmark samples are retained under
`build/verification/incremental-syntax-suffix-qualified-20261018/`.
