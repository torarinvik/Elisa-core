//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// emitPromoteStmt lowers `promote <value> into <region>` (docs/67): it bitwise-
// relocates the value's own backing storage into the named region and rebinds the
// value to the relocated copy. `promote v into r` is sugar for `v = promote v into r`.
//
// The semantic layer has already proven the value is self-contained -- its only live
// region dependency is its single top-level backing, with no interior provenance --
// so relocating that one backing IS the entire promotion. Cost is O(size of the
// backing), matching the verb (docs/67 §2).
func (s *functionState) emitPromoteStmt(n *ast.PromoteStmt) error {
	regionBinding, ok := s.lookupBinding(n.Region)
	if !ok {
		return fmt.Errorf("unknown region %q during promote lowering", n.Region)
	}
	valueType := s.exprType(n.Value)
	if valueType == nil {
		return fmt.Errorf("promote requires a resolved value type")
	}
	sourceValue, _, err := s.emitExpr(n.Value, valueType)
	if err != nil {
		return err
	}
	relocated, err := s.emitPromoteRelocate(sourceValue, valueType, regionBinding, "promote")
	if err != nil {
		return err
	}
	// Rebind: store the relocated value back into the binding so it references the
	// moved backing (otherwise it would still point into the soon-dead source region).
	target := promoteRebindIdent(n.Value)
	if target == "" {
		return fmt.Errorf("promote currently requires a simple binding as its value")
	}
	binding, ok := s.lookupBinding(target)
	if !ok {
		return fmt.Errorf("unknown binding %q during promote lowering", target)
	}
	C.LLVMBuildStore(s.builder, relocated, binding.ptr)
	return nil
}

// promoteRebindIdent resolves a promote value expression to the local binding name
// to rebind, when the value is a simple binding (mirrors promoteTargetSymbol in the
// analyzer). Returns "" for non-binding expressions.
func promoteRebindIdent(expr ast.Expr) string {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.Ident:
			return n.Name
		default:
			return ""
		}
	}
}

// emitPromoteRelocate copies the value's own backing storage into the region's arena
// and returns the value re-pointed at the copy.
func (s *functionState) emitPromoteRelocate(sourceValue C.LLVMValueRef, valueType semantic.Type, regionBinding valueBinding, name string) (C.LLVMValueRef, error) {
	owner := treeAllocOwnerBinding{arenaRef: regionBinding.ptr}
	switch t := semantic.StripAggregateStateType(valueType).(type) {
	case *semantic.RefType:
		// A reference's backing is its single pointee. Relocate size_of(T) bytes via
		// libc memcpy (the size is a compile-time constant) and return the new pointer.
		size, err := s.sizeOfType(t.Elem)
		if err != nil {
			return nil, err
		}
		byteCount, err := s.constUsize(size)
		if err != nil {
			return nil, err
		}
		newPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
		if err != nil {
			return nil, err
		}
		if err := s.emitRawMemcpy(newPtr, sourceValue, size, name+".memcpy"); err != nil {
			return nil, err
		}
		return newPtr, nil
	case *semantic.DArrayType:
		return s.emitPromoteDArray(sourceValue, t, owner, name)
	default:
		return nil, fmt.Errorf("promote lowering does not support %s yet", valueType.String())
	}
}

// emitPromoteDArray relocates a dynamic array's backing buffer into the region and
// returns a header pointing at the relocated, trimmed-to-count buffer. The elements
// are plain value data (the analyzer rejects interior region provenance), so a single
// bulk copy of the live count*elem_size bytes is the whole relocation.
func (s *functionState) emitPromoteDArray(sourceValue C.LLVMValueRef, t *semantic.DArrayType, owner treeAllocOwnerBinding, name string) (C.LLVMValueRef, error) {
	llvmResultType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(t.Elem)
	if err != nil {
		return nil, err
	}
	items := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(name+".src.items"))
	count := C.LLVMBuildExtractValue(s.builder, sourceValue, 1, cStringFree(name+".src.count"))
	byteCount, err := s.emitCheckedElemByteCount(count, elemSize, name)
	if err != nil {
		return nil, err
	}
	newBuf, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
	if err != nil {
		return nil, err
	}
	// arena_memcpy is null/zero-safe (it never dereferences src when byteCount == 0),
	// so an empty darray needs no special-casing.
	if err := s.emitArenaMemcpy(newBuf, items, byteCount, name+".memcpy"); err != nil {
		return nil, err
	}
	materialized := C.LLVMGetUndef(llvmResultType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, newBuf, 0, cStringFree(name+".items"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, count, 1, cStringFree(name+".count"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, count, 2, cStringFree(name+".capacity"))
	return materialized, nil
}

// emitArenaMemcpy emits a runtime arena_memcpy(dst, src, byteCount) for a runtime-sized
// bulk copy (LLVM's loop-idiom pass lowers the byte loop to a real memcpy).
func (s *functionState) emitArenaMemcpy(dstPtr, srcPtr, byteCount C.LLVMValueRef, name string) error {
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstPtr, srcPtr, byteCount}, name)
	return nil
}

// constUsize materializes a compile-time usize constant.
func (s *functionState) constUsize(v uint64) (C.LLVMValueRef, error) {
	usizeLLVMType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(usizeLLVMType, C.ulonglong(v), 0), nil
}
