# 117 — Incremental Build System

## Status
Stage 1 (build-avoidance object cache) **implemented**. Stage 2 (module-granular
incremental compilation) **designed, not implemented** — see "Stage 2" below.

An opt-in cross-process exact-proof cache is also implemented. Set
`ELISACORE_SMT_CACHE_DIR` to a compiler-owned local cache directory to reuse
definitive UNSAT results for byte-identical, complete SMT queries. The key pins
the running compiler artifact, solver bytes/version, platform, solver budget and
query text. Atomic, bounded records with integrity checks make incomplete or
corrupt entries misses. SAT models and unknown/timeouts are not persisted.
`--explain` reports persistent hits separately from in-memory query hits.

This avoids solver work; it does not skip function analysis, query construction,
parsing, IR construction, or optimization. It therefore supports the larger
incremental-compilation goal without completing Stage 2. Cache storage has the
same local trust boundary as compiler object artifacts, rather than serving as
an independently checked proof certificate. Currently the solver is still
started before the first persistent lookup.

The compile server also has an opt-in **native partition-object cache**. Send
`{"mode":"obj","filename":"example.elisa","source":"...","incremental":true}`
to `/api/v1/compile`; `object` in a successful response is a base64 native O0
object. The caller supplies final linking. A stable, unique filename identifies
the translation unit. Anonymous/virtual units use direct emission so separately
compiled snippets do not share temporary private-symbol namespaces.

The server still parses and analyzes current source, rebuilds the whole LLVM
module, verifies it, and performs current global optimization. It reuses native
partitions only after comparing complete current partition bitcode and actual
target triple/CPU/features. SHA256 indexes entries; exact byte comparison
provides reuse authority. Selective definition cloning avoids copying every
foreign function body. The cache owns bytes rather than AST or LLVM handles,
accepts no supplied native artifacts, and shares a 256 MiB payload budget across
server filenames. The library exposes `NewIncrementalObjectSession` and
`WriteObject`, plus counters and phase timings.

Private symbols receive a stable translation-unit namespace while partitions
are merged. Address-significant constants have one owner; only globally unnamed
private constants may be duplicated. Ownership comes from pristine IR, including
when selective cloning has already removed foreign initializers. Modules below
instruction weight 2,000, cross targets, aliases, ifuncs, assembly, address-taken
blocks, and appending globals (including constructor/destructor registrations)
use direct emission. Failed partition merges preserve the previous output.
Cache/LRU locks cover only cache operations; independent O0 lowering/codegen can
run concurrently. Target registration runs once. The known unsafe LLVM default
optimization pipeline remains serialized within a process.

A source-matched local Apple M5 run used the real server executable, eight fresh
workers and eight cache partitions, 256 integer arithmetic functions, nine novel
constant body edits, and alternating request order. Median source-to-object HTTP
time fell from **338.24 to 280.01 ms** (17%). Four-request warm batches measured
**930.60 versus 558.22 ms** median over five batches, or **4.30 versus 7.17
requests/second**. The first cached build took **576.30 ms**. Results varied,
including one slower incremental single-edit sample. These short synthetic O0
measurements exclude final executable linking/execution and do not qualify
representative project or compiler self-build performance.

The full backend and compile-server suites, focused concurrent/race checks,
native exit-status parity at O0/O2/O3, debug relocation, ABI/CPU changes, virtual
units, and LLVM boundary guards passed. A native constant-address regression
failed before the ownership fix and passes afterward. Qualification used
read-only overlays to exclude unrelated loop-proof edits. The 17 relevant source
hashes match Core commit `1cb75ef0d05d`; that commit also contains verifier work
not qualified by these isolated checks. Raw samples, compiler/script hashes,
source snapshots and overlays are in
`build/verification/incremental-native-final-20261018/`.

Reproduce the HTTP workload from the repository root with:

```sh
go build -o /tmp/elisac ./compiler/src
python3 compiler/bench/incremental_native_server.py /tmp/elisac /tmp/native-bench
```

Incremental native partitions now assign functions by stable symbol identity
and strip unused foreign prototypes before computing exact bitcode keys. Five
isolated edit histories (small/large body growth, front/middle insertion and
removal) each emit one of eight partitions and reuse seven, versus eight misses
before this change. Tests link and execute the results, preserve cross-shard
constant pointer identity, and check live callee ABI changes still invalidate
callers. Ordinary one-shot emission retains instruction-balanced partitions.

Three alternating synthetic benchmark pairs (five edits per sample, Apple M5)
measured median native pipeline time falling from 163.21 to 104.37 ms for growth
and 215.74 to 98.03 ms for insertion. These timings include LLVM regeneration,
native emission and partition merging, but exclude parsing, semantic analysis
and final executable linking. Full backend/server checks and focused race checks
passed. Raw logs and relevant source hashes are saved under
`build/verification/incremental-stable-shards-20261018/`.

The structural-edit benchmark also reports per-phase time. A five-edit local
sample measured growth at 31.09 ms IR, 30.81 ms partition preparation, 22.04 ms
native emission and 14.87 ms merging (110.36 ms total including other overhead).
Insertion measured 42.52/43.02/31.08/20.15 ms respectively (153.32 ms total).
These are single diagnostic samples, not a new paired speedup claim; partition
preparation is a material remaining cost alongside full-module lowering.

A follow-up experiment shared ownership computation and unsupported-IR admission
across all shards, retaining per-shard verification. Native regression tests and
C++ rejection fixtures passed, but three alternating ten-edit benchmark pairs
were too variable to establish a throughput gain. The candidate was discarded;
patch sources and raw results are retained under
`build/verification/incremental-admission-experiment-20261018/`. Repeated admission
has not been established as the dominant preparation cost. Isolate cloning and
bitcode serialization before introducing more preparation machinery.

Preparation statistics now separate clone/restrict/prune/verify work from bitcode
serialization and copying. Three ten-edit samples measured growth medians of
23.04 ms cloning and 10.97 ms bitcode, with insertion medians of 22.88 and
11.33 ms respectively. Full native-pipeline medians were 125.69/121.58 ms; these
are diagnostic samples on a shared host, not paired speedup claims. Existing
`PartitionTime` remains the sum of those phases. Raw logs are retained under
`build/verification/incremental-preparation-breakdown-20261018/`.

A merged-output cache experiment passed native tests. Three paired samples
improved the unchanged median from 82.45 to 56.92 ms, but regressed growth from
107.31 to 141.05 ms and insertion from 108.06 to 122.07 ms. One unchanged sample
also regressed (79.03 to 104.81 ms). It serialized the full module and
fingerprinted the host linker on each request; Apple's tool-selection query
alone measured 13–15 ms, comparable to an entire relocatable merge. A variant
keyed from validated native partition bytes avoided extra bitcode serialization,
but did not eliminate that tool-selection overhead. The experimental backend
changes included by concurrent commit `59dba92c` were removed; unrelated verifier
fixes in that commit were preserved. Raw paired timings, test logs and the final
experimental source variant are retained under
`build/verification/incremental-merged-rejected-20261018/`. No merged-cache speedup
is qualified. Moving unchanged-build detection above LLVM lowering remains the
more useful direction for eliminating repeat work.

A semantic CPU profile identified repeated reflective statement-list searches in
the reserve-warning pass. A shared, thread-safe type-shape cache now skips
concrete types incapable of containing statement lists. Interfaces and recursive
types remain conservative; the shape is derived from current Go fields, so AST
schema changes do not silently introduce an omitted traversal. A regression test
checks that an explicit reserve remedy inside a returned lambda is still found.

Three 1,000-iteration component samples reduced the statement-list walk median
from 151.54 to 119.83 microseconds (21%), and allocation from about 74,270 to
37,320 bytes per walk (50%). Broader synthetic semantic-edit measurements with
GOGC=1200 were mixed: integer-return edit medians 67.50 to 70.02 ms and local
initializer edits 2.04 to 1.99 ms. This is a measured component improvement,
not a qualified end-to-end compiler speedup. Reserve-warning and incremental
regressions, including the returned-lambda remedy, passed with the race detector
in 15.807 s. Logs and relevant source hashes are
in `build/verification/incremental-statement-walk-20261018/`.

The interprocedural parameter store-target pass now avoids local-declaration and
body scans when no expanded source parameter can carry region storage. This
uses the same current resolved-type predicate as the existing edge-recording
guard. Eligibility is recomputed on each analysis; storage-carrying functions
retain the original monotone fixpoint and output shapes are preserved.

Three alternating ten-edit benchmark pairs (Apple M5, GOGC=1200, parsing outside
timing) improved integer-return edit medians from 89.15 to 78.68 ms (12%) and
retained local-initializer edits from 2.42 to 2.29 ms (6%). Counters remain one
body checked/255 reused and one initializer checked/127 reused respectively.
One integer sample regressed, so these are synthetic workload medians on a
shared host, not a whole-project speed guarantee. Region/store/retention/escape
and incremental regression tests passed in 16.266 s; the isolated race run
passed in 19.587 s. Two uncommitted prototype tests were excluded alongside the
unrelated source edits they require, keeping the qualification on the committed
region/SMT implementation plus this optimization. Source snapshot, isolation
overlays and raw samples are retained under
`build/verification/incremental-storage-summary-gate-20261018/`.

Retention qualification found a duplicate-argument bug before attempting faster
propagation: `keep_second(value, value)` inspected only the first matched
argument, missing a retained second position. The helper now checks every
storage-carrying matching argument while resolving the callee only once. A
regression reproduced the failure, then checks the corrected result, a read-only
duplicate negative control, and transitive caller propagation. Selected
region/store/escape/death-time/incremental regressions passed normally (3.958 s)
and with the expanded controls under the race detector (17.023 s), using the
same isolated committed-source lane. These retention summaries currently feed
optional death-time reporting rather than code generation. This is a correctness
gain; no compilation speedup is claimed. Evidence is retained under
`build/verification/incremental-retention-duplicate-args-20261018/`.

The normal effect-handler rewrite now uses the existing immutable
`syntaxWalkFields` plans, avoiding scalar/source-position traversal while
preserving pointer/composite callback order, handler capture evaluation,
forwarding contexts and lowered expression links. Existing syntax-walk contract,
effect-handler and incremental tests passed in 2.066 s; the same isolated
regression lane also passed with the race detector.

Three alternating ten-edit pairs (Apple M5, GOGC=1200, parsing excluded) improved
integer-return semantic edit medians from 76.72 to 71.13 ms (7%) and retained
local-initializer edits from 3.36 to 2.31 ms (31%). Integer-edit allocations fell
from roughly 556,000 to 212,000 per edit and bytes from 56.1 to 39.6 MB. Reuse
counters remain one body checked/255 reused and one initializer checked/127
reused. One integer sample was slower, and these synthetic analysis-only results
do not establish whole-project or edit-to-executable speed. Raw samples, source
snapshot and isolation overlays are retained under
`build/verification/incremental-effect-walk-20261018/`.

Type/global/debug metadata can still invalidate additional shards. Avoiding full
LLVM regeneration/optimization, general semantic result reuse, persistence,
normal CLI integration and representative edit-to-executable qualification
remain unfinished. These gains do not complete Stage 2.

The in-memory tracked-query engine in `compiler/src/incremental` is a second
foundation. It commits input batches as revisions, records query and input reads
(including missing inputs), validates only requested dependency closures, and
preserves a result's change revision when recomputation produces identical bytes.
That last distinction prevents a private body edit from invalidating callers
whose exported interface stayed unchanged. Dependencies are replaced after each
evaluation, rather than accumulating obsolete edges. Values are immutable strings;
semantic diagnostics belong in these values and can be reused along with success.

Cycles and interrupted/failed evaluations never publish partial results, even if
an evaluator ignores a failed dependency. Interprocedural recursion needs an
explicit strongly connected summary/fixpoint query. Inputs and evaluation are
serialized for a consistent snapshot, and queued evaluation can be cancelled.
`Database.EvaluateBatch` runs an ordered batch under one database snapshot and
returns position-aligned per-query errors. The semantic session uses it to
prefetch only local initializer products retained by the session after static
implementations are collected. Known misses skip the database lookup; each
initializer still validates its current source and bindings before replay.
The engine now drives the opt-in closed-integer session described below. General
function-product reuse, persistent graph storage and parallel query scheduling
remain separate implementation work.

Function-product identity and anchor APIs now live in
`semantic/incremental_function_identity.go`. Declaration identity includes loader
origin, resolved qualified name, receiver implementation, ordered specialization
context, and an explicit structural signature digest. The digest covers lifted
contracts and lifetime/frame fields; the pretty printer is not a lossless
semantic serialization. Function bodies are separate inputs. Position-independent
node anchors cover the whole declaration, including contracts and sorted
resolved-argument maps. Batch capture validates the source tree; batch replay
validates the destination and returns current node pointers/positions. Mutation,
detached nodes, mismatching kinds or changed graph topology decline replay.
Parsed and lowered trees remain distinct: synthesized lowering nodes must be
restored before replaying metadata that refers to them.
Depth, work and encoded-size budgets reject oversized product graphs without
turning the optional cache path into unbounded recursive work. These source
identities and structural anchors are not resolved API fingerprints or cache-hit
authorization; the enclosing semantic query must separately validate its inputs.

`semantic/incremental_source_anchors.go` adds a separate invocation-local source
index, captured before analysis. Its portable anchors refer only to original
parser nodes that survive analysis; semantic annotation or insertion of lowering
nodes does not renumber them. Rebinding uses a matching pristine current parse
and returns its exact spans, including when spacing changes independently between
statements. Function provenance and declaration identity are checked as well as
the complete source-tree digest. These are source-origin references, not a way
to reconstruct mutated semantic content. Generated default arguments, replaced
nodes and detached nodes decline capture until dedicated origin/reconstruction
adapters exist. This source-index API alone does not authorize skipping analysis;
no mutable AST is persisted.

`semantic/incremental_scalar_facts.go` implements the first typed observation
adapter: ExprTypes, ResolvedTypeNames, ResolvedValueNames, PermGrowthOps,
RewriteDefaults, IndexBoundsProven, LemmaCalls, GhostDecls and GhostContracts.
The product owns a deterministic encoded string; restore returns separate maps
on current source nodes. Numeric CanonicalTypeID values and Type.String
are never used as persistent type identities. Exact field names/types, payload
schema, entry/string/byte budgets, duplicate entries and typed anchor matches
are checked. A failed restore exposes no partial maps. Explicit false entries
remain distinct from absent entries.

The backend regression replaces all nine covered output maps with restored
observations and checks LLVM IR against an independent cold analysis after source
relocation. Other outputs and live-analyzer state still come from fresh analysis
in that test. This qualifies metadata replay only: it does not skip a body,
establish semantic dependencies or validate proof retry policy. The enclosing
function query must supply those guarantees before any production reuse.

Payload v3 adds `semantic/incremental_type_values.go`. Audited owned type recipes
preserve scalar attributes (bounds, region, reference state/storage/mutability,
surface names, generic arguments and error payloads) and sharing/cycles across all
expression entries through one function-wide type table. Ref, view, optional, tuple, ID/address-space, container, generic,
aggregate-state and parameter types are covered. Non-nil AST refinement/type
expressions, non-nil Shape objects, function types, invalid types and unsupported
symbolic state types still decline capture. Every supported node has an exact
field-name/type schema; graph edges, required children, discriminator ranges and
incremental byte/work/depth budgets are checked before publication.

Struct, enum, const-enum, opaque and named errorset definitions are references
into a loader-authorized current `FunctionTypeEnvironment`. Products contain
only stable origin/name/kind keys and complete bounded fingerprints. Restore
recomputes those fingerprints rather than trusting an environment-construction
snapshot, and returns current declaration objects. The derived enum variant
packed-view memo is explicitly excluded; all other unsupported/private state
declines fingerprinting. Field, layout, drop-hook and enum-payload changes
invalidate even when the name is unchanged. Nominal dependency keys/fingerprints
can be extracted with `TypeDependencies` and read from the current environment
with `CurrentDependency`; these are only part of a body query's dependencies.

Capture and lookup must use the same published declaration-analysis phase.
The current fingerprint is conservative and may include implementation syntax;
it is not yet the minimal exported API fingerprint needed to prune all private
implementation edits. The backend tests additionally qualify view metadata and
current nominal pointer restoration through the public encoded product path.
Type ownership across other Result outputs, remaining adapters, generated-node restoration,
live-analyzer hydration and actual body-cache integration remain unfinished.

## First live semantic reuse path (opt-in)

`semantic.NewIncrementalAnalysisSession().Analyze(freshFile, options)` accepts a
freshly parsed complete program on each call. Keep one session per expanded
program/root. Primary body products reuse closed canonical-scalar single-return
functions, including the original integer arithmetic grammar and a guarded
boolean predicate grammar, including intrinsic scalar range membership. The
separate local-initializer product reuses eligible
straight-line canonical-i64 initializers before a plain final return. Each path
has its own syntax and current-binding checks; unsupported bodies and
initializers stay on ordinary analysis. Plain namespace/module functions have
qualified identities; parameter arity separates admitted overload owners.
Other declarations, conditional declaration containers, mutable/defaulted
parameters, calls, preconditions, scoped contract proof blocks and generics stay cold.
Plain value postconditions are reanalyzed and discharged on every invocation;
only their closed scalar return-expression typing may be reused.
The original Analyze/AnalyzeWithOptions APIs retain the cold path.

Each query reads a position-independent, versioned closed-grammar source digest,
complete options/effective environment, canonical primitive bindings with the
absence of applicable primitive binary operator candidates, and an immutable
typing product. Current source nodes
are collected by the same checked grammar; compact `int`/`i64` tags resolve to
the current analyzer's canonical types. Exact AST field-name/type manifests
close the path on schema changes, even for newly added zero fields. Depth,
node-count and source-spelling budgets bound the optional path. Failed or
unusable records take the ordinary path and are replaced after successful work.

Hits skip primary expression typing and reuse the grammar's provably empty
container-element-read summary. The normal body-state save/restore still runs.
Return checks, provenance/borrow tracking, implicit conversion flags, permissions,
CFGs, function facts, global summaries and code generation run on the current
invocation. No diagnostic/SMT timeout is memoized by this slice. Primary counters
separate checked/reused bodies, expression checks and element scans from later
speculative/fixpoint/specialization work. Each body edit invalidates its own
product; source shifts reuse without keeping old nodes or positions.

The session serializes analyses and retains only strings/keys between calls.
Each session indexes at most 4096 admitted owner/initializer keys and retains at
most 1 MiB of encoded typing products. The key limit counts an owner context
and each admitted local initializer separately; it is an admission budget, not
a function-body coverage ratio. Above the key budget, retained eligible
owners and their retained local queries are selected first. Remaining slots go
to new locals of selected owners, then new owners in declaration order. Every
unselected body/initializer compiles normally; the request retains available
hits rather than becoming entirely cold. Unsupported return-body products do
not consume a body slot; independently eligible local initializers may still
consume initializer slots. A local owner needs room for its context and at
least one initializer, so context alone cannot monopolize a slot. Owners
changing from return-body products to locals without retained initializer keys
join new admission rather than displacing existing retained units. Selected
plus queued new-local plans stay within the key budget;
trimmed pointers are cleared. Products above the byte budget are not admitted.
CapacityLimitedAnalyses/CapacitySkippedQueries describe partial admission;
ColdFallbackAnalyses continues to describe whole cold requests. The legacy
CapacityFallbackAnalyses counter remains zero. This deterministic retention
policy is not access-based LRU and can leave new candidates unadmitted while
existing eligible owners remain present.
The HTTP compile server currently offers this through an opt-in, 32-entry LRU
partitioned by canonical filename and analysis profile; default requests remain
stateless. Removed/ineligible owners lose product inputs and query memos; periodic
database compaction bounds historical key tombstones. This is not a persisted
build graph or a complete function cache. Scope/type/call dependency tracking,
general body products, module objects, cross-module inline/generic invalidation
and relinking remain required for Stage 2.

Tests compare all published Result fields (rekeying source nodes and preserving
invocation-local optimization-base alias relations), complete FuncType summaries,
LLVM IR and native O0 output. They prove zero additional primary expression/value
checks and element scans on hits, one-body edit locality, module identity,
options/safety invalidation, new errors, corrupt-record repair, removal/re-addition
and large owner churn. The compile-server integration is covered by output-parity,
opt-in, isolation, LRU and concurrency tests. It is not used by the CLI or native
build path, and an API test is not a whole-compiler speedup claim.

The local benchmark includes fresh parsing and full semantic analysis of 16
synthetic closed functions. It covers 1/32/128/512 literal leaves per function.
The first generic metadata replay was materially slower; the compact path
reduced overhead and allocations. A 3×300ms exploratory matrix found median
cold/warm times of 0.227/0.238ms, 2.623/2.546ms, 11.143/10.711ms and
47.904/41.250ms respectively. Large-case timings were noisy; tiny bodies still
regress. These numbers qualify neither general program speed nor a professional
incremental-build performance milestone. The path remains opt-in while coverage
and driver integration advance.

`FunctionProductRequirements` explicitly classifies all 58 current Result fields.
A new or removed field without an updated policy closes the product-index gate.
These policies are requirements, not implemented replay adapters. Mixed-phase
progress summaries need both body-seed restoration and fresh blocking/SCC work.
The live analyzer also needs hydration: backend APIs for builtin identity,
static branches, program regions and generic specialization delegate to it.
Its private summaries/deferred contributions still need their own adapter audit.
Semantic options, effective environment policy (including initialization-time
auto-reserve state), lookup sets and source authority must be tracked query inputs.
No function-body skip is enabled by this identity/schema groundwork.

Run the reproducible supporting workload with:

```sh
python3 compiler/bench/incremental_proofs.py /absolute/path/to/elisac /empty/output/directory
```

It alternates seven disabled/warm pairs after priming, checks semantic output and
diagnostic parity, and tests one body edit against clean compilation. Whole-object
avoidance is disabled. This synthetic nonlinear-proof workload measures proof
reuse only; report its priming cost and do not extrapolate it to whole-project
incrementality. Function-result reuse, semantic dependency tracking, diagnostic
rebinding, specialization handling, and incremental code generation remain work.

This is the long-term plan for "incremental builds like other languages." It is
deliberately staged: Stage 1 is the content-addressed foundation every mature
build system has (Go's `GOCACHE`, ccache); Stage 2 — true per-unit recompilation
— is built on top of it and depends on a separate-compilation model Elisa does
not have yet.

## Background: how Elisa compiles today

- A **compilation unit is one root file with its `include`s textually expanded**
  into a single translation unit, compiled to one object. `module Name:` is a
  namespace, not a separately-compiled unit.
- Objects are already produced separately and linked: the user module object
  (`writeNativeObjectViaClangIR`) and the runtime object
  (`writeDefaultElisaCoreRuntimeObject`), linked in `native_exec.go`.
- Caching already existed but only for two artifacts: the whole test-runner
  executable (`test_runner_cache.go`, keyed per unique test source) and the
  runtime object (`runtime_object_cache.go`, docs/93). Neither caches the user's
  program object, and neither makes a normal `elisac` build incremental.

The key reusable asset is the cache machinery in `test_runner_cache.go` /
`runtime_object_cache.go`: content hashing (`testRunnerCacheWrite*`), transitive
include expansion (`readSourceWithIncludes`), the **compiler-identity stamp**
(`compilerSourceStamp` — an executable-content digest, so rebuilt code changes
artifact identity), and an atomic stage-then-rename
publish that is safe under the parallel test suite.

## Stage 1 — content-addressed object cache (implemented)

`build_cache.go`. A plain `elisac -emit obj` consults a content-addressed cache
keyed on everything that can change the object's bytes; a hit serves the cached
copy and **skips parse + semantic analysis + codegen entirely**. It loads one
include-expanded program snapshot before lookup; the exact same snapshot is
passed to compilation on a miss.

### Key derivation (correctness-critical)
The cache key is `sha256` of:
- `goos`, `goarch`, Go version;
- the **full include-expanded source** from that loaded snapshot, its effective
  filename, and the sorted source-file authority set used by analysis;
- the **entire `cliOptions`** with only the three fields that cannot affect
  object content blanked (`output`, `addr`, `filter`). Folding in the whole
  struct means any codegen-affecting flag — `-O`, packed profile, target triple,
  `-g`, trace, `-permissive`/`-strict` (which changes whether runtime checks are
  emitted), future flags — is captured by default. Over-keying only costs cache
  reuse; under-keying would serve a stale object, so we err toward over-keying;
- the **compiler-identity stamp** (`compilerSourceStamp`);
- the resolved `clang` path (or `none`).

The dangerous failure mode for any build cache is a key that misses an input —
that yields a *silent stale build*. The whole-struct-minus-three-fields strategy
is chosen specifically so adding a new codegen flag later does not silently
under-key.

The v2 source-snapshot key prevents a save between identity derivation and
codegen from publishing a later revision under an earlier key. Decoded frontend-IR
inputs currently stay cold here: resolved source text alone does not identify the
complete AST, so that path needs a frozen full-bundle identity before reuse.

The lower llc object cache also hashes **exact IR bytes** (v2 namespace). A prior
regex normalized temporary-looking text throughout IR, including user string
constants, which could merge different programs. Provenance and runtime strings
remain part of object identity until a structured remapping design proves an
exclusion safe. Object, runtime-object and test-runner keys now include the backend's resolved
triple, CPU and feature string, using the same resolver as LLVM target-machine
creation. Test runners and runtime objects also include the sorted codegen
environment used by build-object keys. Native compiler/tool identity, external
dependency completeness and persistent artifact integrity still require further qualification; these fixes
do not establish that all native caches are ready for Stage 2.

### Storage & lifecycle
- Cache root: `$ELISAC_BUILD_CACHE_DIR`, else `os.UserCacheDir()/elisacore/build_objects`,
  else a temp dir. Content-addressed: `<root>/<key>/out.o`.
- Atomic publish: stage into a temp dir, `rename` into place; a lost race that
  finds the object already present is success.
- `ELISACORE_BUILD_CACHE=0` disables; `ELISACORE_TEST_CACHE_DEBUG` logs
  hit/miss-store lines. Tests: `build_cache_test.go` (hit, source-change
  invalidation, disabled-is-inert).

### Scope / not-yet
- Only the pure `-emit obj` path is cached. The native link/exe path
  (`buildNativeExecutable`) already caches the runtime object; caching the user
  module object there and the final link is the natural next increment, reusing
  the same key.
- No cache GC yet (entries are small objects; add an LRU/size cap later).

## Stage 2 — module-granular incremental compilation (design)

The goal users mean by "incremental like Rust/Go": change one file in a large
project and recompile only that unit and its dependents, then relink. Stage 1
gives build-*avoidance* (unchanged inputs → no work); Stage 2 gives
*fine-grained* rebuilds. It requires three things Elisa doesn't have yet:

1. **Separate compilation units with interfaces.** Today includes are textual, so
   the unit is the whole expanded root. Stage 2 needs each module compiled to its
   own object against the *interfaces* of its dependencies (an `.elisai`-style
   interface artifact: exported signatures, types, contracts), so a change to a
   dependency's *implementation* that doesn't change its interface need not
   recompile dependents. (docs/93 scoped and declined exactly this for the
   runtime; Stage 2 generalizes it.)

2. **A dependency graph + per-unit hashing.** Build a DAG from the include/module
   graph; hash each unit's own source plus the *interface hashes* of its
   dependencies. Recompile a unit when its source hash or any dependency
   interface hash changes; relink when any object changes.

3. **`@inline`-aware invalidation (the crux).** `@inline(always)` lets a unit
   inline a callee's *body* from another unit (the stage1 lexer relies on this
   for ~20% throughput). So an inlined function's **body**, not just its
   signature, is part of the consuming unit's input. The interface artifact must
   therefore carry the bodies of `@inline(always)` (and optimizer-inlinable)
   functions, and a unit must be recompiled when an inlined-from body changes.
   Getting this wrong produces a silent stale build — the same hazard as Stage 1,
   one level up. Conservative first cut: treat any change to a unit that exports
   an `@inline` function as invalidating all its importers.

### Why Stage 1 first
Stage 2's correctness rests on the same content-addressed, compiler-stamped,
over-keyed hashing Stage 1 establishes; Stage 1 is the substrate Stage 2's
per-unit keys plug into. Shipping Stage 1 also delivers most of the *felt*
speedup (no-op rebuilds are instant) at a fraction of the risk, while the
separate-compilation/interface model for Stage 2 is designed and reviewed.

## Open questions for Stage 2
- Interface artifact format and stability (how to hash a signature+contract so
  cosmetically-different-but-semantically-equal interfaces don't over-invalidate).
- Where the unit boundary lives: per file, per `module`, or per explicit build
  target in `elisa.json`.
- Cross-unit monomorphization of generics (`fn[T]`) — like `@inline`, the
  instantiated body crosses unit boundaries and must be tracked.
- Cache eviction policy and a `elisac clean`/cache-stats surface.


### Running compiler and tool replacement

Native cache compiler identity pins an executable descriptor before `main`.
Linux opens `/proc/self/exe`; other supported Unix systems open the executable
path during package initialization. Hashing is deferred to the first native
cache lookup and memoized, with descriptor metadata checked on every lookup.
Device/inode/change-time metadata detects in-place writes even when size and
mtime are restored. A changed pinned artifact disables reuse conservatively;
unlinking an old image during installation can also cause this fallback.
Unsupported metadata or an unavailable executable descriptor disables native
cache reuse rather than guessing an identity. The initial path-open race on
systems without a running-image handle is not eliminated by this design.

Tool digests are memoized only while their file fingerprint remains unchanged;
same-path replacement invalidates them. This detects replacement between
lookups, but does not bind a path-based tool invocation to the previously hashed
bytes. Replacement between identity lookup and execution remains an open
publication-coherence requirement. Neither change establishes artifact-content
integrity or complete external link dependency tracking.


### Test-runner source snapshots

Test-runner generation and selected-test execution now consume the same
`loadedProgram.source` used for analysis. Neither rereads the input filename.
Harness construction copies the bytes before appending generated law/property
drivers. Early-cache lookup and publication both identify the original loaded
source, effective filename and sorted source-authority set; generated harness
text is not substituted for the original source during publication. Decoded
frontend-IR programs remain outside the early source cache. Disabling the test
cache also disables early publication.

Race tests exercise a replacement source with a different cached runner, source
deletion after loading with successful native execution and a subsequent early
hit, property-driver publication followed by a warm hit, snapshot immutability,
and filename/authority invalidation. External foreign/runtime/tool inputs still
need a coherent snapshot through their compilation and cache publication.


### Reflection traversal overhead

A local CPU profile of fresh parsing plus cold/warm analysis identified repeated
reflection walks as remaining frontend work. Five syntax-oriented walkers now
share an immutable per-struct field plan, visiting pointer/interface/composite
fields while skipping scalar fields and lexer.Pos. Their callbacks and traversal
order are unchanged, including lowered call links. This helper is only for
walkers that already ignore maps and never inspect scalar/position callbacks.
It does not authorize skipping semantic checks or reusing call effects.

The differential traversal test compares visited pointer identity and order,
including nested composite fields and lowered calls. Full semantic and query
suites pass. On Apple M5, a synthetic 1024-node reflection walk measured median
98.5 microseconds for all fields versus 58.0 microseconds for the field plan,
with zero allocations for both (three one-second samples, variable system load).
That is a walker microbenchmark, not an end-to-end compiler speedup claim.
Cold/warm whole-analysis timings remain noisy and broader reuse is incomplete.


### Current parameter binding reuse

An admitted parameter must have a plain i64 type, no mutable qualifier or default
value, and a unique name. The checked parameter/identifier AST schemas fail
closed on field changes. Parameter count is bounded at 256. Ordered parameter
names enter the source digest; owner identity includes parameter arity, origin
and qualified name. At every reuse boundary, current scope lookup must yield a
SymbolParam owned by the current function, with the expected ordinal, canonical
i64 type and immutable, non-ghost, non-alias binding.

Identifier reads still execute normal analyzeExpr checks against those current
symbols. Only surrounding expression typing is restored. Replay follows cold
child-before-parent order, including left-to-right child traversal. No symbols,
scopes or identifier facts are serialized; identifier optimization facts are
recomputed. PrimaryExprChecks includes these fresh reads and
ReusedExpressionNodes excludes them. Return checking, summaries and CFG work
remain fresh as before. This expands real body reuse beyond closed literals,
but does not yet implement locals or call-dependent semantic products.

Tests compare every published Result field and current function summary,
including both overload bodies, and require independent warm overload hits plus
one-owner invalidation after an edit. Parameter renames/type/default/mutability
changes, removed bindings, bare/repeated reads and source shifts are covered.
The LLVM/native parity gate includes a parameterized function and a subsequent
edit. These tests qualify correctness, not broad performance or completion of
professional incremental compilation.

### Sequential scalar local initializer queries

Straight-line canonical-i64 functions may now reuse individual typed, immutable
i64 local initializers before a plain final return. Each initializer supports
the arithmetic grammar over parameters and preceding locals. The final return
expression may use unsupported syntax; it remains on the ordinary analysis
path and does not block reuse of earlier eligible initializers. Normal
declaration analysis creates each current SymbolLocal and runs conversion,
value binding, range/optimization, ownership, borrow and drop logic. Later
identifier reads, return typing and whole-function summaries remain fresh.
Planning accounts spelling bytes cumulatively across the owner, parameters,
local names and all eligible initializer expressions under the 1 MiB bound.

Query identity combines qualified owner/arity with a digest of the unique local
name. Unrelated insertion/removal therefore preserves existing owners. Inputs
include declared name, arithmetic syntax and only the bindings actually read.
Consumed locals now contribute independent binding-source query results;
parameters contribute current signature ordinals to the own-source digest. A
consumed source edit invalidates dependent arithmetic even if its type stays
unchanged. Unread preceding edits preserve hits. These are conservative source
fingerprints, not semantic constant values or equivalence proofs. Statement boundaries and fixed plain-i64 annotation presence are part of
the admitted grammar. Inferred/mutable/ghost/owner-bearing/explicit-binding or
duplicate/shadow declarations, forward/global reads, calls and nested control
flow take the cold path.

The hook revalidates current syntax, function owner and symbol ownership before
lookup. Local symbols must point at current declarations. Products store compact
expression tags plus the original initializer result type before conversions.
No AST, symbol or binding fact enters the database. All primitive products use
a shared publication boundary. Earlier unrelated diagnostics do not block reuse;
the candidate initializer's diagnostic delta is checked before publication.
Current diagnostic-producing expressions fall outside the closed cache grammar
and take the cold path; the delta check remains a defensive guard if an admitted
expression gains a diagnostic path.

LocalInitializersChecked/Reused report actual initializer work separately from
body counters. ReusedExpressionNodes excludes fresh identifier reads. Tests
cover metadata parity, consumed/transitive invalidation, unrelated edits,
removal/readmission, unsupported bindings, owner checks and errorful cold/warm
parity with a diagnostic-producing candidate kept on its cold path. The LLVM/native gate requires identical IR and exits
42, then 63 after a consumer edit, then 69 after an upstream-only producer edit. BenchmarkIncrementalLocalInitializers measures fresh
parsing plus full analysis with 64 warm initializer queries; this is measurement
coverage, not a broad performance claim.

`BenchmarkIncrementalLocalInitializerEdit` measures a single initializer edit
in an eight-function fixture with sixteen independent initializers per
function. Parsing is excluded from both timed paths; the retained session is
warmed on the base revision, then only the edited revision is timed. Full
published-Result parity is checked outside the timed region. Operator-safety
preparation is shared once per analysis, and retained local products are
prefetched as one query batch. A controlled Apple M5 A/B before the known-miss
filter used three samples of 200 edits each. Cold analysis averaged 1.332
ms/op and retained analysis 6.571 ms/op with prefetch; without prefetch, cold
averaged 1.314 ms/op and retained 6.901 ms/op. This historical comparison
showed a 4.8% retained-path improvement, with about 70 KB and 525 allocations
per retained edit. That run reused 127 initializer products and 254 expression
nodes, checked one initializer, and reused zero whole bodies; it remained 4.9×
slower than cold. These earlier numbers predate known-miss filtering and lazy
product publication. This fixture measures semantic analysis only, not
whole-compiler or edit-to-executable speed.

After known-miss filtering and lazy product publication, three 100-edit samples
measured fresh cold analysis at 1.629 ms/op, first analysis through a new
incremental session at 10.467 ms/op, and retained edit analysis at 7.911 ms/op.
The first session analysis checked all 128 local initializers, reused none, and
executed zero incremental queries. Against the same benchmark immediately
before lazy publication, first-session time moved from 10.919 to 10.467 ms/op
(4.1% faster), allocations from 11.29 to 10.86 MB/op (3.8% fewer), and query
executions from 256 to zero. Retained edit time moved from 8.348 to 7.911 ms/op
(5.2% faster). The retained edit reused 127 products and 254 expression nodes,
checked one initializer, and reused no whole bodies. A subsequent run after
batching product publication measured first-session analysis at 10.256 ms/op
and retained edits at 7.724 ms/op; query executions stayed at zero on initial
analysis and all products were published in one database revision. Against the
immediately preceding three-sample run, these were 2.0% and 2.4% faster. In
this latest sample first-session analysis remained 6.62× slower than fresh cold
analysis, and retained analysis remained 4.99× slower. The local A/B does not
establish a project-level speedup; planning and semantic checking still
dominate first-session cost.

A focused 300-iteration CPU/allocation profile of the first-session subbenchmark
passed the benchmark's cold-result parity check. Runtime scheduling and GC
dominated sampled CPU time, so the profile does not identify a stable compiler
CPU hotspot. Allocation data was clearer: `incremental.Session.PublishPrepared`
accounted for 43% cumulative allocation, lexical read publication for 28%,
lexical selection key construction for 10%, source-index encoding for 18%, and
lexical selection evaluation for 16% (cumulative entries overlap). This first
analysis constructs current lexical observations for later reuse even though it
has no prior products to consume. Those observations carry invalidation inputs;
reducing their cost must preserve current-scope and shadowing evidence. No
coverage reduction is justified by this profile alone.

`BenchmarkPrimitiveLocalInitializerPlanning` isolates plan construction for one
function with 128 independent local initializers. A direct expression visitor
now validates each local against the current visible-name set and resolves only
the identifiers actually used, rather than rebuilding and rescanning a
synthetic function signature containing every prior local. On the same Apple
M5, 50 iterations fell from 0.990 ms/op and 2.58 MB/op (4,840 allocations) to
0.240 ms/op and 0.43 MB/op (3,371 allocations). This is a roughly 4.1× planner
improvement, not a full semantic-analysis or edit-to-executable improvement.

A run of the initial prefix-invalidation version on Apple M5 (three one-second
samples, GOMAXPROCS=4) measured median
10.79 ms cold versus 10.97 ms warm, with 64 initializer hits. Allocations fell
from 123600 to 105345, while allocated bytes rose from 12.45 MB to 12.64 MB. This
fixture establishes no meaningful throughput gain; the path remains opt-in and
further dependency/replay work is required.


Capacity regression tests include a real 4097-function program with warm hits,
partial local admission, retained-owner insertion/removal, cross-owner priority,
unsupported caller starvation, and many retained owners gaining local declarations.
The 4096-node expression boundary permits the additional local initializer-result
tag (4097 total tags); one-over-limit expressions still compile cold.


### Operator lookup absence and unrelated impls

Arithmetic reuse no longer requires an empty impl registry. The safety input is
a versioned proof that canonical int/i64 receivers have no exact matching
`__add__`, `__sub__` or `__mul__` candidates. It enumerates candidates using the
SameType relation used by binary operator dispatch. Any match, including multiple
ambiguous matches, forces cold typing. An unrelated impl or unrelated method does
not change the absence value and preserves existing products.

The current numeric unary-minus path and numeric span-algebra path bypass
protocol dispatch. Generic-unifying method resolution is a separate path, so it
is not substituted for the exact resolver used by these binary operations. Tests
cross-check every claimed absence against the actual operator-call builder.

The proof is cached only within a fresh analyzer and keyed by its canonical
primitive type pointers. Impl collection invalidates it on entry and exit;
probes during collection are never memoized. Production receiver/method registry
writes are contained in that collection boundary. This avoids rescanning all
stdlib impls at every body/initializer boundary. No impl, symbol or analyzer
pointer is retained in the session product.

Source and native tests add an unrelated impl (reuse and exit 21), add an exact
i64 addition impl (cold lowering and exit 20), then remove it (reuse and exit 21).
Cold/warm LLVM text, published metadata and impl method summaries are compared.
The parity oracle now maps the whole current AST bijectively, including impl
syntax and declaration-visibility map keys. This expands usable environments;
it is not a general call/trait dependency graph or an end-to-end speedup claim.


### Explicit local binding-source query graph

The database now evaluates `primitive-binding-source` queries from compact
immutable recipes. Each reads only its own recipe input and calls Reader.Query
for admitted producer bindings. `primitive-local-typing` depends on that source
query plus configuration, operator absence and its captured typing product.
The normal return-body query remains separate. Recipes use fixed-size digests
and stable query keys, retaining no AST, symbol, scope or analyzer pointers.
Parameter identities are inline signature data; they are not yet general type
or callee interface queries.

All selected recipes are installed atomically before semantic analysis. A
producer taking a diagnostic/type/safety cold bypass therefore cannot expose a
previous recipe to its consumer. For a producer outside admission, the consumer
recipe carries its current immutable source fingerprint instead. For an admitted
producer the fallback field is omitted entirely: an upstream edit leaves the
consumer input byte-identical and propagates through the actual query edge.
Admission of an equal fallback as a producer query can change dependencies while
preserving the source result, pruning downstream typing reexecution.

Missing/malformed recipes and query cycles produce cache misses; no successful
partial result is published. Removal retires recipe/product inputs and all three
query kinds, and history compaction preserves only current recipe inputs before
cold repopulation. Byte rejection of a typing product does not invalidate the
independent binding-source query used by other consumers.

Besides 4096 admitted semantic units and 1 MiB of typing products, recipe admission
has defaults of 64 MiB reserved encoding space and 65,536 direct dependency records.
The 512-byte header/record bound and edge limit constrain the current format to
about 34 MiB of reservation even before the byte limit; encoded recipes are smaller.
These are current admission limits; recipes retain no AST memory.
New pending recipes reserve resources only after retained recipes. Rejected units
compile cold; consumers use the current compact fallback. Empty local-owner
contexts are removed so recipe pressure does not starve otherwise useful bodies.
RetainedBindingSourceBytes reports actual encoded bytes,
RetainedBindingSourceEdges counts logical query/fallback records, and
BindingSpecAdmissionMisses counts declined recipes.

Integration tests mutate only an admitted producer database input, verify consumer
recipe identity across ordinary edits, replace/detach consumed dependencies,
exercise fallback/equal admission with evaluator-execution counters, reject/recover
missing and cyclic inputs, enforce byte/edge budgets and retained-owner priority,
and churn 4,100 rename revisions while checking immediate memo eviction and bounded
history. Full metadata parity and native producer-only output changes qualify the
wiring. General call interfaces, path facts, SCC summaries, module objects and
persistent graph storage remain unfinished; no broad speedup is claimed here.

### Declared module boundary contracts (prerequisite, not cache admission)

Generated interfaces now retain the value preconditions, postconditions,
frame clauses and variadic flag represented by `ExternFuncDecl`.
Top-level extern formatting emits those clauses; the extern parser accepts
`changes`/`preserves`, and signature construction propagates declared `changes`
into `FuncType.FrameBounded`/`FrameWrites`. Protocol and impl method formatting
retains value/frame contracts, including lifted value contracts on default bodies.
Boundary conversion uses one function adapter and copies existing method nodes
without manually dropping their remaining AST fields.

Tests check first-generation clause presence, stable regeneration, contract/default
edits changing the interface while an ordinary private body edit does not, and
semantic source-to-`.elisai` parity from the analyzed AST. A struct field frame
permits a disjoint caller and rejects an out-of-frame write; a positive argument
and its default are accepted while zero violates the exported precondition.

This does not qualify `.elisai` as a complete incremental module cache key.
Remaining losses include return-region and lmut manifests, visibility and
associated-type defaults, lemma/ghost declaration kinds,
higher-order parameter contracts lifted out of Requires, protocol-bound call frames,
contract applications on methods, default-body proof attachments, and inferred
poststate/provenance/effect summaries with their boundary kind. Named-contract definitions and extern contract-derived frames are covered by
the normalization step below; effect/shape obligations remain incomplete.
Analyzed declarations retain both expanded clauses and their original `uses`
application. Interface normalization below prevents duplicate application, while
the general source formatter preserves unexpanded extern applications.
No module object reuse or performance improvement is claimed by this prerequisite.


### Named contract and law boundary normalization

Post-analysis interfaces retain `contract` and `law` declaration kinds rather
than converting them into callable extern functions. Contract formatting carries
its generic binders, value/frame clauses, scoped proof attachments and law
includes. Law formatting covers value/block, frame, effect and composite laws,
including protocol member laws. This keeps the definitions available for a fresh
downstream `uses` application.

Already-analyzed ordinary function boundaries emit their expanded value/frame
clauses and omit the retained original `Uses`. Import therefore does not apply the
same contract twice. Tests run three analyze/emit/import cycles with stable text,
then apply the exported contract in a new consumer and reject violating arguments.
The public interface generation path remains post-analysis. General extern source
formatting retains unexpanded `uses`; interface clones explicitly omit the
applications after expansion instead of relying on formatter omission.

Expansion also refreshes the existing function types' frame and canonical value
specification in place: value symbols were registered before expansion, so their
old signature could otherwise disagree with the analyzed AST and imported
interface. Canonical binders and type predicates stay intact. Extern expansion
now retains derived Changes/Preserves and refreshes its boundary value clauses.
A no-uses compilation skips the symbol-to-type publication index entirely.

Qualification includes source/native extern value contracts, source/extern
frame-law includes, generic contract proof attachments and all law syntax kinds.
Effect/composite/shape includes that become Fulfills use the exported obligation
representation below; verification of their final summaries remains open. Namespace
contract lookup and method applications also need broader qualification. The
remaining lifetime, visibility, higher-order and inferred-summary gaps above
continue to block general module cache admission. No speedup is claimed here.

### Exported law obligations

`ExternFuncDecl.Fulfills` now represents both declared and named-contract-derived
law obligations. The boundary adapter, top-level/protocol/impl parsers, formatter,
extern expansion and generated AST walkers retain the field. Extern function type
construction passes it to the existing frame-summary resolver; a declared frame
law therefore yields the same bounded writes before and after interface import.
Function-level effect/composite/shape obligations retain their law names rather
than disappearing when an ordinary function becomes an extern boundary.

Tests cover expanded composite/effect/shape obligations on source functions and
native externs through three stable analyze/emit/import cycles, explicit frame-law
summary parity and method/default-method obligation syntax. These tests establish
retention and frame semantics, not proof of an external body's effect/shape claim.
A bodiless boundary continues to require trust or a separately verified producer.

General incremental module admission remains unavailable. Inferred permission,
mutable-global and returned-callback effect rows are stored in FuncType, not the
source declaration; they still need a canonical exported semantic-summary record.
Effect-law checking must also use converged interprocedural rows rather than
assuming the early body-local row is final. Shape/measure verification uses its
own body/codegen evidence and must not be inferred from a retained law name.
Lifetime/provenance, declaration-kind, visibility and higher-order summaries also
remain on the full module reuse path. No throughput improvement is claimed here.


The frontend IR registry assigns the new extern field the next append-only field
ID (19), leaving prior IDs and wire version unchanged. A bundle round-trip test
compares both subject-free and parameter-bound obligations including positions.
Unknown-field tolerance in a wire reader is not evidence of semantic cache
compatibility: compiler/semantic identity must also participate in artifact keys.
General extern formatting now retains raw generic `uses` applications; only
post-analysis interface clones omit them. Protocol bound-call frame summaries
and method law-obligation conformance remain separate semantic qualification work.

### Dependency-driven final permission inference

Final effect inference now uses an adaptive worklist. Its first dense pass keeps
already-stable files on the existing one-pass path without dependency-map or heap
allocation. If execution, mutable-global, returned-callback or nested-lambda rows
change, it seeds a tracked pass and subsequently revisits only readers of changed
function rows. Current/next priority queues preserve declaration order and the
existing total `function count + 4` round limit. The original convergence cap is
retained; this is not a new proof that arbitrary effect equations converge.

Dependencies are recorded at the semantic row readers, including current named
declarations behind specialized/function-value copies and callback-factory source
names. Both empty and nonempty reads count. Destructor effects, cast/init/operator
lowerings and callback transfer use those same readers. Nested lambda collectors
publish mutable-row changes independently of their owner's execution effects.
Consumed edges are replaced on reevaluation. This graph belongs to one fresh
Analyzer invocation and is released afterward; it is not yet a persistent module
or inter-invocation semantic cache.

Tests compare every published effect row against the previous ordered sweep on
100 seeded graphs and focused chain/SCC/diamond, specialized-copy, alias,
returned-callback, callback-transfer, lambda-global, destructor, cast and lowered
initializer/operator cases. Complete-result comparison also runs both solvers
through fresh full analysis, including diagnostics and metadata. Stable/no-call
cases require exactly one evaluation per function and observer cleanup.

Local Apple M5 / Go1.27.2 / GOMAXPROCS=4 measurements use three repetitions.
Synthetic initially-empty inference chains (100 iterations per repetition) have
median sweep/worklist times of 0.558/0.0611 ms at16 nodes and29.83/0.511 ms at128.
Stable128-node inference is0.429/0.437 ms; the fast path adds3 allocations/~32bytes,
so stable throughput is approximately equal within these short samples.

The separate source benchmark includes parsing and complete semantic analysis
(20 iterations per repetition), with an extern allocation effect at the end of a
call chain:

| Functions | Sweep median | Worklist median | Allocations, sweep → worklist |
| --- | --- | --- | --- |
| 16 | 0.616 ms | 0.394 ms | 8,943 → 5,492 |
| 128 | 18.9 ms | 2.79 ms | 256,831 → 35,037 |

These synthetic semantic workloads exclude LLVM emission/linking and do not
establish a real-project incremental speedup. The implementation continues to use
the permission-row equations; resolved type/environment identity,
converged effect-law validation, lossless exported semantic summaries and actual
persistent call/module queries remain required work.

### Typed effect-row identity

Permission-row canonicalization and equality now retain abstract type arguments
and recursive `via` realization metadata. `Writer[i64]` and `Writer[sview]`, or
the same abstract effect realized through different concrete capabilities, no
longer overwrite or compare equal solely because their family/member match.
Family-wide row suppression applies within the same instantiation/realization.
Authorization keys remain family/member based, preserving existing family grants.
Plain rows keep the existing map/sort path without syntax hashing.

Instantiated rows use the bounded position-independent syntax fingerprint for
TypeArgs/Via. This is conservative syntax identity, not resolved nominal-type or
alias equivalence; a persistent semantic product must additionally validate the
current environment, global/type/lookup inputs and compiler schema. No retained
effect result is admitted across compilations by this checkpoint.

If a descriptor exceeds fingerprint limits, exact invocation-local structural
comparison still bounds row union. Duplicate oversized effects in recursive or
diamond graphs therefore do not multiply on every inference round. Equality of
those local values supplies no durable digest: permissionInstantiation reports
failure and a future persistent cache must decline admission. Tied opaque row
order follows local encounter order; no portable canonical key is claimed.

Tests cover specialization/realization edits propagating through unchanged
caller graphs, two Writer specializations surviving source analysis, position
relocation, family/member authorization, permutation of encodable rows, and
oversized cyclic/diamond plus duplicate-self-call union bounds. These preserve
the distinctions a retained dependency value will need; persistent row hydration
(including nested lambda writes), complete semantic input recipes and real-project
incremental performance remain unfinished.

### Server-local source token reuse

Compile-server requests with `incremental: true` now share a bounded token cache
across emit/analysis profiles. Exact loaded source bytes and the diagnostic
filename form the key. Exact hits reuse the full token sequence; changed inputs
can reuse inspected prefixes through the checkpoint path below. Decoded frontend IR keeps
its own fresh AST path and bypasses source-token lookup.

Each admitted cold entry lexes a private source copy: lexer Text/Suffix strings
are zero-copy views, so a shallow token copy alone would retain mutable caller
storage. The cache also owns its filename. Every parser receives a separate token
slice because contextual keyword recovery mutates token kinds. Parser notices remain fresh; exact diagnostic-free revisions can use the
pristine syntax replay path described below. AST/map creation and semantic
analysis remain fresh. Lexer.New still registers
parity source on hits, and existing parse/lex failure cleanup remains in place.

The independent LRU defaults to32 entries and512 MiB of accounted retained payload
per server. Accounting includes the complete owned source backing buffer, token
array, string lengths and diagnostic text; shared source/token text may be counted
more than once. It is not an allocator/RSS limit. Oversized inputs stay cold;
concurrent duplicate misses retain one entry, and hit token copies happen outside
the cache lock. Stateless requests do not populate this cache.

Tests compare output/status/diagnostics through warm/edit/revert requests, ensure
parser keyword rewriting does not corrupt later hits, vary filenames, mutate the
caller's original bytes, verify fresh AST nodes, bound comment-heavy source
backing storage, exercise LRU eviction/concurrent copies and bypass decoded IR.
The mutation oracle lexes independent expected bytes so aliasing cannot corrupt
both expected and actual values together.

Local Apple M5, Go1.27.2, GOMAXPROCS=4 parse-only measurements use3 repetitions of
30 iterations on generated arithmetic functions:

| Functions | Cold lex+parse median | Cached-token parse median |
| --- | --- | --- |
| 16 | 55.7 µs | 35.4 µs |
| 256 | 0.969 ms | 0.655 ms |
| 2,048 | 9.74 ms | 5.32 ms |

At2,048 functions, allocated bytes per parse fall from19.97 MB to8.12 MB and
allocation count from34,837 to30,737. Cold admission pays for the owned snapshot
and retained token copy; these warm measurements exclude priming, semantic work,
LLVM/linking and network overhead. Real-project incremental speed and finer-grained
parse/call/module dependency reuse remain open qualification requirements.


### Checked token-prefix reuse after edits

On a new digest for the same filename, the server selects the most recently used
retained revision as a hint. An older concurrent revision is also safe: reuse is
validated against the actual new bytes rather than a presumed editor generation.
The shared LRU remains bounded by revision count and accounted bytes; an edit-heavy
file can evict other entries. There is no unbounded secondary latest-file index.

The lexer records a checkpoint every256 tokens, capped at4,096 checkpoints and
16 MiB of checkpoint storage, including reserved header capacity, exact cloned
stacks/pending buffers, filename and diagnostic text. Source-token admission also
accounts retained slice capacities under the existing512 MiB server budget.
Comment-heavy token arrays are trimmed when their capacity is disproportionate.

A checkpoint owns the full Lexer state: logical filename/line/column, cursor,
layout/grouping stacks, pending tokens/pattern blocks, last token kind and errors.
It also records the maximum source range inspected so far. All source-sensitive
byte/slice/existence reads go through tracking helpers, including distant `when`
lookahead, boolean continuation and unsuccessful EOF probes. UTF-8 lookahead
records up to four bytes. A field inventory and source-access guard require review
when those state/read channels change.

Only a checkpoint whose entire observed prefix is unchanged can resume. Earlier
tokens are reused; the suffix is freshly lexed with actual new offsets and #line
mapping. Every source-backed prefix string and checkpoint view is rebound to the
new owned source buffer. Decoded escape strings remain intact. The new record
retains no previous SourceTokens/source owner chain. Parsing and semantic analysis
still receive fresh ASTs, maps and mutable token slices.

Differential tests compare every token field and errors against a fresh lexer for
400 seeded edits, insertion/deletion/revert, escaped strings/chars/numeric suffixes,
multiline comments/f-strings, grouped layout, distant contextual lookahead, line
mapping and incomplete UTF-8/EOF append. Mutating the prior owned source after a
resume cannot corrupt the new revision. Server histories span lowered/semantic/
LLVM/IR modes with capacity1 (preventing historical exact hits), and concurrent
edited revisions exercise immutable checkpoint sharing under the race detector.

Final local Apple M5 / Go1.27.2 / GOMAXPROCS=4 measurements use3 repetitions of
30 iterations. The edit benchmark alternates a last-function literal with a
one-entry cache, so every measured request is a digest miss with prefix reuse:

| Functions | Cold lex+parse median | Tail-edit lex+parse median |
| --- | --- | --- |
| 256 | 0.939 ms | 0.745 ms |
| 2,048 | 9.41 ms | 5.75 ms |

At2,048 functions, bytes/parse fall from19.97 MB to12.63 MB and allocation count
from34,837 to30,937. A separate current exact-hit run measured5.04 ms. Ordinary
cold parsing measured9.28 ms in that run, consistent with no obvious regression
against the previous9.74 ms short sample; these are not controlled statistical
claims. Earlier edits, files below the checkpoint stride and cold admission may
benefit less or pay tracking/copy costs. Hashing/common-prefix comparison and
source/token copying remain linear. This lexer-only checkpoint still reparsed the
entire AST; the declaration-prefix replay extension below can now avoid prefix
parsing after snapshot admission. These
synthetic tail-edit results do not establish general edit or whole-project compile
speed. Declaration-level parsing, persistent semantic/call/module reuse and
end-to-end project qualification remain active requirements.


### Pristine syntax replay for repeated source revisions

Exact filename and source-byte hits in the opt-in compile server can bypass
parsing using an owned pristine AST snapshot. Capture happens after successful
parsing and before semantic analysis. Parses emitting any notice or error are
not admitted, so warning suppression and diagnostics retain their normal path.
Decoded frontend IR continues to bypass this cache. Parser construction currently
has no options; future syntax-affecting options must participate in this key.

Capture uses generated typed traversal to own all strings and mutable graph storage,
with bounded work, depth and retained payload. Partial/overlapping backing views,
private fields, unknown struct schemas and unsupported storage decline admission.
Generated typed replay preserves shared nodes, cycles, map-key node identities,
and full capacity-visible slice contents, while allocating private mutable nodes
and containers for each request. Immutable snapshot-owned strings can be shared.
The checked-in generator has a freshness test; runtime field-name/type validation
and generated interface switches refuse stale or unsupported schema coverage.

Snapshots share the token cache's 32-revision/512-MiB LRU payload budget. Container
accounting includes estimated overhead; this is not a process RSS bound. Capture
and replay run outside the cache lock, with publication rechecked under the lock.
Capture is delayed until the revision is requested a second time: a one-off edit
pays no snapshot capture cost. The second request still parses and pays capture;
only subsequent exact hits benefit. Edit histories still use lexer checkpoints
and fresh parsing in this initial snapshot checkpoint; the extension below adds
declaration-prefix replay.

Ownership fixtures cover source mutation, shared and cyclic nodes, pointer-keyed
visibility, full slice tails, partial-view rejection, concurrent copy/mutation,
and generated-versus-reflection replay. Server tests cover shared-budget
accounting, edit invalidation, decoded-IR bypass, diagnostics/filename parity,
and semantic/lowered/LLVM/IR mode transitions including default argument mutation.

On Apple M5, Go1.27.2, GOMAXPROCS=4, three 100-iteration runs of the
2,048-function parse benchmark measured token-only parsing at5.78/6.45/6.45ms
and exact snapshot replay at3.37/2.89/2.95ms. Allocations fell from8.12MB and
30,737 allocations to7.38MB and26,824 allocations per operation. A full compiler
test suite was running concurrently, so these are indicative local microbenchmarks,
not controlled end-to-end project speedups. Warm setup includes two untimed parses
to publish the snapshot; it excludes the second-request capture penalty.

Separate 30-iteration capture runs measured12.37/12.56/12.31ms for this fixture
(10.86MB and55,510 allocations). That cost is paid in addition to parsing on the
second request. Revisions used only twice therefore regress; several later exact
hits are needed to amortize capture. This admission policy is a limited first
step, and cheaper capture or workload-aware admission remains performance work.

Validation: snapshot/generator tests and focused root race tests pass, including
cross-mode/default-argument replay. The broader root compiler-package suite failed
in299.787s; parser and generator packages passed in2.615s/0.681s. Its output was
large and truncated, so this is not an all-failures baseline comparison. Three
visible failures (selected-test phase logging, packed-ML runtime warnings, and
allocator-interface effects) also fail with the parent revision's parser/cache
implementation overlaid via Go's overlay mechanism (2.152s). The full compiler
suite is therefore not green, and no all-errors-resolved claim is made here.


### Typed snapshot capture

Capture now has a separate generated method family from warm replay. The capture
methods own/intern strings, check work/depth, charge storage before allocation,
and reject overlapping storage before publishing a snapshot. Replay methods retain
their previous shallow assignments for immutable strings/positions and contain no
capture ownership/budget traversal. An initial shared-method implementation slowed
warm replay in local measurements; it was replaced before this checkpoint.
The reflection copier is retained only in tests as an independent reference.

Generated capture is compared with owned reflection capture, including equal
payload accounting, caller-buffer mutation, strings/nodes beyond slice length,
shared containers and pointer-keyed declaration visibility. Depth limits are
qualified explicitly; partial-view and byte-budget rejection tests still apply.

With the same M5/Go1.27.2/GOMAXPROCS=4 fixture and three100-iteration runs, the
parent implementation (Go overlay of the two production snapshot files, excluding
the added capture helper) measured capture15.54/11.83/12.94ms. Typed capture
measured5.17/5.83/5.76ms. Capture allocations fell from10.86MB/55,510 to
9.31MB/28,934. Parent replay was2.78/2.85/2.76ms and current replay
2.53/2.62/2.65ms. These sequential local microbenchmarks supersede the reflection
capture figures above; they do not establish end-to-end project performance.
Second-request capture still costs work and must be amortized by later exact hits.

Final typed-capture verification: full AST race tests and generated-output freshness
race tests passed in1.214s/3.678s; focused snapshot/cache root race tests passed
in2.098s; all TestCompileServer tests passed with race instrumentation in1.830s.
Unknown test-only interface implementations are rejected and typed nil expressions
survive capture/replay. The broader compiler-suite failures recorded above remain
unresolved; this checkpoint does not claim a full-suite pass.


### Edited-source declaration-prefix replay

An admitted pristine snapshot can now supply the unchanged declaration prefix of
an edited source revision. The server selects the latest retained same-filename
snapshot before lexing can evict it, compares complete token values and diagnostic
positions, and selects the largest checkpoint whose cumulative inspected prefix
still matches. It clones that declaration graph privately and parses the remaining
source with the checkpoint's outgoing parser state. Successful prefix use touches
the retained revision in the shared LRU; no separate unbounded history is kept.

The parser records at most 4,096 safe top-level boundaries. Cursor initialization,
advance and seek observe their destination; offset/index/span probes observe
lookahead too, including failed EOF probes. Speculative rewinds never erase the
read watermark. Explicit token-count reads require equal lengths. Capacity-only
estimates remain outside syntax dependencies. Static tests audit all current
Parser fields, raw token-read locations and cursor writes. Ordinary cur/peek
retain their original access path, and trace collection runs only for repeated
revisions eligible for snapshot admission. An untraced concurrent request cannot
publish a snapshot before a traced request and permanently lose its checkpoints.

Checkpoints preserve rebind/nursery/by-par counters, the one-per-file ForeignEnum
error-set flag and visibility state. Other modes, nesting, scope stacks, guards
and pending buffers must be quiescent before a boundary is recorded. Synthesized
pending declarations are drained before recording the declaration count. The
trace contains owned scalar/string metadata rather than AST/token pointers and
shares the existing 512-MiB accounted-payload budget with tokens and snapshots.
Capture still occurs before semantic mutation and only for successful parses
without notices or token-recovery rewrites.

CopyPrefix limits declaration capacity to the retained prefix and rebuilds
visibility keys only for reachable cloned declarations, including nested module
members. It preserves cross-node sharing and returns no mutable snapshot storage
or hidden suffix graph. Reused strings are snapshot-owned and immutable; token
position equality avoids guessed source relocation. The rest of the file remains
fresh, including the edited declaration itself. Source/AST metadata from a later
admitted revision owns its storage rather than retaining a snapshot ancestor chain.

Qualification includes 160 seeded independent edits against an admitted revision
(including malformed syntax and recovery), plus six successive revisions with
three requests each under one- and two-entry eviction. AST comparison uses a
bidirectional node correspondence for pointer-keyed visibility. Dedicated cases
cover identical-looking nested/suffix visibility nodes, rebind and nursery/par
counters, typestate expansion, two foreign enums across a replay boundary, and an
edited next-declaration token invalidating prior lookahead. Server tests require
actual nonzero declaration replay across unique edits and semantic/lowered/LLVM/IR
transitions after default-argument mutation. A deterministic publication-order
regression covers the untraced/traced concurrent admission race.

Prefix replay still requires an admitted revision. There is no source relocation,
declaration-body slicing, or persistent general semantic/module/object dependency
reuse yet. First-use and unsupported revisions remain cold; early edits may have
no reusable boundary. A revision with parser notices cannot become a retained
prefix or suffix provider; changed suffix diagnostics are still produced fresh.
Token comparison, prefix graph cloning and some metadata work remain linear, and
snapshot admission still requires amortization. The complete professional
incremental compiler goal and whole-project performance qualification remain open.

### Edited-source declaration-suffix replay

The parser can also reuse an unchanged declaration suffix after parsing an edit.
It admits only a prior diagnostic-free syntax snapshot with the same token count,
and only checkpoints whose old suffix reads never reach into the changed prefix.
The matching tokens must retain their exact source positions; parser counters,
visibility state, and other boundary state must also match. The suffix AST is
deep-copied from the pristine snapshot before it is appended, preserving the
existing rule that semantic passes receive private mutable syntax trees.

This currently covers equal-width edits that preserve token count and all suffix
positions. Insertions, deletions, and other edits that shift suffix positions
decline suffix replay; there is no AST position rebasing or body-level syntax
slicing. A relocation implementation must account for `lexer.Pos` start/end
offsets, lines and columns, shifted token/checkpoint indices, suffix-local reads
of token-stream length, and position-derived names such as `__auto_<offset>`.
The targeted early-edit benchmark changes a six-digit literal to another
six-digit literal in a 513-function source. One captured three-sample run on
Apple M5 measured median 2.672 ms with token reuse and fresh parsing versus
2.318 ms with suffix replay (13% lower). Allocations fell from 7,765 to 6,827
per operation, while allocated bytes rose from 4.44 MB to 5.50 MB. The suffix
samples ranged 2.303–2.780 ms, so this is narrow synthetic evidence, not a
whole-project performance claim. The command used `GOMAXPROCS=1 go test ./src
-run '^$' -bench '^BenchmarkSourceSyntaxCacheEarlyEditSuffix$' -benchtime=200ms
-count=3 -cpu=1`; the captured output is in
`compiler/build/verification/syntax-suffix-20261018/benchmark.txt`. Insert/delete and
multiline position parity still need qualification before broadening reuse.

Final local throughput measurements (M5, Go1.27.2, GOMAXPROCS=4, three
100-iteration runs) use 2,048 simple functions and distinct edits to the last
function. Lexer reuse with fresh parsing measured 5.91/5.45/5.99 ms; admitted
prefix replay measured 3.58/3.98/3.62 ms. Both include digest lookup, edited
lexing, token comparison and parsing/replay; setup's two original parses and
snapshot capture are excluded. The benchmark requires at least 2,047 replayed
declarations per edit, preventing an accidentally cold path from appearing valid.
Allocated bytes rise from 11.31 MB to about 14.21 MB, while allocation count falls
from 26,829 to about 22,889. This deliberately favors throughput over memory.

Cold/one-off controls use the parent parser/cache implementation through Go
overlays and independently built binaries in parent/current/current/parent order
(100 iterations per process). Parent cold runs measured 9.60/9.71 ms and current
12.76/10.10 ms. Parent lexer-only edits measured 6.68/5.93 ms and current
7.39/5.92 ms; allocation counts and bytes are unchanged. An earlier grouped
100-iteration comparison was closer (parent/current cold 10.34/10.45 ms and
lexer-only edits 6.34/6.35 ms). The observed spread and residual cold overhead do
not establish zero regression. The initial tracing draft was substantially slower
and was replaced by destination observation plus demand-based trace collection.
These are synthetic parsing measurements, not whole-project compile speedups.

Final verification for declaration-prefix replay: the full semantic package passed
in 71.500 s. Full parser/AST/generator race suites passed in 33.642/1.276/4.121 s;
compile-server and source-cache race tests, including chained snapshots and the
admission-order regression, passed in 2.694 s. The changed lexer prefix-comparison
code also passed the full lexer race suite earlier in this checkpoint (10.743 s).
These checks do not supersede the previously recorded failures in the broader
root compiler-package suite, which was not rerun here.

### Local project probes and query-key allocation

The opt-in TestLocalIncrementalProjectProbe loads a real entry file with the normal
include loader, then compares stateless and session frontends on the same expanded
bytes and sourceFiles authority. It records source/diagnostic hashes, stage times,
source-cache counters and semantic query statistics. Loading, HTTP/JSON transport,
semantic-report formatting, backend work and linking are outside each timed
frontend call. Error-bearing runs compare status and exposed diagnostics; they do
not prove equivalence of every internal semantic Result field. The normal test
suite skips this probe unless explicitly enabled.

Use ELISA_INCREMENTAL_PROJECT_PROBE=/absolute/path/entry.elisa with
`go test -C compiler ./src -run '^TestLocalIncrementalProjectProbe$' -count=1 -v`.
The optional ELISA_INCREMENTAL_PROJECT_PROBE_SNAPSHOT_OUT and SNAPSHOT_IN variants
write/read a local expanded-input JSON snapshot, preserving filename and loader
sourceFiles. This freezes source input for comparisons while other project work
continues; it is a developer probe artifact, not a hermetic build/dependency cache.
ELISA_INCREMENTAL_PROJECT_PROBE_PROFILE_ONLY=1 runs only the stateless baseline
for profiling, without the repeated/edit parity sequence. Normal probe execution
checks the baseline, three identical session requests and an added pure function
against an edited stateless reference. Early/dependency/error-to-fix edits remain
outside this probe's current history coverage.

Local measurements on the Stage1 lexer component covered 177,422 expanded bytes
from 27 files. Parser-component measurements covered about 2.5 MB from 152 files
and 363,000 tokens. Both preserved exposed diagnostics, but original component
bodies had zero semantic-query reuse. The parser component had 425 parser notices,
so it could not supply a retained parser trace; independent whole-file snapshot
capture also exceeded its work/depth bound. Appending the probe's simple function
admitted only that new 223-byte scalar product. Analysis dominated: roughly
100–175 ms for the lexer component and 5–7 seconds for the parser component,
versus about 8–10 ms and 126–139 ms parsing respectively. These exploratory runs
show admission/coverage gaps, not a project-level incremental speedup. Process,
solver and allocation warmup also differ between sequential stages.

CPU profiling of the parser diagnostic workload attributed about 55% of samples
to memory clearing. A single-baseline allocation profile sampled about 5.18 GB:
tracked-type graph cloning was the largest flat allocation site (~1.55 GB),
followed by mutable-borrow classification (~0.61 GB) and canonical type-ID key
construction (~0.58 GB flat). Generic/shape scope wrappers have high cumulative
cost because they invoke body analysis; their empty-context setup is not the
identified bottleneck. Persistent resolved body/type queries and cheaper isolated
flow-state products remain the main architectural work.

Canonical key construction now reuses its active-path scratch workspace. Each
call still encodes the current graph, with the same traversal-local cycle numbers
and unsupported-type behavior. Pop clears source-type references; backing arrays
above 64 frames are not pooled. Returned strings keep their own builder storage.
No type result or process-local numeric TypeID becomes a persistent query key.
Tests compare the previous unpooled workspace, actual cycles, shared DAGs, deep
paths, unsupported types, mutation between calls, reference clearing and concurrent
isolation. The microbenchmark rotates five short type fixtures; deeper/cyclic
fixtures are correctness checks rather than its measured workload.

On M5/Go1.27.2/GOMAXPROCS=4, three 200,000-iteration runs measured unpooled
171.8/189.1/131.4 ns and pooled 84.07/93.35/112.5 ns per key. Allocations fell
from 473 B/two allocations to 89 B/one allocation. A frozen parser snapshot
(SHA-256 511b5939bf14e412dbe3e287d308d4d65437a7fcf1701de590c468310cd371f4)
was then profiled with parent typeid.go overlaid and with the new implementation.
Both emitted the same 776,280 diagnostic bytes and diagnostic digest. Sampled
whole-probe allocation totals were 5,461.64 MB and 4,693.33 MB; canonical-key
cumulative allocation fell from about 732 MB to 115 MB. The single timed frontend
pair was 4.291 s versus 4.149 s, which is too little evidence for a repeatable
project throughput claim. Earlier source hashes differed between runs and are
explicitly excluded from before/after comparison. The component still has no
original-body incremental reuse after this allocation change.

Verification for the scratch/probe checkpoint: full semantic suite passed in
61.387 s; focused canonical-key/concurrency race tests passed in 1.747 s and
compile-server/source-cache race tests in 2.418 s. Real lexer/parser repeated/edit
probes and the frozen parent/candidate diagnostic comparisons passed. The broader
root-suite failures recorded earlier remain unresolved. Next architectural work
must expand actual resolved body/callee/type dependency products and isolated
flow-state hydration; the allocation optimization does not close that coverage gap.

### Resolved mutable-borrow capability queries

The opt-in semantic session now retains the Boolean capability classification of
resolved nongeneric type graphs. Unlike the initial i64 body products, this
reaches aggregate types in the existing parser component. Each product owns an
immutable structural description; no AST, Symbol, Type pointer, substitution
context or mutable flow state is retained between analyses. Relevant field flags,
container elements, enum/error payloads and mutable-reference flags participate in
the description. Generic instances and unknown types use the original traversal.
Queries run only after declaration shapes freeze. A bounded invocation-local memo
avoids repeatedly describing the same current Type pointer; it is discarded with
the analyzer. Its 65,536-entry limit favors throughput over a smaller working set.

The projection is bounded to 4,096 distinct nodes, 4,096 edges and 64 KiB,
including collection entries and names. Graphs above 256 nodes require the
order-independent depth proof described below; smaller graphs keep the cheap
node-count proof. A retained answer cannot suppress the original depth-limit
diagnostic in any traversal order. Incomplete traversals are never published. Recursive intermediate answers
are not cached: their seen-set context can make a negative result root-dependent.
The session retains at most 1,024 products and 8 MiB of accounted payload; periodic
rebuilds bound database input tombstones as well as live query entries. Trivial
scalar leaves and directly mutable references/views bypass the persistent query.

Fresh-graph/edit/revert tests compare complete semantic Results, current AST
annotations and global permission rows. Coverage includes cycles, ghost/phantom
fields, payloads, unsupported generics, depth diagnostics, bounded cache churn,
invalidated database inputs and mutable-to-readonly aggregate changes. Returning
an immutable global aggregate containing a mutable view still requires
Global.Write after warm reuse and after reverting a readonly edit.

The frozen 2.5 MB parser-component probe preserves the previous source and
776,280-byte diagnostic digests across cold/repeated/added-function analyses.
With the initial 4,096-entry local memo, only 206 unique retained products were
computed; each subsequent analysis reused 16,811 products and 47,246 local
answers. Sequential frontend times were 4.833 s stateless, 4.897 s first session,
5.560/5.384 s repeated, and 5.227/5.162 s stateless/session after appending a
function. These establish real query coverage and diagnostic parity, not an
overall throughput improvement. The larger local memo passed the same frozen-source diagnostic checks: each
subsequent analysis used 59,409 local answers and 4,648 retained products, while
unique computations stayed at 206. It cuts repeated projection/database work;
that run overlapped validation jobs, so its elapsed times are not comparative
throughput evidence. Retained capability payload stayed at 652,286 accounted
bytes. General body/callee products, flow-state hydration and whole-project
incremental compilation remain unfinished.

Focused capability/projection/invalidation and compile-server/source-cache race
checks passed (semantic 1.825 s; root 1.484 s). The initial full semantic run
passed in 56.635 s; the final full suite passed in 60.799 s.
Existing broader root-suite failures and the historical Stage1 diagnostics are
not resolved by this query work. All runs were local.

### Effect-summary completion boundary

Final permission inference now records whether its work queue drained. A bounded
solve returns evaluations, rounds, pending functions and a convergence flag;
`Result.PermissionEffectsConverged` publishes only that flag. Zero functions are
complete, an unchanged initial dense pass is complete, and a nonempty queue at
the budget boundary is incomplete. The production cap and declaration-order
scheduling remain unchanged. A custom test solver which does not establish
completion leaves the flag false. This status is a recomputed aggregate in the
complete-function-product inventory, rather than a replayed body annotation.
Future persistent callee summaries must require it; no general callee-summary
replay is implemented by this status field.

Tests force short budgets on a reverse dependency chain, distinguish pending work
from convergence, check the completed rows against the reference sweep, and check
the public Result boundary. The existing full-analysis sweep/worklist differential
also compares the new field; its reference solver explicitly establishes completion
on an unchanged pass. This keeps a cutoff from silently authorizing a persistent
summary while preserving the existing equations and diagnostics.

The local project probe accepts `ELISA_INCREMENTAL_PROJECT_PROBE_CPU_OUT` to
profile only its warmed `incremental_repeat` analysis. Loading, preflight syntax
capture and earlier analyses are excluded from that profile. The first such
frozen-parser profile sampled 5.02 s CPU: 51.79% flat in memory clearing and about
0.40 s in the raw mutable-borrow traversal's stacks. Recursive cumulative totals
inflate the traversal's apparent cost; the local projection-decline fallback
accounts for most of its remaining work. Structural projection and retained-query
lookup account for roughly 80 ms and 30 ms respectively in this sample. The probe
passed exposed diagnostic parity in 32.381 s. This evidence favors eliminating
body-analysis allocation/reanalysis and representing generic substitution inputs;
it does not establish a whole-project incremental speedup.

Validation for the completion boundary: full semantic suite 66.308 s; focused
worklist/completion/product/capability race checks 2.445 s; exact-budget boundary
checks 0.305 s. Frozen parser diagnostic parity passed again in 28.593 s. The
frontend wrapper returns a nil Result on errors, so a convergence flag cannot be
inferred from that failing-project probe: an interim false value in that log
represented a missing Result, not a proven inference cutoff. The probe now emits
the flag only when a Result is available; the semantic completion fixtures check
the flag directly. Broader root-suite failures remain outside this checkpoint.

### Depth-safe reuse of wide type graphs

The first type-query admission limit conflated total graph size with recursive
path depth. Wide types can now reuse capability products even above 256 nodes.
The common small-graph projection remains the first pass. Only a node-budget
miss retries with the larger bounded projection and records all edges, including
references to previously visited nodes. Strongly connected components are then
weighted by their node counts. A longest-path sum in the condensation DAG bounds
every simple DFS path through the original graph; requiring that sum to fit the
semantic depth limit also accounts for a closing/seen edge, because the original
classifier checks depth before its seen set. Large recursive components and deep
alternative routes to an already visited shared tail remain cold.

A successful raw traversal by itself does not establish this property: another
map iteration order could reach a depth-limited branch before finding a mutable
capability. Such incomplete/potentially incomplete inputs still are not memoized
locally or retained across analyses. Generic substitution inputs remain outside
this projection. The larger proof does not authorize caching generic fallback
answers or recursive intermediate results.

Tests cover fresh wide graphs through mutable/readonly/revert histories, a deep
branch beside a short-circuiting mutable sibling, a long prefix feeding an
already projected shared tail, and wide graphs sharing a small recursive
component. An independent exhaustive simple-path oracle checks 500 seeded small
random graphs; large DAG/chain/cycle fixtures exercise the linear SCC algorithm.
The frozen parser probe retains the same diagnostic digest and expands unique
products from 206 to 208. Per repeated analysis local hits rise from 59,409 to
59,458, retained hits from 4,648 to 4,650, and projection declines fall from 556 to
554. Retained payload grows from 652,286 to 702,566 accounted bytes. This is a
small real coverage gain, not evidence of a whole-project throughput improvement.
General function/callee products and isolated analyzer-state hydration remain the
primary architectural work.

Validation: full semantic suite passed in 50.930 s; focused capability/depth race
checks passed in 1.568 s; frozen parser diagnostic parity passed in 29.155 s. A
400-field source fixture additionally compares complete Results and current AST
annotations across readonly/mutable edits, repeats and reverts (0.436 s); it
asserts that the resolved graph actually exceeds the small-projection gate.
Earlier broader root-suite failures and Stage1 errors remain unresolved.

### Function-scoped lookup dependency audit

`semantic.AnalyzeWithLookupTrace` runs full fresh analysis and returns owned,
versioned lookup observations alongside the ordinary Result. It records ordered
candidate reads for global values, named types and permission families, plus the
enclosing-module preference and its local-shadow outcome. Missing candidates
and present-but-inaccessible candidates remain visible in the record; a selected
binding alone would miss invalidation when a new higher-priority name appears.
Namespace, ordered usings, function label, root-file label and phase are captured
without retaining AST, Scope, Symbol or Type pointers. These labels are not a
complete function identity: imported declaration provenance, impl identity and
specialization are separate requirements.

Final effect inference has a dedicated observation owner. It does not install a
fake current function declaration/type, which would change resolution fallback
or diagnostic locations. Ordinary analysis does not enable recording, and its
recorder reference is cleared before publishing the Result. The loaded-program
analyzer seam preserves the same runtime-source authority and decoded-IR policy
for normal analysis and the opt-in audit.

The trace stores each context once and uses bounded integer references in its
records. Snapshot canonicalization sorts contexts and remaps references, making
numbering independent of observation order; storage admission reserves room for
that ID growth. Payload is bounded to 16 MiB and contexts plus records to 65,536
entries. A candidate list keeps at most 4,096 reads plus an overflow marker; the
entire observation is rejected if incomplete. Invalid UTF-8 is rejected rather
than silently merged by JSON replacement. Truncation and dropped observations
are explicit. Dropped counts can include repeated rejected reads and therefore
are not a count of unique missing dependencies.

This is one audited dependency domain, not an admission certificate or a body
cache. Direct Scope reads, type/callee values and summaries, generic/implicit
contexts, flow state and complete Result/live-analyzer hydration remain required.
Even an untruncated lookup trace cannot authorize skipping function analysis.
The audit performs JSON capture and intentionally adds work; no compile-time
speed improvement is claimed from this instrumentation.

The local frozen-project audit is enabled with
`ELISA_INCREMENTAL_LOOKUP_PROBE_SNAPSHOT_IN`; optional
`ELISA_INCREMENTAL_LOOKUP_PROBE_OUT` writes its owned JSON result. It compares
fresh traced and untraced status plus complete exposed diagnostics under the
same loader authority. Before context interning, the frozen parser produced
1,351,931 observed lookup calls and hit the 16 MiB budget at 39,244 records,
rejecting 893,547 observations while preserving all 776,281 raw diagnostic bytes
(including the trailing newline). The retained subset covered 1,951 function
labels, with 72,519 missing candidates. This partial subset is not representative
of all reads. Context interning addresses its duplicated payload before using
these observations to develop actual tracked lookup/callee queries.

With interned contexts, the same frozen parser audit fits without truncation:
1,351,931 calls, 57,571 distinct records, 3,929 contexts and 14,348,135 payload
bytes, with zero rejected observations. Its 1,951 owner labels have 37,088 retained
function-phase records and 20,483 final-effect-phase records; 111,952 candidate
reads were absent. Domains comprise 37,265 value, 15,906 type, 4,004 permission
and 396 enclosing-member observations. This proves coverage of the instrumented
lookup domain on this workload; it does not prove complete function dependencies.
The traced/untraced diagnostic comparison passed in 13.809 s. The trace itself
adds work (the measured traced analysis was 7.814 s), and differing process/GC
conditions prohibit treating these sequential audit timings as a compiler
throughput result. Saturated traces may retain different subsets under different
observation orders; only complete traces have the canonical-order guarantee.

Validation for this audit checkpoint: final full semantic suite passed in
58.125 s; focused interned-trace race tests passed in 1.468 s; compile-server,
source-cache and loaded-program/runtime-authority focused race checks passed in
2.146 s. Tests cover positive/negative/private lookups, type and permission
queries, local shadowing, actual full-Result/AST parity, final inference ownership,
string ownership, context interning/remapping, invalid UTF-8 and overflow paths.
Broader root-suite failures and Stage1 diagnostics remain unresolved. No remote
compute was used.

### Tracked name-selection and ambiguity queries

The next query layer now evaluates first-visible name selection through
`incremental.Reader.Input`, including absent and inaccessible candidates. Current
Analyzer metadata produces the complete ordered candidates, candidate visibility
and enclosing-local-shadow state before a coherent input revision is committed.
Old audit prefixes are request inventories, never publication authority. Effective
namespace (including the function-name fallback), ordered imports, root label and
lookup domain determine selection context; owner labels are not needed for an
otherwise equal global selection. Enclosing-member queries additionally require a
nonempty lexical-site token, whose source/function authority the producer must
validate separately. Two sites with different local shadows cannot share a key.

The selection value contains only qualified name and found status. Its tracked
negative/visibility dependencies can change and rerun the child without rerunning
parents when the selected name remains equal. Unread lower-priority candidates do
not invalidate this domain. That property alone is insufficient for diagnostics:
a new wildcard-import match can make a reference ambiguous while the selected
name stays equal. A separate ambiguity query reads all relevant imported matches,
including absent/hidden candidates and the namespace-member override, and returns
the sorted diagnostic match set (or no ambiguity). Both query domains have distinct
recipe/binding input keys; their present-nil symbol policies intentionally differ.
The production ambiguity checker now shares a pure match collector with its prior
behavior and still emits diagnostics at the current source position.

Inputs, specs and products own encoded values, not Symbol/Type/AST pointers.
Unknown domains, malformed/noncanonical recipes, invalid UTF-8, unsupported
shadow contexts and oversized inputs fail without memoizing a result. Each encoded
key/recipe/product is bounded to 64 KiB, imports/candidates to 4,096, and generated
name bytes are preflighted before namespace/import concatenation. Counting
potential duplicates conservatively can decline a query; it cannot alter ordinary
resolution. This does not yet bound a session's total retained lookup graph:
full context keys repeat in candidate input names, and obsolete inputs need an
owner's admission/retirement/compaction policy before normal server integration.

Tests compose selection and ambiguity children under a parent dependency query.
They verify missing-to-present invalidation, visibility changes, equal-output
pruning, unread suffixes, aliases, independent lexical shadows, malformed-input
recovery and present-nil behavior in both domains. Fresh analyzers are also tested
through declaration/body/return-type edits, removal and reverts. Name selection
stays equal across a callee's i64-to-bool edit while fresh caller typing correctly
reports the incompatible return: type/callee dependencies cannot be replaced by
a name decision. Direct Scope/builtin precedence is another uncovered domain;
the initial caller fixture exposed the existing builtin `target` record and was
renamed to avoid that unrelated binding. Full identifier resolution also needs
its enclosing-name AST rewrite, generic binder reads and fresh diagnostic inputs.

The opt-in `TestLocalLookupSelectionProjectProbe` uses a saved lookup audit only
as an inventory of requests. It rebuilds all query inputs from a fresh final
Analyzer and compares each result against the current resolver. Across two passes
it checked 57,175 selection requests per pass and 74,530 ambiguity results, skipping
396 enclosing requests without validated lexical sites. The database executed
17,654 unique queries on pass one and none additional on pass two; memo/validation
counters increased. Selected names also differed from the original audit zero
times on this frozen workload. This establishes final-declaration adapter parity
and component reuse, not reconstruction of every original generic/local/proof
context or full Result/backend parity. The probe's default semantic analysis still
has 980 existing errors; no error-reduction or compiler-throughput claim follows.

The kernels are not yet consumed by the compile server to skip general body
analysis. Remaining integration requires coherent declaration publication, bounded
lookup-session storage, direct Scope/binder and type/callee summary queries, and
complete current-AST/live-analyzer result hydration. Native object/relink work
and normal-client integration remain unfinished too.

Validation: lookup-only full semantic suite plus the opt-in project probe passed
in 55.779 s using a Go overlay that restored the two unrelated concurrent SMT
pointer files to 397316c5. Focused isolated lookup/ambiguity/query/trace races passed
in 1.620 s after the final strict serialization checks. The post-preflight project
probe passed in 5.297 s with unchanged admitted coverage. The concurrent pointer
edits were preserved and are outside this checkpoint's validation. Earlier broader
root-suite failures and Stage1 diagnostics remain unresolved. All runs were local.

### Bounded coherent query sessions

`incremental.Session` now owns publication and memo lifetime for an immutable
registry of query evaluators. The registry can include lookup, function, type and
callee parents together; compaction preserves that same registry. Each admitted
query reserves result bytes and dependency count and declares owned positive or
explicitly negative inputs. Identical shared declarations are merged; conflicting
input/publication batches are rejected before database mutation. Existing current
queries receive admission priority; capacity misses are reported as skipped keys,
so a parent missing a child fails closed instead of reading an old cached answer.

Publication is a complete snapshot under a session gate. Revision handles become
unavailable when a replacement generation begins; query evaluation cannot observe
an intermediate batch. `PublishPrepared` invalidates handles before invoking the
input builder too, covering adapter/recipe preparation failures. Rejected builds
preserve bounded memo storage for possible later reuse but leave no current ready
world. A pre-canceled operation preserves the old world consistently. Results are
owned immutable strings; current AST/Symbol/Type rebinding remains separate.

Readers in this owner enforce the admitted input/query universe and per-query
dependency reservation, including transitive children. A retired negative input
must invalidate its old readers even though its Boolean absence did not change;
`Database.RetireInputs` records an explicit retirement revision rather than
physically deleting its timestamp. Retired queries are evicted. Bounded retirement
debt triggers a rebuild from the current owned input snapshot, removing tombstones
and stale dependency records while preserving evaluator kinds and cumulative
statistics. Canonical owned keys prevent dependencies from retaining arbitrary
caller buffers. Unchanged owned values are reused, avoiding a second retained copy
when Database.Apply is a no-op; evaluator results are copied so a small substring
cannot retain a large input buffer.

Default live reservations allow 32,768 queries, 262,144 inputs/ownership edges and
dependency edges, and 128 MiB of accounted storage. Admission counts full query and
input keys, values, result reservations and metadata allowances. Input/query
retirement debt is separately bounded before compaction. These are storage and
graph bounds, not an exact Go allocator/RSS limit or a bound on arbitrary evaluator
computation. Publication validation/planning has explicit count limits; construction inside
a prepare callback remains caller-controlled. Evaluators still
must obtain all changing semantic data through Reader; the owner does not make a
mutable external closure a valid compiler query.

`lookupPublication` validates complete lookup recipe/binding ownership and derives
tight result/dependency reservations from fresh current metadata. It rejects
missing negative inputs and foreign/conflicting bindings. The two lookup kernels
now use the generic registry factory, and a composed-parent test exercises them
through this session owner. Tests additionally cover atomic two-input worlds,
preparation rejection/recovery, generation handles, partial child admission,
transitive negative authority loss both with and without compaction, dependency
and output overflows, key/input/result buffer ownership, cancellation, and churn
of input tombstones and constant query keys.

The bounded frozen-parser probe admitted all 17,654 distinct lookup/ambiguity
queries and 43,431 declared input/ownership/dependency reservations with zero skips.
It accounted for 34,530,986 bytes; the DB held 20,427 actual inputs (negative inputs
need not occupy a row until changed/retired) and 17,654 memoized queries. Republishing
the same world added 17,654 memo hits, with no additional executions. Products
matched the current final declaration-table resolvers. As before, 396 unanchored
enclosing sites were excluded and the frontend had 980 existing errors. This is
bounded component reuse, not function-body replay or an end-to-end compiler speed
measurement. Normal compile-server integration, direct Scope/binder dependencies,
type/callee/SCC products and complete result hydration remain unfinished.

Validation for the owner checkpoint: isolated full semantic suite including both
opt-in project probes passed in 50.927 s; incremental package passed in 0.247 s.
All generic DB/session race tests passed in 1.905 s, including key-debt compaction.
The bounded project-only probe passed in 5.380 s. The overlay restored eight known
unrelated modified Go files to 9f9d56b0 and excluded the unrelated untracked SMT
test, preserving the working files. This is candidate-only validation; the combined
SMT worktree is not claimed green. Earlier broad root failures and Stage1 errors
remain unresolved. No remote compute was used.
Focused isolated session/lookup races also passed: incremental 1.263 s and semantic
1.663 s. General function/body/type/callee products are still required before this
owner can make the normal compile path incremental.

### Lexical selection checkpoint

`incremental_lexical_query.go` adds a portable first-present selection query for
symbol, local-only symbol, type/const/shape parameter, associated type, parameter
interface and permission/error-set/region binder domains. Recipes describe the
complete current chain; missing inner frames are tracked negative inputs. An
unread suffix cannot invalidate a hit. Present nil entries stop lookup: parameter
interfaces report not-found, while symbol and type-like domains report found.
Boolean binder maps use their value, so an inner false continues to outer frames.
Products contain only a source binding slot and presence flags, never a Symbol,
Type, Scope or flow fact.

`publishLexicalSnapshot` publishes symbol and binder requests in one Session
revision. Its invocation-local maps bind portable results to current symbols and
binder values; obsolete revision handles fail, including across equal selections.
A stable global selection slot therefore cannot accidentally return the previous
invocation's Symbol. Scope/type/flow reads still need their own current semantic
work and dependencies. Lookup sites need explicit roles to distinguish contexts.
The adapter snapshots the current scopes; callers must prepare a snapshot at the
actual lookup point, rather than infer earlier reads from final scope contents.

Source liveness capture is batched once per source index within each domain's
preparation, with sealed batches and failed-validation authority revocation.
Combined symbol and binder preparation currently uses separate batches, so a
shared function may be scanned twice. Source mutation, foreign/generated local
nodes, cyclic scopes, duplicate binding slots and oversized requests fail closed.
Captured outer locals from another source index currently decline. Binder source
owners are caller-supplied: liveness and frame count are checked, but actual
push/pop ownership still must be wired into analyzer scope construction. These
adapters are not yet called by the normal compiler and cannot authorize general
body replay. No end-to-end iteration speed gain is claimed at this checkpoint.

Regression coverage exercises tracked misses and unread suffixes in all ten
domains, nil policies, boolean fall-through, global slot replacement, local-only
lookup, coherent symbol/binder publication, equal-output memo reuse across source
relocation with fresh Symbol rebinding, stale revision rejection, foreign binder
owners, missing frame sources and one liveness traversal for 257 statement sites.

Validation: isolated full semantic suite passed in 47.845 s and incremental package
in 0.171 s. Expanded lexical regressions passed under the race detector in 1.382 s,
including source-backed local shadow insertion/removal, distinct before/after
Define sites and replacement Symbol values at the same source slot. The full
suite preceded that final test-only addition. The overlay restored eight unrelated
modified Go files to ac840eac and excluded the untracked SMT test; their working
files were preserved. This does not establish that the combined SMT worktree is
green. Existing Stage1 errors were not rerun; all execution remained local.

### Binder frame ownership wiring

The lexical adapter now requires actual push-time ownership. All eight binder
helper domains record their source owner and live map when an incremental analysis
is active. Function body analysis and its return-provenance/borrowed-owner passes
supply the actual FuncDecl for generic, region, permission and shape frames;
generic decomposition propagates it to type, const, error-set and interface-bound
frames. Ordinary analysis keeps the recorder disabled. The session exposes
cumulative owned/ownerless helper-frame counts without retaining any frame or AST
in persistent query storage.

Preparation matches source owners and domain roles against that live record and
checks invocation-local map identity. A different surviving AST node at the same
stack depth cannot relabel the frame; replacing the map with equal contents also
declines. Pop clears the owner/map references. Each helper keeps the recorder it
used at push, so enabling recording inside an older untracked helper neither
claims that older frame nor pops the new recorder incorrectly. Unowned helper
frames and direct synthetic region pushes decline admission. Non-function
signature/default/associated-type contexts still need explicit source ownership
at their own call sites. Zero-frame misses need no owner.

This supersedes the earlier caller-trust limitation for admitted binder requests.
Source-index liveness validation still applies independently. Ownership alone is
not resolved Type identity, specialization context, effect convergence or full
Result hydration; broad body reuse and normal lexical query publication remain
unfinished. Incremental analysis now records frame provenance, but does not yet
substitute these selection queries for normal resolver work.

Validation: final isolated full semantic suite passed in 43.650 s and incremental
package in 0.118 s. Expanded lexical plus incremental-session race regressions
passed in 1.589 s. Coverage includes all eight helper domains, incorrect live
owners/roles, replaced equal-content maps, nested ownerless frames, direct
synthetic regions, recording enabled inside an untracked helper, restoration
and reference clearing on pop, generic decomposition and complete-result parity
through the real incremental analysis entry point. Raw malformed recipes/cells
also reject and recover after correction. The eight-file SMT overlay remains
based on ac840eac (those baseline files match 59dbd0a0); the unrelated untracked
SMT test stays excluded. No combined-SMT or full-project speed claim is made.

### Actual identifier read-point publication

Incremental analysis now observes the raw Scope selection in the real identifier
path, before global visibility filtering. Each observation freezes the chain's
selected-name cells and source-slot descriptor fields at the read point. Later
Define/removal, Symbol metadata changes or final scope contents cannot rewrite
that observation. Original Symbol pointers are kept only in invocation-local
rebinding/verification tables. The resolver still executes current visibility,
type, region, affine, initialization, function-value and refinement checks.

Observations are limited to primary template body traversal. Suppressed/speculative
passes and specialized bodies cannot silently share that context. Repeated
same-key visits must publish identical input worlds; a conflicting or unsupported
visit revokes that key for the invocation. The completed analysis publishes one
coherent lexical query world and checks every admitted result against the actual
read's Symbol/presence. A verification failure revokes the whole world. Admission
order is sorted by full key, so pressure does not depend on Go map iteration.
Old queries absent from the next world retire. Primitive replay still executes
parameter reads normally and those reads participate in memo reuse.

Source indexes are created before analysis, with at most 4,096 function entries
and 262,144 retained source nodes. Observation count is bounded at 65,536 and
scope-cell capture at 262,144 frames. Generated/foreign sites, unanchored local
symbols, unsupported owners and budget overflows explicitly decline. Liveness is
validated once per indexed function: only independently surviving original nodes
are retained, so an unavailable read no longer denies unrelated reads in the same
function. The strict all-or-nothing Capture API retains its prior behavior.
This partial liveness check cannot grant authority to a removed node or to any
node from a graph that fails traversal.

The lexical session permits 65,536 queries, 327,680 inputs/ownership/dependency
reservations and 256 MiB of accounted storage, with bounded fallback beyond that.
This is intentionally larger than the generic owner's default because read-site
queries outnumber distinct global-name queries. Compiler Scope/AST snapshots and
construction temporaries remain invocation-local and outside that storage model.
The owner stores only portable query inputs/products.

A first benchmark exposed redundant full source indexing in read-free reused
primitive bodies (~62.2 MB/op warm versus ~48.2 MB/op baseline). The collector now
uses the primitive expression enumeration it already computed to prove the
absence of identifier reads and skip that index. A regression requires zero
source indexes and observations for those bodies. In sequential 10-iteration,
two-sample runs of 16 functions with 512 expression leaves, warm allocations
returned to 48.236–48.246 MB/op versus baseline 48.206–48.235 MB/op. Timing varied
37.1–42.3 ms versus baseline 37.2–37.5 ms: no speedup or zero-overhead claim follows.
The initial concurrent timing samples were confounded and are not performance
qualification.

This remains selection dependency publication/verification, not general body
replay. Missing observations are not a certificate of complete function coverage;
future reused-body products must preserve and revalidate their own dependency
edges. Global/type/callee/SCC query composition, binder reads at actual use sites,
complete Result/live-analyzer hydration and object/relink/client integration
remain unfinished. Observing current reads has an overhead until those broader
products can use the dependencies to avoid semantic work.

The frozen parser workload now observes 57,261 primary identifier scope reads per
analysis, indexes 1,913 functions and verifies 35,468 distinct selections. The
repeat adds 35,468 memo hits and zero evaluator executions. All supported queries
fit without capacity skips: 206,530 input/ownership/dependency reservations and
169,715,856 accounted bytes. There are 12,281 declined observations and repeated
reads coalesce; these metrics are not a partition proving complete body coverage.
The frontend still reports its existing 980 errors. The project probe verifies
raw selection parity at actual read points, not whole-project Result/backend
parity or iteration-speed improvement. One complete project run took 15.248 s for
both fresh analyses and parsing; individual analyses took 7.562 s and 6.829 s in
that run with concurrent verification work.

Validation: isolated full semantic suite passed in 53.431 s and incremental in
0.193 s before the final lexical quota increase. The final-quota project probe
passed and expanded lexical/session/source-anchor race checks passed in 1.685 s.
Regressions cover read-point mutation isolation, fresh-AST relocation/memo reuse,
actual parameter reads during primitive replay, read-free index avoidance,
conflicting/unsupported repeated visits, deterministic pressured admission,
observation overflow preserving resolver behavior, verification-failure revocation,
partial liveness without whole-owner poisoning and strict Capture behavior.
The overlay restores eight unrelated modified Go files to ac840eac (unchanged
through 2dbbcb04) and excludes the unrelated untracked SMT test; all working files
remain untouched. Broader root/Stage1 failures remain outside this checkpoint.
No remote compute was used.

### Actual type-binder read-point composition

The NamedType resolver now observes its type-parameter and associated-type
lookups at the actual read. Lookup order stays unchanged: a type-parameter hit
returns immediately; its miss permits the associated-type lookup, then normal
visible/projected type resolution. Contextual Type bindings and all current
semantic checks continue to execute. Signatures, speculative/specialized passes
and non-primary contexts remain outside this observation domain.

Capture derives binder sources from push-time ownership rather than accepting
caller-supplied labels. It checks the complete live frame count, actual owner and
map identity before freezing each queried-name cell. Current Type values stay in
invocation-local observation/rebinding tables. Publication later rechecks source
liveness, so pop or map mutation after the read cannot rewrite the captured world.
Present nil blockers, typed-nil Type values, zero-frame misses and insertion of a
new frame retain the live lookup's behavior. Unsupported owners and replaced maps
decline without changing resolver output.

Symbol and binder reads publish through the same bounded Session revision and
sorted admission batch. Neither domain retires the other's queries. Repeated
binder visits with different source chains revoke that key; verification failure
in either domain revokes the complete world. Fresh Type objects can bind an equal
portable selection result, because that result identifies a source slot rather
than the mutable Type's semantic contents. Resolved Type identity and properties
remain separate required dependencies before any general body replay.

The shared capture limits now allow 131,072 observations and 524,288 scope/binder
frames. Source indexing remains separately bounded at 4,096 function entries and
262,144 nodes. Query storage still allows 65,536 queries and 256 MiB accounted;
input/ownership/dependency reservations are now bounded at 589,824 to match the
larger frame capture budget. Capture metrics are explicit. The first composed
probe showed the old shared limit displaced existing symbol reads, so capture
was expanded rather than sacrificing symbol coverage to add binder coverage.

On the frozen parser workload, each analysis observes 57,261 symbol reads and
20,661 binder reads; 77,548 observations capture 281,997 frames. It verifies
37,755 distinct symbol selections and 12,456 binder selections, improving symbol
coverage over the previous checkpoint while adding real binder reads. The repeat
adds 50,211 memo hits with zero evaluator executions. All 50,211 supported queries
fit without capacity skips: 234,083 input/ownership/dependency reservations and
196,452,558 accounted bytes. There remain 9,369 declined symbol observations and
228 declined binder observations; repeated reads coalesce. The frontend still
has its existing 980 errors. The two-analysis project probe passed in 16.761 s;
individual analyses took 8.299 s and 7.601 s during concurrent verification. This
is selection parity/reuse evidence, not whole-project Type/Result/backend parity
or an end-to-end compilation speed claim.

Other binder lookup domains still need actual use-site observation and their
source contexts. Non-function associated/default frames without ownership
continue to decline. Complete per-body dependency coverage, type/callee/SCC
products, complete current-AST/live-analyzer hydration and object/relink/client
integration remain required. The query world still verifies selections after
fresh semantic work; it does not yet authorize skipping that work.

Validation: isolated full semantic suite passed in 62.549 s and incremental package
in 0.203 s. Expanded lexical/session race checks passed in 1.722 s. Regressions
exercise actual generic and concrete NamedType resolution with complete Result
parity against fresh analysis, symbol/binder revision composition, fresh Type
rebinding across equal selection outputs, mutation/pop isolation, zero-frame miss
to nil-blocker insertion, typed nils, replaced/ownerless map rejection, conflicting
same-site chains and shared budget declines preserving resolver behavior.
The same eight-file SMT overlay and excluded untracked SMT test remain in use;
original working files are preserved. These are candidate-only checks, not a
claim that the combined SMT worktree or historical root/Stage1 failures are green.
All execution remained local.

### Declaration-backed function type values

Function type graphs can now represent named function types as declaration-backed
references, allowing function-valued ExprTypes to enter the existing scalar-fact
capture/restore path. `NewFunctionTypeEnvironmentFromResult` requires a current
Result whose permission-effect worklist completed, and a named function binding
must be exactly the current global function symbol's Type. The legacy constructor
continues to reject direct function references. Anonymous/specialized values
without a current named global declaration still decline.

Products retain only a declaration key, kind and full structural Type fingerprint.
Restore binds that reference to the fresh current function Type, including its
current AST-backed contract/default metadata, instead of copying an earlier
invocation's function object. Effect completion and symbol membership are checked
again at use, so revoking completion, deleting/replacing the binding, or changing
Type metadata invalidates the reference. Nested function signatures also require
completed current effects; nesting them inside a nominal struct cannot bypass
that gate. Loader-authorized origin and resolved declaration names remain caller
responsibilities, and invalid UTF-8 identity strings now decline before JSON could
replace and conflate them.

The fingerprint walks all FuncType fields using the existing bounded structural
encoder. Its private IntBitSet provenance values now have an explicit schema gate
and bounded canonical word encoding: overflow membership participates, allocation
capacity and trailing zero words do not. Source AST fingerprinting leaves this
semantic projection disabled. Existing derived EnumVariant packed-view memo
exclusions remain unchanged. Field additions are included automatically or cause
capture to decline if the new field cannot be represented.

A real analyzed caller now captures its function-valued callee expression through
`CaptureFunctionScalarFactsWithTypes`, exports the callee reference through
`TypeDependencies`, and restores onto relocated source nodes using a separate
fresh analysis's function Type. Changing the callee's Type interface then rejects
that stored caller fact product. Regressions cover parameters, returns, effects,
ABI, owned parameters, frame writes, async metadata, defaults, provenance overflow
bits, nested completion, foreign Result pointers, live completion revocation and
binding removal/replacement.

This is a function Type dependency/rehydration building block, not a complete
callee summary or SCC cache. Symbol metadata, generic/interface constraints,
lookup visibility, body/codegen content and other analysis summaries still need
their own dependencies. Normal compilation does not yet replay general call
bodies through this API. Full per-body dependency coverage, owned AST/live-analyzer
hydration and measured project iteration-speed gains remain unfinished.

Validation: isolated full semantic suite passed in 55.056 s and incremental in
0.186 s. Focused function-Type/scalar-fact/environment races passed in 1.410 s.
The same overlay restores eight unrelated modified Go files to their checkpoint
baseline and excludes the unrelated untracked SMT test; working files are
preserved. No combined-SMT, general body-replay or full-build throughput claim is
made. Existing Stage1/root failures remain unresolved. All work stayed local.

### Type-value dependency queries and caller fact checking

`function-type-value-v1` represents an owned type graph whose declaration
references are supplied by current tracked inputs. A retained recipe preserves
owned structure and declaration identities, with historical fingerprints removed.
The evaluator obtains readiness, recipe and current declaration kind/fingerprint
through Reader, verifies the graph, then fills the current reference stamps into
its portable result. It never reads mutable Type or Result objects from an
untracked evaluator closure. A declaration interface change therefore updates the
child result without pretending the old recipe's fingerprint is current.

The producer fetches dependencies from the current function-Type environment.
Declaration-backed products require a completed current Result, while entirely
owned scalar/composite graphs need no declaration environment. Missing bindings,
revoked completion, malformed recipes, wrong declaration kinds, inconsistent
historical references and unavailable inputs decline. Preparation belongs inside
PublishPrepared so failure revokes the former world. Successful graph values can
be restored through the current environment to fresh declaration Type objects.

A batch producer shares each current declaration fingerprint across requested
graphs within one exclusive compiler metadata snapshot. This memo is discarded
with the batch, so later publications observe mutations. Request order is sorted;
duplicate keys and budget overflow reject the batch. Per graph, reference count
is limited to 4,096 and graph byte/work bounds are preflighted before typed JSON
restoration. Batch preparation permits at most 65,536 requests/declarations and
256 MiB of accounted construction strings/reservations; exact allocator/RSS and
caller-owned source graphs are outside that accounting. Session admission still
applies independently. Input and product ownership does not retain compiler
Type/AST/Result pointers.

`function-scalar-type-gate-v1` checks the declaration-Type portion of actual stored
FunctionScalarFacts against that child query. A real caller/callee regression
captures the caller, repeats unchanged without evaluator work, changes the callee
source by adding a default parameter, rejects the previous caller fact product,
and recovers using a fresh analysis. Restoring the accepted facts onto relocated
caller source binds the current function Type. Tests also demonstrate equal
projection output pruning a grandparent, uncached fresh-typing failures, readiness
revocation/recovery, type/lexical coexistence in one Session world, shared batch
inputs and malformed-input recovery.

These are type-value and partial fact-dependency checks, not complete body-cache
admission. Constants, name/operator selection, flow/proof inputs, target/config,
AST lowering/default reconstruction, symbol metadata and other callee/SCC facts
still need dependencies and hydration. The gate must not authorize replay of
other scalar proof fields merely because declaration Types match. Normal compiler
body analysis does not yet use these queries to skip general call bodies; no
full-build iteration-speed improvement is claimed at this checkpoint.

Validation: isolated full semantic suite passed in 53.183 s and incremental in
0.171 s. Focused type-value/fact-gate race checks passed in 1.402 s. The existing
SMT overlay restores eight unrelated modified files and excludes the unrelated
untracked SMT test, preserving those working files. The combined SMT worktree,
historical root failures and Stage1 errors are not claimed green. All runs stayed
local.

### Sparse scheduling from the first effect pass

Final permission inference now keeps a bounded flat trace of initial reads and
row writes. After the first declaration-ordered pass, it queues only functions
that consumed a row before that row's last write. Later readers that already saw
the new row do not need another pass. Lambda writes and concrete declarations
behind generic/function-value copies participate through the existing observer.
The dependency graph is built only when another evaluation is necessary; the
stable path still avoids graph maps and heaps, while recording the flat trace.

Read ordering is preserved conservatively: adjacent duplicate reads retain the
earliest observation. Changed/self readers still enter later rounds using the
existing heap scheduler. Completion means no outdated readers remain, rather
than requiring a redundant confirming dense pass. Existing round limits and
incomplete-result reporting remain in force. If the initial trace exceeds
1,048,576 records or sees an unidentifiable write, scheduling falls back to the
prior dense second pass. Temporary trace storage is pooled only up to 65,536
records and cleared before pooling, so previous Type/AST arenas are not retained.

The reference sweep still supplies the same equations as an oracle. Existing
random graph, lambda/callback/drop/lowering and complete-Result comparisons pass.
New histories test selective first-pass seeding, fresh later readers reaching a
fixed point in one pass, and tiny trace limits preserving the reference result.
A 256-function sparse graph now evaluates 257 functions versus 512 with the former
scheduler; the 2,048-function version evaluates 2,049 versus 4,096.

Sequential baseline/candidate/candidate/baseline samples (100 iterations and two
repetitions per run) showed sparse phase medians of 37.05 us versus 62.34 us at
256 functions, and 183.24 us versus 457.07 us at 2,048. The latter uses about
44,722 versus 75,633 bytes and 159 versus 2,005 allocations per operation. These
are effect-phase component workloads. Whole-analysis chain medians moved only
1.2–1.6%, which is not sufficient evidence of an overall compiler speed gain.
Short stable-only timing samples were noisy, including the unchanged sweep
control; no zero-overhead assertion follows from those samples.

This optimization executes in normal compilation and reduces fresh effect
scheduler work. It is not a persistent SCC/summary cache and does not finish the
broader incremental compilation objective. Cross-invocation body/declaration
query scheduling, complete dependencies/result hydration, object/relink/client
integration and project edit/iteration qualification remain unfinished.

Longer stable-only samples (candidate/baseline/baseline/candidate, three one-second
repetitions per run) give a chain128 median of 397.825 us versus 391.155 us: about
1.7% slower, with ~343 extra bytes and five extra allocations per pass. This is a
measured stable-path tradeoff for the sparse-phase gain, not a zero-overhead
change. It does not establish whole-project throughput. Logs retain both the
initial noisy short runs and the longer controlled comparison.

Validation: isolated full semantic suite passed in 44.684 s and incremental in
0.284 s. Focused reference/random-graph/actual-source/completion/fallback races
passed in 1.819 s. The same unrelated-SMT overlay was used; working files and the
proof stash were preserved. The combined SMT tree and historical Stage1/root
errors are not claimed green. All runs were local.


### Retained SCC topology plans

The bounded incremental query engine now accepts a complete current node inventory
and adjacency through `SCCPublication`, with `SCCEvaluators` producing a portable
`SCCPlan`. Edges point from readers to dependencies. Components and members have
deterministic ordering, and the condensation graph has a dependency-first order.
Iterative traversal handles long chains without recursive stack growth.

Inventory and every adjacency list are tracked inputs, including empty lists.
Missing adjacency, dangling targets, malformed identities and exceeded budgets
fail without caching success. Limits are 4,096 nodes, 65,536 raw edges and 16 MiB
of accounted source/output storage; construction preflights expansion before
copying and encoding. Reservations remain stable across edge-only edits so a
redundant internal edge removal can preserve equal output and prune parent work.
The retained product contains strings and integers, not old AST or Type pointers.

Tests compare 100 random graphs against mutual-reachability partitions, exercise
a 4,096-node chain, and cover cycle splits, node removals, missing authority,
recovery, stale revisions and equal-output pruning. Incremental race checks
passed in 1.600 s; isolated full semantic and incremental suites passed in
44.024 s and 0.292 s. The existing unrelated-SMT overlay preserved working files;
the combined dirty tree and historical Stage1/root failures are not claimed green.

This is a topology query foundation, not normal effect-summary reuse. Call edges
alone omit grants, callback sources, drop hooks and typed lowering dependencies.
Current declaration authority, complete effect inputs, stable component products,
and normal solver integration remain necessary before retaining semantic rows.
No end-to-end throughput improvement is claimed for this addition. All runs were
local.


### Stable component topology projections

`SCCComponentPublication` adds a tracked per-node projection of its current
recursive group's sorted members and direct dependency groups. It depends on the
complete SCC plan, but exposes names rather than graph-wide component numbers.
Adding an unrelated earlier-sorted node therefore leaves its output equal and
prunes downstream users. Cycle splits, dependency merges and member removal
change the output or fail; restoring the node recovers normally. Changes inside
a dependency's outgoing graph do not alter a direct projection unless its group
membership changes. Semantic summaries must separately depend on the dependency
summaries and all non-topological semantic inputs.

Each admitted projection reserves a fixed 64 KiB result budget and one query
dependency. Larger products decline, requiring a fresh-analysis fallback. The
fixed reservation avoids retiring projections when unrelated inventory sizes
change. This reservation is a capacity cost: admitting every possible node can
exceed the default session byte budget and callers must handle skipped admission.
Projection evaluation currently decodes the complete plan; it is supporting
infrastructure, not a demonstrated hot-path performance optimization.

The full incremental race suite passed in 1.768 s, including a retained-session
history checking unrelated node addition, cycle split, removal/recovery,
transitive-edge isolation and recursive dependency merge. Normal semantic row
reuse and full-project iteration-speed qualification remain unfinished. All work
stayed local and unrelated SMT changes were preserved.


### Real-project analysis benchmark and remaining admission cost

`BenchmarkIncrementalProjectAnalysis` reads the opt-in frozen project selected by
`ELISA_LOOKUP_QUERY_PROBE_SNAPSHOT`. Each sample receives a freshly parsed AST;
parsing and retained-session prewarming are outside the timed/allocation region.
The retained measurement includes dependency capture, publication and validation.
Both paths keep checking diagnostic counts across iterations. This is an
analysis-only benchmark, not an edit-to-executable benchmark or a proof that all
diagnostic contents match.

Local qualification on Apple M5, Go 1.27.2, GOMAXPROCS=4, GOGC=1200 and
GOMEMLIMIT=14GiB used the frozen snapshot SHA-256
`86e7b5e6520848f6083217b40ac7ede1b09336dfe232f64c31d544e8bda94bd1`.
With `-run '^$' -bench '^BenchmarkIncrementalProjectAnalysis$' -benchtime=1x
-count=2`, ordinary analysis took 4.268/4.329 s and warm retained analysis took
5.647/5.664 s. Allocation totals were about 4.56 GB versus 6.07 GB per analysis.
All four samples produced 980 diagnostics; retained samples reported 50,211
lookup memo hits but zero primary body reuses. The two samples per mode are
sequential, not randomized or an ABBA comparison, so they are a baseline showing
substantial remaining cost, not precision throughput qualification. In these
samples retained analysis is about 32% slower, despite the lookup hits.

A separate two-analysis CPU/allocation profile of the existing real-project
probe passed in 11.633 s. Sampled total allocation was 12,287.88 MiB (not live RSS);
tracked-type cloning directly accounted for 3,232.74 MiB, capability traversal
seen-map writes for 704 MiB, and Session.PublishPrepared directly for 727.20 MiB.
CPU samples were dominated by runtime.madvise (36.82%) and memory clearing
(18.23%). These samples identify allocation-heavy work worth investigating; they
do not establish that any particular cache or allocation change will improve
throughput. Normal retained analysis still executes the expensive general body
and flow analysis. Broad safe reuse and reducing publication overhead now take
priority over adding disconnected query primitives or counting memo hits.

Logs/profiles are `/tmp/elisa-project-analysis-bench.log`,
`/tmp/elisa-retained-project-profile.log`, `/tmp/elisa-retained-project.cpu` and
`/tmp/elisa-retained-project.mem`. Both runs used the existing unrelated-SMT
isolation overlay. No combined dirty-tree, zero-error or complete incremental
compiler claim follows from this benchmark. All execution stayed local.


### Compact publication input plans

Session publication now stores each query's normalized input plan as a compact
slice. Up to 16 raw inputs use bounded linear duplicate detection; larger sets
use a temporary key-only hash set. Complete-snapshot conflict checking still
rejects conflicting inputs, including duplicates inside a query. Duplicate query
publications compare normalized sets independent of order, with hashing for
large comparisons. Removal normalization, unique-edge accounting, retained-first
admission, generation invalidation, ownership cloning and reservations remain in
force. This removes a separate Change-valued hash map for each query without
retaining caller storage after publication.

Boundary regressions cover duplicate/reordered publications with 1, 15, 16, 17
and 100 unique inputs, exact unique-edge counts, conflicting duplicates and
rejection invalidating previous revisions. Two component benchmarks publish 1,024
queries with four or 32 inputs each. Sequential baseline/candidate/candidate/
baseline runs used two repetitions per run on Apple M5 with GOMAXPROCS=4:

| Inputs/query | Baseline median | Candidate median | Bytes/op before → after | Allocations/op before → after |
| --- | --- | --- | --- | --- |
| 4 | 3.141 ms | 2.763 ms | 7,750,674 → 7,284,243 | 2,257 → 1,233 |
| 32 | 23.197 ms | 19.387 ms | 57,647,428 → 50,725,698 | 10,215 → 5,095 |

The component time reductions are about 12.0% and 16.4%. These benchmarks include
the complete publication path, not general semantic analysis.

A separate real-project retained-analysis ABBA comparison used the same frozen
snapshot and GOGC=1200/GOMEMLIMIT=14GiB settings as the previous baseline.
Baseline samples were 10.136/5.746 s and candidate samples 8.678/5.751 s. The wide
time variation prevents a credible project speedup claim. Median allocation was
6,072,684,356 versus 6,043,427,400 bytes, about 29 MB less per retained analysis.
Every sample still produced 980 diagnostics, 50,211 lookup hits and zero primary
body reuses. Broad semantic reuse and the overall incremental goal remain
unfinished.

Measurement logs are `/tmp/elisa-publication-component-abba.log`,
`/tmp/elisa-publication-large-abba.log` and
`/tmp/elisa-publication-project-abba.log`. Baseline runs restore only session.go
in addition to the existing unrelated-SMT isolation overlay. All runs stayed
local; unrelated working files and the proof stash were preserved.


Validation: isolated full semantic suite passed in 53.742 s and incremental in
0.194 s; the full incremental race suite passed in 1.535 s. Existing query
admission, retirement, ownership and failed-generation tests remain covered.
The combined dirty SMT tree and historical Stage1/root failures are not claimed
green. Logs, profiles, the frozen source, profile executable and self-contained
overlay backing files were archived under the Stage1 checkout's ignored
`build/verification/incremental-publication-20261017` directory for repeatability.


### Full seed timing and explicit solver benchmark mode

The preceding 4–6-second frozen-project analysis measurements used
`AnalyzeOptions{}`: SMT proof discharge was disabled. They isolate non-solver
analysis and publication, and cannot estimate a production compiler seed.
`BenchmarkIncrementalProjectAnalysis` now reports `smt-enabled` explicitly and
accepts `ELISA_PROJECT_BENCH_SMT=1` to enable proof discharge on both fresh and
retained paths, including prewarming. The fresh path uses AnalyzeWithOptions so
both receive identical options. Other CLI flags and loader runtime authority
are still outside this benchmark; the seed command remains the full workflow
measurement.

A fresh Stage0 built from bb0b884b with the unrelated-SMT overlay attempted the
current Stage1 seed locally. The default 6 GiB RSS guard terminated the first
attempt after 35.208 s (status 125), before analysis completion. This is not a
compile-time measurement. A retry with GOMAXPROCS=4, GOGC=1200, GOMEMLIMIT=10GiB
and a 14 GiB RSS guard completed the attempt in 152.704 s: lex 300 ms, parse
1.390 s, analysis 149.967 s. It exited 1 with semantic diagnostics, before
code generation/linking. Reported maximum child RSS was 6,083,477,504 bytes.
Whole-build cache was disabled and the existing exact-proof cache enabled.
The new Stage0 hash has its own proof-cache namespace, so this is not a fully
warmed-cache qualification.

This result is observational: the source manifest detected changes to
`src/backend/easm_compose_text.elisa` and `src/backend/easm_text_scan.elisa`
during the retry. It is not an immutable-input baseline or a controlled
comparison against the historical 117.05/107.03-second pair. The log contains
158 non-warning source diagnostic rows using the same location extractor that
finds 207 in the historical checkpoint. This does not attribute diagnostic
changes to incremental compilation and does not establish a successful build.
The installed compiler was not replaced.

Attempt logs, source hashes, changed-input list, environment, Stage0 artifact
hash and resource measurements are preserved in the Stage1 checkout's ignored
`build/verification/current-seed-bb0b884b` and
`build/verification/current-seed-bb0b884b-expanded` directories. Stable-source
full seed, warm/edit iteration latency, broader body reuse and successful
codegen/link qualification remain outstanding. LOCAL ONLY.


Benchmark-mode smoke validation passed with SMT off and on using the existing
nonlinear proof-cache fixture as a small frozen snapshot. Both fresh and retained
paths reported the requested 0/1 solver metric and zero diagnostics. These smoke
timings are not performance evidence. Log: `/tmp/elisa-project-bench-smt-smoke.log`.


### Streaming SMT atoms on the actual proof path

A copied Stage1 source tree was profiled with the bb0b884b Stage0 artifact,
proofs enabled, GOMAXPROCS=4, GOGC=1200 and GOMEMLIMIT=10GiB. The manifest remained
unchanged throughout this failed semantic seed. The profiled attempt took
223.616 s (analysis 217.378 s), with 5,156,159,488 bytes maximum child RSS.
It includes CPU profiling and allocation sampling at 64 KiB, uses a different
source path/cache workload, and is not a speed comparison against the prior
152.704-second attempt. No codegen or successful compiler product was produced.

The allocation profile sampled 28,746.35 MiB total (not live memory), including
3,404.05 MiB directly in tracked-type cloning, 2,097.39 MiB in Builder.WriteString,
1,327.83 MiB in FieldsFunc and 1,029.10 MiB in smtAtoms. CPU samples totalled
159.42 s, with memory clearing at 39.52%, madvise at 11.08%, and local-definition
staleness traversal at 4.17% cumulative. Profiling overhead is visible as well;
these measurements identify candidates, not guaranteed end-to-end gains.

Normal SMT query slicing and quantifier weakening now stream atoms through a
small cursor instead of constructing disposable atom slices. Command heads and
declaration names stop after the required one or two atoms. Symbol references
still preserve original ordering and deduplication. Quoted symbols, skipped
strings, delimiter behavior and malformed-input stopping preserve the old
collector's semantics; the change does not alter slicing admission or solver
verdict rules. A differential test compares the previous collector with the
cursor over fixed cases and 2,000 deterministic random inputs, including
malformed quotes, invalid UTF-8 bytes and non-ASCII symbols.

Sequential ABBA component comparisons on M5/GOMAXPROCS=4 used two repetitions
per run. A 128-declaration/256-hypothesis query slice went from 315.384 us to
196.988 us (37.5% less time), 433,075 to 164,307 bytes and 3,521 to 569 allocations.
The quantified-hypothesis fixture went from 999.5 ns to 458.95 ns (54.1% less
time), 1,064 to 232 bytes and 19 to four allocations. These used 1,000 iterations.
A separate 100,000-iteration single-command head comparison went from 269.75 ns
to 25.075 ns and from 496 bytes/five allocations to zero. The initial benchmark
also scanned a whole multi-command query for its head; those initial head figures
are not used as a representative command measurement. Slicing and weakening
figures remain from the unchanged fixture in that initial run.

Build timing now emits the existing SMT cost/cache summary under `[front-smt]`
before diagnostic failure handling, for source and decoded frontend-IR inputs.
This is enabled only with the existing ELISACORE_BUILD_TIMING option. Failed
builds can therefore expose solver time and cache hits; normal diagnostic failure
status is preserved. Focused coverage checks reporting on/off and both input paths.

Full frozen source, manifests, profiled executable and CPU/allocation reports are
archived under Stage1's ignored `build/verification/frozen-seed-profile-bb0b884b`.
Component logs and baseline overlays are in
`build/verification/smt-atom-streaming-20261017`. This is a measured improvement
to a normal proof-processing component, not completed incremental body reuse or
a demonstrated full compiler-build speedup. Stable same-input CLI comparisons,
complete body dependencies/hydration, codegen/relink reuse and iteration-speed
qualification remain outstanding. Work stayed local and unrelated SMT edits
were preserved.


Validation: full isolated semantic suite passed in 72.823 s and incremental in
0.225 s. Focused scanner/slicing/quantifier races passed in 2.244 s; root CLI
failure-reporting race checks passed in 9.189 s. The standalone focused semantic
checks passed in 0.726 s. The existing unrelated-SMT overlay was used throughout;
the combined dirty tree, historical root failures and successful Stage1 build
are not claimed green.


### Streaming local-hypothesis tokens

The remaining local-hypothesis and preamble token consumers now use a cursor
instead of disposable FieldsFunc token arrays. This is a separate cursor from
SMT atom parsing: its delimiter set remains exactly space, parentheses, newline
and tab. Carriage returns, quotes, non-ASCII and invalid UTF-8 bytes preserve the
previous FieldsFunc behavior. This distinction matters for keeping symbol
reachability and proof query text unchanged. Boolean-local reachability also
streams context tokens; macro expansion appends body tokens directly into its
existing queue. The initial retained macro queue still uses the bulk collector.

Differential coverage compares the cursor to the prior FieldsFunc contract over
fixed edge cases and 2,000 deterministic random byte strings. Existing Boolean
macro/local-hypothesis tests remain the semantic controls. Sequential ABBA
component runs on M5/GOMAXPROCS=4 used two repetitions and 1,000 iterations per
run. A 128-local dependency chain slice went from 44.921 us to 36.324 us (19.1%
less time), 67,792 to 55,440 bytes and 299 to 170 allocations. Preamble filtering
went from 21.582 us to 12.495 us (42.1% less time), 27,104 to 14,816 bytes and 139
to 11 allocations. The fixture and output checks were identical across runs.
These are component measurements; full compiler-build speedup and broader
incremental body reuse are not established by them. No attempt is made to equate
the profile's entire FieldsFunc allocation total with the allocations removed.

Logs: `/tmp/elisa-smt-token-abba.log`, `/tmp/elisa-smt-token-focused.log`,
`/tmp/elisa-smt-token-full.log` and `/tmp/elisa-smt-token-race.log`. Baseline runs
restore only the two token consumer files on top of the existing unrelated-SMT
overlay. All work stayed local, preserving unrelated edits and the proof stash.


Macro queue checks used a 16-definition chain with four or 128 references per
body, under the same sequential ABBA/1,000-iteration/two-repetition protocol.
Small expansion median went from 3.234 us to 2.579 us (20.3% less time), 7,056
to 6,848 bytes and 34 to 19 allocations. Large expansion went from 55.662 us to
36.941 us (33.6% less time), 284,499 to 135,073 bytes and 86 to 27 allocations.
The large case specifically checks the queue-growth tradeoff of streaming body
tokens rather than building and bulk-appending a temporary token array.
Log: `/tmp/elisa-smt-token-macro-abba.log`.

Validation: isolated full semantic suite passed in 73.509 s and incremental in
0.338 s. Focused tests passed in 0.644 s and focused races in 2.156 s. Archived
baseline source/overlay backing files and logs are in Stage1's ignored
`build/verification/smt-token-streaming-20261017`. No combined dirty SMT tree,
historical root failure resolution, complete incremental compiler or full-build
throughput claim follows from these component checks. Broader normal body reuse
and real edit/compile latency remain the priority after these measured reductions.


### Current Stage1 seed timing and predicate replay census

On Apple M5 / Go 1.27.2, a fresh Stage0 binary built from the current dirty core
tree in 22.28 s. Using it to seed Stage1 with GOMAXPROCS=4, GOGC=1200,
GOMEMLIMIT=10GiB, whole-build cache disabled and a 12 GiB child RSS guard took
153.92 s. The Stage1 source fingerprint was identical before and after the run
(`79ad54ae...e90dc58`). Phases were lex 486 ms, parse 2.437 s and analyze
2m30.21 s. The proof tier reported 4,863 obligations, 3,894 proven, 969
declined, 591 cached and 56.761 s in solver calls. It exited 1 before codegen or
linking with 150 non-warning source diagnostic rows and 32,077 warning rows.
The blockers group into 80 postcondition proofs, 38 invariants, 11 decreases
proofs, five loop-invariant-entry failures, 14 region-storage escapes and two
precondition failures. The largest file groups are `easm_compose_text.elisa`
(14), `iterator_alias_resolution.elisa` (13), `easm_operand_scan.elisa` and
`deprecated_message_lookup.elisa` (10 each), and `project.elisa` (9).
Including the separate Stage0 rebuild, the elapsed work from Go compiler sources
to this failed Stage1 seed was 176.20 s. These are times to a diagnostic failure,
not a successful compiler build or a speed comparison with earlier runs. An
earlier 130.22 s attempt saw a Stage1 source edit during the run and is excluded.
The current source/log/fingerprint artifacts are under
`/tmp/elisa-local-compile-timing/current`.

A later seed attempt on a different source snapshot (`seed-local-99`) used
Stage0 SHA-256 `18c33056b927a9bda79d0c00ce240206368959cc1311656faac16701b1c4c214`
and source SHA-256 `652514e619553f9c2826496b0dc00e525775bfa4b0c98eb86ca51aada4671677`.
It exited 1 after 118.76 s with 9,996,075,008 bytes peak RSS and 84 non-warning
proof diagnostic rows across 26 files. This is a separate, non-comparable
snapshot; the count must not be read as a reduction from the controlled run
above. Its log and run metadata are also under
`/tmp/elisa-local-compile-timing/current/seed-local-99.*`.

The same frozen parser snapshot (`86e7b5e6...bda94bd1`) was remeasured with
SMT disabled, GOMAXPROCS=4, GOGC=1200 and GOMEMLIMIT=10GiB. One-shot analysis
was 5.387 s fresh and 6.969 s retained, with 4.56 GB and 6.07 GB allocated.
The retained run reused one body and two expression nodes, with no local
initializer reuse or lexical lookup hits; both paths produced 980 diagnostics.
The retained path was about 29% slower on this workload. The syntax census
found 1,937 functions, 170 boolean-return functions and 110 boolean binary
returns, but only one body admitted by the current closed predicate grammar.
This remains an analysis-only sample, not edit-to-executable latency.

A CPU profile of the retained benchmark showed ordinary function-body and flow
analysis still dominating; `withSourcePermissionParams` and
`visitFunctionBodyWithElementScan` were among the largest cumulative paths.
The useful next step is broad function-product reuse with complete dependencies,
current-node rebinding and live analyzer hydration. The two-node predicate replay
does not establish a project throughput gain. The retained path remains about
29% slower than fresh analysis on this workload, so it is not evidence of an
incremental throughput improvement.

A follow-up safety guard now infers the receiver type of every comparison in an
admitted predicate, including nested comparisons whose result is `bool`. The
regression warms `(left < right) == (other_left < other_right)`, then adds a
`BoolEquality for bool` implementation and verifies replay is rejected, cold
lowering occurs, and the outer AST receives its lowered call. The focused
predicate tests and the full semantic package pass; the full package took
51.826 s (the earlier 63.533 s run was after the unary-minus guard). A focused
race run also passes. The broader retained-project benchmark remains the honest
throughput result until realistic edit-to-executable measurements improve.

On the Stage1 error-reduction track, the latest controlled local seed used a
stable source snapshot and exited before code generation with 126 non-warning
diagnostics and 32,078 warnings in 130.54 s; peak RSS was 8,797,274,112 bytes.
Source content and stat fingerprints matched before, after and at the subsequent
current-tree check (`c0dca754…`, `bc46cf7a…`). The count is down from the prior
controlled 137-row seed, but the source changed between snapshots, so the full
delta should not be attributed to one fix. The log is
`/tmp/elisa-local-compile-timing/current/seed-local-18.log`. No successful
Stage1 compiler product has been produced yet.


### Real parser-project edits and diagnostic-content qualification

`BenchmarkIncrementalProjectAnalysis` now checks the complete diagnostic
multiset (all position, severity and message fields), outside timed work, across
fresh analysis, session prewarming and retained analysis. Set
`ELISA_PROJECT_BENCH_EDITS=1` to append a unique scalar function and change its
body on each iteration; prewarming uses a different body. Original project
positions remain unchanged. The benchmark reports body checks as well as reuse.
Unchanged and novel-edit smoke modes also passed repeated-iteration checks.

Three alternating one-edit pairs on the frozen 2.5 MB Elisa parser snapshot
`86e7b5e6520848f6083217b40ac7ede1b09336dfe232f64c31d544e8bda94bd1`
compared the statement-list pruning, impossible store-summary scan gate and
effect-field traversal plan together against their earlier implementations.
Other committed code was held equal, with unrelated edits isolated by overlays.
Apple M5 settings were GOMAXPROCS=4, GOGC=1200, GOMEMLIMIT=8GiB; SMT was disabled.
Parsing, diagnostics comparison and session prewarming were outside timing.

Observed fresh medians fell from 8.34 to 6.58 s, and retained medians from 11.35
to 7.57 s. Samples were very variable: before fresh 12.66/7.49/8.34 s and retained
7.47/17.27/11.35 s; after fresh 5.70/6.58/15.29 s and retained 6.08/7.57/9.40 s.
These shared-host samples are not a stable throughput guarantee. Retained
allocation was roughly 4.50 to 4.39 GB and 24.1 to 22.0 million allocations.

Every run preserved 2,279 diagnostics, including the frozen snapshot's existing
980 errors. The sorted diagnostic digest was
`65396690155f7ad6ae758f9b9e337ee217eafbd96a98474262676748ce08a955`
for both binaries, both modes and every warmup. This is diagnostic-content
parity, not a comparison of every semantic side table or a successful compiler
build: no LLVM generation, linking or executable was produced by this benchmark.

The key remaining gap is coverage: each retained edit reuses one body and checks
1,957 body invocations. A source census found 1,937 declarations and only one
admitted return body in the original snapshot; unsupported headers, calls,
fields, enums and structured bodies dominate. The appended changing probe is
checked rather than replayed. Retained analysis remains slower than fresh on
this workload. Expanding verified body/dependency reuse, rather than treating
these allocation optimizations as completion, remains necessary.

Raw samples, census, source snapshot, benchmark source, source isolation backing
files and binary hashes are retained in
`build/verification/incremental-real-project-edits-20261018/`.


### Scalar return typing with independently checked postconditions

Closed scalar return products now admit plain value `ensure` clauses. This
retains only return-expression typing: normal contract analysis still runs
before the reuse boundary and `analyzeReturnStatement` discharges the current
postconditions after replay. Contract syntax/proof results are not replayed or
included in that typing product. The strict shared header gate remains intact
for local-initializer products. `requires`, scoped proof blocks and other
unsupported declaration features still select ordinary analysis. The source
read-free shortcut is disabled when postconditions are present.

A regression first reproduced the missing reuse. The expanded matrix checks
integer, predicate and literal bodies with SMT both disabled and enabled,
including adding a contract, editing it to false or referencing an undefined
name, recovery, and changing the body. Complete semantic Result parity is
checked against fresh analysis for every revision. Existing lexical audits
report no verification/publication failures. Contract and incremental
regressions passed normally in 3.001 s and with the race detector in 12.329 s.
A second race qualification froze the complete current committed semantic
package at `6e524118` plus the owned changes, excluding unrelated unfinished
prototype work, and passed in 14.331 s.

On the same frozen parser-project novel-body edit workload, return admission
rose from one to seven declarations, including five predicate returns. Warm
analysis reused seven bodies and checked 1,951 invocations (previously one and
1,957). Every one of the 2,279 diagnostic messages matched the established
`65396690155f7ad6ae758f9b9e337ee217eafbd96a98474262676748ce08a955`
multiset digest. The single retained sample took 6.37 s; it does not qualify a
speedup against the earlier variable samples. This expands verified reuse
coverage; most project bodies and the overall incremental goal remain pending.

Sources, frozen qualification backing files, regression logs and project/census
output are retained in
`build/verification/incremental-contract-return-reuse-20261018/`.


### Intrinsic scalar range-predicate reuse

The predicate grammar now admits canonical scalar `in` ranges, including their
combinations with comparisons and logical operators. The dedicated
`predicate-range-v1` product records a non-type marker for range containers:
cold analysis does not assign those nodes an ExprType. Replay restores current
scalar children, performs the normal affine-consumption hooks, recreates the
current one-candidate list wrapper when needed, and rebuilds its ArrayType and
optimization facts. No AST or semantic type handle is retained across analyses.
The source protocol includes range operators and bounds; wrapper normalization
is canonicalized for hashing. Operator/type binding checks remain current.

Range/List AST schemas are pinned. The non-type marker is allowed only at range
container positions, and publication declines if a range acquires a standalone
type or optimization-fact entry. Other candidate-list forms and non-scalar
parameter types remain outside this cache path. The legacy predicate format
still rejects range-only markers.

The regression first reproduced missing reuse. Full Result parity now covers
character, signed/narrow integer and parameter bounds, composed predicates,
source relocation, operator/bound changes, invalid-bound rejection/recovery,
and current postcondition checking with SMT on/off. Strict postcondition tests
use directly decidable arithmetic clauses to separate cache behavior from
existing range-formula prover limitations. Native execution tests exercise
cached inclusive/exclusive ranges and changed boundary behavior.

Frozen committed semantic/backend packages at `d458be49` plus owned range changes
passed selected incremental/membership/contract regressions under the race
detector: semantic 12.261 s, backend 29.688 s. On the matched frozen parser-project
novel-body edit workload, body reuse increased from 7 to 68, checks fell from
1,951 to 1,890, and restored expression nodes increased from 19 to 2,667. The
source census agrees: 68 return bodies admitted, including 66 predicates.
All 2,279 diagnostics preserved digest
`65396690155f7ad6ae758f9b9e337ee217eafbd96a98474262676748ce08a955`.

The single before/after retained samples were 4.948/4.961 s: this establishes
coverage and diagnostic parity, not a measured whole-project speedup. Parsing,
prewarming and diagnostic comparison were excluded, SMT was off, and settings
were GOMAXPROCS=4/GOGC=1200/GOMEMLIMIT=8GiB. The snapshot still has its 980
existing errors and does not reach LLVM generation. Most project bodies remain
outside reuse; the full incremental compilation goal is unfinished.

Sources, exact qualification backing files, binary hashes, native/race logs and
project/census results are retained under
`build/verification/incremental-predicate-ranges-20261018/`.


### Inline metadata independent of return typing

Validated single `@inline` hints no longer disqualify closed scalar return
products. Annotation validation and signature updates still run normally before
body analysis; only typing eligibility ignores the recognized hint. Unsupported,
malformed or multiple annotations stay outside this path. The Annotation schema
is pinned so future fields cannot silently enter the retained grammar.

The regression reproduced missing reuse, then checks fresh Result parity while
adding/removing hints, switching always/never, accepting the existing `force`
alias, rejecting invalid modes/arity, and recovering. It explicitly checks the
current FuncType's InlineMode and HasInlineMode; an LLVM regression proves cached
typing still emits current alwaysinline/noinline attributes. Selected
incremental/annotation/contract regressions passed with the race detector:
semantic 13.906 s and backend 32.011 s. Code was included by concurrent commit
`6bf97c2e`; the final recovery/removal controls and qualification are recorded
separately. Existing source census admission rises from 68 to 72 return bodies
(70 predicates). This is syntax admission, not a measured project runtime reuse
count or speedup. Main incremental work remains incomplete.

Sources and logs are retained under
`build/verification/incremental-inline-typing-20261018/`.


### Local initializer opportunity census (18 October)

The opt-in body census now measures direct local initializer shapes independently
of the enclosing function's return type and unrelated sibling statements. This
is syntax instrumentation only: it does not authorize reuse, analyze nested
statement blocks, or establish dependency validity. Mutable targets and explicit
binding syntax are counted separately from production cache admission.

On the frozen parser-project snapshot (SHA-256
`86e7b5e6520848f6083217b40ac7ede1b09336dfe232f64c31d544e8bda94bd1`),
there are 1,022 direct local initializers. Calls account for 309, field reads
108, effect-granted expressions 88, and binary expressions 90. Only 40 locals
are explicitly int/i64 (13 immutable, 27 mutable). Relaxing the whole-function
header and target restrictions would expose 27 closed scalar syntax candidates
in 26 functions: 26 integer literals and one unary expression, in
`line_map_cache_best`. Multi-node counts include unary and parenthesized
expressions; they do not imply expensive work.

This rejects scalar-local broadening as the immediate throughput priority.
General dependency-aware call/field reuse has a much larger measured syntax
frontier, but needs current symbol/type, effect, ownership and diagnostic
validation before any replay is safe. No production behavior or speedup is
claimed by this census. The selected census test passes; exact source and log
are retained under `build/verification/incremental-local-census-20261018/`.


### Dependency recording experiment rejected (18 October)

Added a query-engine benchmark that invalidates a product every revision, reads
1/4/8/64 inputs twice and reports allocations. A reservation regression exercises
duplicate reads at full budgets (0/1/8/9/64) and rejection of the next unique
edge. These are infrastructure checks, not an end-to-end compiler benchmark.

An experimental linear dependency list with hash promotion reduced allocations
for small products, but did not meet the throughput criterion. In five single-CPU
200ms samples, the four-edge promotion version had before/after median ns/op
1: 1383/916; 4: 1879/1298; 8: 1890/2054; 64: 18533/23013. Shared-host load makes
these noisy observations rather than stable performance claims, but they do not
qualify a general gain. An earlier eight-edge cutoff also regressed. Production
recording was restored exactly. Do not resurrect this based only on reduced
allocations; representative workload timing must justify it.

Logs are retained under
`build/verification/incremental-dependency-recording-20261018/`.


### Isolating edited analysis in CPU profiles (18 October)

The real-project benchmark labels only its timed semantic invocation with
`phase=project-analysis` and `analysis-mode=fresh|retained`. Label installation
and removal stay outside the timer. CPU profiling otherwise includes parsing,
retained-session prewarm and diagnostic comparisons even though those operations
are excluded from benchmark ns/op. Filter profiles with
`go tool pprof -tagfocus=phase=project-analysis BINARY PROFILE`. Background runtime
work may not inherit labels, so filtered samples are not total process CPU.

One labelled retained novel-body edit on the fixed parser snapshot took 5.387 s,
allocated 4.477 GB in 22.052 million allocations, reused 72 bodies/2,837 expression
nodes, and checked 1,886 bodies. Its full 2,279-diagnostic multiset preserved the
established digest (980 existing errors). This is analysis-only, SMT off, not
edit-to-executable latency or a before/after speedup.

The filtered profile still attributes 0.85 s to memory clearing and 0.93 s to
page reclamation; tracked-value type cloning has 0.52 s cumulative CPU and return
provenance inference 0.69 s (overlapping call trees, not additive). This makes
flow-state/type cloning a measured investigation target before adding speculative
field-expression cache protocols. The unfiltered profile exaggerated these
costs by including warmup and parsing. Exact profile/source/logs are retained in
`build/verification/incremental-labelled-profile-20261018/`.


### Avoid recording identity-preserving clone leaves (18 October)

Tracked flow-type cloning now omits cycle-map insertion for the existing
identity-preserving terminal cases (builtin/type parameters, never/null/invalid,
error sets, enums and opaque types). The prior map lookup remains, respecting
caller-supplied substitutions. Recursive/mutable descriptors still get distinct
clones and cycle entries. This does not introduce cross-revision type sharing.

Three single-CPU 200ms component samples cloning records with distinct enum
fields show median before/after 8 fields: 827/483 ns; 64: 8185/3352 ns; 256:
31035/14357 ns. The 64-field allocation falls 19104 to 10072 bytes and 14 to
5 allocations. This benchmark stresses the removed map growth; it does not
represent the whole compiler. Clone/depth/incremental regressions pass under
the race detector (13.542 s).

A sequential frozen-project retained-edit pair preserves 72 reused bodies,
2,837 restored expression nodes and the full diagnostic digest. Allocated bytes
fall 4,475,007,704 to 4,459,585,864, with allocations 22,054,570 to 22,046,290.
Times are 5.674/5.777 s, so no project throughput improvement is established.
An earlier concurrently loaded sample took 11.308 s and is not a matched timing
comparison. This commit claims a component clone improvement and reduced project
allocation work, not a stable end-to-end speedup. Sources and exact logs are in
`build/verification/incremental-clone-leaves-20261018/`.


### Type-derived empty scalar return summaries (18 October)

Lazy region-provenance and borrowed-owner summary inference no longer walk a
callee's whole body merely to establish an empty summary when its resolved
return is a recognized builtin scalar. Normal body checking remains unchanged.
The whitelist excludes aggregates, generic/type-parameter results, references,
unknown builtin names and explicit return-region contracts. Existing known
summaries still take precedence. This reduces duplicated within-invocation work
for retained edits; it does not cache summaries across revisions.

Regression coverage verifies calls before callee definitions, valid and invalid
scalar callee bodies, both published empty summary flags, and conservative
non-scalar/region boundaries. Selected scalar/incremental/provenance/borrowed-owner/
region/lifetime semantic checks pass under the race detector (18.965 s).
Selected incremental/borrow/region/callback/permission backend tests also pass
(27.363 s).

On the unchanged frozen parser novel-body edit workload, all 2,279 diagnostics
retain their established digest. One candidate sample took 5.401 s, allocated
4,404,047,360 bytes in 21,391,770 allocations, and retained 72 bodies/2,837 nodes.
The previous measured leaf-clone build allocated 4,459,585,864 bytes in
22,046,290 allocations (5.777 s): about 55.5 MB and 654,520 allocation operations
avoided. These single samples do not establish a stable whole-project throughput
gain. SMT remains off, parsing/warmup excluded, existing errors remain 980.
Exact sources/logs are under
`build/verification/incremental-scalar-summaries-20261018/`.


### Restore caller interior-region taint after callee analysis

Review of the repeated summary path found that ordinary function analysis and
both lazy return-summary passes reset `currentStructInteriorRegionTaint` without
restoring the caller's map. A failing regression proves lazy provenance inference
loses a seeded caller binding. Both lazy summary paths now preserve it; normal
function entry uses the same save/reset/restore protocol. Callee-local state
remains isolated from the caller. This is a correctness prerequisite for broader
summary reuse, not a measured throughput improvement.

The regression plus scalar-summary/incremental/provenance/borrowed-owner/region/
lifetime selections pass under the race detector (18.153 s). Before this repair,
the complete semantic suite passed on the prior scalar-summary source (101.278 s);
that full-suite result is qualification for the prior change, not full-suite
coverage of this repair. Source, failing reproduction and logs are retained in
`build/verification/incremental-summary-caller-state-20261018/`.


### Share temporary cycle detection across a query batch

`Database.EvaluateBatch` now allocates one active-query stack per serial batch,
instead of one per root query. Recursive evaluation already removes active keys
on every return, including error paths; the database gate holds a single input
snapshot for the entire batch. No persistent query/dependency state is shared
differently. The new regression checks cyclic roots followed by memoized valid
roots, repeatedly, to guard against error-path contamination.

A 4,096-product benchmark changes one body input per revision and validates the
whole batch. Five single-CPU 300ms samples show median 879,673 to 363,298 ns/op
(about 59% lower), bytes 1,770,160 to 132,160 and allocations 8,198 to 8 per batch.
This is query-engine validation throughput, not whole-compiler throughput. The
current benchmark source checks every root's error and expected value. The
archived timing runs predate that full-result check and measured the earlier
root-0 assertion, so they are not a matched before/after measurement of the
current validation. A fresh five-sample run with the full check measured median
370,050 ns/op (346,446–408,877), with 132,160 B/op and 8 allocations; this remains
query-engine validation throughput, not whole-compiler throughput. The captured
run is in `build/verification/incremental-batch-full-results-20261018/`. The full
incremental-engine race suite passed in 1.586 s on the earlier revision.
Source and earlier logs are in
`build/verification/incremental-batch-active-20261018/`.

Semantic incremental race regressions pass (18.096 s). The frozen-project
retained-edit check preserves 72 bodies/2,837 nodes and the established complete
2,279-diagnostic digest. Its concurrently loaded sample took 10.527 s and is
retained for parity evidence only; no project timing improvement is inferred.


### Resolve the default macOS merger once per native session

When PATH selects Apple's `/usr/bin/ld` dispatcher and ELISA_LD is unset, native
object sessions now resolve the selected linker once with `/usr/bin/xcrun --find
ld` and invoke that executable directly for subsequent merges. The default
toolchain is pinned for that session: start a new session to change developer
toolchain selection. First use pays discovery cost. Explicit ELISA_LD commands
and their arguments remain current per request; custom PATH linkers are not
bypassed. Discovery failure keeps the ordinary `ld` fallback. Non-macOS behavior
is unchanged. No artifact identity or semantic-reuse admission changes.

Override/current-argument and custom-PATH controls accompany the change. Selected
merger/incremental-object tests pass under the race detector (27.870 s), including
existing object execution/parity and failed-merge preservation controls.

An initial three-by-three direct-tool comparison reduced median merge time
19.90 to 11.42 ms, but a reverse three-edit sample regressed 18.04 to 24.45 ms.
Both are retained rather than selecting favorable samples. The longer same-binary
comparison uses explicit `/usr/bin/ld` versus the resolved default: two ten-edit
samples each show merge averages 18.71/18.56 versus 14.17/14.62 ms. Total
lowering/native emission averages are 239.19/247.25 versus 227.81/231.24 ms.
Each novel body edit emits one partition and reuses seven; parsing/analysis and
first-use prewarm are outside timing. This supports removing repeated tool
dispatch overhead, not a stable whole-project speedup claim.
Exact source, race logs and all timing samples are retained under
`build/verification/incremental-merger-resolution-20261018/`.


### Defer root cycle storage until a query needs evaluation

Single-query `Evaluate` now passes an absent active set; recursive evaluation
creates it only after checking cancellation, active-cycle membership and the
current-revision memo. Batch evaluation retains its one shared invocation-local
set. Actual dependency validation/evaluator execution still installs the active
key and removes it on every exit. Thus memo hits avoid allocating unused storage
without changing graph validation or revision rules.

A current-revision memo microbenchmark eliminates its 48-byte allocation entirely
(1 to 0 allocations). Five single-CPU samples after the change span 64.32–76.23
ns/op; before samples span 84.87–423.9 ns/op, with obvious shared-host variation.
The allocation result is repeatable, but those timing samples do not qualify a
stable speedup percentage or whole-compiler latency improvement. The new
regression verifies cancellation even for a memo hit and invalidation after an
input edit. The complete incremental-engine suite passes under race detection
(1.939 s). Exact source and logs are in
`build/verification/incremental-root-memo-20261018/`.

Semantic incremental integration also passes under the race detector; its exact
terminal result is retained in `elisa-root-memo-semantic-race.txt` in that evidence
directory. The pre-existing uncommitted batch-benchmark edit was preserved.
