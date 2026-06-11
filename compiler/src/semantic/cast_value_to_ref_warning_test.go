package semantic

import (
	"strings"
	"testing"
)

// `.cast[T&]` on a value whose type IS T reinterprets the value's bits as a pointer (a wild
// pointer), not a reference to it. It almost always means the author wanted `&value`, so it warns.
func TestCastValueToOwnRefTypeWarnsUseAmpersand(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_val_ref.elisa", `def f() -> u8:
    a: mutable u8 = 5
    p: mutable u8& = a.cast[mutable u8&]
    return p
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "use the `&` operator") {
		t.Fatalf("expected a use-`&` warning for value->own-ref-type cast, got:\n%s", allDiagnostics(result))
	}
}

// A genuine int->ptr cast targets a DIFFERENT pointee type (address value of type uintptr cast to
// u8&), and the pointer-width address types (uintptr/usize) cast to their own ref are legitimate
// address->pointer reinterprets — neither must be flagged.
func TestCastIntToPtrNotFlagged(t *testing.T) {
	diffType := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_addr_diff.elisa", `def f() -> u8:
    addr: uintptr = 4096
    p: u8& = addr.cast[u8&]
    return p
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(diffType), "use the `&` operator") {
		t.Fatalf("uintptr->u8& int->ptr must not be flagged, got:\n%s", allDiagnostics(diffType))
	}
	sameAddr := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_addr_same.elisa", `def f() -> uintptr:
    addr: uintptr = 4096
    p: uintptr& = addr.cast[uintptr&]
    return p
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(sameAddr), "use the `&` operator") {
		t.Fatalf("uintptr->uintptr& address->pointer must not be flagged, got:\n%s", allDiagnostics(sameAddr))
	}
}

// The `x.ref[T]` reference shorthand has been removed. The error points at the right replacement:
// a same-pointee borrow on a writable place -> `&x`; a type-pun / mutability-forcing reinterpret -> `(&x).cast[T]`.
func TestRefShorthandRemovedPointsAtReplacement(t *testing.T) {
	borrow := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_borrow.elisa", `def f() -> i64:
    a: mutable i64 = 0
    p: i64& = a.ref[i64&]
    return p.i64()
`, AnalyzeOptions{})
	borrowErrs := strings.Join(borrow.Errors(), "\n")
	if !strings.Contains(borrowErrs, "reference shorthand has been removed") ||
		!strings.Contains(borrowErrs, "`&x` to borrow") {
		t.Fatalf("expected a borrow removal error pointing at `&x`, got:\n%s", borrowErrs)
	}
	reinterpret := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_reint.elisa", `def f() -> i64:
    a: mutable i64 = 0
    p: mutable u8& = a.ref[mutable u8&]
    return p.i64()
`, AnalyzeOptions{})
	reintErrs := strings.Join(reinterpret.Errors(), "\n")
	if !strings.Contains(reintErrs, "`(&x).cast[T]` to reinterpret") {
		t.Fatalf("expected a reinterpret removal error pointing at `(&x).cast[T]`, got:\n%s", reintErrs)
	}
}
