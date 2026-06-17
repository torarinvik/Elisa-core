//go:build cgo

package semantic

import "testing"

// A predicate law parses and analyzes as a pure bool-returning function, and is callable like one
// (the docs/85 "a law is literally a function" foundation, Stage 1a).
func TestLawDeclAnalyzesAsBoolFunction(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def check(n: i64) -> bool:
    return Positive(n)
`
	result := analyzeTreeTestSource(t, "law_basic.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("law + call should analyze cleanly, got: %v", errs)
	}
}

// A generic predicate law infers its subject type at the call site.
func TestLawDeclGenericSubjectInfers(t *testing.T) {
	src := `
law NonEmpty[T](self: darray[T]&) = self.count > 0

def check(xs: darray[f64]&) -> bool:
    return NonEmpty(xs)
`
	result := analyzeTreeTestSource(t, "law_generic.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("generic law should infer subject type, got: %v", errs)
	}
}

// A law must be PURE: a predicate that performs an effect is rejected (docs/85 §9.5).
func TestLawDeclImpureRejected(t *testing.T) {
	src := `
def shout(x: i64) -> bool can[Console.Write]:
    print("x") can Console.Write
    return x > 0

law Bad(self: i64) = shout(self)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "law_impure.elisa", src, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !contains(all, "must be pure") {
		t.Fatalf("impure law should be rejected for purity, got:\n%s", all)
	}
}

// A law needs a subject — at least one value parameter.
func TestLawDeclMissingSubjectRejected(t *testing.T) {
	src := `
law Bad() = true
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "law_nosubject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "needs a subject") {
		t.Fatalf("subjectless law should be rejected, got:\n%s", allDiagnostics(result))
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOfSubstring(haystack, needle) >= 0)
}

func indexOfSubstring(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// `subject is Law` desugars to the call Law(subject) and type-checks (docs/85 Stage 1b).
func TestLawIsExprDesugars(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def check(n: i64) -> bool:
    return n is Positive

def use(n: i64) -> bool:
    requires n is Positive
    return true
`
	result := analyzeTreeTestSource(t, "law_is.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`n is Positive` should analyze (desugar to Positive(n)), got: %v", errs)
	}
	if len(result.LawIsCalls) == 0 {
		t.Fatalf("expected at least one recorded law-is desugar call")
	}
}

// `is Law` flow-narrowing reads as a bool condition; a type mismatch on the subject is an error.
func TestLawIsExprSubjectTypeMismatch(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def check(s: cstr) -> bool:
    return s is Positive
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "law_is_mismatch.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Fatal("`cstr is Positive` should be a type error (subject is i64-typed)")
	}
}
