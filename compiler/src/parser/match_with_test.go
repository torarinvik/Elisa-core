package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// docs/125 §5 — `with NAME = LITERAL` or-alternative discriminator bindings desugar by
// fanning the or-arm into one MatchArm per alternative, each with its own `with`
// constants prepended to the (shared) body as immutable locals.

func TestMatchWithDesugarShape(t *testing.T) {
	src := "def f(e: E) -> i64:\n" +
		"    return match e:\n" +
		"        E.Lit(m) with negated = false | E.Neg(m) with negated = true:\n" +
		"            m\n" +
		"        _: 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	ret := fn.Body[0].(*ast.ReturnStmt)
	match := ret.Value.(*ast.MatchExpr)
	// Two alternatives fan out to two arms, plus the `_` arm = 3.
	if len(match.Arms) != 3 {
		t.Fatalf("arms = %d, want 3 (two fanned + default)", len(match.Arms))
	}
	// Each fanned arm's body begins with its own `with` VarDecl (negated = false/true),
	// then the shared body (`m`).
	for i, wantNegated := range []bool{false, true} {
		decl, ok := match.Arms[i].Body[0].(*ast.VarDeclStmt)
		if !ok {
			t.Fatalf("arm %d body[0] = %T, want prepended VarDeclStmt", i, match.Arms[i].Body[0])
		}
		if decl.Name != "negated" || decl.Mutable {
			t.Fatalf("arm %d with-decl = %+v, want immutable `negated`", i, decl)
		}
		lit, ok := decl.Value.(*ast.BoolLit)
		if !ok || lit.Value != wantNegated {
			t.Fatalf("arm %d with-value = %#v, want BoolLit(%v)", i, decl.Value, wantNegated)
		}
		// The shared body statement follows the prepended decl.
		if len(match.Arms[i].Body) != 2 {
			t.Fatalf("arm %d body has %d stmts, want 2 (with-decl + shared body)", i, len(match.Arms[i].Body))
		}
	}
}

func TestMatchWithMultipleBindings(t *testing.T) {
	src := "def f(e: E) -> i64:\n" +
		"    return match e:\n" +
		"        E.Lit(m) with sign = 1, tag = 10 | E.Neg(m) with sign = -1, tag = 20: m\n" +
		"        _: 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	match := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.MatchExpr)
	// First alternative's arm prepends both `sign = 1` and `tag = 10`.
	body := match.Arms[0].Body
	if len(body) != 3 {
		t.Fatalf("arm 0 body has %d stmts, want 3 (sign, tag, shared)", len(body))
	}
	first := body[0].(*ast.VarDeclStmt)
	second := body[1].(*ast.VarDeclStmt)
	if first.Name != "sign" || second.Name != "tag" {
		t.Fatalf("with-decls = %q, %q; want sign, tag", first.Name, second.Name)
	}
}

// A plain match arm with no `with` clause is unaffected — the shared body is used as-is.
func TestMatchWithoutWithUnchanged(t *testing.T) {
	src := "def f(e: E) -> i64:\n" +
		"    return match e:\n" +
		"        E.Lit(m) | E.Neg(m): m\n" +
		"        _: 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	match := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.MatchExpr)
	if _, ok := match.Arms[0].Body[0].(*ast.VarDeclStmt); ok {
		t.Fatal("plain or-arm must not gain a prepended VarDeclStmt")
	}
}

// docs/125 §5 refusal R1 — every alternative of a `with`-arm must bind the same constants.
// A body reading a constant only some alternatives supply would resolve for those siblings
// and fail late as `undefined identifier` on the others; catch it early at the arm.
func TestMatchWithBindingParityR1(t *testing.T) {
	// Mismatched names, missing on the other side, and a superset all error; identical sets
	// and `with`-free or-arms are accepted (zero-false-positive on existing patterns).
	cases := []struct {
		name    string
		arm     string
		wantErr bool
	}{
		{"different-names", "E.Lit(m) with x = 1 | E.Neg(m) with y = 2: x", true},
		{"missing-on-one", "E.Lit(m) with x = 1 | E.Neg(m): m", true},
		{"superset", "E.Lit(m) with x = 1 | E.Neg(m) with x = 2, y = 3: x", true},
		{"identical-set", "E.Lit(m) with x = 1 | E.Neg(m) with x = 2: x", false},
		{"no-with-orarm", "E.Lit(m) | E.Neg(m): m", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "def f(e: E) -> i64:\n" +
				"    return match e:\n" +
				"        " + tc.arm + "\n" +
				"        _: 0\n"
			_, errs := parseSourceFile(t, src)
			gotErr := false
			for _, e := range errs {
				if strings.Contains(e, "with`-arm must bind the same constants") {
					gotErr = true
				}
			}
			if gotErr != tc.wantErr {
				t.Fatalf("R1 diagnostic present = %v, want %v (errs: %v)", gotErr, tc.wantErr, errs)
			}
		})
	}
}
