package parser

import (
	"elisacore/src/ast"
	"testing"
)

func TestGenericAggregateReturnRegionContract(t *testing.T) {
	file, errs := parseSourceFile(t, "def make[@r](x: Packet[r] @r) -> Packet[r] @r:\n    return x\n")
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if fn.ReturnRegion != "r" {
		t.Fatalf("lost return contract: %q", fn.ReturnRegion)
	}
	ret := fn.ReturnType.(*ast.GenericType)
	if ret.Region != "" {
		t.Fatalf("return suffix not promoted: %q", ret.Region)
	}
	param := fn.Params[0].Type.(*ast.GenericType)
	if param.Region != "r" {
		t.Fatalf("parameter suffix lost: %q", param.Region)
	}
}
