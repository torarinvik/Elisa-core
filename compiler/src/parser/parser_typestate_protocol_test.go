package parser

import (
	"testing"

	"elisacore/src/ast"
)

const typestateSocketSrc = `typestate Socket:
	fd: mutable i64
	states: Closed, Connecting, Connected
	transition connect: Closed -> Connecting
	transition established: Connecting -> Connected
	transition close: Connected -> Closed
`

// The `typestate` sugar desugars to a state-bearing struct plus one transition function per
// `transition`, all landing flat at file scope.
func TestParseTypestateDesugarsToStructAndTransitions(t *testing.T) {
	file, errs := parseSourceFile(t, typestateSocketSrc)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	var st *ast.StructDecl
	var fns []*ast.FuncDecl
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.StructDecl:
			st = n
		case *ast.FuncDecl:
			fns = append(fns, n)
		}
	}
	if st == nil {
		t.Fatalf("expected a synthesized StructDecl, got decls: %v", file.Decls)
	}
	if st.Name != "Socket" {
		t.Fatalf("expected struct named Socket, got %q", st.Name)
	}
	if len(st.NamedStateCases) != 3 {
		t.Fatalf("expected 3 named state cases, got %v", st.NamedStateCases)
	}
	if len(st.DerivedStates) != 3 {
		t.Fatalf("expected 3 derive-state clauses, got %d", len(st.DerivedStates))
	}
	if len(fns) != 3 {
		t.Fatalf("expected 3 transition functions, got %d", len(fns))
	}
	names := map[string]bool{}
	for _, fn := range fns {
		names[fn.Name] = true
		if len(fn.Ensures) != 1 {
			t.Fatalf("transition %q must carry exactly one ensures-poststate, got %d", fn.Name, len(fn.Ensures))
		}
	}
	for _, want := range []string{"connect", "established", "close"} {
		if !names[want] {
			t.Fatalf("missing transition function %q (got %v)", want, names)
		}
	}
}

// An unknown source/target state in a transition is a hard error.
func TestParseTypestateUnknownStateIsError(t *testing.T) {
	_, errs := parseSourceFile(t, `typestate S:
	states: A, B
	transition go: A -> C
`)
	if len(errs) == 0 {
		t.Fatalf("a transition into an undeclared state must be a parse error")
	}
}
