package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestInferredRegionParamNameAvoidsExistingBinders(t *testing.T) {
	fn := &ast.FuncDecl{TypeParams: []string{"__rg_out"}, RegionParams: []string{"__rg_out_1"}, PermissionParams: []string{"__rg_out_2"}}
	if got := InferredRegionParamName(fn, "out"); got != "__rg_out_3" {
		t.Fatalf("got %q", got)
	}
}
