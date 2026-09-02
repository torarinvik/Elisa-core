package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
*/
import "C"

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"elisacore/src/semantic"
)

// The C ABI at an export boundary, per target, pinned against what clang emits for
// the same struct shapes (arm64-apple-darwin, x86_64-apple-darwin,
// wasm32-unknown-unknown):
//
//	arm64  HFA (1..4 leaves of one float type): [N x float]/[N x double] in, the
//	       struct itself out. Other aggregates: <= 8 bytes i64 in / iN out at the
//	       exact width; 9..16 bytes [2 x i64]; > 16 bytes a plain pointer to a
//	       caller copy in, sret out.
//	x86-64 <= 16 bytes: each eightbyte classified INTEGER (an integer of the bytes
//	       the struct uses in it: i24, i48, i64) or SSE (float, <2 x float>,
//	       double); two eightbytes are two parameters and a {a, b} return.
//	       > 16 bytes: byval in, sret out.
//	wasm32 a single scalar leaf is that scalar; anything else byval in, sret out.
//
// Classification walks the LOWERED LLVM struct: element kinds and
// LLVMOffsetOfElement, so nested structs and arrays flatten the same way clang
// flattens them, and both compilers can run the identical algorithm.
type cAbiTarget int

const (
	cAbiArm64 cAbiTarget = iota
	cAbiX86_64
	cAbiWasm32
)

func (g *llvmGenerator) cAbiTarget() cAbiTarget {
	t := strings.ToLower(strings.TrimSpace(g.requestedTargetTriple))
	if t == "" {
		if runtime.GOARCH == "arm64" {
			return cAbiArm64
		}
		return cAbiX86_64
	}
	switch {
	case strings.HasPrefix(t, "wasm32"):
		return cAbiWasm32
	case strings.Contains(t, "arm64"), strings.Contains(t, "aarch64"):
		return cAbiArm64
	default:
		return cAbiX86_64
	}
}

const (
	cAbiLeafInt = iota
	cAbiLeafF32
	cAbiLeafF64
)

type cAbiLeaf struct {
	offset uint64
	size   uint64
	kind   int
}

func (g *llvmGenerator) cAbiFlatten(t C.LLVMTypeRef, base uint64, out *[]cAbiLeaf) error {
	switch C.LLVMGetTypeKind(t) {
	case C.LLVMStructTypeKind:
		n := int(C.LLVMCountStructElementTypes(t))
		for i := 0; i < n; i++ {
			elem := C.LLVMStructGetTypeAtIndex(t, C.unsigned(i))
			off := uint64(C.LLVMOffsetOfElement(g.targetData, t, C.unsigned(i)))
			if err := g.cAbiFlatten(elem, base+off, out); err != nil {
				return err
			}
		}
		return nil
	case C.LLVMArrayTypeKind:
		elem := C.LLVMGetElementType(t)
		n := uint64(C.LLVMGetArrayLength2(t))
		esz := uint64(C.LLVMABISizeOfType(g.targetData, elem))
		for i := uint64(0); i < n; i++ {
			if err := g.cAbiFlatten(elem, base+i*esz, out); err != nil {
				return err
			}
		}
		return nil
	case C.LLVMFloatTypeKind:
		*out = append(*out, cAbiLeaf{offset: base, size: 4, kind: cAbiLeafF32})
		return nil
	case C.LLVMDoubleTypeKind:
		*out = append(*out, cAbiLeaf{offset: base, size: 8, kind: cAbiLeafF64})
		return nil
	case C.LLVMIntegerTypeKind, C.LLVMPointerTypeKind:
		*out = append(*out, cAbiLeaf{offset: base, size: uint64(C.LLVMABISizeOfType(g.targetData, t)), kind: cAbiLeafInt})
		return nil
	default:
		return fmt.Errorf("C ABI: unsupported member kind in an exported aggregate")
	}
}

// cAbiArgPlan says how one exported parameter crosses the boundary.
type cAbiArgPlan struct {
	direct      bool            // parts are the LLVM parameters; else one pointer
	parts       []C.LLVMTypeRef // direct: 1 or 2 parameter types
	partOffsets []uint64        // direct: byte offset of each part in the value
	byval       bool            // indirect: attach byval(T)
	aggregate   bool            // the semantic type is an aggregate at all
	llvmType    C.LLVMTypeRef   // the value's own lowered type
}

// cAbiRetPlan says how the exported return value crosses the boundary.
type cAbiRetPlan struct {
	direct   bool          // retType is returned; else sret
	retType  C.LLVMTypeRef // direct: the ABI return type
	sret     bool
	llvmType C.LLVMTypeRef // the value's own lowered type
}

func (g *llvmGenerator) cAbiIsAggregate(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.ArrayType, *semantic.StructType, *semantic.GenericInstanceType:
		return true
	}
	return false
}

func (g *llvmGenerator) cAbiIntType(bits uint64) C.LLVMTypeRef {
	return C.LLVMIntTypeInContext(g.context, C.unsigned(bits))
}

// eightbyte classification for x86-64 SysV: the ABI type of eightbyte `eb` of a
// value of `size` bytes with the given leaves, or nil when no leaf lands in it.
func (g *llvmGenerator) cAbiX86Eightbyte(leaves []cAbiLeaf, size uint64, eb uint64) C.LLVMTypeRef {
	lo, hi := eb*8, eb*8+8
	ints, f32s, f64s := 0, 0, 0
	for _, l := range leaves {
		if l.offset >= hi || l.offset+l.size <= lo {
			continue
		}
		switch l.kind {
		case cAbiLeafInt:
			ints++
		case cAbiLeafF32:
			f32s++
		case cAbiLeafF64:
			f64s++
		}
	}
	if ints+f32s+f64s == 0 {
		return nil
	}
	if ints > 0 {
		used := size - lo
		if used > 8 {
			used = 8
		}
		return g.cAbiIntType(used * 8)
	}
	if f64s > 0 {
		return C.LLVMDoubleTypeInContext(g.context)
	}
	if f32s >= 2 {
		return C.LLVMVectorType(C.LLVMFloatTypeInContext(g.context), 2)
	}
	return C.LLVMFloatTypeInContext(g.context)
}

func (g *llvmGenerator) cAbiClassifyArg(t semantic.Type) (cAbiArgPlan, error) {
	llvmType, err := g.lowerType(t)
	if err != nil {
		return cAbiArgPlan{}, err
	}
	if !g.cAbiIsAggregate(t) {
		return cAbiArgPlan{direct: true, parts: []C.LLVMTypeRef{llvmType}, partOffsets: []uint64{0}, llvmType: llvmType}, nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return cAbiArgPlan{}, err
	}
	size := uint64(C.LLVMABISizeOfType(g.targetData, llvmType))
	if size == 0 {
		return cAbiArgPlan{direct: true, parts: []C.LLVMTypeRef{llvmType}, partOffsets: []uint64{0}, aggregate: true, llvmType: llvmType}, nil
	}
	var leaves []cAbiLeaf
	if err := g.cAbiFlatten(llvmType, 0, &leaves); err != nil {
		return cAbiArgPlan{}, err
	}
	plan := cAbiArgPlan{aggregate: true, llvmType: llvmType}
	switch g.cAbiTarget() {
	case cAbiWasm32:
		if len(leaves) == 1 {
			plan.direct = true
			plan.parts = []C.LLVMTypeRef{g.cAbiLeafType(leaves[0])}
			plan.partOffsets = []uint64{leaves[0].offset}
			return plan, nil
		}
		plan.byval = true
		return plan, nil
	case cAbiArm64:
		if hfaType, n, ok := g.cAbiHFA(leaves); ok {
			plan.direct = true
			plan.parts = []C.LLVMTypeRef{C.LLVMArrayType2(hfaType, C.uint64_t(n))}
			plan.partOffsets = []uint64{0}
			return plan, nil
		}
		if size <= 8 {
			plan.direct = true
			plan.parts = []C.LLVMTypeRef{g.cAbiIntType(64)}
			plan.partOffsets = []uint64{0}
			return plan, nil
		}
		if size <= 16 {
			plan.direct = true
			plan.parts = []C.LLVMTypeRef{C.LLVMArrayType2(g.cAbiIntType(64), 2)}
			plan.partOffsets = []uint64{0}
			return plan, nil
		}
		return plan, nil // indirect, plain pointer
	default: // x86-64 SysV
		if size > 16 {
			plan.byval = true
			return plan, nil
		}
		plan.direct = true
		for eb := uint64(0); eb*8 < size; eb++ {
			if part := g.cAbiX86Eightbyte(leaves, size, eb); part != nil {
				plan.parts = append(plan.parts, part)
				plan.partOffsets = append(plan.partOffsets, eb*8)
			}
		}
		if len(plan.parts) == 0 {
			plan.parts = []C.LLVMTypeRef{g.cAbiIntType(size * 8)}
			plan.partOffsets = []uint64{0}
		}
		return plan, nil
	}
}

func (g *llvmGenerator) cAbiLeafType(l cAbiLeaf) C.LLVMTypeRef {
	switch l.kind {
	case cAbiLeafF32:
		return C.LLVMFloatTypeInContext(g.context)
	case cAbiLeafF64:
		return C.LLVMDoubleTypeInContext(g.context)
	}
	return g.cAbiIntType(l.size * 8)
}

// cAbiHFA reports a homogeneous floating-point aggregate: 1..4 leaves of one float
// kind, contiguous.
func (g *llvmGenerator) cAbiHFA(leaves []cAbiLeaf) (C.LLVMTypeRef, int, bool) {
	if len(leaves) == 0 || len(leaves) > 4 {
		return nil, 0, false
	}
	kind := leaves[0].kind
	if kind == cAbiLeafInt {
		return nil, 0, false
	}
	for i, l := range leaves {
		if l.kind != kind || l.offset != uint64(i)*l.size {
			return nil, 0, false
		}
	}
	return g.cAbiLeafType(leaves[0]), len(leaves), true
}

func (g *llvmGenerator) cAbiClassifyRet(t semantic.Type) (cAbiRetPlan, error) {
	llvmType, err := g.lowerType(t)
	if err != nil {
		return cAbiRetPlan{}, err
	}
	if isVoidType(t) || !g.cAbiIsAggregate(t) {
		return cAbiRetPlan{direct: true, retType: llvmType, llvmType: llvmType}, nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return cAbiRetPlan{}, err
	}
	size := uint64(C.LLVMABISizeOfType(g.targetData, llvmType))
	if size == 0 {
		return cAbiRetPlan{direct: true, retType: llvmType, llvmType: llvmType}, nil
	}
	var leaves []cAbiLeaf
	if err := g.cAbiFlatten(llvmType, 0, &leaves); err != nil {
		return cAbiRetPlan{}, err
	}
	plan := cAbiRetPlan{llvmType: llvmType}
	switch g.cAbiTarget() {
	case cAbiWasm32:
		if len(leaves) == 1 {
			plan.direct, plan.retType = true, g.cAbiLeafType(leaves[0])
			return plan, nil
		}
		plan.sret = true
		return plan, nil
	case cAbiArm64:
		if _, _, ok := g.cAbiHFA(leaves); ok {
			plan.direct, plan.retType = true, llvmType
			return plan, nil
		}
		if size <= 8 {
			plan.direct, plan.retType = true, g.cAbiIntType(size*8)
			return plan, nil
		}
		if size <= 16 {
			plan.direct, plan.retType = true, C.LLVMArrayType2(g.cAbiIntType(64), 2)
			return plan, nil
		}
		plan.sret = true
		return plan, nil
	default:
		if size > 16 {
			plan.sret = true
			return plan, nil
		}
		var parts []C.LLVMTypeRef
		for eb := uint64(0); eb*8 < size; eb++ {
			if part := g.cAbiX86Eightbyte(leaves, size, eb); part != nil {
				parts = append(parts, part)
			}
		}
		plan.direct = true
		switch len(parts) {
		case 0:
			plan.retType = g.cAbiIntType(size * 8)
		case 1:
			plan.retType = parts[0]
		default:
			plan.retType = C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(parts), C.unsigned(len(parts)), 0)
		}
		return plan, nil
	}
}

// exportFuncABI is the whole ABI signature of an exported function: the LLVM function
// type C sees, and the per-parameter / return plans the wrapper implements.
type exportFuncABI struct {
	fnType C.LLVMTypeRef
	args   []cAbiArgPlan
	ret    cAbiRetPlan
}

func (g *llvmGenerator) exportFuncABI(sig *semantic.FuncType) (exportFuncABI, error) {
	var abi exportFuncABI
	if sig == nil {
		return abi, fmt.Errorf("export without a signature")
	}
	ret, err := g.cAbiClassifyRet(sig.Return)
	if err != nil {
		return abi, err
	}
	abi.ret = ret
	var params []C.LLVMTypeRef
	if ret.sret {
		params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
	}
	for _, p := range sig.Params {
		plan, err := g.cAbiClassifyArg(p)
		if err != nil {
			return abi, err
		}
		abi.args = append(abi.args, plan)
		if plan.direct {
			params = append(params, plan.parts...)
		} else {
			params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
		}
	}
	retType := ret.retType
	if ret.sret {
		retType = C.LLVMVoidTypeInContext(g.context)
	}
	abi.fnType = C.LLVMFunctionType(retType, llvmTypeSlicePtr(params), C.unsigned(len(params)), 0)
	return abi, nil
}

// cAbiSlot allocates a spill slot big enough for both the value and its ABI parts,
// 8-byte aligned (an i64 array), and returns it as a pointer.
func (g *llvmGenerator) cAbiSlot(builder C.LLVMBuilderRef, bytes uint64, name string) C.LLVMValueRef {
	words := (bytes + 7) / 8
	if words == 0 {
		words = 1
	}
	arr := C.LLVMArrayType2(g.cAbiIntType(64), C.uint64_t(words))
	slot := C.LLVMBuildAlloca(builder, arr, cStringFree(name))
	C.LLVMSetAlignment(slot, 8)
	return slot
}

func (g *llvmGenerator) cAbiPtrAt(builder C.LLVMBuilderRef, base C.LLVMValueRef, offset uint64, name string) C.LLVMValueRef {
	if offset == 0 {
		return base
	}
	idx := C.LLVMConstInt(g.cAbiIntType(64), C.ulonglong(offset), 0)
	return C.LLVMBuildGEP2(builder, C.LLVMInt8TypeInContext(g.context), base, &idx, 1, cStringFree(name))
}

// cAbiApplyAttrs attaches sret / byval to the wrapper's parameters.
func (g *llvmGenerator) cAbiApplyAttrs(fn C.LLVMValueRef, abi exportFuncABI) {
	pos := 0
	if abi.ret.sret {
		g.addFuncTypeAttr(fn, llvmParamAttrIndex(0), "sret", abi.ret.llvmType)
		pos = 1
	}
	for _, a := range abi.args {
		if a.direct {
			pos += len(a.parts)
			continue
		}
		if a.byval {
			g.addFuncTypeAttr(fn, llvmParamAttrIndex(pos), "byval", a.llvmType)
		}
		pos++
	}
}

// keep unsafe referenced for the cgo build regardless of helper churn
var _ = unsafe.Pointer(nil)
