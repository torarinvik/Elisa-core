# 117 — Incremental Build System

## Status
Stage 1 (build-avoidance object cache) **implemented**. Stage 2 (module-granular
incremental compilation) **designed, not implemented** — see "Stage 2" below.

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
(`compilerSourceStamp` — a digest of all compiler `.go` sources, so any codegen
change invalidates every cached artifact), and an atomic stage-then-rename
publish that is safe under the parallel test suite.

## Stage 1 — content-addressed object cache (implemented)

`build_cache.go`. A plain `elisac -emit obj` consults a content-addressed cache
keyed on everything that can change the object's bytes; a hit serves the cached
copy and **skips parse + semantic analysis + codegen entirely** (the check runs
in `runWithOptions`, before `loadProgramInput`).

### Key derivation (correctness-critical)
The cache key is `sha256` of:
- `goos`, `goarch`, Go version;
- the **full include-expanded source** (`readSourceWithIncludes` of the root);
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
