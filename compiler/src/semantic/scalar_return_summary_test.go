//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

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
	analyzer := newSemanticHardeningTestAnalyzer()
	for _, typ := range []Type{&RefType{Elem: &BuiltinType{Name: "i64"}}, &TypeParamType{Name: "T"}, &StructType{Name: "Record"}, &BuiltinType{Name: "future_builtin"}} {
		if analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: typ}) {
			t.Fatalf("non-scalar summary accepted: %T", typ)
		}
	}
	plainEnum := &EnumType{Variants: []*EnumVariant{{Name: "First", Payload: []Type{&BuiltinType{Name: "i64"}}}}}
	refinedParent := &EnumType{Name: "Widget"}
	refinedChild := &EnumType{
		Name:     "Control",
		Parent:   refinedParent,
		Variants: []*EnumVariant{{Name: "Label", Payload: []Type{&SViewType{}}}},
	}
	refinedParent.Children = []*EnumType{refinedChild}
	for _, typ := range []Type{
		&ArrayType{Elem: &BuiltinType{Name: "u8"}},
		&OptionalType{Value: &BuiltinType{Name: "i64"}},
		plainEnum,
		&AggregateStateType{Base: &BuiltinType{Name: "bool"}},
	} {
		if !analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: typ}) {
			t.Fatalf("reference-free result not recognized: %T", typ)
		}
	}
	for _, typ := range []Type{
		&ArrayType{Elem: &RefType{Elem: &BuiltinType{Name: "i64"}}},
		&OptionalType{Value: &RefType{Elem: &BuiltinType{Name: "i64"}}},
		&EnumType{Variants: []*EnumVariant{{Name: "Borrowed", Payload: []Type{&RefType{Elem: &BuiltinType{Name: "i64"}}}}}},
		&EnumType{Packed: true},
		&EnumType{StoreBackedPlain: true},
		refinedParent,
		refinedChild,
	} {
		if analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: typ}) {
			t.Fatalf("reference-carrying result accepted: %T", typ)
		}
	}
	if analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: &BuiltinType{Name: "i64"}, ReturnRegion: "r"}) {
		t.Fatal("declared return region ignored")
	}
}

func TestScalarReturnSummaryResolvesGenericStructFieldsConservatively(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	base := &StructType{
		Name:       "Box",
		TypeParams: []string{"T"},
		Decl:       &ast.StructDecl{Name: "Box", TypeParams: []string{"T"}, Fields: []ast.FieldDecl{{Name: "value"}}},
		Fields:     map[string]Field{"value": {Name: "value", Type: &TypeParamType{Name: "T"}}},
	}
	plain := &GenericInstanceType{Name: "Box[i64]", Base: base, Args: []Type{&BuiltinType{Name: "i64"}}}
	borrowed := &GenericInstanceType{Name: "Box[&i64]", Base: base, Args: []Type{&RefType{Elem: &BuiltinType{Name: "i64"}}}}
	if !analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: plain}) {
		t.Fatal("a fully resolved pointer-free generic record should have no reference provenance")
	}
	if analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: borrowed}) {
		t.Fatal("a generic record instantiated with a reference must retain provenance analysis")
	}
	regionOwned := *plain
	regionOwned.Region = "r"
	if analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: &regionOwned}) {
		t.Fatal("generic instance region metadata must remain conservative")
	}
}

func TestPointerFreeRecordReturnStillChecksCalleeBody(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	for _, value := range []string{"17", "missing_value"} {
		source := `const enum Kind of u8:
    Item = 0
struct Record:
    kind: Kind
    number: i64
def caller() -> Record:
    return callee()
def callee() -> Record:
    return Record{kind: Kind.Item, number: ` + value + `}
`
		result := Analyze(parseIncrementalSessionFile(t, source))
		if (len(result.Errors()) != 0) != (value == "missing_value") {
			t.Fatalf("record body diagnostics changed: %v", result.Errors())
		}
		symbol, ok := result.GlobalScope.Lookup("caller")
		if !ok {
			t.Fatal("missing caller")
		}
		fn := symbol.Type.(*FuncType)
		if !analyzer.scalarReturnHasNoReferenceProvenance(fn) || hasRegionProvenance(fn.ReturnProvenance) || hasBorrowedOwnerRefSummary(fn.ReturnBorrowedOwnerRefs) {
			t.Fatal("pointer-free record summary inconsistent")
		}
	}
}

func TestPointerFreeSummaryRejectsReferenceRecordsAndCycles(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	refRecord := &StructType{Decl: &ast.StructDecl{}, Fields: map[string]Field{"value": {Type: &RefType{Elem: &BuiltinType{Name: "i64"}}}}}
	cycle := &StructType{Decl: &ast.StructDecl{}, Fields: map[string]Field{}}
	cycle.Fields["self"] = Field{Type: cycle}
	owning := &StructType{Decl: &ast.StructDecl{}, Affine: true}
	for _, typ := range []Type{refRecord, cycle, owning} {
		if analyzer.scalarReturnHasNoReferenceProvenance(&FuncType{Return: typ}) {
			t.Fatalf("owning or recursive type accepted: %T", typ)
		}
	}
}
