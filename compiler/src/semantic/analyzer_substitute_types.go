package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

func (a *Analyzer) substituteType(t Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef) Type {
	return a.substituteTypeWithDepth(t, bindings, shapeBindings, regionBindings, permissionBindings, 0)
}

func (a *Analyzer) substituteTypeWithDepth(t Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef, depth int) Type {
	if t == nil {
		return nil
	}
	if depth > semanticSubstitutionDepthLimit {
		a.reportSemanticDepthLimit("type substitution", semanticSubstitutionDepthLimit)
		return invalidType
	}
	switch n := t.(type) {
	case *TypeParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *ConstParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *ConstValueType:
		return n
	case *IDType:
		tag := a.substituteTypeWithDepth(n.Tag, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(tag) {
			return invalidType
		}
		storage := a.substituteTypeWithDepth(n.Storage, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(storage) {
			return invalidType
		}
		return &IDType{Tag: tag, Storage: storage}
	case *AddressSpaceType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		storage := a.substituteTypeWithDepth(n.Storage, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(storage) {
			return invalidType
		}
		return &AddressSpaceType{Space: n.Space, Elem: elem, Storage: storage}
	case *AssociatedTypeProjection:
		receiver := a.substituteTypeWithDepth(n.Receiver, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(receiver) {
			return invalidType
		}
		projected := &AssociatedTypeProjection{Receiver: receiver, InterfaceName: n.InterfaceName, Name: n.Name}
		if resolved, ok := ResolveAssociatedTypeProjection(projected, a.staticImpls); ok {
			return resolved
		}
		return projected
	case *RegionParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *RegionValueType:
		return n
	case *ErrorUnionType:
		value := a.substituteTypeWithDepth(n.Value, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(value) {
			return invalidType
		}
		errors := substituteErrorSetParams(n.Errors, bindings)
		return &ErrorUnionType{Value: value, Errors: errors}
	case *OptionalType:
		value := a.substituteTypeWithDepth(n.Value, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(value) {
			return invalidType
		}
		return &OptionalType{Value: value}
	case *RefType:
		region := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			region = bound
		}
		state := n.State
		storage := n.Storage
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &RefType{Elem: elem, Mutable: n.Mutable, State: state, Storage: storage, Region: region, ExplicitStorage: n.ExplicitStorage}
	case *ArrayType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		if n.ConstParam != "" {
			if resolved, ok := bindings[n.ConstParam]; ok {
				if value, valueOK := resolved.(*ConstValueType); valueOK && value.Value.Kind == ConstInt {
					return &ArrayType{Elem: elem, Size: strconv.FormatInt(value.Value.Int, 10), HasConstSize: true, ConstSize: value.Value.Int, SurfaceName: n.SurfaceName}
				}
			}
		}
		return &ArrayType{Elem: elem, Size: n.Size, HasConstSize: n.HasConstSize, ConstSize: n.ConstSize, ConstParam: n.ConstParam, SurfaceName: n.SurfaceName}
	case *DArrayType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		region := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			region = bound
		}
		return &DArrayType{Elem: elem, Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName, Region: region}
	case *ViewType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &ViewType{Elem: elem, Begin: n.Begin, End: n.End, SurfaceName: n.SurfaceName, Region: n.Region}
	case *DStrType:
		dstrRegion := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			dstrRegion = bound
		}
		return &DStrType{Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName, Region: dstrRegion}
	case *DictType:
		key := a.substituteTypeWithDepth(n.Key, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(key) {
			return invalidType
		}
		value := a.substituteTypeWithDepth(n.Value, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(value) {
			return invalidType
		}
		dictRegion := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			dictRegion = bound
		}
		return &DictType{Key: key, Value: value, SurfaceName: n.SurfaceName, Region: dictRegion}
	case *SetType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		setRegion := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			setRegion = bound
		}
		return &SetType{Elem: elem, SurfaceName: n.SurfaceName, Region: setRegion}
	case *SViewType:
		sviewRegion := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			sviewRegion = bound
		}
		return &SViewType{Begin: n.Begin, End: n.End, Region: sviewRegion}
	case *GenericInstanceType:
		args := make([]Type, 0, len(n.Args))
		for _, arg := range n.Args {
			resolvedArg := a.substituteTypeWithDepth(arg, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
			if IsInvalidType(resolvedArg) {
				return invalidType
			}
			args = append(args, resolvedArg)
		}
		return &GenericInstanceType{Name: n.Name, Base: n.Base, Args: args}
	case *TupleType:
		fields := make([]TupleField, 0, len(n.Fields))
		for _, field := range n.Fields {
			fieldType := a.substituteTypeWithDepth(field.Type, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
			if IsInvalidType(fieldType) {
				return invalidType
			}
			fields = append(fields, TupleField{Name: field.Name, Type: fieldType})
		}
		return &TupleType{Fields: fields}
	case *AggregateStateType:
		base := a.substituteTypeWithDepth(n.Base, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(base) {
			return invalidType
		}
		return cloneAggregateStateWithBase(base, aggregateStateStates(n))
	case *PackedEnumStoreType:
		state := a.substituteTypeWithDepth(n.State, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(state) {
			return invalidType
		}
		return PackedEnumStoreWithState(n, state)
	case *TreeStoreType:
		state := a.substituteTypeWithDepth(n.State, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(state) {
			return invalidType
		}
		return TreeStoreWithState(n, state)
	case *FuncType:
		params := make([]Type, 0, len(n.Params))
		for _, param := range n.Params {
			resolvedParam := a.substituteTypeWithDepth(param, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
			if IsInvalidType(resolvedParam) {
				return invalidType
			}
			params = append(params, resolvedParam)
		}
		declaredRefs, refs, usedPermissionParams := substitutePermissionRefs(n.DeclaredPermissionRefs, n.PermissionRefs, n.UsedPermissionParams, permissionBindings)
		returnType := a.substituteTypeWithDepth(n.Return, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(returnType) {
			return invalidType
		}
		return &FuncType{Name: n.Name, TypeParams: append([]string(nil), n.TypeParams...), RegionParams: append([]string(nil), n.RegionParams...), PermissionParams: append([]string(nil), n.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), n.GenericParams...), UsedPermissionParams: usedPermissionParams, DeclaredPermissionRefs: declaredRefs, DeclaredPermissions: permissionFamiliesFromRefs(declaredRefs), PermissionRefs: refs, Permissions: permissionFamiliesFromRefs(refs), ShapeParams: append([]string(nil), n.ShapeParams...), FreshReturnShapeParams: append([]string(nil), n.FreshReturnShapeParams...), Static: n.Static, CapturesThreadUnsafe: n.CapturesThreadUnsafe, InlineMode: n.InlineMode, HasInlineMode: n.HasInlineMode, HasNoRecurse: n.HasNoRecurse, HasAsyncEntry: n.HasAsyncEntry, HasSegmentAgnostic: n.HasSegmentAgnostic, HasSegmentEstablishing: n.HasSegmentEstablishing, HasReentrantSafe: n.HasReentrantSafe, SegmentTransition: n.SegmentTransition, TemperatureMode: n.TemperatureMode, HasTemperatureMode: n.HasTemperatureMode, CallConv: n.CallConv, IntrinsicName: n.IntrinsicName, GuardEffects: cloneFuncGuardEffects(n.GuardEffects), BoundaryPointerParamIndices: append([]int(nil), n.BoundaryPointerParamIndices...), Poststates: cloneFuncPoststates(n.Poststates), Params: params, ExplicitParamCount: n.ExplicitParamCount, ExplicitParamNames: append([]string(nil), n.ExplicitParamNames...), ExplicitParamDefaultExprs: append([]ast.Expr(nil), n.ExplicitParamDefaultExprs...), ExplicitParamHasDefault: append([]bool(nil), n.ExplicitParamHasDefault...), ImplicitParamNames: append([]string(nil), n.ImplicitParamNames...), Return: returnType, Variadic: n.Variadic, SinkParams: append([]bool(nil), n.SinkParams...), SinkParamsKnown: n.SinkParamsKnown, ReturnProvenance: cloneRegionRefState(n.ReturnProvenance), ReturnProvenanceKnown: n.ReturnProvenanceKnown, ReturnBorrowedOwnerRefs: cloneBorrowedOwnerRefSummary(n.ReturnBorrowedOwnerRefs), ReturnBorrowedOwnerRefsKnown: n.ReturnBorrowedOwnerRefsKnown, ReturnIsolation: n.ReturnIsolation, ReturnIsolationKnown: n.ReturnIsolationKnown, ReturnsOwnedRegion: n.ReturnsOwnedRegion, OwnedParams: append([]bool(nil), n.OwnedParams...)}
	default:
		return t
	}
}

func substitutePermissionRefs(declared []ast.PermissionRef, refs []ast.PermissionRef, permissionParams []string, bindings map[string][]ast.PermissionRef) ([]ast.PermissionRef, []ast.PermissionRef, []string) {
	if len(bindings) == 0 {
		return append([]ast.PermissionRef(nil), declared...), append([]ast.PermissionRef(nil), refs...), append([]string(nil), permissionParams...)
	}
	substitute := func(items []ast.PermissionRef) []ast.PermissionRef {
		if len(items) == 0 {
			return nil
		}
		out := make([]ast.PermissionRef, 0, len(items))
		for _, ref := range items {
			if ref.Member == "" {
				if bound, ok := bindings[ref.Name]; ok {
					out = append(out, bound...)
					continue
				}
			}
			out = append(out, ref)
		}
		return canonicalizePermissionRefs(out)
	}
	remaining := make([]string, 0, len(permissionParams))
	for _, name := range permissionParams {
		if _, ok := bindings[name]; !ok {
			remaining = append(remaining, name)
		}
	}
	return substitute(declared), substitute(refs), remaining
}

func (a *Analyzer) substituteShape(shape Shape, bindings map[string]Shape) Shape {
	param, ok := shape.(*ShapeParam)
	if !ok {
		return shape
	}
	if resolved, ok := bindings[param.Name]; ok {
		return resolved
	}
	return shape
}

// substituteErrorSetParams resolves the unresolved error-set params of a set
// against the call-site bindings, unioning each bound set into the concrete
// component. Unbound params stay symbolic; a fully bound set comes out concrete.
func substituteErrorSetParams(errors *ErrorSetType, bindings map[string]Type) *ErrorSetType {
	if errors == nil || !errors.HasParams() {
		return errors
	}
	resolved := &ErrorSetType{
		Name:     errors.Name,
		Tags:     append([]string(nil), errors.Tags...),
		Payloads: errors.Payloads,
	}
	changed := false
	var residual []string
	for _, param := range errors.Params {
		if bound, ok := bindings[param]; ok {
			if set, isSet := bound.(*ErrorSetType); isSet && set != nil {
				resolved = UnionErrorSets(resolved, set)
				changed = true
				continue
			}
		}
		residual = append(residual, param)
	}
	if !changed {
		return errors
	}
	resolved.Params = residual
	if len(residual) != 0 || len(errors.Tags) != 0 {
		resolved.Name = errorSetNameWithParams(resolved.Name, len(resolved.Tags), residual)
	}
	return resolved
}
