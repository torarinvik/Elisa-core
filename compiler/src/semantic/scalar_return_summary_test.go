//go:build cgo

package semantic

import "testing"

func TestScalarReturnSummaryStillChecksCalleeBody(t *testing.T) {
	for _, body := range []string{"return value + 1", "return missing_value"} {
		source := "def caller(value: i64) -> i64:\n    return callee(value)\ndef callee(value: i64) -> i64:\n    " + body + "\n"
		result := Analyze(parseIncrementalSessionFile(t, source))
		if (len(result.Errors()) != 0) != (body == "return missing_value") {
			t.Fatalf("callee body diagnostics changed: %v", result.Errors())
		}
		for _, name := range []string{"caller", "callee"} {
			symbol, ok := result.GlobalScope.Lookup(name)
			if !ok {
				t.Fatal("missing function", name)
			}
			fn := symbol.Type.(*FuncType)
			if !fn.ReturnProvenanceKnown || !fn.ReturnBorrowedOwnerRefsKnown || hasRegionProvenance(fn.ReturnProvenance) || hasBorrowedOwnerRefSummary(fn.ReturnBorrowedOwnerRefs) {
				t.Fatalf("scalar summary inconsistent: %s", name)
			}
		}
	}
}

func TestScalarReturnSummaryDoesNotClassifyReferenceOrGenericResults(t *testing.T) {
	for _, typ := range []Type{&RefType{Elem: &BuiltinType{Name: "i64"}}, &TypeParamType{Name: "T"}, &StructType{Name: "Record"}, &BuiltinType{Name: "future_builtin"}} {
		if scalarReturnHasNoReferenceProvenance(&FuncType{Return: typ}) {
			t.Fatalf("non-scalar summary accepted: %T", typ)
		}
	}
	if scalarReturnHasNoReferenceProvenance(&FuncType{Return: &BuiltinType{Name: "i64"}, ReturnRegion: "r"}) {
		t.Fatal("declared return region ignored")
	}
}
