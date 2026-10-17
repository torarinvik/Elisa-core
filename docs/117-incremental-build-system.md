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
program/root. Primary body products reuse canonical-i64 single-return functions with no
parameters or with immutable, non-defaulted canonical-i64 parameters. Expressions
admit parameter reads, unsuffixed integer literals, parentheses, unary minus and
binary `+`, `-`, `*`. Plain namespace/module functions have qualified identities;
parameter arity separates admitted overload owners. Other declarations,
conditional declaration containers, mutable/defaulted/nonnumeric parameters,
locals, calls, contracts and generics stay cold.
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
most 1 MiB of encoded typing products. Above the key budget, retained eligible
owners and their retained local queries are selected first. Remaining slots go
to new locals of selected owners, then new owners in declaration order. Every
unselected body/initializer compiles normally; the request retains available
hits rather than becoming entirely cold. Unsupported return grammars never
consume a slot. A local owner needs room for its context and at least one
initializer, so context alone cannot monopolize a slot. Owners changing from
return-body products to locals without retained initializer keys join new
admission rather than displacing existing retained units. Selected plus queued
new-local plans stay within the key budget;
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
i64 local initializers before a final closed return. Each initializer supports
the arithmetic grammar over parameters and preceding locals. Normal declaration
analysis creates each current SymbolLocal and runs conversion, value binding,
range/optimization, ownership, borrow and drop logic. Later identifier reads,
return typing and whole-function summaries remain fresh.

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
a shared publication boundary. Existing diagnostics force cold initializer analysis
until a broader diagnostic/path-context dependency model is available.

LocalInitializersChecked/Reused report actual initializer work separately from
body counters. ReusedExpressionNodes excludes fresh identifier reads. Tests
cover metadata parity, consumed/transitive invalidation, unrelated edits,
removal/readmission, unsupported
bindings and owner checks. The LLVM/native gate requires identical IR and exits
42, then 63 after a consumer edit, then 69 after an upstream-only producer edit. BenchmarkIncrementalLocalInitializers measures fresh
parsing plus full analysis with 64 warm initializer queries; this is measurement
coverage, not a broad performance claim.

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
associated-type defaults, law/lemma/ghost/named-contract declaration kinds,
higher-order parameter contracts lifted out of Requires, fulfills-derived frames,
contract applications on methods, default-body proof attachments, and inferred
poststate/provenance/effect summaries with their boundary kind. Named-contract
exports and extern contract-derived frames need dedicated semantic round trips.
`uses` applications are not newly serialized here: analyzed declarations retain
both expanded clauses and their original application, so an explicit canonical
export representation must prevent duplicate application on import.
No module object reuse or performance improvement is claimed by this prerequisite.
