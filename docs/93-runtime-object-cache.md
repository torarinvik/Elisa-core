# 93 — Runtime Object Cache (test/build speedup)

## Status
Implemented. Companion to the test-suite parallelization landed in `8d4f916a`
(180s → 118s).

**Scope correction (measured after implementation).** This cache does **not** speed up the
`go test ./src/...` suite, and the suite cost is **not** what the "Problem" section below
predicted. Two findings, both empirical:

1. **The suite takes a different codegen path.** Test programs `include` the runtime
   *implementation* (`elisacore_runtime.elisa`), so `resultDefinesDefaultElisaCoreRuntime`
   is true and the runtime is compiled **inlined into each per-test module**
   (`writeNativeObjectViaClangIR`), bypassing the separate `elisacore_runtime.o` this cache
   serves. A whole-suite subset showed only **1** runtime-object cache call. The earlier
   292ms→109ms profiling used `fluid_dynamics_sim.elisa`, which does *not* self-include — it
   is the default-runtime-object path, not the suite path.
2. **The suite is not compile-CPU-bound.** Cold A/B (both caches cleared, outer cache cold):
   cache OFF = 122s, ON = 124s (noise). The suite uses only ~1.3 of 10 cores, so eliminating
   compile CPU just frees idle cores. The suite wall-time is bound by per-test
   build→link→exec latency chains plus the ~47s serial tail (ASan sanitizer + timing-
   sensitive tests that cannot use `t.Parallel()`), not by aggregate compilation.

**What this cache actually does deliver:** ~1.8x on a *single* `elisac` build of a program
that uses the default runtime object (the Elisa dev inner loop; standalone app builds such
as the fluid/NES/Wolf3D fixtures; CLI compiles). Measured single-build: 542ms → 284ms,
`compile` 292ms → 109ms on a warm hit. Kept for that reason; it is correct, low-risk, and
gated.

The genuine suite lever (compile test programs against a runtime *interface* `.elisai` and
link the cached object instead of inlining the implementation) was scoped and **declined**:
it is a large effort (a ~196-function core-runtime interface + export-whitelist linkage work)
and, because the suite is latency-bound rather than compile-bound, unlikely to move suite
wall-time. ~118s is treated as the structural floor.

## Problem (measured)

Every native build that does not self-define the core runtime recompiles the entire
runtime support module from scratch. In `compiler/src/native_exec.go`,
`buildNativeExecutableWithClang` calls `writeDefaultElisaCoreRuntimeObject`
(native_exec.go:260-265) which, **every single build**:

1. `readSourceWithIncludes` + `parseProgram` + `semantic.Analyze` the full
   `compiler/runtime/elisacore_std/native_runtime_support.elisa` (10-line aggregator that
   include-expands the runtime), then
2. `writeNativeObjectViaClangIR(... backend.OptimizationLevel3 ...)` — compiles it at **O3**.

There is **no caching**. The object is identical across all builds with the same
(runtime source, packed profile, target triple), yet it is regenerated each time.

### Magnitude
- Standalone `-O3 -emit obj native_runtime_support.elisa`: **~230ms** (→ 32KB object),
  ~180-200ms in-process (excludes the standalone process-start tax).
- The `-emit test` path shows `compile=260-405ms` per test, and the cost is **fixed, not
  program-size-dependent** (a 60-line test compiled *slower* than a 285-line one). The
  runtime object is the dominant fixed component.
- ~215 heavy compile-link-run tests in `./src/` × ~200ms ≈ **~45s of redundant runtime
  recompilation** — roughly half of the post-parallelization 118s suite.

The existing whole-executable test-runner cache (`locateCachedTestRunner`/
`publishCachedTestRunner`) does **not** help within a suite run: it is keyed on the
per-test `runnerSource`, which is unique per test, so it misses on first run of each test.
A runtime-object cache is shared across *all* tests because the runtime is identical.

## Why this is low-risk (the hard parts already exist)

The earlier worry — "Elisa is whole-program monolithic, splitting the runtime needs a
codegen split + promoting private-linkage runtime symbols to external" — does **not apply**.
The build already:
- compiles the program and the runtime into **separate objects** (`elisacore_module.o` via
  `writeNativeObjectViaClangIR`, `elisacore_runtime.o` via `writeDefaultElisaCoreRuntimeObject`),
- links them together (native_exec.go:335+),
- resolves cross-object runtime symbols correctly today (the `isDefaultNativeRuntimeSupportExport`
  whitelist already gives the needed external linkage).

So no codegen or linkage change is required. This is purely adding an artifact cache around
an already-separate, already-externally-linked object.

## Design

Add a content-addressed cache for `elisacore_runtime.o`, mirroring the existing test-runner
cache machinery and the `os.TempDir()/elisacore-native-artifact-cache` convention.

### Cache key
Hash of everything that affects the emitted object:
- the **include-expanded** runtime source bytes (`readSourceWithIncludes` output, not just the
  10-line aggregator — an edit to any included file must invalidate),
- `packedProfile`,
- `targetTriple`,
- the fixed `OptimizationLevel3` + `debugInfo=false, traceInfo=false` used on this path
  (include them so the key stays correct if that ever changes),
- a **compiler build stamp** so a codegen change to the binary invalidates stale objects
  (e.g. the test-runner cache's existing version/stamp approach; reuse it).

### Flow
1. In `writeDefaultElisaCoreRuntimeObject`: compute the key, look up
   `<cache-root>/runtime/<key>/elisacore_runtime.o`.
2. **Hit**: copy (or hardlink) the cached object to `outputPath`. Skip parse/analyze/compile
   entirely. This is the ~100%-hit common case within and across suite runs.
3. **Miss**: compile as today, then publish into the cache via the atomic
   build-in-tempdir-then-rename pattern already used by the native-artifact cache (safe under
   the concurrent `t.Parallel()` tests landed in stage 1).
4. Gate behind the same enable check as the test-runner cache (`testRunnerCacheEnabled` /
   `ELISACORE_TEST_CACHE`), so `ELISACORE_TEST_CACHE=0` bypasses it for A/B measurement.

### Apply the same to the sibling objects
`writeDebugRefereeObject` (native_exec.go:273) has the identical recompile-every-time shape
for `debug_referee.elisa`; cache it with the same mechanism. Smaller object, smaller win, but
free once the machinery exists.

## Risks & mitigations
- **Key incompleteness → stale object served.** The only real correctness risk. Mitigation:
  key on the *expanded* source + packedProfile + targetTriple + opt/debug/trace flags +
  compiler stamp. Add a test that edits a runtime-included file and asserts a rebuild (cache
  miss) occurs.
- **Concurrency.** Stage-1 parallel tests share the cache dir; use the existing atomic
  tempdir-then-rename publish (already proven concurrency-safe in stage 1).
- **Disk growth.** One object per (runtime-hash × profile × triple). Tiny and bounded; the
  runtime hash changes only when the runtime source or compiler changes.

## Validation plan
1. Full `go test ./src/...` green.
2. Measure: expect per-test `compile=` to drop by ~180-200ms on cache hit; suite wall time
   target ~70-80s (from 118s).
3. Assert cache hit rate ~100% after the first test in a run (instrument like the test-runner
   cache debug line).
4. Edit a runtime-included file → assert one miss + rebuild + republish, then hits again.
5. `ELISACORE_TEST_CACHE=0` → full bypass, original timings (no stale-serve).

## Expected payoff
Combined with stage-1 parallelization (180s → 118s), eliminating ~45s of redundant runtime
compilation should bring `./src/` toward **~70-80s** — a ~2.3-2.5x total improvement over the
original 180s, with no codegen/linkage change and a well-trodden caching pattern.
