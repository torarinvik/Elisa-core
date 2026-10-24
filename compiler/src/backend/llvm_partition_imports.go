//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"reflect"
)

// Experimental bounded imports preserve a single emitted owner. Exact current
// module bitcode, including every imported body, remains the cache authority.
func (g *llvmGenerator) importPartitionFunction(name string, fn *semantic.FuncType, value C.LLVMValueRef) error {
	if g.partBody == nil || g.optLevel == OptimizationLevel0 || g.bodyDepth == 0 || g.bodyDepth > 3 || !configuredCodegenFlag("ELISACORE_IR_IMPORTS", g.partitionImportDefaults) || C.LLVMCountBasicBlocks(value) != 0 || g.partBody(llvmValueName(value)) {
		return nil
	}
	if fn == nil || fn.Static || fn.IsNativeExtern || len(fn.TypeParams) != 0 || len(fn.GenericParams) != 0 || fn.IntrinsicName != "" || (fn.HasInlineMode && fn.InlineMode == semantic.FuncInlineModeNever) {
		return nil
	}
	sym, ok := g.result.GlobalScope.Lookup(name)
	if !ok || sym == nil || sym.Type != fn {
		return nil
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil || decl.Static || len(decl.GenericParams) != 0 || len(decl.TypeParams) != 0 || len(decl.Body) == 0 || decl.IsGhost || decl.IsContract || decl.IsLemma {
		return nil
	}
	weight := 0
	if g.sharedLookups != nil {
		if cached, ok := g.sharedLookups.importWeights.Load(decl); ok {
			weight = cached.(int)
		}
	}
	if weight == 0 {
		weight = partitionImportWeight(decl)
		if g.sharedLookups != nil {
			g.sharedLookups.importWeights.Store(decl, weight)
		}
	}
	if weight > 64 || g.partitionImportWeight+weight > 1024 {
		return nil
	}
	g.partitionImportWeight += weight
	g.setDefinedFunctionLinkage(name, value, fn)
	if err := g.defineFunctionBody(decl, fn, value); err != nil {
		return err
	}
	C.LLVMSetLinkage(value, C.LLVMAvailableExternallyLinkage)
	return nil
}

// Stop at the admission boundary; source-position structs do not contribute.
// Counting pointers bounds recursion even for shared/lowered syntax graphs.
func partitionImportWeight(decl *ast.FuncDecl) int {
	count := 0
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		if !v.IsValid() || count > 64 {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Pointer:
			if !v.IsNil() {
				count++
				visit(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField() && count <= 64; i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len() && count <= 64; i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(decl.Body))
	if count == 0 {
		return 1
	}
	return count
}
