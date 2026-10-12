package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestNamedTupleResultConstruction(t *testing.T) {
	for _, tc := range []struct {
		name, returned string
		wantError      bool
	}{
		{"identity", "flag, value", false},
		{"negated_bool", "not flag, value", true},
		{"changed_integer", "flag, value + 1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def flags(flag: bool, value: i64) -> (valid: bool, value: i64):\n    ensure result.valid == flag\n    ensure result.value == value\n    return (" + tc.returned + ")\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "tuple_result.elisa", source, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if (len(result.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(result))
			}
		})
	}
}

func TestNamedTupleResultFieldIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, returned string
		wantError      bool
	}{
		{"ordered", "flag, not flag", false},
		{"swapped", "not flag, flag", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def flags(flag: bool) -> (enabled: bool, disabled: bool):\n    ensure result.enabled == flag\n    ensure result.disabled == (not flag)\n    return (" + tc.returned + ")\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "tuple_fields.elisa", source, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if (len(result.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(result))
			}
		})
	}
}

func TestTypedTupleProjectionDoesNotAllocate(t *testing.T) {
	first := &ast.Ident{Name: "flag"}
	second := &ast.Ident{Name: "value"}
	tuple := &ast.TupleExpr{Elems: []ast.Expr{first, second}}
	analyzer := &Analyzer{exprTypes: map[ast.Expr]Type{tuple: &TupleType{Fields: []TupleField{{Name: "valid", Type: &BuiltinType{Name: "bool"}}, {Name: "value", Type: &BuiltinType{Name: "i64"}}}}}}
	if got, ok := analyzer.tupleLiteralFieldValue(tuple, "value"); !ok || got != second {
		t.Fatal("lost named field identity")
	}
	allocations := testing.AllocsPerRun(100, func() {
		if got, ok := analyzer.tupleLiteralFieldValue(tuple, "valid"); !ok || got != first {
			panic("lost projection")
		}
	})
	if allocations != 0 {
		t.Fatalf("single tuple field lookup allocated %v times", allocations)
	}
	if _, ok := analyzer.tupleLiteralFieldValue(tuple, "unknown"); ok {
		t.Fatal("invented unknown field")
	}
	analyzer.exprTypes[tuple] = &TupleType{Fields: []TupleField{{Name: "valid"}, {Name: "valid"}}}
	if _, ok := analyzer.tupleLiteralFieldValue(tuple, "valid"); ok {
		t.Fatal("accepted ambiguous field name")
	}
	analyzer.exprTypes[tuple] = &TupleType{Fields: []TupleField{{Name: "valid"}}}
	if _, ok := analyzer.tupleLiteralFieldValue(tuple, "valid"); ok {
		t.Fatal("accepted mismatched tuple arity")
	}
	delete(analyzer.exprTypes, tuple)
	if _, ok := analyzer.tupleLiteralFieldValue(tuple, "valid"); ok {
		t.Fatal("invented untyped projection")
	}
}
