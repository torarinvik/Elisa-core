package parser

import (
	"testing"

	"elisacore/src/ast"
)

// Phase 4: `can <set> as <Family>:` parses into CanStmt.As.
func TestParseCanCastHeader(t *testing.T) {
	src := `def build() -> i64:
    can Disk.Read as IO:
        return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	canStmt, ok := fn.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected can stmt, got %T", fn.Body[0])
	}
	if canStmt.As != "IO" {
		t.Fatalf("expected cast target IO, got %q", canStmt.As)
	}
	if len(canStmt.Permissions) != 1 || canStmt.Permissions[0].Name != "Disk" || canStmt.Permissions[0].Member != "Read" {
		t.Fatalf("expected Disk.Read permission, got %#v", canStmt.Permissions)
	}
}

// A plain `can <set>:` header still parses with no cast target.
func TestParsePlainCanHeaderHasNoCastTarget(t *testing.T) {
	src := `def build() -> i64:
    can Disk.Read:
        return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	canStmt := fn.Body[0].(*ast.CanStmt)
	if canStmt.As != "" {
		t.Fatalf("expected no cast target, got %q", canStmt.As)
	}
}
