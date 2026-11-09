//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static void elisacoreSetAlignment(LLVMValueRef Value, unsigned Bytes) {
	LLVMSetAlignment(Value, Bytes);
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// setInstrAlignment overrides the alignment (in bytes) of a load/store instruction. Used by the
// AoS slot-narrowing path: records at a narrowed (e.g. 12-byte) stride are only slot-aligned, so
// stores/loads of 8-byte payload fields must be told their pointer is slot-aligned (align 4) rather
// than letting LLVM assume the value type's natural 8-byte alignment.
func (g *llvmGenerator) setInstrAlignment(value C.LLVMValueRef, bytes uint64) {
	if g == nil || value == nil || bytes == 0 {
		return
	}
	C.elisacoreSetAlignment(value, C.uint(bytes))
}

func (g *llvmGenerator) applyTypeAlignment(value C.LLVMValueRef, t semantic.Type) {
	if g == nil || value == nil {
		return
	}
	alignment, ok := semantic.RequestedAlignment(t)
	if !ok || alignment == 0 {
		return
	}
	C.elisacoreSetAlignment(value, C.uint(alignment))
}
func (g *llvmGenerator) lowerFunctionType(fn *semantic.FuncType) (C.LLVMTypeRef, error) {
	return g.lowerFunctionTypeInternal(fn, false)
}

// lowerFunctionTypeForSymbol applies symbol-level ABI adaptations that do not belong
// in semantic.FuncType. In particular, a source-level `void main` is still void to
// Elisa callers, but the externally visible C entry point must return an i32 status.
// Keeping this override at the declaration boundary preserves the language type while
// making the process exit status deterministic.
func (g *llvmGenerator) lowerFunctionTypeForSymbol(name string, fn *semantic.FuncType) (C.LLVMTypeRef, error) {
	if name == "main" && fn != nil && isVoidType(fn.Return) {
		return g.lowerFunctionTypeInternal(fn, true)
	}
	return g.lowerFunctionType(fn)
}

func (g *llvmGenerator) lowerFunctionTypeInternal(fn *semantic.FuncType, entryStatus bool) (C.LLVMTypeRef, error) {
	if g == nil || fn == nil {
		return nil, fmt.Errorf("missing function type for LLVM lowering")
	}
	if !entryStatus {
		if cached, ok := g.functionTypes[fn]; ok && cached != nil {
			return cached, nil
		}
	}
	returnType, err := g.lowerFunctionReturnType(fn.Return)
	if err != nil {
		return nil, err
	}
	_, isErrorUnionReturn := nonVoidErrorUnion(fn.Return)
	// `-> T?` at a C boundary is the bare nullable payload pointer, not Elisa's
	// {tag, payload} struct; the call site rebuilds the optional from a null test.
	// See externOptionalABI.
	_, nicheOptionalReturn := externNicheOptionalPayload(fn, fn.Return)
	if nicheOptionalReturn {
		returnType = C.LLVMPointerTypeInContext(g.context, 0)
	}
	// An `extern resource` crosses as its bare handle (llvm_extern_resource_abi.go).
	if externReturnsResource(fn) {
		returnType = C.LLVMPointerTypeInContext(g.context, 0)
	}
	// Large aggregate returns are returned via an sret out-pointer (return void),
	// mirroring the error-union out-param mechanism. (Error-union returns already
	// carry their value via an out-pointer, so they are mutually exclusive.)
	cabi := funcTypeIsCABI(fn)
	sretReturn := !isErrorUnionReturn && !nicheOptionalReturn && g.aggregateIsMemoryClassABI(fn.Return, cabi)
	if sretReturn {
		returnType = C.LLVMVoidTypeInContext(g.context)
	}
	if entryStatus {
		returnType = C.LLVMInt32TypeInContext(g.context)
	}
	params := make([]C.LLVMTypeRef, 0, len(fn.Params)+1)
	if unionType, ok := nonVoidErrorUnion(fn.Return); ok {
		outParamType, err := g.lowerErrorUnionOutParamType(unionType)
		if err != nil {
			return nil, err
		}
		params = append(params, outParamType)
	}
	if sretReturn {
		params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
	}
	// A C-ABI extern's C parameter order may differ from its semantic one: each `view[T]`
	// is (ptr, len), placed adjacently or where `@bounds` says (llvm_extern_view_abi.go).
	if parts := externCParts(fn); parts != nil {
		lengthType, err := g.lowerBuiltin("usize")
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			switch part.Part {
			case semantic.CParamViewPtr:
				params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
			case semantic.CParamViewLen:
				params = append(params, lengthType)
			default:
				param := fn.Params[part.Param]
				if _, ok := externNicheOptionalPayload(fn, param); ok {
					params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
					continue
				}
				if _, ok := externResourceParam(fn, param); ok {
					params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
					continue
				}
				if g.aggregateIsMemoryClassABI(param, cabi) {
					params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
					continue
				}
				paramType, err := g.lowerType(param)
				if err != nil {
					return nil, err
				}
				params = append(params, paramType)
			}
		}
	}
	for i, param := range fn.Params {
		if externCParts(fn) != nil {
			_ = i
			break
		}
		if _, ok := externNicheOptionalPayload(fn, param); ok {
			// `T?` at a C boundary is the bare nullable pointer; see externOptionalABI.
			params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
			continue
		}
		if _, ok := externResourceParam(fn, param); ok {
			params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
			continue
		}
		if g.aggregateIsMemoryClassABI(param, cabi) {
			// Memory-class params are passed by pointer (byval / indirect).
			params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
			continue
		}
		paramType, err := g.lowerType(param)
		if err != nil {
			return nil, err
		}
		params = append(params, paramType)
	}
	// Region-parameterized containers: one hidden Arena& (pointer) parameter per
	// region parameter, appended after the explicit/implicit params. The callee
	// reads these into its region environment; callers pass the bound region's
	// arena. Inert for every existing function (none declare region params).
	for range fn.RegionParams {
		params = append(params, C.LLVMPointerTypeInContext(g.context, 0))
	}
	lowered := C.LLVMFunctionType(returnType, llvmTypeSlicePtr(params), C.unsigned(len(params)), boolToLLVMBool(fn.Variadic))
	if !entryStatus {
		g.functionTypes[fn] = lowered
	}
	return lowered, nil
}
func (g *llvmGenerator) lowerFunctionReturnType(t semantic.Type) (C.LLVMTypeRef, error) {
	if unionType, ok := nonVoidErrorUnion(t); ok {
		return g.lowerType(unionType.Errors)
	}
	return g.lowerType(t)
}
func (g *llvmGenerator) lowerErrorUnionOutParamType(unionType *semantic.ErrorUnionType) (C.LLVMTypeRef, error) {
	if unionType == nil || isVoidType(unionType.Value) {
		return nil, fmt.Errorf("missing value-carrying error union metadata")
	}
	if _, err := g.lowerType(unionType.Value); err != nil {
		return nil, err
	}
	return C.LLVMPointerTypeInContext(g.context, 0), nil
}
func (g *llvmGenerator) lowerType(t semantic.Type) (C.LLVMTypeRef, error) {
	if t == nil {
		return C.LLVMVoidTypeInContext(g.context), nil
	}
	switch tt := t.(type) {
	case *semantic.InvalidType:
		return C.LLVMVoidTypeInContext(g.context), nil
	case *semantic.NeverType:
		return C.LLVMVoidTypeInContext(g.context), nil
	case *semantic.NullType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.BuiltinType:
		return g.lowerBuiltin(tt.Name)
	case *semantic.BitIntType:
		return g.lowerBitInt(tt.Width), nil
	case *semantic.IDType:
		return g.lowerType(tt.Storage)
	case *semantic.AddressSpaceType:
		return g.lowerType(tt.Storage)
	case *semantic.ConstEnumType:
		return g.lowerType(tt.Storage)
	case *semantic.ErrorSetType:
		if tt.HasPayloads() {
			return g.ensureErrorSetType(tt)
		}
		return g.lowerBuiltin("u32")
	case *semantic.AssociatedTypeProjection:
		resolved, err := g.resolveAssociatedTypeProjection(tt)
		if err != nil {
			return nil, err
		}
		return g.lowerType(resolved)
	case *semantic.ErrorUnionType:
		errType, err := g.lowerType(tt.Errors)
		if err != nil {
			return nil, err
		}
		if isVoidType(tt.Value) {
			return errType, nil
		}
		return g.ensureErrorUnionType(tt)
	case *semantic.OptionalType:
		if isVoidType(tt.Value) {
			return nil, fmt.Errorf("optional type %s cannot wrap void", tt.String())
		}
		return g.ensureOptionalType(tt)
	case *semantic.TupleType:
		fields := make([]C.LLVMTypeRef, 0, len(tt.Fields))
		for _, field := range tt.Fields {
			fieldType, err := g.lowerType(field.Type)
			if err != nil {
				return nil, err
			}
			fields = append(fields, fieldType)
		}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.BitGroupType:
		return g.lowerBitInt(tt.BackingWidth), nil
	case *semantic.TypeParamType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.RefType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.PackedEnumStoreType:
		return g.lowerPackedEnumStoreType(tt)
	case *semantic.PackedVariantViewType:
		return g.ensurePackedVariantViewCarrierType(tt)
	case *semantic.ArrayType:
		elemType, err := g.lowerType(tt.Elem)
		if err != nil {
			return nil, err
		}
		if tt.HasConstSize {
			return C.LLVMArrayType2(elemType, C.uint64_t(tt.ConstSize)), nil
		}
		return nil, fmt.Errorf("array type %s is missing a compile-time constant size", tt.String())
	case *semantic.DArrayType:
		return g.ensureRuntimeDynArray(tt.Elem)
	case *semantic.ViewType:
		return g.ensureRuntimeDynArrayView()
	case *semantic.StoreRowsViewType:
		storePtrType := C.LLVMPointerTypeInContext(g.context, 0)
		fields := []C.LLVMTypeRef{storePtrType}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.StoreRowViewType:
		usizeType, err := g.lowerType(g.namedType("usize"))
		if err != nil {
			return nil, err
		}
		storePtrType := C.LLVMPointerTypeInContext(g.context, 0)
		fields := []C.LLVMTypeRef{storePtrType, usizeType}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.CStrType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.DictType:
		base, ok := g.lookupNamedType("DynDict")
		if !ok {
			return nil, fmt.Errorf("missing runtime struct DynDict")
		}
		return g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{tt.Key, tt.Value}})
	case *semantic.SetType:
		base, ok := g.lookupNamedType("DynSet")
		if !ok {
			return nil, fmt.Errorf("missing runtime struct DynSet")
		}
		return g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynSet", Base: base, Args: []semantic.Type{tt.Elem}})
	case *semantic.DictEntryType:
		dictRefType, err := g.lowerType(&semantic.RefType{Elem: tt.Dict, Mutable: tt.Mutable, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true})
		if err != nil {
			return nil, err
		}
		keyType, err := g.lowerType(tt.Dict.Key)
		if err != nil {
			return nil, err
		}
		valueRefType, err := g.lowerType(&semantic.RefType{Elem: tt.Dict.Value, Mutable: tt.Mutable, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true})
		if err != nil {
			return nil, err
		}
		fields := []C.LLVMTypeRef{dictRefType, keyType, valueRefType}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.SViewType:
		st, ok := g.lookupStructType("StringView")
		if !ok {
			return nil, fmt.Errorf("missing runtime struct StringView")
		}
		return g.ensureStructBody(st.Name, st)
	case *semantic.EnumType:
		if tt.Packed {
			return g.lowerPackedEnumType(tt)
		}
		// docs/77: every enum in a sealed hierarchy lowers to the ROOT's representation (unified tag +
		// payload union over all leaves), so all members share one LLVM type and upcast is a no-op.
		if tt.Parent != nil || len(tt.Children) > 0 {
			root := tt.Root()
			return g.ensureEnumBody(root.Name, root)
		}
		return g.ensureEnumBody(tt.Name, tt)
	case *semantic.StructType:
		// Region parameters carry provenance, not layout: an `@owner` container/ref field
		// has the same representation regardless of which region backs it. So a struct
		// parameterized ONLY over regions (`struct State[@owner]:`) lowers to a
		// concrete body directly — it is the no-Arena-field replacement for arena-carrying
		// state structs. Only TYPE/state parameters genuinely require concrete arguments.
		needsConcrete := false
		for _, gp := range structGenericParams(tt) {
			if gp.Kind != ast.GenericParamRegion {
				needsConcrete = true
				break
			}
		}
		if !needsConcrete {
			return g.ensureStructBody(tt.Name, tt)
		}
		return nil, fmt.Errorf("cannot lower generic struct %s without concrete type arguments", tt.Name)
	case *semantic.AggregateStateType:
		return g.lowerType(tt.Base)
	case *semantic.OpaqueType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.GenericInstanceType:
		return g.ensureGenericInstanceStruct(tt)
	case *semantic.FuncType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	default:
		return nil, fmt.Errorf("unsupported semantic type %T", t)
	}
}

func (g *llvmGenerator) lowerBitInt(width int) C.LLVMTypeRef {
	if width < 1 {
		width = 1
	}
	if width > 64 {
		width = 64
	}
	return C.LLVMIntTypeInContext(g.context, C.unsigned(width))
}
