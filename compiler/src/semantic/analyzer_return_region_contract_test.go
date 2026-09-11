package semantic

import (
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
