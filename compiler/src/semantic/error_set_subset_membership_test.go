package semantic

import (
	"strings"
	"testing"
)

// Brace-subset error sets carry EXACTLY the listed variants: a function declaring
// `error[FooError{Bad1}, BarError{Bad3, Bad4}]` propagates and matches only Bad1,
// Bad3, Bad4 — never Bad2 (which cannot occur). These tests pin that membership is
// precise in all four directions: exhaustiveness, missing-variant, out-of-set match,
// and out-of-set raise.

const subsetMembershipDecls = `error FooError:
    Bad1
    Bad2
error BarError:
    Bad3
    Bad4(n: u32)
extern foo() -> i64 error[FooError{Bad1}, BarError{Bad3, Bad4}]
`

// A catch handling exactly the in-set variants (no catch-all, no Bad2) is exhaustive.
func TestErrorSubsetCatchExhaustiveOverSubsetOnly(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "subset_exhaustive.elisa", subsetMembershipDecls+`
def use() -> i64:
    catch foo():
        n:
            return n
        FooError.Bad1:
            return 1
        BarError.Bad3:
            return 3
        BarError.Bad4(x):
            return x.i64()
    return 0
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "non-exhaustive") || strings.Contains(all, "Bad2") {
		t.Fatalf("a catch over exactly the brace-subset {Bad1,Bad3,Bad4} must be exhaustive without Bad2 or a catch-all, got:\n%s", all)
	}
}

// Omitting an in-set variant is non-exhaustive — and the report names the missing
// in-set variant, never the out-of-set Bad2.
func TestErrorSubsetCatchMissingInSetVariant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "subset_missing.elisa", subsetMembershipDecls+`
def use() -> i64:
    catch foo():
        n:
            return n
        FooError.Bad1:
            return 1
        BarError.Bad4(x):
            return x.i64()
    return 0
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "non-exhaustive") || !strings.Contains(all, "Bad3") {
		t.Fatalf("expected non-exhaustive citing missing BarError.Bad3, got:\n%s", all)
	}
	if strings.Contains(all, "Bad2") {
		t.Fatalf("must NOT demand the out-of-set FooError.Bad2, got:\n%s", all)
	}
}

// Matching an out-of-set variant (Bad2) is rejected — it cannot occur in this union.
func TestErrorSubsetCatchRejectsOutOfSetVariant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "subset_outofset.elisa", subsetMembershipDecls+`
def use() -> i64:
    catch foo():
        n:
            return n
        FooError.Bad1:
            return 1
        FooError.Bad2:
            return 2
        BarError.Bad3:
            return 3
        BarError.Bad4(x):
            return x.i64()
    return 0
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "Bad2") || !strings.Contains(all, "does not match") {
		t.Fatalf("expected FooError.Bad2 to be rejected as not in the set, got:\n%s", all)
	}
}

// Raising an out-of-set variant is rejected.
func TestErrorSubsetRaiseRejectsOutOfSetVariant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "subset_raise.elisa", `
error FooError:
    Bad1
    Bad2
def g() -> i64 error[FooError{Bad1}]:
    raise FooError.Bad2
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "Bad2") || !strings.Contains(all, "cannot propagate") {
		t.Fatalf("expected raise of out-of-set FooError.Bad2 to be rejected, got:\n%s", all)
	}
}
