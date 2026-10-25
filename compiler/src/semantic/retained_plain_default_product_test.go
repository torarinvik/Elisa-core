//go:build cgo

package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

const plainDefaultProductPrefix = `global values: i32[2] = [11, 22]
global cursor: usize = 0
`
const plainDefaultProductFirst = `def first(value: i32 = values[cursor]) -> i32:
    return value
`
const plainDefaultProductSecond = `def second(value: i32 = values[cursor]) -> i32:
    return value
`

func plainDefaultProductFixture(t *testing.T, source string) (*Result, *RetainedAnalysis) {
	t.Helper()
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	return result, state
}

func TestPlainDefaultProductCapturesAndRebindsFreshDestination(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := plainDefaultProductPrefix + plainDefaultProductFirst
	result, state := plainDefaultProductFixture(t, source)
	fn := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	product, err := captureRetainedPlainDefaultProduct(state.a, fn)
	if err != nil {
		t.Fatal(err)
	}
	if product.Rows["exprFacts"] == 0 || product.Rows["exprTypes"] == 0 || product.Rows["indexBoundsRuntimeSites"] == 0 || len(product.GlobalDependencies) != 2 {
		t.Fatalf("missing real header/default facts: %+v", product.Rows)
	}
	other, otherState := plainDefaultProductFixture(t, source)
	target := retainedProofNamedDecl(t, other, "first").(*ast.FuncDecl)
	rebound, err := rebindRetainedPlainDefaultProduct(product, otherState.a, target)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Owner == product.Owner || rebound.Certificate != product.Certificate {
		t.Fatal("rebind retained source owner or changed semantic contribution")
	}
	if product.admissionDecline() == "" {
		t.Fatal("unintegrated Stage A accidentally authorized Update")
	}
}

func TestPlainDefaultProductRawCleanupPreservesSharedGlobalIdentities(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := plainDefaultProductPrefix + plainDefaultProductFirst + plainDefaultProductSecond
	result, state := plainDefaultProductFixture(t, source)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	survivor := retainedProofNamedDecl(t, result, "second").(*ast.FuncDecl)
	product, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	globalTokens := map[*Symbol]string{}
	for symbol, token := range product.Symbols {
		if symbol.Node != owner {
			globalTokens[symbol] = token
		}
	}
	if len(globalTokens) != 2 {
		t.Fatal("fixture did not share both actual global identities")
	}
	survivingIndex := survivor.Params[0].DefaultValue.(*ast.IndexExpr)
	survivingFacts := result.ExprFacts[survivingIndex]
	if err := cleanupRetainedPlainDefaultProduct(state.a, result, product); err != nil {
		t.Fatal(err)
	}
	// Stage A deliberately does not pretend to have run Update admission. Complete
	// unregistering via the existing semantic removal operation in this component test.
	state.removeLiveFunctions([]*ast.FuncDecl{owner})
	for symbol, token := range globalTokens {
		if state.a.optimizationSymbolIDs[symbol] != token {
			t.Fatal("shared global identity token was deleted/reallocated")
		}
	}
	if !reflect.DeepEqual(result.ExprFacts[survivingIndex], survivingFacts) {
		t.Fatal("unaffected actual default facts changed")
	}
	for _, node := range product.Nodes {
		key := reflect.ValueOf(node)
		for _, path := range specializationASTMapPaths() {
			table := specializationField(reflect.ValueOf(state.a).Elem(), path)
			if !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) && table.MapIndex(key).IsValid() {
				t.Fatalf("raw owner row survived cleanup: %s/%T", reflect.TypeOf(*state.a).FieldByIndex(path).Name, node)
			}
		}
	}
	cold := AnalyzeWithOptions(retainedParse(t, plainDefaultProductPrefix+plainDefaultProductSecond), AnalyzeOptions{})
	for _, pair := range []struct {
		name       string
		cold, warm int
	}{{"ExprTypes", len(cold.ExprTypes), len(result.ExprTypes)}, {"ExprFacts", len(cold.ExprFacts), len(result.ExprFacts)}, {"FunctionAnalyses", len(cold.FunctionAnalyses), len(result.FunctionAnalyses)}} {
		if pair.cold != pair.warm {
			t.Fatalf("raw %s differs after removal: cold=%d warm=%d", pair.name, pair.cold, pair.warm)
		}
	}
	if cold.ElisionSummary.ArrayBounds != result.ElisionSummary.ArrayBounds {
		t.Fatal("raw cleanup left stale live bounds counts")
	}
}

func TestPlainDefaultProductDeclinesUnknownShapesAndSharedSyntax(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := plainDefaultProductPrefix + plainDefaultProductFirst + plainDefaultProductSecond
	result, state := plainDefaultProductFixture(t, source)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	survivor := retainedProofNamedDecl(t, result, "second").(*ast.FuncDecl)
	product, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit alias-graph boundary: another live declaration now references the
	// owner's actual default syntax. It must decline before deleting any rows.
	survivor.Params[0].DefaultValue = owner.Params[0].DefaultValue
	before := len(result.ExprFacts)
	if err := cleanupRetainedPlainDefaultProduct(state.a, result, product); err == nil {
		t.Fatal("shared live source AST was silently deleted")
	}
	if len(result.ExprFacts) != before {
		t.Fatal("declined cleanup mutated facts")
	}
	unknownSource := `def first[T](value: T) -> T:
    return value
`
	unknown, unknownState := plainDefaultProductFixture(t, unknownSource)
	generic := retainedProofNamedDecl(t, unknown, "first").(*ast.FuncDecl)
	if _, err := captureRetainedPlainDefaultProduct(unknownState.a, generic); err == nil {
		t.Fatal("generic product admitted without ownership/schema support")
	}
}
