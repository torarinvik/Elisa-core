package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// SOUNDNESS: a callee that stores a by-value darray parameter into a field reached through a
// struct reference parameter (`h.xs <- xs`) keeps the argument's backing alive past the call.
// A caller passing a container that lives in a shorter-lived local region (the function's
// inferred region, or a per-iteration loop region) left `h.xs` dangling: it compiled and read
// freed memory at runtime. The caller must reject the call.
func TestCallStoringLocalDarrayIntoStructRefFieldRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_call_store.elisa", `struct Holder:
    xs: mutable darray[i64] = []

def stash(h: mutable Holder&, xs: darray[i64]) -> void:
    h.xs <- xs

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        for i in 0..<50 |local|:
            local.push(i.i64())
        stash(h, local)

def fill_loop(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        for k in 0..<3 |h|:
            local: mutable darray[i64] = []
            local.push(k.i64())
            stash(h, local)

def stash_alias(h: mutable Holder&, xs: darray[i64]) -> void:
    tmp: darray[i64] = xs
    h.xs <- tmp

def stash_forward(h: mutable Holder&, xs: darray[i64]) -> void:
    stash(h, xs)

def fill_alias(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash_alias(h, local)

def fill_forward(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash_forward(h, local)

def fill_ufcs(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        h.stash(local)
`)
	joined := strings.Join(result.Errors(), "\n")
	if got := strings.Count(joined, "call may store a value in region"); got != 5 {
		t.Fatalf("expected both local-darray stores through a struct ref to be rejected, got %d:\n%s", got, joined)
	}
}

// The check is directed by the callee's store summary: a callee that only READS the darray
// argument (`h.count <- xs.count`) stores nothing, and a target struct living in the same
// region as the stored container cannot outlive it.
func TestCallStoringIntoStructRefFieldSafeFormsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_call_store_ok.elisa", `struct Holder:
    xs: mutable darray[i64] = []
    count: mutable usize = 0

def note(h: mutable Holder&, xs: darray[i64]) -> void:
    h.count <- xs.count

def stash(h: mutable Holder&, xs: darray[i64]) -> void:
    h.xs <- xs

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        note(h, local)

def same_scope() -> usize:
    can Memory.Allocate, Abort.Panic:
        g: mutable Holder = Holder{}
        local: mutable darray[i64] = []
        local.push(3)
        stash(g, local)
        return g.xs.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected read-only and same-region forms to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A by-value darray parameter is a header copy that SHARES its element buffer with the caller:
// `put0(h.rows, xs)` with `rows[0] <- xs` writes into h's buffer, so a local-region xs must be
// rejected exactly like a store through a reference (runtime-verified UAF when exempted).
// A by-value parameter with no shared indirect storage (a plain scalar struct) stays exempt.
func TestCallStoringThroughByValueSharedBufferRejected(t *testing.T) {
	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_byvalue_shared.elisa", `struct Rows:
    rows: mutable darray[darray[i64]] = []

def put0(rows: darray[darray[i64]], xs: darray[i64]) -> void:
    can Abort.Panic:
        rows[0] <- xs

def stash2(h: mutable Rows&, xs: darray[i64]) -> void:
    can Abort.Panic:
        put0(h.rows, xs)

def fill(h: mutable Rows&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash2(h, local)
`)
	if joined := strings.Join(bad.Errors(), "\n"); !strings.Contains(joined, "call may store a value in region") {
		t.Fatalf("an element write through a by-value darray that shares the target's buffer must be rejected, got:\n%s", joined)
	}
	ok := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_byvalue_scalar.elisa", `struct Pt:
    x: i64
    y: i64

struct Holder:
    xs: mutable darray[i64] = []
    at: mutable Pt = Pt{x: 0, y: 0}

def bump(p: Pt, xs: darray[i64]) -> i64:
    return p.x + xs.count.i64()

def note(h: mutable Holder&, xs: darray[i64]) -> void:
    h.at <- Pt{x: bump(h.at, xs), y: 0}

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        note(h, local)
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("a by-value scalar struct parameter shares nothing with the caller; expected acceptance, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The struct-local store into a ref-param field is only made sound when the ambient region can
// be bound to that single target; with a second reference param it must stay rejected.
func TestStructLocalStoredIntoRefFieldWithTwoRefParamsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_local_two_refs.elisa", `struct Inner:
    xs: mutable darray[u64]

struct Outer:
    inner: mutable Inner

def fill(o: mutable Outer&, other: mutable Outer&) -> void can[Abort.Panic, Memory.Allocate]:
    local: mutable Inner = Inner{xs: []}
    local.xs.push(1)
    o.inner <- local
`)
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "escapes") {
		t.Fatalf("storing a struct local into one of two ref params must be rejected, got:\n%s", joined)
	}
}

// A call result stored into the target is judged by what the callee RETURNS from each argument:
// returning the darray itself (`keep(xs)`) carries its local backing (rejected); returning a
// fresh copy of its scalar elements (`copy_of(xs)`) carries nothing local (accepted).
func TestCallStoringCallResultJudgedByReturn(t *testing.T) {
	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_ret_header.elisa", `struct Holder:
    xs: mutable darray[i64] = []

def keep(xs: darray[i64]) -> darray[i64]:
    return xs

def stash3(h: mutable Holder&, xs: darray[i64]) -> void:
    h.xs <- keep(xs)

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash3(h, local)
`)
	if joined := strings.Join(bad.Errors(), "\n"); !strings.Contains(joined, "call may store a value in region") {
		t.Fatalf("a callee storing an identity-returned local darray must be rejected, got:\n%s", joined)
	}
	ok := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_ret_copy.elisa", `struct Holder:
    xs: mutable darray[i64] = []

def copy_of(h: mutable Holder&, xs: darray[i64]) -> void:
    can Memory.Allocate, Abort.Panic:
        for x in xs |h|:
            h.xs.push(x)

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        copy_of(h, local)
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("copying scalar elements must be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// With two MUTABLE container params the ambient region cannot be bound to a single target, so
// pushing a grown struct local stays rejected (it would dangle in the callee's region).
func TestVoidGrowerStructLocalTwoMutableTargetsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "void_grower_two_targets.elisa", `struct Path:
    names: mutable darray[i64] = []

def capture(paths: mutable darray[Path]&, other: mutable darray[Path]&, src: darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        path: mutable Path = Path{}
        path.names.extend(src)
        paths.push(path)
`)
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "escapes") {
		t.Fatalf("pushing a grown struct local with two mutable targets must be rejected, got:\n%s", joined)
	}
}

// Element-mode stores: copying scalar elements out of a local darray into the target retains no
// local storage (accepted); copying an element that is itself a local-region container header
// (`h.rows.push(rows[0])` with `rows: darray[darray[i64]]` built locally) does (rejected).
func TestCallStoringElementsIntoStructRefField(t *testing.T) {
	ok := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_elem_ok.elisa", `struct Holder:
    xs: mutable darray[i64] = []

def copy_in(h: mutable Holder&, xs: darray[i64]) -> void:
    can Memory.Allocate, Abort.Panic:
        for x in xs |h|:
            h.xs.push(x)

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        copy_in(h, local)
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("copying scalar elements into a struct ref must be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_elem_bad.elisa", `struct Rows:
    rows: mutable darray[darray[i64]] = []

def take_first(h: mutable Rows&, rows: darray[darray[i64]]) -> void:
    can Memory.Allocate, Abort.Panic:
        h.rows.push(rows[0])

def fill(h: mutable Rows&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[darray[i64]] = []
        inner: mutable darray[i64] = []
        inner.push(1)
        local.push(inner)
        take_first(h, local)
`)
	if joined := strings.Join(bad.Errors(), "\n"); !strings.Contains(joined, "call may store a value in region") {
		t.Fatalf("copying a local-region container element into a struct ref must be rejected, got:\n%s", joined)
	}
}

// The store scan's binder collection must return a pattern's bound NAMES only: the qualifying
// enum/struct type and variant (`Expr.Index(a, b)`) are not values. Treating "Expr" as a tainted
// binder made every later `Expr.Field(..)` constructor look like a method on a tainted receiver.
func TestStoreScanBinderNamesSkipPatternQualifiers(t *testing.T) {
	stmt := &ast.MatchStmt{Arms: []ast.MatchArm{{
		Pattern: &ast.MatchVariantPattern{EnumName: "Expr", Variant: "Index", Args: []ast.MatchPatternArg{
			{Pattern: &ast.MatchBindPattern{Name: "head"}},
			{Pattern: &ast.MatchStructPattern{TypeName: "Pair", As: "whole"}},
		}},
	}}}
	got := map[string]bool{}
	for _, name := range stmtBoundNames(stmt) {
		got[name] = true
	}
	for _, qualifier := range []string{"Expr", "Index", "Pair"} {
		if got[qualifier] {
			t.Errorf("pattern qualifier %q collected as a bound name: %v", qualifier, got)
		}
	}
	for _, binder := range []string{"head", "whole"} {
		if !got[binder] {
			t.Errorf("binder %q missing: %v", binder, got)
		}
	}
}

// A callee that receives ONE fresh aggregate bundling a target-side component with a source-side
// one can split it and store the source into the target (rule 225). That only matters when the
// target-side component can be written through: a container, reference or aggregate holding one.
// A target-derived string or view next to the source is read-only and moves nothing.
func TestCallStoringBundledTargetComponentNeedsWritableStorage(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_bundle.elisa", `struct Holder:
    rows: mutable darray[darray[i64]] = []
    name: sview = ""

struct Pair:
    a: darray[darray[i64]]
    b: darray[i64]

struct Named:
    name: sview
    b: darray[i64]

def splice(p: Pair) -> void:
    if p.a.count > 0:
        p.a[0] <- p.b

def label(n: Named) -> usize:
    return n.name.len + n.b.count

def stash(h: mutable Holder&, xs: darray[i64]) -> void:
    splice(Pair{a: h.rows, b: xs})

def note(h: mutable Holder&, xs: darray[i64]) -> usize:
    return label(Named{name: h.name, b: xs})

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash(h, local)

def read(h: mutable Holder&) -> usize:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        return note(h, local)
`)
	joined := strings.Join(result.Errors(), "\n")
	if got := strings.Count(joined, "call may store a value in region"); got != 1 || !strings.Contains(joined, "struct_target_bundle.elisa:30:") {
		t.Fatalf("expected only the writable bundle (line 30) to be rejected, got %d:\n%s", got, joined)
	}
}
