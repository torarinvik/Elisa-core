package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestRecordUpdateForwardsContainerBackingRegion(t *testing.T) {
	source := `struct Holder:
    values: mutable darray[i64]
    count: i64

def grow(holder: mutable Holder&) -> void:
    holder.values.push(1)

def work() -> void:
    original: mutable Holder = Holder{values: [], count: 0}
    updated: mutable Holder = original{count = original.count + 1}
    grow(&updated)
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "record_update_region.elisa", source).Errors()
	if len(errs) != 0 {
		t.Fatalf("record update should preserve backing region: %s", strings.Join(errs, "\n"))
	}
}

func TestRecordUpdateDoesNotInheritBackingForReplacementContainer(t *testing.T) {
	source := `struct Holder:
    values: mutable darray[i64]
    count: i64

def grow(holder: mutable Holder&) -> void:
    holder.values.push(1)

def work() -> darray[i64]:
    original: mutable Holder = Holder{values: [], count: 0}
    result: mutable darray[i64] = []
    region short:
        inner: mutable darray[i64] = []
        inner.push(7)
        updated: mutable Holder = original{values = inner}
        grow(&updated)
        result <- updated.values
    return result
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "record_update_short_region.elisa", source).Errors()
	diagnostics := strings.Join(errs, " | ")
	if !strings.Contains(diagnostics, `cannot infer region parameter "__rg_holder"`) ||
		!strings.Contains(diagnostics, `value in region "short" is stored into longer-lived region`) {
		t.Fatalf("expected the replacement and escape to be rejected, got: %s", diagnostics)
	}
}

func TestNestedRecordUpdatePreservesContainerBackingRegion(t *testing.T) {
	source := `struct Details:
    count: i64
struct Holder:
    values: mutable darray[i64]
    details: Details

def grow(holder: mutable Holder&) -> void:
    holder.values.push(1)

def work() -> void:
    original: mutable Holder = Holder{values: [], details: Details{count: 0}}
    updated: mutable Holder = original{details = original.details{count = 1}}
    grow(&updated)
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "nested_record_update_region.elisa", source).Errors()
	if len(errs) != 0 {
		t.Fatalf("nested update should retain the original backing: %s", strings.Join(errs, "\n"))
	}
}

func TestNestedRecordUpdateFromDifferentBaseDoesNotInheritRegion(t *testing.T) {
	source := `struct Details:
    values: mutable darray[i64]
    count: i64
struct Holder:
    details: Details

def grow(holder: mutable Holder&) -> void:
    holder.details.values.push(1)

def work() -> darray[i64]:
    original: mutable Holder = Holder{details: Details{values: [], count: 0}}
    result: mutable darray[i64] = []
    region short:
        inner: mutable darray[i64] = []
        inner.push(7)
        other: mutable Holder = Holder{details: Details{values: inner, count: 0}}
        updated: mutable Holder = original{details = other.details{count = 1}}
        grow(&updated)
        result <- updated.details.values
    return result
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "different_nested_update_region.elisa", source).Errors()
	diagnostics := strings.Join(errs, " | ")
	if !strings.Contains(diagnostics, `cannot infer region parameter "__rg_holder"`) ||
		!strings.Contains(diagnostics, `value in region "short" is stored into longer-lived region`) {
		t.Fatalf("expected different-base replacement and escape to be rejected, got: %s", diagnostics)
	}
}

func TestRecordUpdatePathIdentityRejectsEffects(t *testing.T) {
	base := &ast.Ident{Name: "original"}
	equal := &ast.ParenExpr{Inner: &ast.Ident{Name: "original"}}
	if !recordUpdateSamePath(base, equal, 0) {
		t.Fatal("grouping should preserve a plain path")
	}
	if recordUpdateSamePath(base, &ast.MoveExpr{Operand: equal}, 0) {
		t.Fatal("move must not be treated as a plain path")
	}
	if recordUpdateSamePath(&ast.CallExpr{Func: base}, &ast.CallExpr{Func: base}, 0) {
		t.Fatal("calls must not establish shared backing")
	}
	if recordUpdateSamePath(&ast.FieldExpr{Object: base, Field: "item", Safe: true}, &ast.FieldExpr{Object: equal, Field: "item"}, 0) {
		t.Fatal("safe field access must not establish a plain path")
	}
}

func TestRecordUpdateScalarProofRejectsCallsAndMoves(t *testing.T) {
	literal := &ast.IntLit{Value: "1"}
	if !recordUpdateStableScalar(&ast.BinaryExpr{Left: literal, Right: literal}, 0) {
		t.Fatal("literal arithmetic should be stable")
	}
	field := &ast.FieldExpr{Object: &ast.Ident{Name: "original"}, Field: "count"}
	if !recordUpdateStableScalar(field, 0) {
		t.Fatal("a plain scalar field read should be stable")
	}
	if recordUpdateStableScalar(&ast.FieldExpr{Object: &ast.CallExpr{Func: &ast.Ident{Name: "mutate"}}, Field: "count"}, 0) {
		t.Fatal("a field read rooted in a call may mutate the base")
	}
	if recordUpdateStableScalar(&ast.CallExpr{Func: &ast.Ident{Name: "mutate"}}, 0) {
		t.Fatal("a scalar-producing call may mutate the base")
	}
	if recordUpdateStableScalar(&ast.MoveExpr{Operand: literal}, 0) {
		t.Fatal("a move must not be treated as a stable scalar")
	}
	if recordUpdateStableScalar(&ast.BinaryExpr{Left: literal, Right: literal, LoweredCall: &ast.CallExpr{}}, 0) {
		t.Fatal("an overloaded operator may call user code")
	}
}
