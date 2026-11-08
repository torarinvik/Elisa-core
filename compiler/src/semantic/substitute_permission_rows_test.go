package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestSubstitutePermissionRowsIndependent(t *testing.T) {
	declared := []ast.PermissionRef{{Name: "A"}}
	refs := []ast.PermissionRef{{Name: "B"}}
	d, r, _ := substitutePermissionRefs(declared, refs, nil, nil)
	d[0].Name = "changed"
	d = append(d, ast.PermissionRef{Name: "extra"})
	if declared[0].Name != "A" || refs[0].Name != "B" || r[0].Name != "B" {
		t.Fatal("permission rows alias")
	}
	if d, r, _ := substitutePermissionRefs(nil, nil, nil, nil); d != nil || r != nil {
		t.Fatal("empty rows must be nil")
	}
}

func TestSubstituteSingletonPermissionBindingIndependent(t *testing.T) {
	bound := []ast.PermissionRef{{Name: "B", Member: "z"}, {Name: "B", Member: "a"}}
	declared := []ast.PermissionRef{{Name: "P"}}
	d, r, _ := substitutePermissionRefs(declared, declared, nil, map[string][]ast.PermissionRef{"P": bound})
	if len(d) != 2 || d[0].Member != "a" || d[1].Member != "z" {
		t.Fatal("bound row was not canonicalized")
	}
	d[0].Name = "changed"
	if bound[1].Name != "B" || r[0].Name != "B" || declared[0].Name != "P" {
		t.Fatal("substituted rows alias their inputs or each other")
	}
}
