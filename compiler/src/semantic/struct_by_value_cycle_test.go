package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func structCycleErrors(t *testing.T, src string) string {
	t.Helper()
	p := parser.New(lexer.New("struct_cycle.elisa", []byte(src)).Tokenize())
	file := p.ParseFile("struct_cycle.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return strings.Join(Analyze(file).Errors(), "\n")
}

// A struct that contains itself by value has no finite layout. It used to pass analysis and
// then crash the LLVM backend with a Go stack overflow (lowerType <-> ensureStructBody).
func TestStructContainingItselfByValueIsRejected(t *testing.T) {
	direct := structCycleErrors(t, "struct A:\n    a: A\n\ndef main() -> i32:\n    return 0\n")
	if !strings.Contains(direct, `struct "A" cannot contain "A" by value`) {
		t.Fatalf("direct self-containment not rejected:\n%s", direct)
	}
	mutual := structCycleErrors(t, "struct A:\n    b: B\nstruct B:\n    a: A\n\ndef main() -> i32:\n    return 0\n")
	if !strings.Contains(mutual, `struct "A" cannot contain "A" by value`) || !strings.Contains(mutual, `struct "B" cannot contain "B" by value`) {
		t.Fatalf("mutual self-containment not rejected:\n%s", mutual)
	}
}

// Indirection breaks the cycle: references and containers stay legal, and so does a
// non-cyclic by-value nesting.
func TestStructSelfReferenceThroughIndirectionIsAccepted(t *testing.T) {
	for _, src := range []string{
		"struct Node:\n    next: Node&?\n    v: i32\n\ndef main() -> i32:\n    return 0\n",
		"struct Tree:\n    kids: darray[Tree]\n\ndef main() -> i32:\n    return 0\n",
		"struct P:\n    x: i32\nstruct Q:\n    p: P\n    p2: P\n\ndef main() -> i32:\n    return 0\n",
	} {
		if got := structCycleErrors(t, src); strings.Contains(got, "by value") {
			t.Fatalf("indirect/acyclic struct wrongly rejected:\n%s\n%s", src, got)
		}
	}
}
