package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func TestDeclaredReturnRegionCarriesParamProvenance(t *testing.T) {
	source := `
enum Expr:
    Invalid

def identity[@r](values: darray[Expr]& @r) -> Expr @r:
    return Expr.Invalid

def caller(values: mutable darray[Expr]&):
    value: Expr = identity(values)
    values.push(value)
`
	lexerInstance := lexer.New("return_region_contract.elisa", []byte(source))
	tokens := lexerInstance.Tokenize()
	if errs := lexerInstance.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	parsed := parser.New(tokens)
	file := parsed.ParseFile("return_region_contract.elisa")
	if errs := parsed.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	result := Analyze(file)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	symbol, ok := result.GlobalScope.Lookup("identity")
	if !ok {
		t.Fatal("expected identity symbol")
	}
	functionType, ok := symbol.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected identity function type, got %T", symbol.Type)
	}
	if functionType.ReturnRegion != "r" {
		t.Fatalf("expected return region r, got %q", functionType.ReturnRegion)
	}
	if !functionType.ReturnProvenanceKnown || !functionType.ReturnProvenance.HasDirectParamDep || functionType.ReturnProvenance.DirectParamDep != 0 {
		t.Fatalf("expected return provenance to depend on values parameter, got %#v", functionType.ReturnProvenance)
	}
	if len(functionType.Params) != 1 {
		t.Fatalf("expected one parameter, got %d", len(functionType.Params))
	}
	if region := regionParamReturnTypeRegion(functionType.Params[0]); region != "r" {
		t.Fatalf("expected values parameter to carry region r, got %q (%s)", region, functionType.Params[0])
	}
	callerSymbol, ok := result.GlobalScope.Lookup("caller")
	if !ok {
		t.Fatal("expected caller symbol")
	}
	callerType, ok := callerSymbol.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected caller function type, got %T", callerSymbol.Type)
	}
	if len(callerType.RegionParams) != 1 || len(callerType.Params) != 1 || regionParamReturnTypeRegion(callerType.Params[0]) != callerType.RegionParams[0] {
		t.Fatalf("expected forwarding caller to infer one region parameter, got regions=%v param=%s", callerType.RegionParams, callerType.Params[0])
	}
}

func TestTupleRegionContractTracksSViewFields(t *testing.T) {
	result := analyzeTreeTestSource(t, "tuple_region_contract.elisa", `def pair[@r](source: sview @r) -> (known: bool, value: sview) @r:
	return (true, source)

def project[@r](pair: (known: bool, value: sview) @r) -> sview @r:
	return pair.value
`)
	for _, name := range []string{"pair", "project"} {
		symbol, ok := result.GlobalScope.Lookup(name)
		if !ok {
			t.Fatalf("expected %s symbol", name)
		}
		functionType, ok := symbol.Type.(*FuncType)
		if !ok {
			t.Fatalf("expected %s function type, got %T", name, symbol.Type)
		}
		if functionType.ReturnRegion != "r" {
			t.Fatalf("expected %s return region r, got %q", name, functionType.ReturnRegion)
		}
	}
	pairSymbol, _ := result.GlobalScope.Lookup("pair")
	pairType := pairSymbol.Type.(*FuncType)
	returnTuple, ok := pairType.Return.(*TupleType)
	if !ok || len(returnTuple.Fields) != 2 {
		t.Fatalf("expected pair to return a two-field tuple, got %T", pairType.Return)
	}
	viewType, ok := returnTuple.Fields[1].Type.(*SViewType)
	if !ok || viewType.Region != "r" {
		t.Fatalf("expected tuple sview field to retain region r, got %T %#v", returnTuple.Fields[1].Type, returnTuple.Fields[1].Type)
	}
	projectSymbol, _ := result.GlobalScope.Lookup("project")
	projectType := projectSymbol.Type.(*FuncType)
	parameterTuple, ok := projectType.Params[0].(*TupleType)
	if !ok || len(parameterTuple.Fields) != 2 {
		t.Fatalf("expected project parameter to remain a two-field tuple, got %T", projectType.Params[0])
	}
	parameterView, ok := parameterTuple.Fields[1].Type.(*SViewType)
	if !ok || parameterView.Region != "r" {
		t.Fatalf("expected tuple parameter sview field to retain region r, got %T %#v", parameterTuple.Fields[1].Type, parameterTuple.Fields[1].Type)
	}
}

func TestTupleSViewCannotEscapeItsBackingRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tuple_sview_region_escape.elisa", `def leak() -> (known: bool, value: sview):
	can Memory.Allocate, Abort.Panic:
		region local(64):
			bytes: mutable darray[u8] @local = []
			bytes.push(65)
			bytes.push(0)
			view: sview @local = bytes.as_sview()
			return (true, view)
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, "escapes via return") {
		t.Fatalf("expected tuple containing local-region sview to be rejected, got:\n%s", diagnostics)
	}
}

func TestTupleRegionContractRejectsMixedSViewLifetimes(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tuple_mixed_sview_regions.elisa", `def combine[@a, @b](left: sview @a, right: sview @b) -> (left: sview, right: sview) @a:
	return (left, right)

def relay[@a, @b](pair: (left: sview, right: sview) @b) -> (left: sview, right: sview) @a:
	return pair
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, `tuple return field "right" is tied to region "b"`) || !strings.Contains(diagnostics, `tuple return field "left" is tied to region "b"`) {
		t.Fatalf("expected tuple literals and tuple variables to reject lifetimes not proven to satisfy @a, got:\n%s", diagnostics)
	}
}
