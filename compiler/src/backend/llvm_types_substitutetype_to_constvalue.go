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
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"encoding/hex"
	"fmt"
	"reflect"
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
	case *semantic.RegionParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RegionValueType:
		return t
	case *semantic.ErrorUnionType:
		errors := tt.Errors
		if errors != nil && errors.HasParams() {
			resolved := &semantic.ErrorSetType{Name: errors.Name, Tags: append([]string(nil), errors.Tags...), Payloads: errors.Payloads}
			changed := false
			for _, param := range errors.Params {
				if mapped, ok := subst[param]; ok {
					if set, isSet := mapped.(*semantic.ErrorSetType); isSet && set != nil {
						resolved = semantic.UnionErrorSets(resolved, set)
						changed = true
						continue
					}
				}
				resolved = semantic.UnionErrorSets(resolved, &semantic.ErrorSetType{Name: param, Params: []string{param}})
			}
			if changed {
				errors = resolved
			}
		}
		value := substituteType(tt.Value, subst, impls)
		// An infallible binding (R := ∅) collapses `T error[∅]` back to `T` so the
		// monomorphized signature uses the plain value ABI (mirrors the analyzer).
		if errors.IsEmpty() {
			return value
		}
		return &semantic.ErrorUnionType{Value: value, Errors: errors}
	case *semantic.OptionalType:
		return &semantic.OptionalType{Value: substituteType(tt.Value, subst, impls)}
	case *semantic.RefType:
		return &semantic.RefType{
			Elem:            substituteType(tt.Elem, subst, impls),
			Mutable:         tt.Mutable,
			State:           tt.State,
			Storage:         tt.Storage,
			Region:          substituteRegionName(tt.Region, subst),
			ExplicitStorage: tt.ExplicitStorage,
			Linear:          tt.Linear,
		}
	case *semantic.ArrayType:
		elem := substituteType(tt.Elem, subst, impls)
		if tt.ConstParam != "" {
			if mapped, ok := subst[tt.ConstParam]; ok {
				if value, valueOK := mapped.(*semantic.ConstValueType); valueOK && value.Value.Kind == semantic.ConstInt {
					return &semantic.ArrayType{Elem: elem, ElemTypeExpr: tt.ElemTypeExpr, Size: strconv.FormatInt(value.Value.Int, 10), HasConstSize: true, ConstSize: value.Value.Int, SurfaceName: tt.SurfaceName}
				}
			}
		}
		return &semantic.ArrayType{Elem: elem, ElemTypeExpr: tt.ElemTypeExpr, Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, ConstParam: tt.ConstParam, SurfaceName: tt.SurfaceName}
	case *semantic.DArrayType:
		return &semantic.DArrayType{
			Elem:         substituteType(tt.Elem, subst, impls),
			Shape:        tt.Shape,
			SurfaceName:  tt.SurfaceName,
			ElemTypeExpr: tt.ElemTypeExpr,
			Region:       substituteRegionName(tt.Region, subst),
		}
	case *semantic.ViewType:
		return &semantic.ViewType{
			Elem:        substituteType(tt.Elem, subst, impls),
			Mutable:     tt.Mutable,
			Begin:       tt.Begin,
			End:         tt.End,
			SurfaceName: tt.SurfaceName,
			Region:      substituteRegionName(tt.Region, subst),
		}
	case *semantic.TupleType:
		fields := make([]semantic.TupleField, 0, len(tt.Fields))
		for _, field := range tt.Fields {
			fields = append(fields, semantic.TupleField{Name: field.Name, Type: substituteType(field.Type, subst, impls)})
		}
		return &semantic.TupleType{Fields: fields}
	case *semantic.PackedVariantViewType:
		return tt
	case *semantic.DictType:
		return &semantic.DictType{Key: substituteType(tt.Key, subst, impls), Value: substituteType(tt.Value, subst, impls), SurfaceName: tt.SurfaceName, Region: substituteRegionName(tt.Region, subst)}
	case *semantic.SetType:
		return &semantic.SetType{Elem: substituteType(tt.Elem, subst, impls), SurfaceName: tt.SurfaceName, Region: substituteRegionName(tt.Region, subst)}
	case *semantic.CStrType:
		return &semantic.CStrType{Shape: tt.Shape, SurfaceName: tt.SurfaceName, Region: substituteRegionName(tt.Region, subst)}
	case *semantic.SViewType:
		return &semantic.SViewType{Begin: tt.Begin, End: tt.End, Region: substituteRegionName(tt.Region, subst)}
	case *semantic.GenericInstanceType:
		args := make([]semantic.Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, substituteType(arg, subst, impls))
		}
		return &semantic.GenericInstanceType{Name: tt.Name, Base: substituteType(tt.Base, subst, impls), Args: args, Region: substituteRegionName(tt.Region, subst)}
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
			IsNativeExtern:         tt.IsNativeExtern,
			IntrinsicName:          tt.IntrinsicName,
			Params:                 params,
			ExplicitParamCount:     tt.ExplicitParamCount,
			ExplicitParamNames:     append([]string(nil), tt.ExplicitParamNames...),
			ImplicitParamNames:     append([]string(nil), tt.ImplicitParamNames...),
			Return:                 substituteType(tt.Return, subst, impls),
			Variadic:               tt.Variadic,
		}
	default:
		return t
	}
}

func substituteRegionName(region string, subst map[string]semantic.Type) string {
	if region == "" {
		return ""
	}
	mapped, ok := subst[region]
	if !ok {
		return region
	}
	switch mapped := mapped.(type) {
	case *semantic.RegionParamType:
		if mapped != nil && mapped.Name != "" {
			return mapped.Name
		}
	case *semantic.RegionValueType:
		if mapped != nil && mapped.Name != "" {
			return mapped.Name
		}
	}
	return region
}
func (g *llvmGenerator) runtimeDynArrayName(elem semantic.Type) (string, error) {
	// Preserve established builtin carrier names, which are also consumed by
	// runtime IR. Nominal/compound elements use the tagged structural identity.
	if builtin, ok := elem.(*semantic.BuiltinType); ok && builtin != nil && builtin.Name != "" && sanitizeIdentifier(builtin.Name) == builtin.Name {
		return "DynArray__" + builtin.Name, nil
	}
	switch elem.(type) {
	case *semantic.SViewType:
		return "DynArray__sview", nil
	case *semantic.CStrType:
		return "DynArray__cstr", nil
	}

	return g.mangleGenericLayoutType("DynArray", []semantic.Type{elem})
}

// structGenericParams returns a read-only parameter list. Complete structured
// declarations can be borrowed; only legacy/inferred parameter synthesis needs
// a new slice. Callers must not mutate the returned parameters.
func structGenericParams(base *semantic.StructType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.GenericParams) != 0 {
		complete := len(base.RegionParams) == 0
		// Small declarations avoid a map; larger ones keep the original
		// linear synthesis path instead of doing a quadratic membership scan.
		if !complete && len(base.GenericParams) <= 64 && len(base.RegionParams) <= 8 {
			complete = true
			for _, region := range base.RegionParams {
				found := false
				for _, param := range base.GenericParams {
					if param.Kind == ast.GenericParamRegion && param.Name == region {
						found = true
						break
					}
				}
				if !found {
					complete = false
					break
				}
			}
		}
		if complete {
			return base.GenericParams
		}
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
	if len(base.TypeParams) == 0 && len(base.RegionParams) == 0 && len(base.NamedStateCases) == 0 {
		return nil
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
func genericInstantiationKey(base string, args []semantic.Type, layout bool) (string, error) {
	var key strings.Builder
	fmt.Fprintf(&key, "%d:%s|%d|", len(base), base, len(args))
	var identity string
	var err error
	if layout {
		key.WriteString("nominal-layout-v1|")
		identity, err = semantic.TypeSymbolKeysForNominalLayout(args)
	} else {
		key.WriteString("specialization-v1|")
		identity, err = semantic.TypeSymbolKeys(args)
	}
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&key, "%d:%s|", len(identity), identity)

	return key.String(), nil
}

func (g *llvmGenerator) mangleGenericType(base string, args []semantic.Type) (string, error) {
	return g.mangleGenericSymbol(base, args, false)
}

func (g *llvmGenerator) mangleGenericLayoutType(base string, args []semantic.Type) (string, error) {
	return g.mangleGenericSymbol(base, args, true)
}

func (g *llvmGenerator) mangleGenericSymbol(base string, args []semantic.Type, layout bool) (string, error) {
	shared := g.sharedLookups
	if shared != nil {
		shared = shared.programLookups()
	}
	lookupKey, cacheable := genericSymbolLookupKeyFor(base, args, layout)
	if cacheable && shared != nil {
		if cached, ok := shared.genericSymbols.Load(lookupKey); ok {
			entry := cached.(genericSymbolCacheValue)
			if genericSymbolCacheArgsMatch(lookupKey, entry.arguments) {
				if err := g.rememberGenericSymbol(entry.name, entry.identity, base); err != nil {
					return "", err
				}
				return entry.name, nil
			}
		}
	}
	key, err := genericInstantiationKey(base, args, layout)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(key))
	prefix := sanitizeIdentifier(base)
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	name := prefix + "$generic_v1$" + hex.EncodeToString(digest[:])
	if err := g.rememberGenericSymbol(name, key, prefix); err != nil {
		return "", err
	}
	if cacheable && shared != nil {
		entry := genericSymbolCacheValue{arguments: append([]semantic.Type(nil), args...), identity: key, name: name}
		if cached, loaded := shared.genericSymbols.LoadOrStore(lookupKey, entry); loaded {
			previous := cached.(genericSymbolCacheValue)
			if genericSymbolCacheArgsMatch(lookupKey, previous.arguments) && previous.identity != key {
				return "", fmt.Errorf("generic symbol inputs changed during code generation for %s", prefix)
			}
		}
	}
	return name, nil
}

func genericSymbolLookupKeyFor(base string, args []semantic.Type, layout bool) (genericSymbolLookupKey, bool) {
	var key genericSymbolLookupKey
	if len(args) > len(key.args) {
		return key, false
	}
	key.base, key.layout, key.count = base, layout, uint8(len(args))
	for index, arg := range args {
		if arg == nil {
			return genericSymbolLookupKey{}, false
		}
		value := reflect.ValueOf(arg)
		if value.Kind() != reflect.Ptr || value.IsNil() {
			return genericSymbolLookupKey{}, false
		}
		key.args[index] = value.Pointer()
	}
	return key, true
}

func genericSymbolCacheArgsMatch(key genericSymbolLookupKey, args []semantic.Type) bool {
	if len(args) != int(key.count) {
		return false
	}
	for index, arg := range args {
		if arg == nil {
			return false
		}
		value := reflect.ValueOf(arg)
		if value.Kind() != reflect.Ptr || value.IsNil() || value.Pointer() != key.args[index] {
			return false
		}
	}
	return true
}

func (g *llvmGenerator) rememberGenericSymbol(name, key, base string) error {
	if g.genericSymbolKeys == nil {
		g.genericSymbolKeys = make(map[string]string)
	}
	if previous, exists := g.genericSymbolKeys[name]; exists && previous != key {
		prefix := sanitizeIdentifier(base)
		if len(prefix) > 48 {
			prefix = prefix[:48]
		}
		return fmt.Errorf("generic symbol digest collision for %s", prefix)
	}
	g.genericSymbolKeys[name] = key
	return nil
}

func legacyGenericExternName(base string, args []semantic.Type) string {
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
	if t, ok := g.resultExprTypeOK(expr); ok {
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
