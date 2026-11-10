package semantic

import "testing"

func TestFunctionTypeSubstitutionKeepsFastMath(t *testing.T) {
	parameter := &TypeParamType{Name: "T"}
	concrete := &BuiltinType{Name: "f64"}
	for _, enabled := range []bool{false, true} {
		source := &FuncType{Name: "scale", Params: []Type{parameter}, Return: parameter, FastMath: enabled}
		analyzer := &Analyzer{}
		result, ok := analyzer.substituteType(source, map[string]Type{"T": concrete}, nil, nil, nil).(*FuncType)
		if !ok || result == source || result.FastMath != enabled || result.Return != concrete || result.Params[0] != concrete {
			t.Fatalf("fast-math=%v: unexpected substituted type: %+v", enabled, result)
		}
		exported := specializeExportFuncType(analyzer, source, map[string]Type{"T": concrete})
		if exported == nil || exported.FastMath != enabled {
			t.Fatalf("fast-math=%v: export specialization lost floating-point policy", enabled)
		}
		cloned := analyzer.cloneFunctionValueType(source)
		if cloned == nil || cloned == source || cloned.FastMath != enabled {
			t.Fatalf("fast-math=%v: branch clone lost floating-point policy", enabled)
		}
	}
}
