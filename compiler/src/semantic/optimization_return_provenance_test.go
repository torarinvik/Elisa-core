package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestReturnedRecursiveCallRecordsLazilyInferredProvenance(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := `struct BorrowedBox:
    value: i64&
def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}
def cycle_a(value: i64&, count: i64) -> BorrowedBox:
    if count <= 0:
        return pack(value)
    return cycle_b(value, count - 1)
def cycle_b(value: i64&, count: i64) -> BorrowedBox:
    return cycle_a(value, count)
`
	result := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("fresh analysis: %v", errors)
	}
	found := false
	for _, decl := range retainedWalkDecls(result.File) {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "cycle_a" {
			continue
		}
		walkStoreScanNodes(funcScanStmts(fn), func(node any) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return
			}
			callee, ok := call.Func.(*ast.Ident)
			if !ok || callee.Name != "cycle_b" {
				return
			}
			found = true
			facts, known := result.ExprOptimizationFacts(call)
			if !known || !facts.PackedStoreProvenance.HasNonStoreProvenance || facts.PackedStoreProvenance.HasAnyPackedStoreProvenance() {
				t.Errorf("returned recursive call omitted its inferred parameter provenance: known=%v facts=%+v", known, facts)
			}
		})
	}
	if !found {
		t.Fatal("recursive return call was not analyzed")
	}
}
