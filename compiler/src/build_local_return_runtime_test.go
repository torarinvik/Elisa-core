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
// `def fill[@r](out: mutable darray[u8]& @r, ...)` grows the caller's darray and
// the mutation (count + reallocated items pointer across many growths) is visible in the
// caller AFTER the call — the `&` makes it pass-by-reference, and `@r` (normalized down onto
// the container, so it unifies with the `&v` argument's region) threads the caller's region
// arena as the growth allocator. Reads all 50k bytes back in the caller under ASan: a wrong
// region pick (growth into a freed/foreign arena) would fault; a dropped header update would
// lose the count. Explicit `region a(...)` form.
const refParamGrowthExplicitRegionBody = `
def fill[@r](out: mutable darray[u8]& @r, n: usize) -> void:
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
            sum: mutable u64 = 0
            for i in 0..<v.count.i64():
                sum <- sum + v[i].u64()
            if sum != 6367960:
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
// auto region is threaded into the `[@r]` callee through the `&v` argument. This was the
// original P4 vector (inferred container into a region param → silent miscompile). Caller reads
// all 50k bytes back under ASan: proves the auto-region threading composes with the by-ref
// region param and stays sound.
const refParamGrowthInferredRegionBody = `
def fill[@r](out: mutable darray[u8]& @r, n: usize) -> void:
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
        sum: mutable u64 = 0
        for i in 0..<v.count.i64():
            sum <- sum + v[i].u64()
        if sum != 6367960:
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

// S2 (docs/75) — CALLEE-SIDE region inference with ZERO region annotations. `def fill(out:
// mutable darray[u8]&, n)` has NO `[@r]` and NO `@r`: the analyzer detects that it GROWS a
// region-less by-reference container param (`out.push(...)`) and rewrites it into the proven S1
// `[@__rg_out](out: ... @__rg_out)` form, so the caller's region arena is threaded as the
// growth allocator and the mutation (count + reallocated items pointer across many growths) is
// visible in the caller AFTER the call. Caller's container is itself inferred-region (`v = []`, no
// scope). Reads all 50k bytes back under ASan: a wrong region pick (growth into a freed/foreign
// arena) faults; a dropped header update loses the count. This is the zero-ceremony cross-fn
// lifetime goal — `fill(&v)` with no region syntax anywhere.
const inferredRegionParamGrowthBody = `
def fill(out: mutable darray[u8]&, n: usize) -> void:
    i: mutable usize = 0
    while i < n:
        out.push((i.u8()))
        i <- i + 1

@test
def inferred_region_param_growth_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        v: mutable darray[u8] = []
        fill(&v, 50000)
        if v.count != 50000:
            panic("caller lost count (header update dropped)")
        sum: mutable u64 = 0
        for i in 0..<v.count.i64():
            sum <- sum + v[i].u64()
        if sum != 6367960:
            panic("grown elements corrupted (UAF / wrong region?)")
`

func TestInferredRegionParamGrowthCallerVisibleNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "inferred_region_param_growth", inferredRegionParamGrowthBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "inferred_region_param_growth_lives")
}

// S2 negative guard: a darray& param used ONLY for reading (`.count`, indexing) must NOT be made
// region-polymorphic — no growth call, so no region param is synthesized and no spurious hidden
// Arena& is threaded. If it were, callers would need to supply a region they don't have. Proves
// the growth-detection gate (paramContainerIsGrown) is the discriminator, not the `&` alone.
const readOnlyRefParamBody = `
def total(xs: darray[i64]&) -> i64:
    sum: mutable i64 = 0
    for i in 0..<xs.count.i64():
        sum <- sum + xs[i]
    return sum

@test
def read_only_ref_param_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        v: darray[i64] = [1, 2, 3, 4, 5]
        if total(&v) != 15:
            panic("read-only ref param miscomputed")
`

func TestReadOnlyRefParamNotRegionPoly(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "read_only_ref_param", readOnlyRefParamBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "read_only_ref_param_lives")
}

// S3: dstr param parity. `dstr` is the u8 darray (a *ast.NamedType with no `@r` annotation
// surface), so the inference stamps the region onto the enclosing reference, which resolveType
// pushes down onto the resolved DArrayType — the same normalization the darray builtin path uses.
// Zero region annotations: the callee grows a caller-owned dstr by reference and the grown bytes
// are caller-visible and intact (no UAF / wrong region).
const inferredRegionParamDStrGrowthBody = `
def build_msg(out: mutable dstr&, n: usize) -> void:
    i: mutable usize = 0
    while i < n:
        out.push((65 + (i % 26).u8()))
        i <- i + 1

@test
def inferred_region_param_dstr_growth_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        s: mutable dstr = []
        build_msg(&s, 50000)
        if s.count != 50000:
            panic("caller lost dstr count (header update dropped)")
        sum: mutable u64 = 0
        for i in 0..<s.count.i64():
            sum <- sum + s[i].u64()
        if sum != 3874976:
            panic("grown dstr bytes corrupted (UAF / wrong region?)")
`

func TestInferredRegionParamDStrGrowthCallerVisibleNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "inferred_region_param_dstr_growth", inferredRegionParamDStrGrowthBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "inferred_region_param_dstr_growth_lives")
}

// S3: multi-param. Two grown region-less container ref params get two DISTINCT region params
// (__rg_a, __rg_b), each threaded from its own caller arg's region. The per-param loop already
// supported this; this proves it threads independently with no cross-talk and both grows are
// caller-visible and intact after the call.
const inferredRegionParamMultiGrowthBody = `
def fill_two(a: mutable darray[u8]&, b: mutable darray[i64]&, n: usize) -> void:
    i: mutable usize = 0
    while i < n:
        a.push((i.u8()))
        b.push((i.i64() * 2))
        i <- i + 1

@test
def inferred_region_param_multi_growth_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[u8] = []
        ys: mutable darray[i64] = []
        fill_two(&xs, &ys, 40000)
        if xs.count != 40000 or ys.count != 40000:
            panic("caller lost count on one of two grown params")
        suma: mutable u64 = 0
        for i in 0..<xs.count.i64():
            suma <- suma + xs[i].u64()
        sumb: mutable i64 = 0
        for i in 0..<ys.count.i64():
            sumb <- sumb + ys[i]
        if suma != 5093856:
            panic("grown param a corrupted (UAF / wrong region?)")
        if sumb != 1599960000:
            panic("grown param b corrupted (UAF / wrong region?)")
`

func TestInferredRegionParamMultiGrowthCallerVisibleNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "inferred_region_param_multi_growth", inferredRegionParamMultiGrowthBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "inferred_region_param_multi_growth_lives")
}

// dstr string-literal init: `s: dstr = "..."` desugars (in the parser) to the byte-list literal
// `['s'.u8(), ...]`, so it allocates a growable region-backed dstr — assignable, escape-checked, and
// pushable afterward — instead of being a non-assignable static cstr. Covers empty (`= ""`), escape
// decoding, and post-init growth. ASan-linked to catch any region/lifetime mistake.
const dstrStringLiteralInitBody = `
@test
def dstr_string_literal_init_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        empty: mutable dstr = ""
        if empty.count != 0:
            panic("empty dstr literal not empty")
        empty.push(90)
        if empty.count != 1 or empty[0] != 90:
            panic("empty dstr literal not growable")
        s: mutable dstr = "AB\nC"
        if s.count != 4:
            panic("dstr literal wrong length (escape decode?)")
        if s[0] != 65 or s[1] != 66 or s[2] != 10 or s[3] != 67:
            panic("dstr literal wrong bytes")
        s.push(33)
        if s.count != 5 or s[4] != 33:
            panic("dstr literal not growable after init")
`

func TestDStrStringLiteralInit(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "dstr_string_literal_init", dstrStringLiteralInitBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "dstr_string_literal_init_lives")
}

// dstrStringLiteralReturnAssignBody exercises the two non-var-decl positions where a `dstr` may now
// be initialized from a string literal: a `return "..."` from a `-> dstr` function (rewritten at
// parse time so the region-poly-return classification sees a list literal) and a `s <- "..."`
// assignment into a local whose region is already established by its `[]` declaration. Both desugar
// to the byte-list literal the literal is equivalent to; escapes are decoded by the lexer.
const dstrStringLiteralReturnAssignBody = `
def make_msg() -> dstr:
    return "Hi"

def pick(b: bool) -> dstr:
    if b:
        return "yes"
    return "no\n"

@test
def dstr_literal_return_assign_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        m: dstr = make_msg()
        if m.count != 2 or m[0] != 72 or m[1] != 105:
            panic("return string literal wrong")
        y: dstr = pick(true)
        if y.count != 3 or y[0] != 121:
            panic("nested-return string literal wrong")
        n: dstr = pick(false)
        if n.count != 3 or n[2] != 10:
            panic("nested-return escape literal wrong")
        s: mutable dstr = []
        s.push(64)
        s <- "ab\nc"
        if s.count != 4 or s[0] != 97 or s[2] != 10 or s[3] != 99:
            panic("local-assign string literal wrong")
        s.push(33)
        if s.count != 5 or s[4] != 33:
            panic("local-assign result not growable")
`

func TestDStrStringLiteralReturnAssign(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "dstr_string_literal_return_assign", dstrStringLiteralReturnAssignBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "dstr_literal_return_assign_lives")
}

// inferredRegionParamReassignBody exercises region-param inference triggered by whole-container
// REASSIGNMENT from a literal (not just push): a bare `mutable T&` ref param reassigned via
// `s <- [...]` / `s <- "..."` is inferred region-polymorphic, so the fresh backing allocates in the
// caller's region (sound) instead of a dying local auto-region (which would dangle through the ref).
// The caller's region is itself inferred (`= []`), so this also exercises the inferred→inferred
// threading path. ASan would catch a use-after-free here.
const inferredRegionParamReassignBody = `
def repl_da(s: mutable darray[u8]&) -> void:
    s <- [88, 89, 90]

def repl_str(s: mutable dstr&) -> void:
    s <- "Hello"

@test
def inferred_region_param_reassign_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        v: mutable darray[u8] = []
        v.push(65)
        repl_da(&v)
        if v.count != 3 or v[0] != 88 or v[2] != 90:
            panic("darray bare-reassign wrong")
        s: mutable dstr = []
        s.push(64)
        repl_str(&s)
        if s.count != 5 or s[0] != 72 or s[4] != 111:
            panic("dstr bare-string-reassign wrong")
        s.push(33)
        if s.count != 6 or s[5] != 33:
            panic("post-reassign growth wrong")
`

func TestInferredRegionParamReassignCallerVisibleNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "inferred_region_param_reassign", inferredRegionParamReassignBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "inferred_region_param_reassign_lives")
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

// #3 return-a-view-into-grown-param: a function grows a caller-owned darray by reference and
// returns a `view[u8]` window into it, with ZERO region annotations. Inference ties the param to a
// region param AND (inferReturnViewRegion) stamps the return view's region to that param, so the
// returned view binds to the caller's region. The caller reads the window back under ASan — a
// region-less return (the pre-fix state) or a wrong region pick would fault or read garbage.
const returnViewIntoGrownParamBody = `
def head(out: mutable darray[u8]&, n: usize) -> view[u8]:
    i: mutable usize = 0
    while i < n:
        out.push(65)
        i <- i + 1
    return out[0:n]

@test
def return_view_into_grown_param_lives() -> void:
    can Abort.Panic, Memory.Allocate:
        v: mutable darray[u8] = []
        w: view[u8] = head(&v, 64)
        total: mutable usize = 0
        for b in w:
            total <- total + b.usize()
        if total != 64 * 65:
            panic("returned view corrupted (UAF / wrong region?)")
`

func TestReturnViewIntoGrownParamNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "return_view_into_grown_param", returnViewIntoGrownParamBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "return_view_into_grown_param_lives")
}
