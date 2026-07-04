//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

// emitBuiltinFStrCall lowers the `__fstr(part, ...)` builtin (the parser's f-string desugar) to:
//
//	%total = <sum of part lengths>
//	%hdr   = call dstr @ctx_fstr_alloc(%total)          ; ONE presized allocation
//	store %hdr -> %slot (alloca)
//	call void @ctx_fstr_append(%slot, %ptr_i, %len_i)   ; one memcpy per part
//	%result = load %slot
//
// Part (ptr, len) extraction by semantic type: a literal chunk is a constant cstr with a KNOWN
// length (no strlen); an sview extracts {data, len}; a dstr extracts {items, count}; a plain cstr
// calls ctx_strlen. All four symbols are whitelisted native-runtime-support exports, so this works
// in programs that never include the std runtime themselves.
func (s *functionState) emitBuiltinFStrCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "__fstr" {
		return nil, nil, false, nil
	}
	dstrType, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok || dstrType == nil {
		return nil, nil, false, nil
	}

	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVM, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}

	// Evaluate every part once, in order, collecting (ptr, len).
	type fstrPart struct {
		ptr C.LLVMValueRef
		len C.LLVMValueRef
	}
	parts := make([]fstrPart, 0, len(expr.Args))
	for _, arg := range expr.Args {
		if lit, isLit := arg.(*ast.StringLit); isLit {
			value, _, err := s.emitExpr(arg, nil)
			if err != nil {
				return nil, nil, true, err
			}
			parts = append(parts, fstrPart{ptr: value, len: C.LLVMConstInt(usizeLLVM, C.uint64_t(len(lit.Value)), 0)})
			continue
		}
		argType := s.exprType(arg)
		for {
			ref, isRef := argType.(*semantic.RefType)
			if !isRef {
				break
			}
			argType = ref.Elem
		}
		switch argType.(type) {
		case *semantic.SViewType:
			value, _, err := s.emitExpr(arg, argType)
			if err != nil {
				return nil, nil, true, err
			}
			ptr := C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("fstr.sview.ptr"))
			ln := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("fstr.sview.len"))
			if C.LLVMTypeOf(ln) != usizeLLVM {
				ln = C.LLVMBuildIntCast2(s.builder, ln, usizeLLVM, 0, cStringFree("fstr.sview.len.usize"))
			}
			parts = append(parts, fstrPart{ptr: ptr, len: ln})
		case *semantic.DArrayType:
			value, _, err := s.emitExpr(arg, argType)
			if err != nil {
				return nil, nil, true, err
			}
			ptr := C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("fstr.dstr.items"))
			ln := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("fstr.dstr.count"))
			if C.LLVMTypeOf(ln) != usizeLLVM {
				ln = C.LLVMBuildIntCast2(s.builder, ln, usizeLLVM, 0, cStringFree("fstr.dstr.count.usize"))
			}
			parts = append(parts, fstrPart{ptr: ptr, len: ln})
		case *semantic.DStrType:
			// cstr: NUL-terminated pointer; length via the whitelisted ctx_strlen.
			value, _, err := s.emitExpr(arg, argType)
			if err != nil {
				return nil, nil, true, err
			}
			strlenType := s.g.cachedRuntimeHelperType("ctx_strlen", func() *semantic.FuncType {
				return &semantic.FuncType{Name: "ctx_strlen", Params: []semantic.Type{voidRefType}, Return: usizeType}
			})
			strlenCallee, err := s.g.ensureFunctionDeclared("ctx_strlen", strlenType)
			if err != nil {
				return nil, nil, true, err
			}
			strlenLLVM, err := s.g.lowerFunctionType(strlenType)
			if err != nil {
				return nil, nil, true, err
			}
			args := []C.LLVMValueRef{value}
			ln := C.LLVMBuildCall2(s.builder, strlenLLVM, strlenCallee, llvmValueSlicePtr(args), C.unsigned(len(args)), cStringFree("fstr.cstr.len"))
			parts = append(parts, fstrPart{ptr: value, len: ln})
		default:
			return nil, nil, true, fmt.Errorf("f-string part has unsupported type %s", argType)
		}
	}

	// total = sum of lengths.
	total := C.LLVMConstInt(usizeLLVM, 0, 0)
	for _, part := range parts {
		total = C.LLVMBuildAdd(s.builder, total, part.len, cStringFree("fstr.total"))
	}

	// hdr = ctx_fstr_alloc(total); slot = alloca dstr; store hdr.
	allocType := s.g.cachedRuntimeHelperType("ctx_fstr_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_fstr_alloc", Params: []semantic.Type{usizeType}, Return: dstrType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("ctx_fstr_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVM, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocArgs := []C.LLVMValueRef{total}
	hdr := C.LLVMBuildCall2(s.builder, allocLLVM, allocCallee, llvmValueSlicePtr(allocArgs), C.unsigned(len(allocArgs)), cStringFree("fstr.alloc"))

	dstrLLVM, err := s.g.lowerType(dstrType)
	if err != nil {
		return nil, nil, true, err
	}
	slot := C.LLVMBuildAlloca(s.builder, dstrLLVM, cStringFree("fstr.slot"))
	C.LLVMBuildStore(s.builder, hdr, slot)

	// append each part.
	dstrRefType := &semantic.RefType{Elem: dstrType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	appendType := s.g.cachedRuntimeHelperType("ctx_fstr_append", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_fstr_append", Params: []semantic.Type{dstrRefType, voidRefType, usizeType}, Return: s.g.result.NamedTypes["void"]}
	})
	appendCallee, err := s.g.ensureFunctionDeclared("ctx_fstr_append", appendType)
	if err != nil {
		return nil, nil, true, err
	}
	appendLLVM, err := s.g.lowerFunctionType(appendType)
	if err != nil {
		return nil, nil, true, err
	}
	// A void call's result name must still be a NON-NIL C string (LLVM derefs Name[0]);
	// cStringFree("") returns nil, so use cString (always non-nil) and free it.
	emptyName := cString("")
	defer C.free(unsafe.Pointer(emptyName))
	for _, part := range parts {
		appendArgs := []C.LLVMValueRef{slot, part.ptr, part.len}
		C.LLVMBuildCall2(s.builder, appendLLVM, appendCallee, llvmValueSlicePtr(appendArgs), C.unsigned(len(appendArgs)), emptyName)
	}

	result := C.LLVMBuildLoad2(s.builder, dstrLLVM, slot, cStringFree("fstr.result"))
	return result, dstrType, true, nil
}
