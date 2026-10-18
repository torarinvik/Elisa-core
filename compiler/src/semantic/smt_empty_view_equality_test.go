//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestSMTEmptyViewEqualityUsesLength(t *testing.T) {
	for _, expression := range []string{`view == ""`, `"" == view`, `not (view != "")`} {
		source := `def empty(view: sview) -> bool:
    ensure result == (view.len == 0)
    return ` + expression + `
`
		result := analyzeContractStrict(t, "empty_view.elisa", source)
		if len(result.Errors()) != 0 {
			t.Fatalf("%s: %v", expression, result.Errors())
		}
	}
}

func TestSMTIndexedEmptyViewEquality(t *testing.T) {
	source := `global region source
def empty(values: darray[sview @source]&, index: usize) -> bool:
    requires index < values.count
    ensure result == (values[index].len == 0)
    return values[index] == ""
`
	result := analyzeContractStrict(t, "indexed_empty_view.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
}

func TestSMTQuantifiedEmptyNestedRecordViewEqualityKeepsBinderScoped(t *testing.T) {
	source := `struct Metadata:
    text: sview
struct Item:
    metadata: Metadata

def is_empty(items: darray[Item], index: usize) -> bool:
    return items[index].metadata.text == ""
`
	result := analyzeFunctionAnalysisTestSource(t, "quantified_empty_nested_view.elisa", source)
	fnSymbol, ok := result.GlobalScope.Lookup("is_empty")
	if !ok || fnSymbol == nil {
		t.Fatal("is_empty function is missing")
	}
	fn, ok := fnSymbol.Node.(*ast.FuncDecl)
	if !ok || fn == nil {
		t.Fatal("is_empty declaration is not a function")
	}
	fnType, ok := fnSymbol.Type.(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) != 2 {
		t.Fatal("is_empty function signature is missing its parameters")
	}
	ret, ok := fn.Body[len(fn.Body)-1].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		t.Fatal("is_empty return expression is missing")
	}
	analyzer := result.analyzer
	analyzer.currentFuncDecl = fn
	analyzer.currentScope = NewScope(analyzer.globalScope)
	analyzer.currentScope.Define(&Symbol{Name: "items", Kind: SymbolParam, Type: fnType.Params[0], Node: fn, ParamIndex: 0})
	analyzer.currentScope.Define(&Symbol{Name: "index", Kind: SymbolParam, Type: fnType.Params[1], Node: fn, ParamIndex: 1})
	tr := analyzer.newSMTTranslator(nil)
	term, ok := tr.boolTerm(ret.Value, map[string]string{"index": "q_field_index"})
	if !ok || !strings.Contains(term, "q_field_index") {
		t.Fatalf("expected empty-view equality to retain the quantified index in its term, got %q (ok=%v)", term, ok)
	}
	for _, aux := range tr.auxDecls {
		if smtTermMentionsQuantifierBinder(aux) {
			t.Fatalf("quantifier binder escaped into a top-level auxiliary declaration: %s", aux)
		}
	}
}

func TestSMTEmptyViewEqualityRejectsWrongClaim(t *testing.T) {
	source := `def wrong(view: sview) -> bool:
    ensure result == (view.len == 1)
    return view == ""
`
	result := analyzeContractStrict(t, "wrong_empty_view.elisa", source)
	if ensureFailuresFor(result.Errors(), "wrong") == 0 {
		t.Fatal("wrong length must not prove")
	}
}

func TestSMTEmptyViewEqualityDoesNotReuseHeaderBeforeMutation(t *testing.T) {
	source := `def wrong(view: mutable sview&) -> bool:
    ensure result == (view == "")
    before: bool = view == ""
    view <- "changed"
    return before
`
	result := analyzeContractStrict(t, "empty_view_mutation.elisa", source)
	if ensureFailuresFor(result.Errors(), "wrong") == 0 {
		t.Fatalf("stale header must not prove: %v", result.Errors())
	}
}

func TestSMTEmptyViewEqualityAgreesWithStringDispatch(t *testing.T) {
	for _, join := range []string{"or", "and"} {
		source := `def special(view: sview) -> bool:
    ensure result == (view == "" ` + join + ` view == "sp")
    return when view:
        "" | "sp" -> true
        _ -> false
`
		result := analyzeContractStrict(t, "empty_view_dispatch.elisa", source)
		if got := ensureFailuresFor(result.Errors(), "special") > 0; got != (join == "and") {
			t.Fatalf("%s: %v", join, result.Errors())
		}
	}
}
