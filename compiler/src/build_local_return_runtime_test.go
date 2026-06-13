package main

import (
	"strings"
	"testing"
)

// region-return-inference Stage 1, RUNTIME SOUNDNESS under ASan: a build-local-return
// builder (`def collect(n) -> darray[i64]` that grows a local in the inferred region and
// returns it) must have its `__auto_*` region ADOPTED into the caller's region, so the
// caller can read every element AFTER the call. If the front-end suppressed the escape but
// the backend did NOT adopt (the silent-UAF failure mode the gate guards against), the
// builder's region would be freed on return and ASan would fault on the read-back.
const buildLocalReturnBody = `
def collect(n: usize) -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        out: mutable darray[i64] = []
        i: mutable usize = 0
        while i < n:
            out.push(i.i64() * 2)
            i <- i + 1
        return out

@test
def build_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        xs: darray[i64] = collect(50000)
        if xs.count != 50000:
            panic("returned darray lost its count")
        sum: mutable i64 = 0
        for v in xs:
            sum <- sum + v
        if sum != 2499950000:
            panic("returned darray elements corrupted (UAF?)")
        if xs[0] != 0 or xs[49999] != 99998:
            panic("returned darray boundary corrupted")
`

func TestBuildLocalReturnAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "build_local_return_uaf", buildLocalReturnBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "build_local_return_lives")
}

// UNTYPED build-local-return: `out = []` (no type annotation) filled by a loop-nested push of
// a COMPUTED value, returned from a `-> darray[i64]` function. The element type is pinned by
// the return annotation (the syntactic push-arg scan can't resolve `i.i64() * 2`), the body is
// wrapped in a synthesized auto region, and that region must be adopted into the caller — so a
// 50k-element read-back after the call is ASan-clean. Guards the untyped-builder ergonomic
// against both an inference miss (would not compile) and an adoption miss (would UAF).
const untypedBuildLocalReturnBody = `
def collect(n: usize) -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        out = []
        i: mutable usize = 0
        while i < n:
            out.push(i.i64() * 2)
            i <- i + 1
        return out

@test
def untyped_build_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        xs: darray[i64] = collect(50000)
        if xs.count != 50000:
            panic("returned darray lost its count")
        sum: mutable i64 = 0
        for v in xs:
            sum <- sum + v
        if sum != 2499950000:
            panic("returned darray elements corrupted (UAF?)")
        if xs[0] != 0 or xs[49999] != 99998:
            panic("returned darray boundary corrupted")
`

func TestUntypedBuildLocalReturnAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "untyped_build_local_return_uaf", untypedBuildLocalReturnBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "untyped_build_local_return_lives")
}

// Multi-return / conditional build-local-return: two literal-locals built in the SAME
// function-body auto region (maybeWrapFunctionBodyInAutoRegion wraps the whole body in one
// region), each returned on a different path. Both must be adopted into the caller's region —
// no per-path region to unify. ASan-clean read-back of both.
const buildLocalReturnMultiBody = `
def pick(c: bool, n: usize) -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        i: mutable usize = 0
        while i < n:
            a.push(i.i64())
            b.push(i.i64() * 10)
            i <- i + 1
        if c:
            return a
        return b

@test
def multi_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        xs: darray[i64] = pick(true, 1000)
        ys: darray[i64] = pick(false, 1000)
        if xs.count != 1000 or ys.count != 1000:
            panic("multi-return wrong count")
        if xs[999] != 999 or ys[999] != 9990:
            panic("multi-return values corrupted (UAF?)")
`

func TestBuildLocalReturnMultiPathAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "build_local_return_multi", buildLocalReturnMultiBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "multi_return_lives")
}

// GENERIC build-local-return under ASan: region-poly threading must compose with generic
// specialization. specializeFuncType now carries the RegionPolymorphic flag, so each instance
// (i64, f64) threads + adopts the caller region. Two distinct specializations also guard against
// param mis-indexing between the threaded `__region_auto` and substituted params. A regression
// (dropped flag) would free each instance's own arena -> dangling header -> ASan SEGV on read-back.
const buildLocalReturnGenericBody = `
def make_gen[T](n: usize, seed: T) -> darray[T]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        out: mutable darray[T] = []
        _ = out.resize(n)
        i: mutable usize = 0
        while i < n:
            out[i] <- seed
            i <- i + 1
        return out

@test
def generic_build_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        xs: darray[i64] = make_gen[i64](5000.usize(), 7.i64())
        if xs.count != 5000 or xs[4999] != 7:
            panic("generic i64 builder corrupted (UAF?)")
        ys: darray[f64] = make_gen[f64](3000.usize(), 2.5)
        if ys.count != 3000 or ys[2999] != 2.5:
            panic("generic f64 builder corrupted (UAF?)")
`

func TestBuildLocalReturnGenericAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "build_local_return_generic", buildLocalReturnGenericBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "generic_build_local_return_lives")
}

// CROSS-FN GROWTH of a CALLER-OWNED container through a by-reference region param.
// `def fill[region r](out: mutable darray[u8]& @r, ...)` grows the caller's darray and
// the mutation (count + reallocated items pointer across many growths) is visible in the
// caller AFTER the call — the `&` makes it pass-by-reference, and `@r` (normalized down onto
// the container, so it unifies with the `&v` argument's region) threads the caller's region
// arena as the growth allocator. Reads all 50k bytes back in the caller under ASan: a wrong
// region pick (growth into a freed/foreign arena) would fault; a dropped header update would
// lose the count. Explicit `region a(...)` form.
const refParamGrowthExplicitRegionBody = `
def fill[region r](out: mutable darray[u8]& @r, n: usize) -> void:
    i: mutable usize = 0
    while i < n:
        out.push((i.u8()))
        i <- i + 1

@test
def ref_param_growth_explicit_region_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(262144):
            v: mutable darray[u8] @a = []
            fill(&v, 50000)
            if v.count != 50000:
                panic("caller lost count (header update dropped)")
            sum: mutable u64 = 0u64
            for i in 0..<v.count.i64():
                sum <- sum + v[i].u64()
            if sum != 6367960u64:
                panic("grown elements corrupted (UAF / wrong region?)")
`

func TestRefParamGrowthExplicitRegionCallerVisibleNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "ref_param_growth_explicit", refParamGrowthExplicitRegionBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "ref_param_growth_explicit_region_lives")
}

// Same cross-fn by-ref growth, but the caller's container has NO region annotation and NO
// explicit `region NAME(...)` scope: `v: mutable darray[u8] = []`. The caller's synthesized
// auto region is threaded into the `[region r]` callee through the `&v` argument. This was the
// original P4 vector (inferred container into a region param → silent miscompile). Caller reads
// all 50k bytes back under ASan: proves the auto-region threading composes with the by-ref
// region param and stays sound.
const refParamGrowthInferredRegionBody = `
def fill[region r](out: mutable darray[u8]& @r, n: usize) -> void:
    i: mutable usize = 0
    while i < n:
        out.push((i.u8()))
        i <- i + 1

@test
def ref_param_growth_inferred_region_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        v: mutable darray[u8] = []
        fill(&v, 50000)
        if v.count != 50000:
            panic("caller lost count (header update dropped)")
        sum: mutable u64 = 0u64
        for i in 0..<v.count.i64():
            sum <- sum + v[i].u64()
        if sum != 6367960u64:
            panic("grown elements corrupted (UAF / wrong region?)")
`

func TestRefParamGrowthInferredRegionCallerVisibleNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "ref_param_growth_inferred", refParamGrowthInferredRegionBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "ref_param_growth_inferred_region_lives")
}

// `[ f(x) for x in xs by par ]` now lowers to the generic `par_map_collect` build-local-return
// combinator (it builds the result in its own region and the caller adopts it) instead of an inline
// presize+par_map block. This exercises the full stack under ASan — generic region-poly + nursery +
// region adoption through the comprehension desugar — and reads every element back after the call.
const byParMapCollectBody = `
@test
def by_par_map_collect_lives() -> void:
    can Parallel, Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = [k for k in 0..<8000]
        doubled: darray[i64] = [x * 2 for x in a by par]
        if doubled.count != 8000:
            panic("by par map lost elements")
        ok: mutable bool = true
        for i in 0..<a.count:
            if doubled[i] != a[i] * 2:
                ok <- false
        if not ok:
            panic("by par map element mismatch (UAF?)")
        if doubled[7999] != 15998:
            panic("by par map boundary corrupted")
`

func TestByParMapCollectAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "by_par_map_collect", byParMapCollectBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "by_par_map_collect_lives")
}
