package semantic_test

import (
	"strings"
	"testing"
)

// `view[T]` is a READ-ONLY borrow (like `&[T]`); `mutable view[T]` is a WRITABLE borrow (like
// `&mut [T]`). Write-through (indexed store / mutable-ref iteration) requires the static type to be
// mutable, and a `mutable view[T]` cannot be backed by a read-only (immutable-source) slice.

func TestAnalyzeRejectsWriteThroughReadOnlyViewIndex(t *testing.T) {
	src := `def bad(src: mutable darray[i64]&) -> void:
    v: view[i64] = src[0:2]
    v[0] <- 99
`
	_, errs := parseAndAnalyze(t, "view_index_write.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to read-only view index result") {
		t.Fatalf("expected a read-only view-index-write rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsWriteThroughMutableView(t *testing.T) {
	src := `def ok(src: mutable darray[i64]&) -> void:
    v: mutable view[i64] = src[0:2]
    v[0] <- 99
`
	_, errs := parseAndAnalyze(t, "mut_view_write.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("write through a mutable view of a mutable source must be legal, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A `mutable view[T]` cannot be created from an immutable source — that would launder write access
// onto a borrow whose backing is read-only.
func TestAnalyzeRejectsMutableViewFromImmutableSource(t *testing.T) {
	src := `def bad(src: darray[i64]&) -> void:
    v: mutable view[i64] = src[0:2]
    v[0] <- 99
`
	_, errs := parseAndAnalyze(t, "mut_view_immutable_src.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected a mutable view over an immutable source to be rejected, got none")
	}
}

// A `mutable view[T]` parameter is a writable borrow; a plain `view[T]` parameter is read-only.
func TestAnalyzeMutableViewParamWritableReadonlyParamNot(t *testing.T) {
	okSrc := `def g(v: mutable view[i64]) -> void:
    v[0] <- 9
`
	if _, errs := parseAndAnalyze(t, "mut_view_param_ok.elisa", okSrc); len(errs) != 0 {
		t.Fatalf("write through a mutable view param must be legal, got:\n%s", strings.Join(errs, "\n"))
	}
	badSrc := `def g(v: view[i64]) -> void:
    v[0] <- 9
`
	if _, errs := parseAndAnalyze(t, "view_param_bad.elisa", badSrc); len(errs) == 0 {
		t.Fatal("write through a read-only view param must be rejected, got none")
	}
}

func TestAnalyzeRejectsMutableRefIterationOverReadOnlyView(t *testing.T) {
	src := `def bad(src: mutable darray[i64]&) -> void:
    v: view[i64] = src[0:2]
    for mutable e in v:
        e <- e + 1
`
	_, errs := parseAndAnalyze(t, "view_mut_iter.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "requires a writable") {
		t.Fatalf("expected mutable-ref iteration over a read-only view to be rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsMutableRefIterationOverMutableView(t *testing.T) {
	src := `def ok(src: mutable darray[i64]&) -> void:
    v: mutable view[i64] = src[0:2]
    for mutable e in v:
        e <- e + 1
`
	if _, errs := parseAndAnalyze(t, "mut_view_iter.elisa", src); len(errs) != 0 {
		t.Fatalf("mutable-ref iteration over a mutable view must be legal, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Reads (indexed read + by-value iteration) are always allowed, on either view kind.
func TestAnalyzeAcceptsReadOnlyViewRead(t *testing.T) {
	src := `def ok(src: darray[i64]&) -> i64:
    v: view[i64] = src[0:2]
    s: mutable i64 = 0
    for e in v:
        s <- s + e
    return s + v[0]
`
	if _, errs := parseAndAnalyze(t, "view_read.elisa", src); len(errs) != 0 {
		t.Fatalf("view reads must remain legal, got:\n%s", strings.Join(errs, "\n"))
	}
}
