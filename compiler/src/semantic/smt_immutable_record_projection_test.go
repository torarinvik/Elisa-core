//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestImmutableProjectionRecordDeclinesMutableStorage(t *testing.T) {
	integer := &BuiltinType{Name: "u32"}
	record := func(field Field) *StructType {
		return &StructType{Name: "Record", Decl: &ast.StructDecl{}, Fields: map[string]Field{"value": field}}
	}
	for _, tc := range []struct {
		name   string
		typ    Type
		stable bool
	}{
		{"scalar_record", record(Field{Type: integer}), true},
		{"nested_record", record(Field{Type: record(Field{Type: integer})}), true},
		{"mutable_field", record(Field{Type: integer, Mutable: true}), false},
		{"reference", record(Field{Type: &RefType{Elem: integer}}), false},
		{"container", record(Field{Type: &DArrayType{Elem: integer}}), false},
		{"unknown", record(Field{}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := immutableProjectionRecord(tc.typ, 0); got != tc.stable {
				t.Fatalf("stable %v, want %v", got, tc.stable)
			}
		})
	}
}

func TestSMTImmutableRecordProjectionBeforeLaterCall(t *testing.T) {
	for _, tc := range []struct {
		name, expected string
		reject         bool
	}{
		{"correct", "id.owner.value", false},
		{"wrong", "id.owner.value + 1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `struct Owner:
    value: u32
struct Id:
    owner: Owner
    serial: u64
struct Row:
    id: Id
    flag: bool
def flag() -> bool:
    return true
def build(id: Id) -> Row:
    ensure result.id.owner.value == ` + tc.expected + `
    ensure result.id.serial == id.serial
    return Row{id: id, flag: flag()}
`
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			if got := ensureFailuresFor(result.Errors(), "build") > 0; got != tc.reject {
				t.Fatalf("rejected %v, want %v: %v", got, tc.reject, result.Errors())
			}
		})
	}
}
