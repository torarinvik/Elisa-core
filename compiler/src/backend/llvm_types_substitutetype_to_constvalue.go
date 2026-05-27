//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static void elisacoreAddAlwaysInlineAttr(LLVMContextRef Ctx, LLVMValueRef Fn, const char* Name, size_t NameLen) {
	unsigned Kind = LLVMGetEnumAttributeKindForName(Name, NameLen);
	if (Kind == 0) {
		return;
	}
	LLVMAttributeRef Attr = LLVMCreateEnumAttribute(Ctx, Kind, 0);
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, Attr);
}

static LLVMTypeRef elisacoreGlobalValueType(LLVMValueRef Value) {
	return LLVMGlobalGetValueType(Value);
}

static void elisacoreSetAlignment(LLVMValueRef Value, unsigned Bytes) {
	LLVMSetAlignment(Value, Bytes);
}

static char* elisacorePrintType(LLVMTypeRef Type) {
	return LLVMPrintTypeToString(Type);
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strconv"
	"strings"
	"unsafe"
)

func substituteType(t semantic.Type, subst map[string]semantic.Type, impls map[string]*semantic.StaticImpl) semantic.Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *semantic.TypeParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.ConstParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.ConstValueType:
		return t
	case *semantic.AssociatedTypeProjection:
		receiver := substituteType(tt.Receiver, subst, impls)
		projected := &semantic.AssociatedTypeProjection{Receiver: receiver, InterfaceName: tt.InterfaceName, Name: tt.Name}
		if resolved, ok := semantic.ResolveAssociatedTypeProjection(projected, impls); ok {
			return resolved
		}
		return projected
	case *semantic.RefStorageParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RefStateParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RegionParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RegionValueType:
		return t
	case *semantic.ErrorUnionType:
		return &semantic.ErrorUnionType{Value: substituteType(tt.Value, subst, impls), Errors: tt.Errors}
	case *semantic.OptionalType:
		return &semantic.OptionalType{Value: substituteType(tt.Value, subst, impls)}
	case *semantic.RefType:
		region := tt.Region
		if region != "" {
			if mapped, ok := subst[region]; ok {
				switch mapped := mapped.(type) {
				case *semantic.RegionParamType:
					region = mapped.Name
				case *semantic.RegionValueType:
					region = mapped.Name
				}
			}
		}
		state := tt.State
		stateParam := tt.StateParam
		if stateParam != "" {
			if mapped, ok := subst[stateParam]; ok {
				switch mapped := mapped.(type) {
				case *semantic.RefStateValueType:
					state = mapped.State
					stateParam = ""
				case *semantic.RefStateParamType:
					stateParam = mapped.Name
				}
			}
		}
		storage := tt.Storage
		storageParam := tt.StorageParam
		if storageParam != "" {
			if mapped, ok := subst[storageParam]; ok {
				switch mapped := mapped.(type) {
				case *semantic.RefStorageValueType:
					storage = mapped.Storage
					storageParam = ""
				case *semantic.RefStorageParamType:
					storageParam = mapped.Name
				}
			}
		}
		return &semantic.RefType{Elem: substituteType(tt.Elem, subst, impls), State: state, StateParam: stateParam, Storage: storage, StorageParam: storageParam, Region: region, ExplicitStorage: tt.ExplicitStorage}
	case *semantic.ArrayType:
		elem := substituteType(tt.Elem, subst, impls)
		if tt.ConstParam != "" {
			if mapped, ok := subst[tt.ConstParam]; ok {
				if value, valueOK := mapped.(*semantic.ConstValueType); valueOK && value.Value.Kind == semantic.ConstInt {
					return &semantic.ArrayType{Elem: elem, Size: strconv.FormatInt(value.Value.Int, 10), HasConstSize: true, ConstSize: value.Value.Int, SurfaceName: tt.SurfaceName}
				}
			}
		}
		return &semantic.ArrayType{Elem: elem, Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, ConstParam: tt.ConstParam, SurfaceName: tt.SurfaceName}
	case *semantic.DArrayType:
		return &semantic.DArrayType{Elem: substituteType(tt.Elem, subst, impls), Shape: tt.Shape, SurfaceName: tt.SurfaceName}
	case *semantic.ViewType:
		return &semantic.ViewType{Elem: substituteType(tt.Elem, subst, impls), Begin: tt.Begin, End: tt.End}
	case *semantic.DArrayViewType:
		return &semantic.DArrayViewType{Elem: substituteType(tt.Elem, subst, impls), Begin: tt.Begin, End: tt.End, SurfaceName: tt.SurfaceName}
	case *semantic.TupleType:
		fields := make([]semantic.TupleField, 0, len(tt.Fields))
		for _, field := range tt.Fields {
			fields = append(fields, semantic.TupleField{Name: field.Name, Type: substituteType(field.Type, subst, impls)})
		}
		return &semantic.TupleType{Fields: fields}
	case *semantic.PackedVariantViewType:
		return tt
	case *semantic.DictType:
		return &semantic.DictType{Key: substituteType(tt.Key, subst, impls), Value: substituteType(tt.Value, subst, impls), SurfaceName: tt.SurfaceName}
	case *semantic.SViewType:
		return &semantic.SViewType{Begin: tt.Begin, End: tt.End}
	case *semantic.GenericInstanceType:
		args := make([]semantic.Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, substituteType(arg, subst, impls))
		}
		return &semantic.GenericInstanceType{Name: tt.Name, Base: substituteType(tt.Base, subst, impls), Args: args}
	case *semantic.AggregateStateType:
		return &semantic.AggregateStateType{Base: substituteType(tt.Base, subst, impls), State: tt.State, States: append([]semantic.RefState(nil), tt.States...)}
	case *semantic.FuncType:
		params := make([]semantic.Type, 0, len(tt.Params))
		for _, param := range tt.Params {
			params = append(params, substituteType(param, subst, impls))
		}
		return &semantic.FuncType{
			Name:                   tt.Name,
			TypeParams:             append([]string(nil), tt.TypeParams...),
			RefStorageParams:       append([]string(nil), tt.RefStorageParams...),
			RefStateParams:         append([]string(nil), tt.RefStateParams...),
			RegionParams:           append([]string(nil), tt.RegionParams...),
			GenericParams:          append([]ast.GenericParam(nil), tt.GenericParams...),
			ShapeParams:            append([]string(nil), tt.ShapeParams...),
			FreshReturnShapeParams: append([]string(nil), tt.FreshReturnShapeParams...),
			InlineMode:             tt.InlineMode,
			HasInlineMode:          tt.HasInlineMode,
			HasNoRecurse:           tt.HasNoRecurse,
			HasAsyncEntry:          tt.HasAsyncEntry,
			HasSegmentAgnostic:     tt.HasSegmentAgnostic,
			HasSegmentEstablishing: tt.HasSegmentEstablishing,
			HasReentrantSafe:       tt.HasReentrantSafe,
			SegmentTransition:      tt.SegmentTransition,
			TemperatureMode:        tt.TemperatureMode,
			HasTemperatureMode:     tt.HasTemperatureMode,
			CallConv:               tt.CallConv,
			IntrinsicName:          tt.IntrinsicName,
			Params:                 params,
			Return:                 substituteType(tt.Return, subst, impls),
			Variadic:               tt.Variadic,
		}
	default:
		return t
	}
}
func runtimeDynArrayName(elem semantic.Type) string {
	return mangleGenericType("DynArray", []semantic.Type{elem})
}
func structGenericParams(base *semantic.StructType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.GenericParams) != 0 {
		params := append([]ast.GenericParam(nil), base.GenericParams...)
		seenRegion := map[string]bool{}
		for _, param := range params {
			if param.Kind == ast.GenericParamRegion {
				seenRegion[param.Name] = true
			}
		}
		for _, name := range base.RegionParams {
			if !seenRegion[name] {
				params = append(params, ast.GenericParam{Kind: ast.GenericParamRegion, Name: name})
			}
		}
		return params
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams)+len(base.RegionParams)+1)
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	for _, name := range base.RegionParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamRegion, Name: name})
	}
	if len(base.NamedStateCases) != 0 {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), base.NamedStateCases...), StateOwner: base.Name})
	}
	return params
}
func funcGenericParams(base *semantic.FuncType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams))
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	return params
}
func genericBindingsForArgs(params []ast.GenericParam, args []semantic.Type) map[string]semantic.Type {
	if len(params) == 0 || len(args) == 0 {
		return nil
	}
	bindings := make(map[string]semantic.Type, len(params))
	for i, param := range params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		bindings[param.Name] = args[i]
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}
func mangleGenericType(base string, args []semantic.Type) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, sanitizeIdentifier(arg.String()))
	}
	if len(parts) == 0 {
		return sanitizeIdentifier(base)
	}
	return sanitizeIdentifier(base) + "__" + strings.Join(parts, "__")
}
func sanitizeIdentifier(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	value := strings.Trim(out.String(), "_")
	if value == "" {
		return "anon"
	}
	return value
}
func boolToLLVMBool(v bool) C.LLVMBool {
	if v {
		return 1
	}
	return 0
}
func cString(s string) *C.char {
	return C.CString(s)
}
func llvmTypeSlicePtr(types []C.LLVMTypeRef) *C.LLVMTypeRef {
	if len(types) == 0 {
		return nil
	}
	return (*C.LLVMTypeRef)(unsafe.Pointer(&types[0]))
}
func declIsExternVar(decl ast.Decl) bool {
	_, ok := decl.(*ast.ExternVarDecl)
	return ok
}
func llvmValueSlicePtr(values []C.LLVMValueRef) *C.LLVMValueRef {
	if len(values) == 0 {
		return nil
	}
	return (*C.LLVMValueRef)(unsafe.Pointer(&values[0]))
}
func llvmBlockSlicePtr(blocks []C.LLVMBasicBlockRef) *C.LLVMBasicBlockRef {
	if len(blocks) == 0 {
		return nil
	}
	return (*C.LLVMBasicBlockRef)(unsafe.Pointer(&blocks[0]))
}
func (g *llvmGenerator) exprType(expr ast.Expr) semantic.Type {
	if g.result == nil || expr == nil {
		return nil
	}
	if t, ok := g.result.ExprTypes[expr]; ok {
		return t
	}
	return nil
}
func (g *llvmGenerator) constValue(name string) (semantic.ConstValue, bool) {
	if strings.HasPrefix(name, "$consteval.") {
		localName := strings.TrimPrefix(name, "$consteval.")
		for i := len(g.constEvalScopes) - 1; i >= 0; i-- {
			if value, ok := g.constEvalScopes[i][localName]; ok {
				return value, true
			}
		}
		return semantic.ConstValue{}, false
	}
	if g.result == nil {
		return semantic.ConstValue{}, false
	}
	value, ok := g.result.ConstValues[name]
	return value, ok
}
