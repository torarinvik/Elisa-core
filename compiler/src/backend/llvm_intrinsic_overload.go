//go:build cgo

package backend

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
func (g *llvmGenerator) resolveIntrinsicOverloadTypes(id llvmIRUnsigned, fn *semantic.FuncType) ([]llvmIRTypeRef, bool, error) {
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
	var distinct []llvmIRTypeRef
	for _, t := range signature {
		if t == nil {
			continue
		}
		lowered, err := g.lowerType(t)
		if err != nil {
			return nil, false, err
		}
		if lowered == llvmVoidTypeInContext(g.context) {
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
		got := llvmIntrinsicGetType(g.context, id, llvmTypeSlicePtr(candidate), llvmIRSize(len(candidate)))
		if got == want {
			return append([]llvmIRTypeRef(nil), candidate...), true, nil
		}
	}
	return nil, false, nil
}

func (g *llvmGenerator) elisaBuiltinIntrinsicFunction(name string) (llvmIRValueRef, bool) {
	name = strings.TrimSpace(name)
	if !semantic.IsElisaBuiltinIntrinsic(name) {
		return nil, false
	}
	nameC := llvmCString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	if existing := llvmGetNamedFunction(g.module, nameC); existing != nil {
		return existing, true
	}
	switch name {
	case "elisa.mul_hi.u64":
		i64 := llvmInt64TypeInContext(g.context)
		i128 := llvmInt128TypeInContext(g.context)
		params := []llvmIRTypeRef{i64, i64}
		fnType := llvmFunctionType(i64, llvmTypeSlicePtr(params), 2, 0)
		fn := llvmAddFunction(g.module, nameC, fnType)
		llvmSetLinkage(fn, llvmInternalLinkage)
		attr := "alwaysinline"
		attrC := llvmCString(attr)
		llvmAddAlwaysInlineAttr(g.context, fn, attrC, llvmIRSize(len(attr)))
		llvmFree(unsafe.Pointer(attrC))
		builder := llvmCreateBuilderInContext(g.context)
		defer llvmDisposeBuilder(builder)
		entry := llvmAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
		llvmPositionBuilderAtEnd(builder, entry)
		left := llvmBuildZExt(builder, llvmGetParam(fn, 0), i128, cStringFree("a"))
		right := llvmBuildZExt(builder, llvmGetParam(fn, 1), i128, cStringFree("b"))
		product := llvmBuildNUWMul(builder, left, right, cStringFree("p"))
		high := llvmBuildLShr(builder, product, llvmConstInt(i128, 64, 0), cStringFree("hi"))
		llvmBuildRet(builder, llvmBuildTrunc(builder, high, i64, cStringFree("r")))
		return fn, true
	case "elisa.div_wide.u64":
		i64 := llvmInt64TypeInContext(g.context)
		i128 := llvmInt128TypeInContext(g.context)
		params := []llvmIRTypeRef{i64, i64, i64}
		fnType := llvmFunctionType(i64, llvmTypeSlicePtr(params), 3, 0)
		fn := llvmAddFunction(g.module, nameC, fnType)
		llvmSetLinkage(fn, llvmInternalLinkage)
		attr := "alwaysinline"
		attrC := llvmCString(attr)
		llvmAddAlwaysInlineAttr(g.context, fn, attrC, llvmIRSize(len(attr)))
		llvmFree(unsafe.Pointer(attrC))
		builder := llvmCreateBuilderInContext(g.context)
		defer llvmDisposeBuilder(builder)
		entry := llvmAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
		llvmPositionBuilderAtEnd(builder, entry)
		high := llvmBuildZExt(builder, llvmGetParam(fn, 0), i128, cStringFree("hi"))
		low := llvmBuildZExt(builder, llvmGetParam(fn, 1), i128, cStringFree("lo"))
		divisor := llvmBuildZExt(builder, llvmGetParam(fn, 2), i128, cStringFree("d"))
		joined := llvmBuildOr(builder, llvmBuildShl(builder, high, llvmConstInt(i128, 64, 0), cStringFree("hs")), low, cStringFree("n"))
		quotient := llvmBuildUDiv(builder, joined, divisor, cStringFree("q"))
		llvmBuildRet(builder, llvmBuildTrunc(builder, quotient, i64, cStringFree("r")))
		return fn, true
	case "elisa.f64_to_bits", "elisa.f64_from_bits":
		i64 := llvmInt64TypeInContext(g.context)
		f64 := llvmDoubleTypeInContext(g.context)
		from, to := f64, i64
		if name == "elisa.f64_from_bits" {
			from, to = i64, f64
		}
		params := []llvmIRTypeRef{from}
		fnType := llvmFunctionType(to, llvmTypeSlicePtr(params), 1, 0)
		fn := llvmAddFunction(g.module, nameC, fnType)
		llvmSetLinkage(fn, llvmInternalLinkage)
		attr := "alwaysinline"
		attrC := llvmCString(attr)
		llvmAddAlwaysInlineAttr(g.context, fn, attrC, llvmIRSize(len(attr)))
		llvmFree(unsafe.Pointer(attrC))
		builder := llvmCreateBuilderInContext(g.context)
		defer llvmDisposeBuilder(builder)
		entry := llvmAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
		llvmPositionBuilderAtEnd(builder, entry)
		llvmBuildRet(builder, llvmBuildBitCast(builder, llvmGetParam(fn, 0), to, cStringFree("bits")))
		return fn, true
	}
	return nil, false
}
