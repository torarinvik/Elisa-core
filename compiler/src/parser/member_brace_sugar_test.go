package parser

import (
	"testing"

	"elisacore/src/ast"
)

// `can[Family{A, B}]` expands to one PermissionRef per member (== `Family.A, Family.B`).
func TestParsePermissionMemberBraceSugar(t *testing.T) {
	src := `def g() -> void can[Disk{Read, Write}]:
    pass
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if len(fn.Permissions) != 2 {
		t.Fatalf("expected 2 permission refs from brace sugar, got %d: %+v", len(fn.Permissions), fn.Permissions)
	}
	if fn.Permissions[0].Name != "Disk" || fn.Permissions[0].Member != "Read" ||
		fn.Permissions[1].Member != "Write" {
		t.Fatalf("unexpected expansion: %+v", fn.Permissions)
	}
}

// `error[E{A, B}]` expands to one ErrorTagExpr per tag (== `E.A, E.B`).
func TestParseErrorSetMemberBraceSugar(t *testing.T) {
	src := `error E:
    Bad1
    Bad2

extern f() -> i64 error[E{Bad1, Bad2}]
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	ext := file.Decls[1].(*ast.ExternFuncDecl)
	var set *ast.ErrorSetExpr
	if u, ok := ext.ReturnType.(*ast.ErrorUnionTypeExpr); ok {
		set, _ = u.Errors.(*ast.ErrorSetExpr)
	}
	if set == nil {
		t.Fatalf("expected error-set return; got %T", ext.ReturnType)
	}
	if len(set.Tags) != 2 || set.Tags[0].SetName != "E" || set.Tags[0].Tag != "Bad1" || set.Tags[1].Tag != "Bad2" {
		t.Fatalf("unexpected error-set expansion: %+v", set.Tags)
	}
}
