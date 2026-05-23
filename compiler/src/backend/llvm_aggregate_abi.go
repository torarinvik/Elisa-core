//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

// Create a type attribute (e.g. "byval"/"sret") for the given type.
static LLVMAttributeRef elisacoreCreateTypeAttr(LLVMContextRef ctx, const char *name, size_t nameLen, LLVMTypeRef ty) {
	unsigned kind = LLVMGetEnumAttributeKindForName(name, nameLen);
	if (kind == 0) {
		return NULL;
	}
	return LLVMCreateTypeAttribute(ctx, kind, ty);
}

static void elisacoreAddFuncTypeAttr(LLVMValueRef fn, unsigned index, LLVMContextRef ctx, const char *name, size_t nameLen, LLVMTypeRef ty) {
	LLVMAttributeRef attr = elisacoreCreateTypeAttr(ctx, name, nameLen, ty);
	if (attr != NULL) {
		LLVMAddAttributeAtIndex(fn, index, attr);
	}
}

static void elisacoreAddCallTypeAttr(LLVMValueRef call, unsigned index, LLVMContextRef ctx, const char *name, size_t nameLen, LLVMTypeRef ty) {
	LLVMAttributeRef attr = elisacoreCreateTypeAttr(ctx, name, nameLen, ty);
	if (attr != NULL) {
		LLVMAddCallSiteAttribute(call, index, attr);
	}
}
*/
import "C"

import (
	"unsafe"

	"elisacore/src/semantic"
)

// aggregateMemoryClassThresholdBytes is the size at/above which an aggregate
// (struct/array) function parameter or return value is passed/returned by
// pointer (byval/sret) rather than as a first-class LLVM aggregate value.
//
// It is set high enough that only genuinely large aggregates are affected
// (e.g. the shader IR's inst/block/program types, which embed large fixed
// arrays). Small structs keep the existing by-value lowering, so the change
// has no effect on the vast majority of code -- and large structs are passed
// by memory under every supported C ABI anyway, so this also matches the
// platform calling convention for FFI.
const aggregateMemoryClassThresholdBytes = 1024

// aggregateIsMemoryClass reports whether a type is a large aggregate that
// should be passed/returned by pointer. This is the single source of truth
// shared by function-type lowering, attribute application, the callee prologue,
// and call sites, so all four stay consistent for a given function type.
func (g *llvmGenerator) aggregateIsMemoryClass(t semantic.Type) bool {
	if g == nil || t == nil {
		return false
	}
	if !semanticTypeIsAggregate(t) {
		return false
	}
	size, err := g.abiSizeOfType(t)
	if err != nil {
		return false
	}
	return size >= aggregateMemoryClassThresholdBytes
}

// funcAbiLayout describes the leading hidden parameters of a lowered function:
// an error-union out-pointer and/or an sret out-pointer for a large aggregate
// return. Explicit params follow, in order, starting at paramBase().
type funcAbiLayout struct {
	errorUnionOut bool
	sret          bool
	sretType      C.LLVMTypeRef
}

func (l funcAbiLayout) paramBase() int {
	base := 0
	if l.errorUnionOut {
		base++
	}
	if l.sret {
		base++
	}
	return base
}

// sretParamPos returns the LLVM parameter position of the sret pointer (after
// any error-union out-param), or -1 if there is no sret param.
func (l funcAbiLayout) sretParamPos() int {
	if !l.sret {
		return -1
	}
	if l.errorUnionOut {
		return 1
	}
	return 0
}

func (g *llvmGenerator) computeFuncAbiLayout(fn *semantic.FuncType) (funcAbiLayout, error) {
	if fn == nil {
		return funcAbiLayout{}, nil
	}
	_, eu := nonVoidErrorUnion(fn.Return)
	layout := funcAbiLayout{errorUnionOut: eu}
	if !eu && g.aggregateIsMemoryClass(fn.Return) {
		t, err := g.lowerType(fn.Return)
		if err != nil {
			return layout, err
		}
		layout.sret = true
		layout.sretType = t
	}
	return layout, nil
}

// applyAggregateAbiAttrs adds the sret/byval type attributes implied by the
// function type to a freshly-created LLVM function value.
func (g *llvmGenerator) applyAggregateAbiAttrs(fn C.LLVMValueRef, fnType *semantic.FuncType) {
	if fn == nil || fnType == nil {
		return
	}
	layout, err := g.computeFuncAbiLayout(fnType)
	if err != nil {
		return
	}
	if layout.sret {
		// sret attribute index = sretParamPos + 1.
		g.addFuncTypeAttr(fn, C.uint(layout.sretParamPos()+1), "sret", layout.sretType)
	}
	base := layout.paramBase()
	for i, param := range fnType.Params {
		if !g.aggregateIsMemoryClass(param) {
			continue
		}
		ty, err := g.lowerType(param)
		if err != nil {
			continue
		}
		g.addFuncByvalAttr(fn, base+i, ty)
	}
}

// convertByvalArgs replaces memory-class aggregate argument values with
// pointers to (memcpy-filled) stack temporaries, so they can be passed byval.
// Returns the (possibly rewritten) args and a map of arg-index -> aggregate
// LLVM type for the args that became byval pointers.
func (s *functionState) convertByvalArgs(funcType *semantic.FuncType, args []C.LLVMValueRef) ([]C.LLVMValueRef, map[int]C.LLVMTypeRef, error) {
	out := make([]C.LLVMValueRef, len(args))
	copy(out, args)
	var byval map[int]C.LLVMTypeRef
	for i := 0; i < len(funcType.Params) && i < len(args); i++ {
		if !s.g.aggregateIsMemoryClass(funcType.Params[i]) {
			continue
		}
		ty, err := s.g.lowerType(funcType.Params[i])
		if err != nil {
			return nil, nil, err
		}
		tmp, err := s.createEntryAlloca("byval.arg", funcType.Params[i])
		if err != nil {
			return nil, nil, err
		}
		if err := s.storeValue(tmp, args[i], funcType.Params[i], "byval.arg"); err != nil {
			return nil, nil, err
		}
		out[i] = tmp
		if byval == nil {
			byval = map[int]C.LLVMTypeRef{}
		}
		byval[i] = ty
	}
	return out, byval, nil
}

func semanticTypeIsAggregate(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.StructType, *semantic.ArrayType:
		return true
	default:
		return false
	}
}

// LLVM attribute index helpers: index 0 is the return value, params are 1-based.
func llvmParamAttrIndex(llvmParamPos int) C.uint {
	return C.uint(llvmParamPos + 1)
}

func (g *llvmGenerator) addFuncByvalAttr(fn C.LLVMValueRef, llvmParamPos int, ty C.LLVMTypeRef) {
	g.addFuncTypeAttr(fn, llvmParamAttrIndex(llvmParamPos), "byval", ty)
}

func (g *llvmGenerator) addFuncSretAttr(fn C.LLVMValueRef, ty C.LLVMTypeRef) {
	// sret applies to the (prepended) first parameter, attribute index 1.
	g.addFuncTypeAttr(fn, 1, "sret", ty)
}

func (g *llvmGenerator) addFuncTypeAttr(fn C.LLVMValueRef, index C.uint, name string, ty C.LLVMTypeRef) {
	if fn == nil || ty == nil {
		return
	}
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	C.elisacoreAddFuncTypeAttr(fn, index, g.context, nameC, C.size_t(len(name)), ty)
}

func (s *functionState) addCallByvalAttr(call C.LLVMValueRef, llvmParamPos int, ty C.LLVMTypeRef) {
	s.addCallTypeAttr(call, llvmParamAttrIndex(llvmParamPos), "byval", ty)
}

func (s *functionState) addCallSretAttr(call C.LLVMValueRef, ty C.LLVMTypeRef) {
	s.addCallTypeAttr(call, 1, "sret", ty)
}

func (s *functionState) addCallTypeAttr(call C.LLVMValueRef, index C.uint, name string, ty C.LLVMTypeRef) {
	if call == nil || ty == nil || s == nil || s.g == nil {
		return
	}
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	C.elisacoreAddCallTypeAttr(call, index, s.g.context, nameC, C.size_t(len(name)), ty)
}
