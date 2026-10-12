//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestSMTNestedLiteralProjectionContracts(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		reject      bool
	}{
		{"correct", "42", false}, {"wrong_value", "41", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "struct Owner:\n    value: u32\nstruct Binding:\n    owner: Owner\n    serial: u32\n\ndef missing() -> (known: bool, binding: Binding):\n    ensure not result.known implies result.binding.owner.value == 42\n    return (false, Binding{owner: Owner{value: " + tc.value + "}, serial: 0})\n"
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTNestedBoolLiteralProjectionContracts(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		source := "struct Owner:\n    flag: bool\nstruct Binding:\n    owner: Owner\n\ndef present() -> (known: bool, binding: Binding):\n    ensure result.binding.owner.flag == true\n    return (true, Binding{owner: Owner{flag: " + value + "}})\n"
		result := analyzeContractStrict(t, value+".elisa", source)
		rejected := strings.Contains(allDiagnostics(result), "could not be proven")
		if rejected != (value == "false") {
			t.Fatalf("value %s rejected %v: %v", value, rejected, result.Errors())
		}
	}
}

func TestSMTNestedLiteralProjectionDoesNotAllocate(t *testing.T) {
	leaf := &ast.IntLit{Value: "42"}
	owner := &ast.StructLitExpr{Args: []ast.Expr{leaf}, ArgNames: []string{"value"}}
	binding := &ast.StructLitExpr{Args: []ast.Expr{owner}, ArgNames: []string{"owner"}}
	field := &ast.FieldExpr{Object: &ast.FieldExpr{Object: binding, Field: "owner"}, Field: "value"}
	analyzer := &Analyzer{}
	allocations := testing.AllocsPerRun(100, func() {
		value, ok := analyzer.nestedLiteralFieldValue(field, 0)
		if !ok || value != leaf {
			t.Fatal("projection failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("allocations = %v", allocations)
	}
	binding.Args = append(binding.Args, owner)
	binding.ArgNames = append(binding.ArgNames, "owner")
	if _, ok := analyzer.nestedLiteralFieldValue(field, 0); ok {
		t.Fatal("ambiguous field accepted")
	}
}
