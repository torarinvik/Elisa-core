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
