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

// Add a value-less enum attribute (e.g. "noalias") to a function at the given
// attribute index.
static void elisacoreAddFuncEnumAttr(LLVMValueRef fn, unsigned index, LLVMContextRef ctx, const char *name, size_t nameLen) {
	unsigned kind = LLVMGetEnumAttributeKindForName(name, nameLen);
	if (kind == 0) {
		return;
	}
	LLVMAttributeRef attr = LLVMCreateEnumAttribute(ctx, kind, 0);
	LLVMAddAttributeAtIndex(fn, index, attr);
}
*/
import "C"

import (
	"runtime"
	"strings"
	"unsafe"

	"elisacore/src/semantic"
)

// targetUsesByvalAggregateArgs reports whether memory-class aggregate arguments
// should carry the LLVM "byval" attribute under the active target's C ABI.
//
// On x86-64 SysV, a memory-class argument is passed on the stack and "byval" is
// the correct lowering. On arm64 (AAPCS / Apple), a composite larger than 16
// bytes is instead passed *indirectly* — the caller copies it and passes a plain
// pointer (in a GP register), with no "byval" attribute (this is what clang
// emits). Since convertByvalArgs already materializes the caller-side copy and
// the parameter's LLVM type is a plain pointer either way, the only difference
// is whether the attribute is attached. Returns (sret) are unaffected: sret is
// correct on both targets.
func (g *llvmGenerator) targetUsesByvalAggregateArgs() bool {
	t := strings.ToLower(strings.TrimSpace(g.requestedTargetTriple))
	if t == "" {
		// Native build: tuned for the host (see llvm_target.go).
		return runtime.GOARCH != "arm64"
	}
	return !strings.Contains(t, "arm64") && !strings.Contains(t, "aarch64")
}

// aggregateMemoryClassThresholdBytes is the size at/above which an aggregate is
// passed/returned by pointer (byval/sret) under Elisa's INTERNAL calling
// convention. It is set high enough that only genuinely large aggregates are
// affected; small/medium structs keep the first-class lowering that internal
// call emitters (e.g. the concurrency runtime's parallel-chunk structs) assume.
const aggregateMemoryClassThresholdBytes = 1024

// cAbiAggregateMemoryClassThresholdBytes is the threshold under the platform C
// ABI: both arm64 AAPCS and x86-64 SysV pass aggregates LARGER than 16 bytes via
// memory (sret returns / indirect args). This is required for FFI — calling C /
// Objective-C functions that take or return such structs by value (e.g. CMTime,
// 24 bytes). It applies only at the C-ABI boundary (extern/@callconv(c) functions and
// call_as indirect calls, identified by CallConv == "c"), so Elisa-internal
// calls are completely unaffected.
const cAbiAggregateMemoryClassThresholdBytes = 17

// funcTypeIsCABI reports whether a function type uses the C calling convention,
// i.e. its parameter/return aggregate lowering must follow the platform C ABI.
func funcTypeIsCABI(fn *semantic.FuncType) bool {
	return fn != nil && fn.CallConv == "c"
}

// aggregateArgUsesByval reports whether memory-class aggregate ARGUMENTS carry
// the LLVM "byval" attribute. Internal calls always use byval (caller and callee
// agree, so it is self-consistent). At the C-ABI boundary, x86-64 SysV uses byval
// but arm64 passes a plain indirect pointer (matching clang) — see
// targetUsesByvalAggregateArgs.
func (g *llvmGenerator) aggregateArgUsesByval(cabi bool) bool {
	return !cabi || g.targetUsesByvalAggregateArgs()
}

// aggregateIsMemoryClass reports memory-class under the internal ABI (the common
// case). C-ABI call/def sites use aggregateIsMemoryClassABI with cabi=true.
func (g *llvmGenerator) aggregateIsMemoryClass(t semantic.Type) bool {
	return g.aggregateIsMemoryClassABI(t, false)
}

// aggregateIsMemoryClassABI reports whether a type is an aggregate passed/returned
// by pointer (byval/sret). The threshold depends on whether the surrounding
// function uses the C ABI: this is the single source of truth shared by
// function-type lowering, attribute application, the callee prologue, and call
// sites, so all four stay consistent for a given function type.
func (g *llvmGenerator) aggregateIsMemoryClassABI(t semantic.Type, cabi bool) bool {
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
	var threshold uint64 = aggregateMemoryClassThresholdBytes
	if cabi {
		threshold = cAbiAggregateMemoryClassThresholdBytes
	}
	return size >= threshold
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
	if !eu && g.aggregateIsMemoryClassABI(fn.Return, funcTypeIsCABI(fn)) {
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
	// noalias on the eligible mutable-ref subset is independent of the byval ABI
	// lowering below, so apply it first (it must hold on arm64, where byval is skipped).
	g.applyMutableRefNoaliasAttrs(fn, fnType, layout.paramBase())
	cabi := funcTypeIsCABI(fnType)
	// At the C-ABI boundary on arm64, memory-class args are plain indirect
	// pointers (no byval attribute); only x86-64 SysV (and all internal calls)
	// use byval. The pointer parameter type and caller-side copy happen regardless.
	if !g.aggregateArgUsesByval(cabi) {
		return
	}
	base := layout.paramBase()
	for i, param := range fnType.Params {
		if !g.aggregateIsMemoryClassABI(param, cabi) {
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
	cabi := funcTypeIsCABI(funcType)
	var byval map[int]C.LLVMTypeRef
	for i := 0; i < len(funcType.Params) && i < len(args); i++ {
		if !s.g.aggregateIsMemoryClassABI(funcType.Params[i], cabi) {
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

func (g *llvmGenerator) addFuncEnumAttr(fn C.LLVMValueRef, llvmParamPos int, name string) {
	if fn == nil {
		return
	}
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	C.elisacoreAddFuncEnumAttr(fn, llvmParamAttrIndex(llvmParamPos), g.context, nameC, C.size_t(len(name)))
}

// pointeeEligibleForNoalias reports whether a `mutable T&` parameter whose pointee
// is `elem` belongs to the subset for which `noalias` is sound to stamp.
//
// Limited to scalar (numeric) and darray pointees — the subset the aliasing model
// proves free of laundered aliases (see memory: noalias-blocked-by-region-escape,
// alias-laundering-hole). Struct/enum/view/void pointees are EXCLUDED: struct refs
// still have open laundering residuals (field-rebind, nested carriers) and `void&`
// is the type-erased aliasing escape hatch the trusted runtime relies on.
func pointeeEligibleForNoalias(elem semantic.Type) bool {
	if elem == nil {
		return false
	}
	if semantic.IsNumericType(elem) {
		return true
	}
	if _, ok := elem.(*semantic.DArrayType); ok {
		return true
	}
	return false
}

// applyMutableRefNoaliasAttrs stamps LLVM `noalias` on the eligible subset of
// `mutable T&` parameters when opted in (ELISACORE_NOALIAS_MUTABLE_REFS).
//
// Soundness rests on the aliasing checker rejecting two live mutable references to
// the same storage (call-arg alias check + escape analysis + closed laundering
// vectors), so a `mutable T&` is the unique mutator of its pointee for the call —
// exactly the restrict semantics `noalias` encodes. This is OFF by default: a
// noalias miscompile is silent, so it stays an explicit opt-in pending empirical
// validation before it can become a default.
func (g *llvmGenerator) applyMutableRefNoaliasAttrs(fn C.LLVMValueRef, fnType *semantic.FuncType, base int) {
	if fn == nil || fnType == nil || !g.noaliasMutableRefs {
		return
	}
	// At the C-ABI boundary the callee is foreign code that may legitimately alias
	// its pointer args; never promise restrict across it.
	if funcTypeIsCABI(fnType) {
		return
	}
	for i, param := range fnType.Params {
		rt, ok := param.(*semantic.RefType)
		if !ok || rt == nil || !rt.Mutable {
			continue
		}
		if !pointeeEligibleForNoalias(rt.Elem) {
			continue
		}
		g.addFuncEnumAttr(fn, base+i, "noalias")
	}
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
