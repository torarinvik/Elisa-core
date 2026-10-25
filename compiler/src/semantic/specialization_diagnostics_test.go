//go:build cgo

package semantic

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func specializationDiagnosticDecl(t *testing.T, result *Result, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range result.File.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("missing function %s", name)
	return nil
}

func TestPrivateSpecializationRejectsNewConcreteSemanticError(t *testing.T) {
	// With T opaque, T& + int is valid pointer arithmetic and returns T&.
	// Concrete bool& instead enters scalar value arithmetic: bool + int is an
	// actual new semantic error. u8& retains byte-pointer arithmetic and is valid.
	result := analyzeTreeTestSource(t, "specialization_error.elisa", `def step[T](value: T&) -> T&:
    can Unsafe.PointerArithmetic:
        return value + 1
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatal("template fixture must be valid before concrete reanalysis", errors)
	}
	fn := specializationDiagnosticDecl(t, result, "step")
	beforeDiagnostics := slices.Clone(result.Diagnostics)
	beforeAnalyzerDiagnostics := slices.Clone(result.analyzer.diagnostics)
	beforeSyntax := copyASTDecl(fn)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	for attempt := 0; attempt < 2; attempt++ {
		product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["bool"]})
		if err == nil || product != nil {
			t.Fatal("failed concrete/private analysis published a successful product")
		}
		if !strings.Contains(err.Error(), "operator requires numeric operands") {
			t.Fatalf("lost the actual concrete semantic diagnostic: %v", err)
		}
		if len(result.shared.bodies) != 0 {
			t.Fatal("failed specialization entered the successful product memo")
		}
	}
	if overlay := result.SpecializedExprTypes(fn, []Type{result.NamedTypes["bool"]}); overlay != nil {
		t.Fatal("compatibility projection exposed failed concrete analysis")
	}
	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["u8"]})
	if err != nil || product == nil {
		t.Fatalf("invalid concrete request poisoned an independent valid specialization: %v", err)
	}
	if !reflect.DeepEqual(beforeDiagnostics, result.Diagnostics) || !reflect.DeepEqual(beforeAnalyzerDiagnostics, result.analyzer.diagnostics) || !reflect.DeepEqual(beforeSyntax, fn) {
		t.Fatal("private semantic failure changed canonical diagnostics or syntax")
	}
}

func TestPrivateSpecializationNewConcreteWarningsRemainNonfatal(t *testing.T) {
	// Generic reference equality is valid. At T=u8 this becomes raw C-string
	// pointer equality, which emits the existing content-versus-address warning.
	result := analyzeTreeTestSource(t, "specialization_warning.elisa", `def same[T](left: T&, right: T&) -> bool:
    return left == right
`)
	before := slices.Clone(result.Diagnostics)
	fn := specializationDiagnosticDecl(t, result, "same")
	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["u8"]})
	if err != nil || product == nil {
		t.Fatalf("concrete warning incorrectly rejected specialization: %v", err)
	}
	// Publication intentionally retains canonical diagnostics. The error gate
	// must neither start publishing private warnings nor mutate canonical rows.
	if !reflect.DeepEqual(before, result.Diagnostics) || !reflect.DeepEqual(before, product.Result.Diagnostics) {
		t.Fatal("canonical warning/publication behavior changed")
	}
}

func TestPrivateSpecializationDoesNotRejectInheritedCanonicalErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "specialization_inherited_error.elisa", `def unrelated() -> i64:
    return true

def keep[T](value: T) -> T:
    return value

def step[T](value: T&) -> T&:
    can Unsafe.PointerArithmetic:
        return value + 1
`)
	if len(result.Errors()) == 0 {
		t.Fatal("fixture must contain a real preexisting canonical error")
	}
	before := slices.Clone(result.Diagnostics)
	bad := specializationDiagnosticDecl(t, result, "step")
	if product, err := result.SpecializeFunction(bad, []Type{result.NamedTypes["bool"]}); product != nil || err == nil || !strings.Contains(err.Error(), "operator requires numeric operands") {
		t.Fatalf("inherited diagnostics hid a new concrete/private error: product=%v error=%v", product, err)
	}
	fn := specializationDiagnosticDecl(t, result, "keep")
	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["i64"]})
	if err != nil || product == nil {
		t.Fatalf("inherited error incorrectly counted as a new private error: %v", err)
	}
	if !reflect.DeepEqual(before, result.Diagnostics) || !reflect.DeepEqual(before, product.Result.Diagnostics) {
		t.Fatal("inherited canonical diagnostic prefix was altered")
	}
}
