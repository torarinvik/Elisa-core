package parser

import (
	"testing"

	"elisacore/src/ast"
)

// errorSetOfFirstDecl returns the error-set expression of the file's first function
// declaration's `T error[...]` return type.
func errorSetOfFirstDecl(t *testing.T, file *ast.File) *ast.ErrorSetExpr {
	t.Helper()
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", file.Decls[0])
	}
	union, ok := fn.ReturnType.(*ast.ErrorUnionTypeExpr)
	if !ok {
		t.Fatalf("expected an ErrorUnionTypeExpr return, got %T", fn.ReturnType)
	}
	set, ok := union.Errors.(*ast.ErrorSetExpr)
	if !ok {
		t.Fatalf("expected an ErrorSetExpr, got %T", union.Errors)
	}
	return set
}

// `error[Mod::Set]` — a MODULE-QUALIFIED error-set name. Before parseErrorSetQualifiedSetName
// the error clause was the one type position whose grammar took a bare identifier only, so this
// failed in the parser with "expected ], got ::" and an error set declared inside a module could
// not be named from outside it. The qualified spelling is normalized to the analyzer's internal
// dotted form (`Mod.Set`), which is what joinQualifiedName produces for the declaration.
func TestParseQualifiedErrorSetName(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(x: i64) -> i64 error[M::MyErr]:
    return x
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	set := errorSetOfFirstDecl(t, file)
	if len(set.Tags) != 1 {
		t.Fatalf("expected exactly one error tag, got %d", len(set.Tags))
	}
	if set.Tags[0].SetName != "M.MyErr" {
		t.Fatalf("expected SetName %q, got %q", "M.MyErr", set.Tags[0].SetName)
	}
	if set.Tags[0].Tag != "" {
		t.Fatalf("expected a whole-family reference (empty Tag), got %q", set.Tags[0].Tag)
	}
}

// A deeper path and an explicit `.Tag` still split correctly: `::` separates modules,
// `.` selects the variant, so the two never collide.
func TestParseQualifiedErrorSetNameWithTag(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(x: i64) -> i64 error[Outer::Inner::MyErr.Bad]:
    return x
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	set := errorSetOfFirstDecl(t, file)
	if len(set.Tags) != 1 {
		t.Fatalf("expected exactly one error tag, got %d", len(set.Tags))
	}
	if set.Tags[0].SetName != "Outer.Inner.MyErr" {
		t.Fatalf("expected SetName %q, got %q", "Outer.Inner.MyErr", set.Tags[0].SetName)
	}
	if set.Tags[0].Tag != "Bad" {
		t.Fatalf("expected Tag %q, got %q", "Bad", set.Tags[0].Tag)
	}
}

// An UNQUALIFIED name is unchanged — the overwhelmingly common spelling must not regress.
func TestParseBareErrorSetNameUnchanged(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(x: i64) -> i64 error[MyErr]:
    return x
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	set := errorSetOfFirstDecl(t, file)
	if set.Tags[0].SetName != "MyErr" {
		t.Fatalf("expected SetName %q, got %q", "MyErr", set.Tags[0].SetName)
	}
}
