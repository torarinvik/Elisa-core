package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
)

func TestCombinedContainerRegionScanPreservesScopeRules(t *testing.T) {
	growth := &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "out"}, Field: "push"}}}
	fresh := &ast.AssignStmt{Target: &ast.Ident{Name: "out"}, Value: &ast.ParenExpr{Inner: &ast.ListLitExpr{}}}
	optional := &ast.AssignStmt{Target: &ast.Ident{Name: "out"}, Value: &ast.ListLitExpr{}, Optional: true}
	perm := func(body ...ast.Stmt) ast.Stmt { return &ast.InStoreStmt{Store: &ast.Ident{Name: "perm"}, Body: body} }
	local := func(body ...ast.Stmt) ast.Stmt { return &ast.InStoreStmt{Store: &ast.Ident{Name: "local"}, Body: body} }
	cases := []struct {
		name string
		body []ast.Stmt
		want bool
	}{
		{"empty", nil, false},
		{"direct growth", []ast.Stmt{growth}, true},
		{"growth in program arena", []ast.Stmt{perm(growth)}, false},
		{"growth in local arena", []ast.Stmt{local(growth)}, true},
		{"fresh assignment", []ast.Stmt{fresh}, true},
		{"fresh assignment in program arena", []ast.Stmt{perm(fresh)}, true},
		{"optional assignment", []ast.Stmt{optional}, false},
		{"later growth", []ast.Stmt{perm(growth), growth}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roots := map[string]bool{"perm": true}
			got := regionframesyntax.ParamContainerNeedsRegion(tc.body, "out", roots, containerGrowthMethods, storeRegionRootName)
			old := paramContainerIsGrownNeedingRegion(tc.body, "out", roots) || paramContainerReassignedFromLiteral(tc.body, "out")
			if got != tc.want || got != old {
				t.Fatalf("combined scan = %v, previous scans = %v, want %v", got, old, tc.want)
			}
		})
	}
}
