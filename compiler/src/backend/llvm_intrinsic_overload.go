//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static void elisacoreAddAlwaysInlineAttrExt(LLVMContextRef Ctx, LLVMValueRef Fn, const char* Name, size_t NameLen) {
	unsigned Kind = LLVMGetEnumAttributeKindForName(Name, NameLen);
	if (Kind == 0) {
		return;
	}
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, LLVMCreateEnumAttribute(Ctx, Kind, 0));
}
*/
import "C"

import (
	"strings"
	"unsafe"

	"elisacore/src/semantic"
)

// resolveIntrinsicOverloadTypes picks the overload type list for an overloaded LLVM
// intrinsic so that the instantiated declaration has exactly the extern's signature.
// An intrinsic is overloaded on a small number of "any" types (llvm.umax on one,
// llvm.memcpy on two, ...), not on every call argument: passing every parameter
// type mangled `llvm.umax.i64.i64` and failed verification. The candidates are the
// distinct types of the signature in order of appearance (return first), and the
// first prefix whose LLVMIntrinsicGetType equals the lowered extern type wins.
// ok=false leaves the caller's fixed per-name table in charge.
func (g *llvmGenerator) resolveIntrinsicOverloadTypes(id C.uint, fn *semantic.FuncType) ([]C.LLVMTypeRef, bool, error) {
	if fn == nil {
		return nil, false, nil
	}
	want, err := g.lowerFunctionTypeForSymbol("", fn)
	if err != nil {
		return nil, false, err
	}
	signature := []semantic.Type{}
	if fn.Return != nil {
		signature = append(signature, fn.Return)
	}
	signature = append(signature, fn.Params...)
	var distinct []C.LLVMTypeRef
	for _, t := range signature {
		if t == nil {
			continue
		}
		lowered, err := g.lowerType(t)
		if err != nil {
			return nil, false, err
		}
		if lowered == C.LLVMVoidTypeInContext(g.context) {
			continue
		}
		seen := false
		for _, d := range distinct {
			if d == lowered {
				seen = true
				break
			}
		}
		if !seen {
			distinct = append(distinct, lowered)
		}
	}
	for k := 1; k <= len(distinct); k++ {
		candidate := distinct[:k]
		got := C.LLVMIntrinsicGetType(g.context, id, llvmTypeSlicePtr(candidate), C.size_t(len(candidate)))
		if got == want {
			return append([]C.LLVMTypeRef(nil), candidate...), true, nil
		}
	}
	return nil, false, nil
}

func (g *llvmGenerator) elisaBuiltinIntrinsicFunction(name string) (C.LLVMValueRef, bool) {
	name = strings.TrimSpace(name)
	if !semantic.IsElisaBuiltinIntrinsic(name) {
		return nil, false
	}
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	if existing := C.LLVMGetNamedFunction(g.module, nameC); existing != nil {
		return existing, true
	}
	switch name {
	case "elisa.mul_hi.u64":
		i64 := C.LLVMInt64TypeInContext(g.context)
		i128 := C.LLVMInt128TypeInContext(g.context)
		params := []C.LLVMTypeRef{i64, i64}
		fnType := C.LLVMFunctionType(i64, llvmTypeSlicePtr(params), 2, 0)
		fn := C.LLVMAddFunction(g.module, nameC, fnType)
		C.LLVMSetLinkage(fn, C.LLVMInternalLinkage)
		attr := "alwaysinline"
		attrC := C.CString(attr)
		C.elisacoreAddAlwaysInlineAttrExt(g.context, fn, attrC, C.size_t(len(attr)))
		C.free(unsafe.Pointer(attrC))
		builder := C.LLVMCreateBuilderInContext(g.context)
		defer C.LLVMDisposeBuilder(builder)
		entry := C.LLVMAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
		C.LLVMPositionBuilderAtEnd(builder, entry)
		left := C.LLVMBuildZExt(builder, C.LLVMGetParam(fn, 0), i128, cStringFree("a"))
		right := C.LLVMBuildZExt(builder, C.LLVMGetParam(fn, 1), i128, cStringFree("b"))
		product := C.LLVMBuildNUWMul(builder, left, right, cStringFree("p"))
		high := C.LLVMBuildLShr(builder, product, C.LLVMConstInt(i128, 64, 0), cStringFree("hi"))
		C.LLVMBuildRet(builder, C.LLVMBuildTrunc(builder, high, i64, cStringFree("r")))
		return fn, true
	case "elisa.div_wide.u64":
		i64 := C.LLVMInt64TypeInContext(g.context)
		i128 := C.LLVMInt128TypeInContext(g.context)
		params := []C.LLVMTypeRef{i64, i64, i64}
		fnType := C.LLVMFunctionType(i64, llvmTypeSlicePtr(params), 3, 0)
		fn := C.LLVMAddFunction(g.module, nameC, fnType)
		C.LLVMSetLinkage(fn, C.LLVMInternalLinkage)
		attr := "alwaysinline"
		attrC := C.CString(attr)
		C.elisacoreAddAlwaysInlineAttrExt(g.context, fn, attrC, C.size_t(len(attr)))
		C.free(unsafe.Pointer(attrC))
		builder := C.LLVMCreateBuilderInContext(g.context)
		defer C.LLVMDisposeBuilder(builder)
		entry := C.LLVMAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
		C.LLVMPositionBuilderAtEnd(builder, entry)
		high := C.LLVMBuildZExt(builder, C.LLVMGetParam(fn, 0), i128, cStringFree("hi"))
		low := C.LLVMBuildZExt(builder, C.LLVMGetParam(fn, 1), i128, cStringFree("lo"))
		divisor := C.LLVMBuildZExt(builder, C.LLVMGetParam(fn, 2), i128, cStringFree("d"))
		joined := C.LLVMBuildOr(builder, C.LLVMBuildShl(builder, high, C.LLVMConstInt(i128, 64, 0), cStringFree("hs")), low, cStringFree("n"))
		quotient := C.LLVMBuildUDiv(builder, joined, divisor, cStringFree("q"))
		C.LLVMBuildRet(builder, C.LLVMBuildTrunc(builder, quotient, i64, cStringFree("r")))
		return fn, true
	case "elisa.f64_to_bits", "elisa.f64_from_bits":
		i64 := C.LLVMInt64TypeInContext(g.context)
		f64 := C.LLVMDoubleTypeInContext(g.context)
		from, to := f64, i64
		if name == "elisa.f64_from_bits" {
			from, to = i64, f64
		}
		params := []C.LLVMTypeRef{from}
		fnType := C.LLVMFunctionType(to, llvmTypeSlicePtr(params), 1, 0)
		fn := C.LLVMAddFunction(g.module, nameC, fnType)
		C.LLVMSetLinkage(fn, C.LLVMInternalLinkage)
		attr := "alwaysinline"
		attrC := C.CString(attr)
		C.elisacoreAddAlwaysInlineAttrExt(g.context, fn, attrC, C.size_t(len(attr)))
		C.free(unsafe.Pointer(attrC))
		builder := C.LLVMCreateBuilderInContext(g.context)
		defer C.LLVMDisposeBuilder(builder)
		entry := C.LLVMAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
		C.LLVMPositionBuilderAtEnd(builder, entry)
		C.LLVMBuildRet(builder, C.LLVMBuildBitCast(builder, C.LLVMGetParam(fn, 0), to, cStringFree("bits")))
		return fn, true
	}
	return nil, false
}
