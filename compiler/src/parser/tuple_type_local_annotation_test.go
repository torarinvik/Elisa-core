package parser

import (
	"elisacore/src/ast"
	"testing"
)

// A tuple type is legal in every annotation position. The parameter and return
// positions always worked; a LOCAL annotation did not, because the statement-level
// typed-var-decl lookahead only admitted a type starting with IDENT/mutable/tail/
// heap/stack/static — never `(`. A tuple-annotated local therefore fell through to
// expression parsing and died on its own colon ("unexpected token : in expression").
func TestParseTupleTypeLocalAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, `def probe(t: (a: int, b: int)) -> (x: int, y: int):
    local: (m: int, n: int) = (t.a, t.b)
    return (local.m, local.n)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", file.Decls[0])
	}

	// The local's annotation must reach the AST as a real tuple type, not as a
	// stray expression statement.
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected the tuple-annotated local to parse as a VarDeclStmt, got %T", fn.Body[0])
	}
	if decl.Name != "local" {
		t.Fatalf("expected local named %q, got %q", "local", decl.Name)
	}
	tupleType, ok := decl.Type.(*ast.TupleTypeExpr)
	if !ok {
		t.Fatalf("expected local's type to be a TupleTypeExpr, got %T", decl.Type)
	}
	if len(tupleType.Fields) != 2 {
		t.Fatalf("expected 2 tuple fields, got %d", len(tupleType.Fields))
	}
	if tupleType.Fields[0].Name != "m" || tupleType.Fields[1].Name != "n" {
		t.Fatalf("expected fields (m, n), got (%s, %s)", tupleType.Fields[0].Name, tupleType.Fields[1].Name)
	}
}

// Elisa's tuple type is NAMED-FIELD only: `(a: int, b: int)`. A positional spelling
// `(int, int)` is not tuple syntax — the parser reads `int` as the field NAME and
// then requires the colon. Pinned so the local-annotation lookahead above cannot be
// mistaken for support of a positional form.
func TestParsePositionalTupleTypeRejected(t *testing.T) {
	for _, src := range []string{
		"def probe(t: (int, int)) -> bool:\n    return true\n",
		"def probe() -> (int, int):\n    return (1, 2)\n",
		"def probe() -> int:\n    t: (int, int) = (1, 2)\n    return t.m\n",
	} {
		_, errs := parseSourceFile(t, src)
		if len(errs) == 0 {
			t.Fatalf("expected a parser error for positional tuple type in %q, got none", src)
		}
	}
}
