//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Iterator-invalidation: two previously-uncaught use-after-free vectors
// (fix/iter-invalidation-alias-callee). Both relocate the iterand's buffer
// mid-iteration through a path the source-string-keyed lock could not see.

// Vector 1 (alias/borrow): `ys = &xs; for v in xs: ys.push(v)` — the push
// through the borrow alias `ys` reallocates `xs`'s buffer while it is iterated.
func TestIterInvalidationViaAliasNowRejected(t *testing.T) {
	src := `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        ys: mutable darray[i64]& = &xs
        for v in xs:
            ys.push(v)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "iter_alias_push.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "being iterated") {
		t.Fatalf("mutating xs through borrow alias ys during iteration must be rejected, got: %s", all)
	}
}

// Negative: pushing into a genuinely separate container while iterating xs is fine.
func TestIterInvalidationAliasNegativeUnrelated(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "iter_alias_safe.elisa", `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        ys: mutable darray[i64] = []
        for v in xs:
            ys.push(v)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("iterating xs while pushing into independent ys must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// Vector 2 (callee-via-mutable-ref): `for v in xs: push_one(&xs, v)` where the callee takes the
// container as `mutable darray[T]&` and may relocate it. The function-local lock cannot see the
// callee's push, so the call-site conservatively rejects passing the iterand by mutable ref.
func TestIterInvalidationViaCalleeRefNowRejected(t *testing.T) {
	src := `def push_one(arr: mutable darray[i64]&, v: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        arr.push(v)

def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        for v in xs:
            push_one(&xs, v)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "iter_callee_ref.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "being iterated") {
		t.Fatalf("passing iterated xs to a mutable-ref callee must be rejected, got: %s", all)
	}
}

// Vector 2 via a borrow alias: passing a mutable-ref borrow of the iterand to a mutable-ref callee.
func TestIterInvalidationViaCalleeRefAliasNowRejected(t *testing.T) {
	src := `def push_one(arr: mutable darray[i64]&, v: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        arr.push(v)

def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        ys: mutable darray[i64]& = &xs
        for v in xs:
            push_one(ys, v)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "iter_callee_ref_alias.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "being iterated") {
		t.Fatalf("passing a borrow of iterated xs to a mutable-ref callee must be rejected, got: %s", all)
	}
}

// Negative: an IMMUTABLE-ref callee (read-only) during iteration is allowed — no relocation possible.
func TestIterInvalidationCalleeRefNegativeImmutable(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "iter_callee_immut.elisa", `def count_it(arr: darray[i64]&) -> i64:
    return arr.count

def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        for v in xs:
            _ = count_it(&xs)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("immutable-ref callee during iteration must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// Negative: passing a SEPARATE container by mutable ref to a callee during iteration is fine.
func TestIterInvalidationCalleeRefNegativeUnrelated(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "iter_callee_unrelated.elisa", `def push_one(arr: mutable darray[i64]&, v: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        arr.push(v)

def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        ys: mutable darray[i64] = []
        for v in xs:
            push_one(&ys, v)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("passing an independent container to a mutable-ref callee during iteration must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// Negative: mutating xs through the alias AFTER the loop ends is fine.
func TestIterInvalidationAliasNegativeAfterLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "iter_alias_after.elisa", `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        ys: mutable darray[i64]& = &xs
        for v in xs:
            _ = v
        ys.push(2)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("mutating through the alias after the loop must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}
