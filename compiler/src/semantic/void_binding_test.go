//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/119 §1 (E1/E2): binding a void expression, or declaring a binding with a
// void-bearing type, is a semantic error — previously these fell through to raw
// LLVM errors ("Cannot allocate unsized type"). `_ = voidFn()` stays legal (a
// discard, not a binding), as do `void&` refs and `-> void` returns.

func semanticErrorsFor(t *testing.T, src string) []string {
	t.Helper()
	result := analyzeTreeTestSourceWithSemanticErrors(t, "void_binding_test.elisa", src)
	return result.Errors()
}

func requireOneErrorContaining(t *testing.T, src, want string) {
	t.Helper()
	msgs := semanticErrorsFor(t, src)
	if len(msgs) == 0 {
		t.Fatalf("expected an error containing %q, got none", want)
	}
	for _, m := range msgs {
		if strings.Contains(m, want) {
			return
		}
	}
	t.Fatalf("expected an error containing %q, got: %v", want, msgs)
}

func TestVoidBindingInferredIsError(t *testing.T) {
	requireOneErrorContaining(t, `
def side_effect() -> void:
    pass

def main() -> void:
    x = side_effect()
    pass
`, `cannot bind void expression to "x"`)
}

func TestVoidAnnotatedBindingIsError(t *testing.T) {
	requireOneErrorContaining(t, `
def side_effect() -> void:
    pass

def main() -> void:
    y: void = side_effect()
    pass
`, `variable "y": void is not a bindable type`)
}

func TestVoidContainerElementIsError(t *testing.T) {
	requireOneErrorContaining(t, `
def main() -> void:
    xs: darray[void] = []
    pass
`, `variable "xs": void is not a bindable type`)
}

func TestVoidTupleElementBindingIsError(t *testing.T) {
	requireOneErrorContaining(t, `
def side_effect() -> void:
    pass

def main() -> void:
    a, b = 1, side_effect()
    pass
`, `cannot bind void expression to "b"`)
}

func TestVoidDiscardStaysLegal(t *testing.T) {
	analyzeTreeTestSource(t, "void_discard_ok.elisa", `
def side_effect() -> void:
    pass

def main() -> void:
    _ = side_effect()
    pass
`)
}

func TestVoidMutableBindingIsError(t *testing.T) {
	requireOneErrorContaining(t, `
def side_effect() -> void:
    pass

def main() -> void:
    x: mutable i64 = 0
    z = side_effect()
    pass
`, `cannot bind void expression to "z"`)
}
