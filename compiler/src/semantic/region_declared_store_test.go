package semantic

import (
	"strings"
	"testing"
)

func expectErrorLines(t *testing.T, errs []string, want []string) {
	t.Helper()
	if len(errs) != len(want) {
		t.Fatalf("expected errors at %v, got:\n%s", want, strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected errors at %v, got:\n%s", want, strings.Join(errs, "\n"))
		}
	}
}

// docs/128 §8: a store-backed root may declare a program-lived store region. Its nodes live
// until exit, so every region-carrying payload -- annotated or not -- is a store into that
// region: a caller-region or parameter-region view is rejected; a program-lived one is not.
func TestDeclaredStoreRegionPayloads(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "declared_store.elisa", `global region source

enum Node layout(handle: u32) @source:
    pass

enum Expr is Node:
    Name(name: sview)
    Pair(left: Expr, right: Expr)

def local_view() -> Expr:
    can Memory.Allocate:
        d: mutable dstr = []
        d.push(65)
        return Expr.Name(d.as_sview())

def param_view[@a](s: sview @a) -> Expr:
    return Expr.Name(s)

def program_view(s: sview @source) -> Expr:
    return Expr.Pair(Expr.Name(s), Expr.Name("lit"))
`)
	expectErrorLines(t, result.Errors(), []string{":14:", ":17:"})
}

// The declaration is validated: program-lived region, hierarchy root, store-backed enum.
func TestDeclaredStoreRegionValidation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "declared_store_bad.elisa", `global region source

enum Root layout(handle: u32) @source:
    pass

enum Child is Root @source:
    Leaf(value: i64)
    Pair(left: Child, right: Child)

enum Flat @source:
    A
    B

def f[@r]() -> void:
    return

enum Other layout(handle: u32) @nowhere:
    pass
`)
	expectErrorLines(t, result.Errors(), []string{":6:", ":10:", ":17:"})
}
