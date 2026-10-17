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

This remains prefix replay from an admitted revision. There is no unchanged-suffix
replay after an earlier edit, source relocation, declaration-body slicing, or
persistent general semantic/module/object dependency reuse yet. First-use and
unsupported revisions remain cold; early edits may have no reusable boundary.
A revision with parser notices cannot become a retained prefix provider; changed
suffix diagnostics are still produced fresh.
Token comparison, prefix graph cloning and some metadata work remain linear, and
snapshot admission still requires amortization. The complete professional
incremental compiler goal and whole-project performance qualification remain open.

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
