package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestFactoryNestedLaterHelperRecordsLazilyInferredProvenance(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `struct BorrowedBox:
    value: i64&
def factory(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: helper(value)}
def helper(value: i64&) -> i64&:
    return value
`
	result := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("fresh analysis: %v", errors)
	}

	found := false
	for _, decl := range retainedWalkDecls(result.File) {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "factory" {
			continue
		}
		walkStoreScanNodes(funcScanStmts(fn), func(node any) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return
			}
			callee, ok := call.Func.(*ast.Ident)
			if !ok || callee.Name != "helper" {
				return
			}
			found = true
			facts, known := result.ExprOptimizationFacts(call)
			if !known || !facts.PackedStoreProvenance.HasNonStoreProvenance || facts.PackedStoreProvenance.HasAnyPackedStoreProvenance() {
				t.Errorf("nested returned call omitted lazily inferred parameter provenance: known=%v facts=%+v", known, facts)
			}
		})
	}
	if !found {
		t.Fatal("factory's nested helper call was not analyzed")
	}
}
