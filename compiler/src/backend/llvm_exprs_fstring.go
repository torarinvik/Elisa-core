//go:build cgo

package backend

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
func (s *functionState) emitBuiltinFStrCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "__fstr" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, "__fstr") {
		return nil, nil, false, nil
	}
	dstrType, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok || dstrType == nil {
		return nil, nil, false, nil
	}

	usizeType := s.g.namedType("usize")
	usizeLLVM, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}

	// Evaluate every part once, in order, collecting (ptr, len).
	type fstrPart struct {
		ptr llvmIRValueRef
		len llvmIRValueRef
	}
	parts := make([]fstrPart, 0, len(expr.Args))
	for _, arg := range expr.Args {
		if lit, isLit := arg.(*ast.StringLit); isLit {
			value, _, err := s.emitExpr(arg, nil)
			if err != nil {
				return nil, nil, true, err
			}
			parts = append(parts, fstrPart{ptr: value, len: llvmConstInt(usizeLLVM, llvmIRUnsignedLongLong(len(lit.Value)), 0)})
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
			ptr := llvmBuildExtractValue(s.builder, value, 0, cStringFree("fstr.sview.ptr"))
			ln := llvmBuildExtractValue(s.builder, value, 1, cStringFree("fstr.sview.len"))
			if llvmTypeOf(ln) != usizeLLVM {
				ln = llvmBuildIntCast2(s.builder, ln, usizeLLVM, 0, cStringFree("fstr.sview.len.usize"))
			}
			parts = append(parts, fstrPart{ptr: ptr, len: ln})
		case *semantic.DArrayType:
			value, _, err := s.emitExpr(arg, argType)
			if err != nil {
				return nil, nil, true, err
			}
			ptr := llvmBuildExtractValue(s.builder, value, 0, cStringFree("fstr.dstr.items"))
			ln := llvmBuildExtractValue(s.builder, value, 1, cStringFree("fstr.dstr.count"))
			if llvmTypeOf(ln) != usizeLLVM {
				ln = llvmBuildIntCast2(s.builder, ln, usizeLLVM, 0, cStringFree("fstr.dstr.count.usize"))
			}
			parts = append(parts, fstrPart{ptr: ptr, len: ln})
		case *semantic.CStrType:
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
			args := []llvmIRValueRef{value}
			ln := llvmBuildCall2(s.builder, strlenLLVM, strlenCallee, llvmValueSlicePtr(args), llvmIRUnsigned(len(args)), cStringFree("fstr.cstr.len"))
			parts = append(parts, fstrPart{ptr: value, len: ln})
		default:
			return nil, nil, true, fmt.Errorf("f-string part has unsupported type %s", argType)
		}
	}

	// total = sum of lengths.
	total := llvmConstInt(usizeLLVM, 0, 0)
	for _, part := range parts {
		total = llvmBuildAdd(s.builder, total, part.len, cStringFree("fstr.total"))
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
	allocArgs := []llvmIRValueRef{total}
	hdr := llvmBuildCall2(s.builder, allocLLVM, allocCallee, llvmValueSlicePtr(allocArgs), llvmIRUnsigned(len(allocArgs)), cStringFree("fstr.alloc"))

	dstrLLVM, err := s.g.lowerType(dstrType)
	if err != nil {
		return nil, nil, true, err
	}
	slot := llvmBuildAlloca(s.builder, dstrLLVM, cStringFree("fstr.slot"))
	llvmBuildStore(s.builder, hdr, slot)

	// append each part.
	dstrRefType := &semantic.RefType{Elem: dstrType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	appendType := s.g.cachedRuntimeHelperType("ctx_fstr_append", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_fstr_append", Params: []semantic.Type{dstrRefType, voidRefType, usizeType}, Return: s.g.namedType("void")}
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
	defer llvmFree(unsafe.Pointer(emptyName))
	for _, part := range parts {
		appendArgs := []llvmIRValueRef{slot, part.ptr, part.len}
		llvmBuildCall2(s.builder, appendLLVM, appendCallee, llvmValueSlicePtr(appendArgs), llvmIRUnsigned(len(appendArgs)), emptyName)
	}

	result := llvmBuildLoad2(s.builder, dstrLLVM, slot, cStringFree("fstr.result"))
	return result, dstrType, true, nil
}
