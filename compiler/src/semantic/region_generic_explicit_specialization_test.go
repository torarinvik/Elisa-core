package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestGenericParamsForFuncTypeIncludesRegionParams(t *testing.T) {
	params := genericParamsForFuncType(&FuncType{
		TypeParams:   []string{"T"},
		RegionParams: []string{"owner", "scratch"},
		GenericParams: []ast.GenericParam{
			{Kind: ast.GenericParamType, Name: "T"},
			{Kind: ast.GenericParamRegion, Name: "owner"},
		},
	})
	want := []ast.GenericParam{
		{Kind: ast.GenericParamType, Name: "T"},
		{Kind: ast.GenericParamRegion, Name: "owner"},
		{Kind: ast.GenericParamRegion, Name: "scratch"},
	}
	if len(params) != len(want) {
		t.Fatalf("expected merged type/region parameters %v, got %v", want, params)
	}
	for i := range want {
		if params[i].Kind != want[i].Kind || params[i].Name != want[i].Name {
			t.Fatalf("parameter %d: expected %+v, got %+v", i, want[i], params[i])
		}
	}
}

func TestGenericParamsForFuncTypeExcludesInferredRegionParams(t *testing.T) {
	params := genericParamsForFuncType(&FuncType{
		TypeParams:           []string{"T"},
		RegionParams:         []string{"explicit", "inferred"},
		InferredRegionParams: []string{"inferred"},
	})
	if len(params) != 2 || params[0].Name != "T" || params[1].Name != "explicit" {
		t.Fatalf("inferred region must not become an explicit specialization argument: %+v", params)
	}
}

func TestExplicitRegionOnlyFunctionSpecialization(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "explicit_region_only_function_specialization.elisa", `def empty[@r]() -> darray[u8] @r can[Memory.Allocate]:
	return []

def main() -> i64:
	region request:
		value: darray[u8] @request = empty[request]()
		return 0
`)
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("explicit region-only specialization should resolve its region argument:\n%s", errors)
	}
	functionType := specializedFunctionTypeFromResult(t, result)
	if len(functionType.RegionParams) != 0 {
		t.Fatalf("specialized function retains generic region params: %v", functionType.RegionParams)
	}
	returnedArray, ok := functionType.Return.(*DArrayType)
	if !ok || returnedArray.Region != "request" {
		t.Fatalf("expected specialized return darray in request, got %#v", functionType.Return)
	}
}

func TestExplicitMixedTypeAndRegionFunctionSpecialization(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "explicit_mixed_function_specialization.elisa", `def singleton[T, @r](value: T) -> darray[T] @r can[Memory.Allocate]:
	return [value]

def main() -> i64:
	region request:
		values: darray[i64] @request = singleton[i64, request](42)
		return values.count.i64()
`)
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("explicit mixed type/region specialization should resolve both arguments:\n%s", errors)
	}
	functionType := specializedFunctionTypeFromResult(t, result)
	if len(functionType.TypeParams) != 0 || len(functionType.RegionParams) != 0 {
		t.Fatalf("specialized function retains generic parameters: types=%v regions=%v", functionType.TypeParams, functionType.RegionParams)
	}
	returnArray, ok := functionType.Return.(*DArrayType)
	if !ok || returnArray.Region != "request" || returnArray.Elem.String() != "i64" {
		t.Fatalf("expected mixed specialization return darray[i64] in request, got %#v", functionType.Return)
	}
}

func specializedFunctionTypeFromResult(t *testing.T, result *Result) *FuncType {
	t.Helper()
	for expr, typ := range result.ExprTypes {
		if _, ok := expr.(*ast.SpecializeExpr); !ok {
			continue
		}
		functionType, ok := typ.(*FuncType)
		if !ok {
			t.Fatalf("expected specialized expression type to be a function, got %T", typ)
		}
		return functionType
	}
	t.Fatal("specialization expression was not recorded in ExprTypes")
	return nil
}
