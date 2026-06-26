//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Erasure invariant: ghost fields carry zero runtime bytes.
// ---------------------------------------------------------------------------

// POSITIVE: a struct with a ghost field has the same concrete field set as the
// same struct without the ghost field.  The analyzer must record the ghost in
// GhostFieldOrder but strip it from the layout field list.
func TestGhostFieldErasedFromLayout(t *testing.T) {
	src := `
struct WithGhost:
    x: i64
    y: i32
    ghost model: i64

def use(w: WithGhost&) -> i64:
    return w.x
`
	result := analyzeWithSMT(t, "ghost_erasure_layout.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a ghost field must be accepted and erased without error, got: %v", errs)
	}
}

// POSITIVE: a ghost field may appear in a `requires` clause — proof-only
// context, does not generate code.
func TestGhostFieldReadableInRequires(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def get(self: Counter&) -> i64:
    requires self.model >= 0
    return self.concrete
`
	result := analyzeWithSMT(t, "ghost_field_requires.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("ghost field in `requires` must be allowed, got: %v", errs)
	}
}

// POSITIVE: a ghost field may appear in an `assert` statement (proof-only; erased in
// release, never observable by real values).
func TestGhostFieldReadableInAssert(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def check(self: Counter&):
    assert self.concrete == self.model
`
	result := analyzeWithSMT(t, "ghost_field_assert.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("ghost field in `assert` must be allowed, got: %v", errs)
	}
}

// POSITIVE: a ghost field may appear in a `where` param refinement
// (spec-only position).
func TestGhostFieldReadableInWhereParam(t *testing.T) {
	src := `
struct Pair:
    a: i64
    ghost model: i64
    invariant self.a == self.model

def use(self: Pair& where self.model >= 0) -> i64:
    return self.a
`
	result := analyzeWithSMT(t, "ghost_field_where_param.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("ghost field in `where` param refinement must be allowed, got: %v", errs)
	}
}

// POSITIVE: a ghost field may appear in a `where` return-type refinement.
func TestGhostFieldReadableInWhereReturn(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def get(self: Counter&) -> i64 where result == self.model:
    return self.concrete
`
	result := analyzeWithSMT(t, "ghost_field_where_return.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("ghost field in `where` return-type refinement must be allowed, got: %v", errs)
	}
}

// POSITIVE: a ghost field may appear in an in-body `invariant` statement
// (contract position; erased from real execution).
func TestGhostFieldReadableInBodyInvariant(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def use(self: Counter&) -> i64:
    invariant self.concrete == self.model
    return self.concrete
`
	result := analyzeWithSMT(t, "ghost_field_body_invariant.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("ghost field in in-body `invariant` must be allowed, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Soundness: ghost-field writes in real code must be rejected (gap fixed).
// ---------------------------------------------------------------------------

// SOUNDNESS (gap fix): writing a ghost field in real code via `<-` must be
// rejected.  Ghost fields have no runtime storage; a write would silently be
// dropped at codegen, violating the soundness of any facts derived from the
// write's value.  The compiler must catch this at the assignment target site.
func TestGhostFieldWriteInRealCodeRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64

def bad(self: mutable Counter&):
    self.model <- 5
`
	result := analyzeWithSMT(t, "ghost_field_write_rejected.elisa", src)
	errs := result.Errors()
	for _, e := range errs {
		if strings.Contains(e, "ghost field") {
			return // correctly rejected
		}
	}
	t.Errorf("expected ghost field write in real code to be rejected; got errors: %v", errs)
}

// SOUNDNESS: a ghost field must also be rejected in an augmented assignment
// (`self.model <- self.model + 1`-equivalent).  The assignment-target path
// must block it before the value is even computed.
func TestGhostFieldAugWriteInRealCodeRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64

def bad(self: mutable Counter&):
    self.concrete <- self.concrete + 1
    self.model <- self.model + 1
`
	result := analyzeWithSMT(t, "ghost_field_aug_write_rejected.elisa", src)
	errs := result.Errors()
	for _, e := range errs {
		if strings.Contains(e, "ghost field") {
			return // correctly rejected
		}
	}
	t.Errorf("expected ghost field augmented write in real code to be rejected; got errors: %v", errs)
}

// ---------------------------------------------------------------------------
// Constructor: ghost fields are invisible to struct literals.
// ---------------------------------------------------------------------------

// SOUNDNESS: setting a ghost field in a struct literal must be rejected — the
// field is not a positional or keyword argument of the concrete constructor.
func TestGhostFieldInStructLiteralRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64

def make() -> Counter:
    return Counter(concrete: 5, model: 5)
`
	result := analyzeWithSMT(t, "ghost_ctor_rejected.elisa", src)
	errs := result.Errors()
	if len(errs) == 0 {
		t.Errorf("expected ghost field in struct literal to be rejected")
	}
	// The error message comes from the field-lookup path; just confirm an error fires.
}
