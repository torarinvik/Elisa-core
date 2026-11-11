package semantic

import (
	"strings"
	"testing"
)

// A bare call to a namespaced overload set dispatches by receiver like a qualified one,
// and a private overload is private to the namespace of its visible name.
func TestNamespacedOverloadDispatchesFromBareCall(t *testing.T) {
	src := `module Semantic:
    struct NameSet:
        v: i64

    def entry() -> NameSet:
        return NameSet{v: 1}

    private:
        def contains(count: u32, name: sview) -> bool:
            return false

        def contains(set: NameSet&, name: sview) -> bool:
            return name.len == 0

extend Semantic:
    def user(n: sview) -> bool:
        s: mutable NameSet = entry()
        return contains(&s, n) and not contains(3, n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ovl_bare.elisa", src, AnalyzeOptions{})
	for _, d := range result.Diagnostics {
		t.Errorf("unexpected diagnostic: %s", d.Message)
	}
}

func TestPrivateOverloadOwnerIsVisibleNamespace(t *testing.T) {
	src := `module A:
    struct S:
        v: i64

    def mk() -> S:
        return S{v: 1}

    def f(count: u32) -> bool:
        return false

    private:
        def f(set: S&) -> bool:
            return true

module B:
    def user() -> bool:
        s: A::S = A::mk()
        return A::f(&s)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ovl_private.elisa", src, AnalyzeOptions{})
	found := false
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message, "is private to module \"A\"") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the private overload to be private to module A, got %v", result.Diagnostics)
	}
}

func TestPrivateSelectiveImportDiagnosticRequiresMatchingArguments(t *testing.T) {
	src := `module A:
    private:
        def pick[T](value: T, second: T) -> i64:
            return 1

from A import pick

def pick(value: bool) -> i64:
    return 2

def public_match() -> i64:
    return pick(true)

def private_match() -> i64:
    return pick(true, false)

def neither_matches() -> i64:
    return pick(true, 1)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "private_selective_import.elisa", src, AnalyzeOptions{})
	privacyErrors := 0
	for _, diagnostic := range result.Diagnostics {
		if strings.Contains(diagnostic.Message, "is private to module") {
			privacyErrors++
			if diagnostic.Pos.Line != 15 {
				t.Errorf("privacy diagnostic should identify only the matching private signature: %v", diagnostic)
			}
		}
	}
	if privacyErrors != 1 {
		t.Fatalf("expected one privacy diagnostic, got %v", result.Diagnostics)
	}
}
