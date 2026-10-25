//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestLiveBoundsDefaultHeaderRestoresAllBodyCollectors(t *testing.T) {
	const source = `extern alloc_value() -> i64 can[Memory.Allocate]
law NoAlloc forbids Memory.Allocate

def consume(value: i64 = alloc_value()) -> i64 fulfills NoAlloc:
    return value
`
	result := analyzeTreeTestSource(t, "default_header_effect_scope.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	if len(result.analyzer.currentFunctionUsedPermissions) != 0 || len(result.analyzer.currentFunctionUsedPermissionRefs) != 0 || len(result.analyzer.currentFunctionGuardedIndexes) != 0 {
		t.Fatal("header/default observation leaked into idle body collectors")
	}
	symbol, ok := result.GlobalScope.Lookup("consume")
	if !ok {
		t.Fatal("missing consume")
	}
	typ := symbol.Type.(*FuncType)
	if strings.Contains(PermissionRefsString(typ.PermissionRefs), "Memory.Allocate") {
		t.Fatal("declaration default execution inferred as callee body effect")
	}
}

func TestLiveBoundsActualCallerDefaultPreservesAllocationEffect(t *testing.T) {
	const source = `extern alloc_value() -> i64 can[Memory.Allocate]
law NoAlloc forbids Memory.Allocate

def consume(value: i64 = alloc_value()) -> i64:
    return value

def caller() -> i64 fulfills NoAlloc:
    can Memory.Allocate:
        return consume()
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "default_caller_effect_scope.elisa", source)
	diagnostics := allDiagnostics(result)
	if !strings.Contains(diagnostics, "fulfills NoAlloc") || !strings.Contains(diagnostics, "Memory.Allocate") {
		t.Fatalf("actual default allocation escaped effect law: %s", diagnostics)
	}
	symbol, ok := result.GlobalScope.Lookup("caller")
	if !ok {
		t.Fatal("missing caller")
	}
	if !strings.Contains(PermissionRefsString(symbol.Type.(*FuncType).PermissionRefs), "Memory.Allocate") {
		t.Fatal("actual caller default lost inferred allocation effect")
	}
}

func TestLiveBoundsActualStructDefaultPreservesAllocationEffect(t *testing.T) {
	const source = `extern alloc_value() -> i64 can[Memory.Allocate]
law NoAlloc forbids Memory.Allocate
struct Holder:
    value: i64 = alloc_value()

def caller() -> i64 fulfills NoAlloc:
    can Memory.Allocate:
        holder: Holder = Holder{}
        return holder.value
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "struct_default_effect_scope.elisa", source)
	diagnostics := allDiagnostics(result)
	if !strings.Contains(diagnostics, "fulfills NoAlloc") || !strings.Contains(diagnostics, "Memory.Allocate") {
		t.Fatalf("actual struct-default allocation escaped effect law: %s", diagnostics)
	}
	symbol, ok := result.GlobalScope.Lookup("caller")
	if !ok {
		t.Fatal("missing caller")
	}
	if !strings.Contains(PermissionRefsString(symbol.Type.(*FuncType).PermissionRefs), "Memory.Allocate") {
		t.Fatal("actual struct default lost inferred allocation effect")
	}
}
