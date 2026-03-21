package semantic

import (
	"fmt"
	"strings"
	"unicode"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func astAggregateStates(expr *ast.AggregateStateTypeExpr) []RefState {
	if expr == nil {
		return nil
	}
	if len(expr.States) != 0 {
		states := make([]RefState, len(expr.States))
		for i, state := range expr.States {
			states[i] = RefState(state)
		}
		return states
	}
	return []RefState{RefState(expr.State)}
}

func (a *Analyzer) errorLegacyBuiltinReplacement(pos lexer.Pos, oldName, replacement string) {
	a.errorf(pos, "legacy built-in %q has been replaced; use %q instead", oldName, replacement)
}

func (a *Analyzer) defineGlobal(sym *Symbol, pos lexer.Pos) {
	if existing, ok := a.globalScope.Define(sym); !ok {
		a.errorf(pos, "duplicate declaration %q (already defined as %s)", existing.Name, existing.Kind)
	}
}

func (a *Analyzer) defineLocal(sym *Symbol, pos lexer.Pos) {
	if a.currentScope == nil {
		return
	}
	if existing, ok := a.currentScope.Define(sym); !ok {
		a.errorf(pos, "duplicate local %q (already defined as %s)", existing.Name, existing.Kind)
		return
	}
	a.trackAffineValueSymbol(sym)
}

func (a *Analyzer) funcTypeFromDecl(name string, typeParams []string, refStorageParams []string, refStateParams []string, genericParams []ast.GenericParam, regionParams []string, permissionParams []string, permissionRefs []ast.PermissionRef, params []ast.ParamDecl, ret ast.TypeExpr, variadic bool) *FuncType {
	ptypes := make([]Type, 0, len(params))
	retType := a.namedTypes["void"]
	shapeParams := a.collectImplicitShapeParams(params, ret)
	var resolvedPermissionRefs []ast.PermissionRef
	var permissions []string
	a.withGenericParams(genericParams, nil, func() {
		a.withRegionParams(regionParams, func() {
			a.withPermissionParams(permissionParams, func() {
				resolvedPermissionRefs = a.resolvePermissionRefs(permissionRefs, true)
				permissions = a.resolvePermissionFamilies(permissionRefs, true)
				a.withShapeParams(shapeParams, func() {
					for _, p := range params {
						ptypes = append(ptypes, a.resolveType(p.Type))
					}
					if ret != nil {
						retType = a.resolveType(ret)
					}
				})
			})
		})
	})
	return &FuncType{
		Name:                   name,
		TypeParams:             append([]string(nil), typeParams...),
		RefStorageParams:       append([]string(nil), refStorageParams...),
		RefStateParams:         append([]string(nil), refStateParams...),
		RegionParams:           append([]string(nil), regionParams...),
		PermissionParams:       append([]string(nil), permissionParams...),
		GenericParams:          append([]ast.GenericParam(nil), genericParams...),
		UsedPermissionParams:   append([]string(nil), a.permissionParamsInRefs(permissionRefs)...),
		DeclaredPermissionRefs: append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
		DeclaredPermissions:    append([]string(nil), permissions...),
		PermissionRefs:         append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
		Permissions:            permissions,
		ShapeParams:            shapeParams,
		FreshReturnShapeParams: knownFreshReturnShapeParams(name, retType),
		Params:                 ptypes,
		Return:                 retType,
		Variadic:               variadic,
	}
}

func (a *Analyzer) resolveType(expr ast.TypeExpr) Type {
	switch n := expr.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "dstr":
			return &DStrType{Shape: &WildcardShape{}, SurfaceName: "dstr"}
		case "DStr":
			a.errorLegacyBuiltinReplacement(n.Pos(), "DStr", "dstr")
			return invalidType
		case "dstring":
			a.errorLegacyBuiltinReplacement(n.Pos(), "dstring", "dstr")
			return invalidType
		}
		if t, ok := a.lookupTypeParam(n.Name); ok {
			return t
		}
		if t, ok := a.namedTypes[n.Name]; ok {
			return DefaultAggregateStateType(t)
		}
		a.errorf(n.Pos(), "unknown type %q", n.Name)
		return invalidType
	case *ast.AggregateStateTypeExpr:
		baseType := StripAggregateStateType(a.resolveType(n.Base))
		count := AggregateStateParamCount(baseType)
		if count == 0 {
			a.errorf(n.Pos(), "type %q does not declare an aggregate state parameter", baseType.String())
			return invalidType
		}
		states := astAggregateStates(n)
		if len(states) != count {
			a.errorf(n.Pos(), "type %q expects %d aggregate state arguments, got %d", baseType.String(), count, len(states))
			return invalidType
		}
		return cloneAggregateStateWithBase(baseType, states)
	case *ast.RefStateLiteralTypeExpr:
		return &RefStateValueType{State: RefState(n.State)}
	case *ast.RefStorageLiteralTypeExpr:
		return &RefStorageValueType{Storage: RefStorage(n.Storage)}
	case *ast.ErrorSetExpr:
		return a.resolveErrorSetExpr(n)
	case *ast.ErrorUnionTypeExpr:
		valueType := a.resolveType(n.Value)
		errorType := a.resolveType(n.Errors)
		errSet, ok := errorType.(*ErrorSetType)
		if !ok {
			a.errorf(n.Pos(), "error union expects an error set on the right-hand side, got %s", errorType.String())
			return invalidType
		}
		return &ErrorUnionType{Value: valueType, Errors: errSet}
	case *ast.OptionalTypeExpr:
		valueType := a.resolveType(n.Value)
		if isVoidType(valueType) {
			a.errorf(n.Pos(), "value optionals cannot wrap void")
			return invalidType
		}
		if _, ok := valueType.(*RefType); ok {
			a.errorf(n.Pos(), "value optionals cannot wrap references; use &? instead of %s?", valueType.String())
			return invalidType
		}
		return &OptionalType{Value: valueType}
	case *ast.RefType:
		region := n.Region
		storageParam := n.StorageParam
		if storageParam != "" {
			if _, ok := a.lookupRefStorageParam(storageParam); ok {
				region = ""
			} else if a.regionQualifierDefined(storageParam) {
				region = storageParam
				storageParam = ""
			} else {
				a.errorf(n.Pos(), "unknown region qualifier %q", storageParam)
			}
		}
		stateParam := n.StateParam
		if stateParam != "" {
			if _, ok := a.lookupRefStateParam(stateParam); !ok {
				a.errorf(n.Pos(), "unknown refstate parameter %q", stateParam)
			}
		}
		if region != "" && !a.regionQualifierDefined(region) {
			a.errorf(n.Pos(), "unknown region qualifier %q", region)
		}
		elemType := a.resolveType(n.Elem)
		if a.containsAffineHandleValues(elemType, map[string]bool{}) && !isBorrowableAffineOwnerType(elemType) {
			a.errorf(n.Pos(), "references to values containing affine handles are not supported; got %s&", elemType.String())
		}
		return &RefType{Elem: elemType, State: RefState(n.State), StateParam: stateParam, Storage: RefStorage(n.Storage), StorageParam: storageParam, Region: region, ExplicitStorage: n.Explicit}
	case *ast.ArrayType:
		return a.resolveArrayType(n)
	case *ast.BuiltinTypeExpr:
		return a.resolveBuiltinSurfaceType(n)
	case *ast.FuncTypeExpr:
		ptypes := make([]Type, 0, len(n.Params))
		for _, param := range n.Params {
			ptypes = append(ptypes, a.resolveType(param))
		}
		retType := a.namedTypes["void"]
		if n.Return != nil {
			retType = a.resolveType(n.Return)
		}
		resolvedPermissionRefs := a.resolvePermissionRefs(n.Permissions, true)
		permissions := a.resolvePermissionFamilies(n.Permissions, true)
		return &FuncType{
			Name:                   "func",
			RefStorageParams:       nil,
			RefStateParams:         nil,
			UsedPermissionParams:   append([]string(nil), a.permissionParamsInRefs(n.Permissions)...),
			DeclaredPermissionRefs: append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
			DeclaredPermissions:    append([]string(nil), permissions...),
			PermissionRefs:         append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
			Permissions:            permissions,
			Params:                 ptypes,
			Return:                 retType,
			Variadic:               n.Variadic,
		}
	case *ast.MutableType:
		return a.resolveType(n.Elem)
	case *ast.TailType:
		elemType := a.resolveType(n.Elem)
		if a.containsAffineHandleValues(elemType, map[string]bool{}) && !isBorrowableAffineOwnerType(elemType) {
			a.errorf(n.Pos(), "references to values containing affine handles are not supported; got %s&", elemType.String())
		}
		return &RefType{Elem: elemType, State: RefStateNonNull, Storage: RefStorageAny}
	case *ast.GenericType:
		if shaped, ok := a.resolveDynamicShapeType(n); ok {
			return shaped
		}
		if arrayExpr, ok := a.genericTypeAsArrayType(n); ok {
			return a.resolveArrayType(arrayExpr)
		}
		args := make([]Type, 0, len(n.Args))
		base, ok := a.namedTypes[n.Name]
		if !ok {
			a.errorf(n.Pos(), "unknown type %q", n.Name)
			return invalidType
		}
		switch base := base.(type) {
		case *PackedEnumStoreType:
			if len(n.Args) != 1 {
				a.errorf(n.Pos(), "packed enum store type %q expects 1 state argument, got %d", n.Name, len(args))
				return invalidType
			}
			args = append(args, a.resolveType(n.Args[0]))
			return PackedEnumStoreWithState(base, args[0])
		case *StructType:
			params := genericParamsForStructType(base)
			if len(n.Args) != len(params) {
				a.errorf(n.Pos(), "type %q expects %d %s, got %d", n.Name, len(params), genericArgLabel(params), len(n.Args))
				return invalidType
			}
			for i, arg := range n.Args {
				args = append(args, a.resolveGenericArgForParam(arg, params[i]))
			}
			if base.Builtin && base.Name == "atomic" && len(args) == 1 {
				if !a.typeStructurallyAtomicSafe(args[0], map[string]bool{}) {
					a.errorf(n.Pos(), "atomic payload type must satisfy atomic_safe(T), got %s", args[0].String())
					return invalidType
				}
			}
			return DefaultAggregateStateType(&GenericInstanceType{Name: n.Name, Base: base, Args: args})
		case *OpaqueType:
			if len(n.Args) != 0 {
				a.errorf(n.Pos(), "type %q expects 0 type arguments, got %d", n.Name, len(n.Args))
				return invalidType
			}
			return &GenericInstanceType{Name: n.Name, Base: base, Args: args}
		default:
			a.errorf(n.Pos(), "type %q cannot be used with generic arguments", n.Name)
			return invalidType
		}
	default:
		return invalidType
	}
}

func (a *Analyzer) resolveErrorSetExpr(expr *ast.ErrorSetExpr) Type {
	if expr == nil {
		return invalidType
	}
	if len(expr.Tags) == 0 {
		a.errorf(expr.Pos(), "error[...] requires at least one qualified error tag")
		return invalidType
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		a.errorf(expr.Pos(), "error[Set.*, ...] is no longer supported; use error[Set, ...] or error[Set] instead")
		return invalidType
	}
	if containsWildcardErrorTag(expr.Tags) {
		if len(expr.Tags) != 1 {
			a.errorf(expr.Pos(), "error[Set.*] cannot be mixed with explicit tags")
			return invalidType
		}
		a.errorf(expr.Pos(), "error[Set.*] is no longer supported; use error[Set] instead")
		return invalidType
	}
	if len(expr.Tags) == 1 && expr.Tags[0].Tag == "" {
		_, errSet := a.lookupDeclaredErrorSet(expr.Tags[0])
		if errSet == nil {
			return invalidType
		}
		return errSet
	}
	if expr.HasEllipsis {
		return a.resolveExpandedErrorFamilies(expr)
	}

	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range expr.Tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		familySets[tag.SetName] = errSet
		if tag.Tag == "" {
			fullFamilies[tag.SetName] = true
			for _, qualifiedTag := range errSet.Tags {
				if prev, ok := seenTags[qualifiedTag]; ok {
					_, shortName := SplitErrorTagName(qualifiedTag)
					a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s", shortName, prev.SetName, prev.Tag, tag.SetName)
					return invalidType
				}
				seenTags[qualifiedTag] = ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: ErrorTagShortName(qualifiedTag)}
			}
			continue
		}
		qualifiedTag := QualifyErrorTag(tag.SetName, tag.Tag)
		if !errSet.HasTag(qualifiedTag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		if prev, ok := seenTags[qualifiedTag]; ok {
			a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s.%s", tag.Tag, prev.SetName, prev.Tag, tag.SetName, tag.Tag)
			return invalidType
		}
		seenTags[qualifiedTag] = tag
		if selectedTags[tag.SetName] == nil {
			selectedTags[tag.SetName] = map[string]bool{}
		}
		selectedTags[tag.SetName][qualifiedTag] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, selectedTags)
}

func (a *Analyzer) lookupDeclaredErrorSet(tag ast.ErrorTagExpr) (string, *ErrorSetType) {
	t, ok := a.namedTypes[tag.SetName]
	if !ok {
		a.errorf(tag.Position, "unknown error set %q", tag.SetName)
		return "", nil
	}
	errSet, ok := t.(*ErrorSetType)
	if !ok {
		a.errorf(tag.Position, "%q is not an error set", tag.SetName)
		return "", nil
	}
	return tag.SetName, errSet
}

func containsWildcardErrorTag(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "*" {
			return true
		}
	}
	return false
}

func containsBareFamily(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "" {
			return true
		}
	}
	return false
}

func (a *Analyzer) resolveExpandedErrorFamilies(expr *ast.ErrorSetExpr) Type {
	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	for _, tag := range expr.Tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		if tag.Tag != "" && !errSet.HasQualifiedTag(tag.SetName, tag.Tag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		familySets[tag.SetName] = errSet
		fullFamilies[tag.SetName] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, nil)
}

func onlyBareFamilies(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag != "" {
			return false
		}
	}
	return len(tags) > 0
}

func singleExplicitErrorFamily(tags []ast.ErrorTagExpr) (string, bool) {
	family := ""
	for _, tag := range tags {
		if tag.Tag == "" {
			return "", false
		}
		if family == "" {
			family = tag.SetName
			continue
		}
		if tag.SetName != family {
			return "", false
		}
	}
	return family, family != ""
}

func (a *Analyzer) resolveDynamicShapeType(expr *ast.GenericType) (Type, bool) {
	switch expr.Name {
	case "Dict":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "Dict", "dict")
		return invalidType, true
	case "view":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "view expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &ViewType{Elem: a.resolveType(expr.Args[0])}, true
	case "dview":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "dview expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DArrayViewType{Elem: a.resolveType(expr.Args[0]), SurfaceName: "dview"}, true
	case "DArray":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "DArray", "darray")
		return invalidType, true
	case "DArrayView":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "DArrayView", "dview")
		return invalidType, true
	case "DList":
		a.errorf(expr.Pos(), "DList has been removed from the language; use darray instead")
		return invalidType, true
	case "DListView":
		a.errorf(expr.Pos(), "DListView has been removed from the language; use dview instead")
		return invalidType, true
	case "DStr":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "DStr", "dstr")
		return invalidType, true
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveBuiltinSurfaceType(expr *ast.BuiltinTypeExpr) Type {
	switch expr.Name {
	case "array":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "array expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		resolved := a.resolveArrayType(&ast.ArrayType{Position: expr.Position, Elem: expr.TypeArgs[0], Size: expr.ValueArgs[0]})
		if arrayType, ok := resolved.(*ArrayType); ok {
			arrayType.SurfaceName = "array"
		}
		return resolved
	case "darray":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) > 1 {
			a.errorf(expr.Pos(), "darray expects 1 or 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		if len(expr.ValueArgs) == 0 {
			return &DArrayType{Elem: a.resolveType(expr.TypeArgs[0]), Shape: &WildcardShape{}, SurfaceName: "darray"}
		}
		return &DArrayType{Elem: a.resolveType(expr.TypeArgs[0]), Shape: a.resolveShapeExpr(expr.ValueArgs[0]), SurfaceName: "darray"}
	case "dict":
		if len(expr.TypeArgs) != 2 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "dict expects 2 type arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return a.resolveDictType(expr.TypeArgs[0], expr.TypeArgs[1], "dict")
	case "str":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "str expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		resolved := a.resolveArrayType(&ast.ArrayType{
			Position: expr.Position,
			Elem:     &ast.NamedType{Position: expr.Position, Name: "u8"},
			Size:     expr.ValueArgs[0],
		})
		if arrayType, ok := resolved.(*ArrayType); ok {
			arrayType.SurfaceName = "str"
		}
		return resolved
	case "string":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "string", "str")
		return invalidType
	case "dstr":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "dstr expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &DStrType{Shape: a.resolveShapeExpr(expr.ValueArgs[0]), SurfaceName: "dstr"}
	case "dstring":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "dstring", "dstr")
		return invalidType
	case "view":
		if len(expr.TypeArgs) != 1 {
			a.errorf(expr.Pos(), "view expects 1 type argument, got %d", len(expr.TypeArgs))
			return invalidType
		}
		viewType := &ViewType{Elem: a.resolveType(expr.TypeArgs[0])}
		if len(expr.ValueArgs) == 2 {
			viewType.Begin = a.exprSummary(expr.ValueArgs[0])
			viewType.End = a.exprSummary(expr.ValueArgs[1])
		} else if len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "view expects either 1 or 3 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return viewType
	case "dview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "dview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &DArrayViewType{Elem: a.resolveType(expr.TypeArgs[0]), SurfaceName: "dview"}
	case "sview":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 2 {
			a.errorf(expr.Pos(), "sview expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &SViewType{Begin: a.exprSummary(expr.ValueArgs[0]), End: a.exprSummary(expr.ValueArgs[1])}
	default:
		a.errorf(expr.Pos(), "unknown built-in type %q", expr.Name)
		return invalidType
	}
}

func (a *Analyzer) resolveDictType(keyExpr ast.TypeExpr, valueExpr ast.TypeExpr, surfaceName string) Type {
	keyType := a.resolveType(keyExpr)
	valueType := a.resolveType(valueExpr)
	if IsInvalidType(keyType) || IsInvalidType(valueType) {
		return invalidType
	}
	if !isDictRuntimeKeyType(keyType) {
		a.errorf(keyExpr.Pos(), "dict currently only supports dstr keys in the first runtime-backed slice, got %s", keyType.String())
		return invalidType
	}
	return &DictType{Key: keyType, Value: valueType, SurfaceName: surfaceName}
}

func (a *Analyzer) resolveShapeArg(expr ast.TypeExpr) Shape {
	name, ok := shapeNameFromTypeExpr(expr)
	if !ok {
		a.errorf(expr.Pos(), "shape witness must be an identifier")
		return &NamedShape{Name: "?"}
	}
	if shape, ok := a.lookupShapeParam(name); ok {
		return shape
	}
	return &NamedShape{Name: name}
}

func (a *Analyzer) resolveShapeExpr(expr ast.Expr) Shape {
	name, ok := shapeNameFromValueExpr(expr)
	if !ok {
		return &NamedShape{Name: a.exprSummary(expr)}
	}
	if shape, ok := a.lookupShapeParam(name); ok {
		return shape
	}
	return &NamedShape{Name: name}
}

func (a *Analyzer) genericTypeAsArrayType(expr *ast.GenericType) (*ast.ArrayType, bool) {
	if len(expr.Args) != 1 {
		return nil, false
	}
	base, ok := a.namedTypes[expr.Name]
	if !ok {
		return nil, false
	}
	switch base.(type) {
	case *StructType, *OpaqueType, *PackedEnumStoreType:
		return nil, false
	}
	sizeTypeExpr, ok := expr.Args[0].(*ast.NamedType)
	if !ok {
		return nil, false
	}
	return &ast.ArrayType{
		Position: expr.Position,
		Elem:     &ast.NamedType{Position: expr.Position, Name: expr.Name},
		Size:     &ast.Ident{Position: sizeTypeExpr.Position, Name: sizeTypeExpr.Name},
	}, true
}

func genericParamsForStructType(base *StructType) []ast.GenericParam {
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams))
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	return params
}

func genericParamsForFuncType(base *FuncType) []ast.GenericParam {
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams))
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	return params
}

func genericArgLabel(params []ast.GenericParam) string {
	if len(params) == 0 {
		return "generic arguments"
	}
	for _, param := range params {
		if param.Kind != ast.GenericParamType {
			return "generic arguments"
		}
	}
	return "type arguments"
}

func genericBindingsForParams(params []ast.GenericParam, args []Type) map[string]Type {
	if len(params) == 0 || len(args) == 0 {
		return nil
	}
	bindings := make(map[string]Type, len(params))
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

func genericBindingsForStructInstance(base *StructType, args []Type) map[string]Type {
	return genericBindingsForParams(genericParamsForStructType(base), args)
}

func (a *Analyzer) resolveGenericArgForParam(expr ast.TypeExpr, param ast.GenericParam) Type {
	switch param.Kind {
	case ast.GenericParamRefStorage:
		switch n := expr.(type) {
		case *ast.NamedType:
			if t, ok := a.lookupRefStorageParam(n.Name); ok {
				return t
			}
		}
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStorageParamType); ok {
			return resolved
		}
		if _, ok := resolved.(*RefStorageValueType); ok {
			return resolved
		}
		a.errorf(expr.Pos(), "generic argument %q for refstorage parameter %q must be a refstorage literal or parameter", resolved.String(), param.Name)
		return invalidType
	case ast.GenericParamRefState:
		switch n := expr.(type) {
		case *ast.NamedType:
			if t, ok := a.lookupRefStateParam(n.Name); ok {
				return t
			}
		}
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStateParamType); ok {
			return resolved
		}
		if _, ok := resolved.(*RefStateValueType); ok {
			return resolved
		}
		a.errorf(expr.Pos(), "generic argument %q for refstate parameter %q must be a refstate literal or parameter", resolved.String(), param.Name)
		return invalidType
	default:
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStorageParamType); ok {
			a.errorf(expr.Pos(), "refstorage parameter %q cannot be used as a type argument", resolved.String())
			return invalidType
		}
		if _, ok := resolved.(*RefStorageValueType); ok {
			a.errorf(expr.Pos(), "refstorage literal %q cannot be used as a type argument", resolved.String())
			return invalidType
		}
		if _, ok := resolved.(*RefStateParamType); ok {
			a.errorf(expr.Pos(), "refstate parameter %q cannot be used as a type argument", resolved.String())
			return invalidType
		}
		if _, ok := resolved.(*RefStateValueType); ok {
			a.errorf(expr.Pos(), "refstate literal %q cannot be used as a type argument", resolved.String())
			return invalidType
		}
		return resolved
	}
}

func (a *Analyzer) inferLiteralType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				return t
			}
			if n.Suffix == "u" {
				return a.namedTypes["usize"]
			}
		}
		return a.namedTypes["int"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	case *ast.BoolLit:
		return a.namedTypes["bool"]
	case *ast.NullLit:
		return nullType
	default:
		return invalidType
	}
}

func (a *Analyzer) validCast(src, dst Type) bool {
	if SameType(src, dst) || IsInvalidType(src) || IsInvalidType(dst) {
		return true
	}
	if _, ok := src.(*ConstEnumType); ok {
		return SameType(src, dst) || IsNumericType(dst)
	}
	if _, ok := dst.(*ConstEnumType); ok {
		return IsNumericType(src)
	}
	if _, ok := src.(*TypeParamType); ok {
		return true
	}
	if _, ok := dst.(*TypeParamType); ok {
		return true
	}
	if IsNumericType(src) && IsNumericType(dst) {
		return true
	}
	if IsNullType(src) {
		if ref, ok := dst.(*RefType); ok {
			return ref.State != RefStateNonNull
		}
		if _, ok := dst.(*OptionalType); ok {
			return true
		}
	}
	if srcRef, ok := src.(*RefType); ok {
		if dstRef, ok := dst.(*RefType); ok {
			return refStateAssignable(dstRef.State, srcRef.State) && refRegionAssignable(dstRef.Region, srcRef.Region)
		}
	}
	if isPointerLikeCastType(src) && isPointerLikeCastType(dst) {
		return true
	}
	if isPointerLikeCastType(src) && IsNumericType(dst) {
		return true
	}
	if IsNumericType(src) && isPointerLikeCastType(dst) {
		return true
	}
	return false
}

func isPointerLikeCastType(t Type) bool {
	switch t.(type) {
	case *RefType, *DStrType, *FuncType:
		return true
	default:
		return false
	}
}

func (a *Analyzer) regionQualifierDefined(name string) bool {
	if name == "" {
		return false
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(name); ok && sym.Kind == SymbolRegion {
			return true
		}
	}
	return a.lookupRegionParam(name)
}

func (a *Analyzer) exprSummary(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Value
	case *ast.Ident:
		return n.Name
	case *ast.BinaryExpr:
		return fmt.Sprintf("%s%s%s", a.exprSummary(n.Left), lexer.TokenName(n.Op), a.exprSummary(n.Right))
	default:
		return "?"
	}
}

func (a *Analyzer) withTypeParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &TypeParamType{Name: name}
		}
	}
	a.typeParamScopes = append(a.typeParamScopes, bindings)
	fn()
	a.typeParamScopes = a.typeParamScopes[:len(a.typeParamScopes)-1]
}

func (a *Analyzer) withRefStorageParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &RefStorageParamType{Name: name}
		}
	}
	a.refStorageParamScopes = append(a.refStorageParamScopes, bindings)
	fn()
	a.refStorageParamScopes = a.refStorageParamScopes[:len(a.refStorageParamScopes)-1]
}

func (a *Analyzer) withRefStateParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &RefStateParamType{Name: name}
		}
	}
	a.refStateParamScopes = append(a.refStateParamScopes, bindings)
	fn()
	a.refStateParamScopes = a.refStateParamScopes[:len(a.refStateParamScopes)-1]
}

func (a *Analyzer) withGenericParams(params []ast.GenericParam, args []Type, fn func()) {
	if len(params) == 0 {
		fn()
		return
	}
	typeNames := make([]string, 0)
	typeArgs := make([]Type, 0)
	refStorageNames := make([]string, 0)
	refStorageArgs := make([]Type, 0)
	refStateNames := make([]string, 0)
	refStateArgs := make([]Type, 0)
	for i, param := range params {
		var arg Type
		if i < len(args) {
			arg = args[i]
		}
		switch param.Kind {
		case ast.GenericParamType:
			typeNames = append(typeNames, param.Name)
			typeArgs = append(typeArgs, arg)
		case ast.GenericParamRefStorage:
			refStorageNames = append(refStorageNames, param.Name)
			refStorageArgs = append(refStorageArgs, arg)
		case ast.GenericParamRefState:
			refStateNames = append(refStateNames, param.Name)
			refStateArgs = append(refStateArgs, arg)
		}
	}
	a.withTypeParams(typeNames, typeArgs, func() {
		a.withRefStorageParams(refStorageNames, refStorageArgs, func() {
			a.withRefStateParams(refStateNames, refStateArgs, fn)
		})
	})
}

func (a *Analyzer) lookupTypeParam(name string) (Type, bool) {
	for i := len(a.typeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.typeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupRefStorageParam(name string) (Type, bool) {
	for i := len(a.refStorageParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.refStorageParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupRefStateParam(name string) (Type, bool) {
	for i := len(a.refStateParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.refStateParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupShapeParam(name string) (Shape, bool) {
	for i := len(a.shapeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.shapeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) withPermissionParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]bool, len(names))
	for _, name := range names {
		bindings[name] = true
	}
	a.permissionParamScopes = append(a.permissionParamScopes, bindings)
	fn()
	a.permissionParamScopes = a.permissionParamScopes[:len(a.permissionParamScopes)-1]
}

func (a *Analyzer) lookupPermissionParam(name string) bool {
	for i := len(a.permissionParamScopes) - 1; i >= 0; i-- {
		if a.permissionParamScopes[i][name] {
			return true
		}
	}
	return false
}

func (a *Analyzer) permissionParamsInRefs(refs []ast.PermissionRef) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Member != "" || !a.lookupPermissionParam(ref.Name) || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		out = append(out, ref.Name)
	}
	return out
}

func (a *Analyzer) withRegionParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]bool, len(names))
	for _, name := range names {
		bindings[name] = true
	}
	a.regionParamScopes = append(a.regionParamScopes, bindings)
	fn()
	a.regionParamScopes = a.regionParamScopes[:len(a.regionParamScopes)-1]
}

func (a *Analyzer) lookupRegionParam(name string) bool {
	for i := len(a.regionParamScopes) - 1; i >= 0; i-- {
		if a.regionParamScopes[i][name] {
			return true
		}
	}
	return false
}

func (a *Analyzer) substituteType(t Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef) Type {
	switch n := t.(type) {
	case *TypeParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *RefStorageParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *RefStateParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *ErrorUnionType:
		return &ErrorUnionType{Value: a.substituteType(n.Value, bindings, shapeBindings, regionBindings, permissionBindings), Errors: n.Errors}
	case *OptionalType:
		return &OptionalType{Value: a.substituteType(n.Value, bindings, shapeBindings, regionBindings, permissionBindings)}
	case *RefType:
		region := n.Region
		if bound, ok := regionBindings[n.Region]; ok {
			region = bound
		}
		state := n.State
		stateParam := n.StateParam
		if stateParam != "" {
			if resolved, ok := bindings[stateParam]; ok {
				switch resolved := resolved.(type) {
				case *RefStateValueType:
					state = resolved.State
					stateParam = ""
				case *RefStateParamType:
					stateParam = resolved.Name
				}
			}
		}
		storage := n.Storage
		storageParam := n.StorageParam
		if storageParam != "" {
			if resolved, ok := bindings[storageParam]; ok {
				switch resolved := resolved.(type) {
				case *RefStorageValueType:
					storage = resolved.Storage
					storageParam = ""
				case *RefStorageParamType:
					storageParam = resolved.Name
				}
			}
		}
		return &RefType{Elem: a.substituteType(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings), State: state, StateParam: stateParam, Storage: storage, StorageParam: storageParam, Region: region, ExplicitStorage: n.ExplicitStorage}
	case *ArrayType:
		return &ArrayType{Elem: a.substituteType(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings), Size: n.Size, HasConstSize: n.HasConstSize, ConstSize: n.ConstSize, SurfaceName: n.SurfaceName}
	case *DArrayType:
		return &DArrayType{Elem: a.substituteType(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings), Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName}
	case *ViewType:
		return &ViewType{Elem: a.substituteType(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings), Begin: n.Begin, End: n.End}
	case *DArrayViewType:
		return &DArrayViewType{Elem: a.substituteType(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings), Begin: n.Begin, End: n.End, SurfaceName: n.SurfaceName}
	case *DStrType:
		return &DStrType{Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName}
	case *DictType:
		return &DictType{Key: a.substituteType(n.Key, bindings, shapeBindings, regionBindings, permissionBindings), Value: a.substituteType(n.Value, bindings, shapeBindings, regionBindings, permissionBindings), SurfaceName: n.SurfaceName}
	case *SViewType:
		return &SViewType{Begin: n.Begin, End: n.End}
	case *GenericInstanceType:
		args := make([]Type, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, a.substituteType(arg, bindings, shapeBindings, regionBindings, permissionBindings))
		}
		return &GenericInstanceType{Name: n.Name, Base: n.Base, Args: args}
	case *AggregateStateType:
		return cloneAggregateStateWithBase(a.substituteType(n.Base, bindings, shapeBindings, regionBindings, permissionBindings), aggregateStateStates(n))
	case *PackedEnumStoreType:
		return PackedEnumStoreWithState(n, a.substituteType(n.State, bindings, shapeBindings, regionBindings, permissionBindings))
	case *FuncType:
		params := make([]Type, 0, len(n.Params))
		for _, param := range n.Params {
			params = append(params, a.substituteType(param, bindings, shapeBindings, regionBindings, permissionBindings))
		}
		declaredRefs, refs, usedPermissionParams := substitutePermissionRefs(n.DeclaredPermissionRefs, n.PermissionRefs, n.UsedPermissionParams, permissionBindings)
		return &FuncType{Name: n.Name, TypeParams: append([]string(nil), n.TypeParams...), RefStorageParams: append([]string(nil), n.RefStorageParams...), RefStateParams: append([]string(nil), n.RefStateParams...), RegionParams: append([]string(nil), n.RegionParams...), PermissionParams: append([]string(nil), n.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), n.GenericParams...), UsedPermissionParams: usedPermissionParams, DeclaredPermissionRefs: declaredRefs, DeclaredPermissions: permissionFamiliesFromRefs(declaredRefs), PermissionRefs: refs, Permissions: permissionFamiliesFromRefs(refs), ShapeParams: append([]string(nil), n.ShapeParams...), FreshReturnShapeParams: append([]string(nil), n.FreshReturnShapeParams...), Params: params, Return: a.substituteType(n.Return, bindings, shapeBindings, regionBindings, permissionBindings), Variadic: n.Variadic, ReturnProvenance: cloneRegionRefState(n.ReturnProvenance), ReturnProvenanceKnown: n.ReturnProvenanceKnown, ReturnBorrowedOwnerRefs: cloneBorrowedOwnerRefSummary(n.ReturnBorrowedOwnerRefs), ReturnBorrowedOwnerRefsKnown: n.ReturnBorrowedOwnerRefsKnown}
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

func (a *Analyzer) paramIsMutable(param ast.ParamDecl) bool {
	if param.Mutable {
		return true
	}
	_, ok := param.Type.(*ast.MutableType)
	return ok
}

func (a *Analyzer) withShapeParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Shape, len(names))
	for _, name := range names {
		bindings[name] = &ShapeParam{Name: name}
	}
	a.shapeParamScopes = append(a.shapeParamScopes, bindings)
	fn()
	a.shapeParamScopes = a.shapeParamScopes[:len(a.shapeParamScopes)-1]
}

func (a *Analyzer) collectImplicitShapeParams(params []ast.ParamDecl, ret ast.TypeExpr) []string {
	seen := map[string]bool{}
	order := make([]string, 0)
	for _, param := range params {
		a.collectImplicitShapeParamsFromType(param.Type, seen, &order)
	}
	if ret != nil {
		a.collectImplicitShapeParamsFromType(ret, seen, &order)
	}
	return order
}

func (a *Analyzer) collectImplicitShapeParamsFromType(expr ast.TypeExpr, seen map[string]bool, order *[]string) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ErrorUnionTypeExpr:
		a.collectImplicitShapeParamsFromType(n.Value, seen, order)
		a.collectImplicitShapeParamsFromType(n.Errors, seen, order)
	case *ast.ErrorSetExpr:
		return
	case *ast.RefType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.MutableType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.TailType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.ArrayType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.BuiltinTypeExpr:
		switch n.Name {
		case "array", "view", "dview":
			for _, arg := range n.TypeArgs {
				a.collectImplicitShapeParamsFromType(arg, seen, order)
			}
		case "dict":
			for _, arg := range n.TypeArgs {
				a.collectImplicitShapeParamsFromType(arg, seen, order)
			}
		case "darray":
			if len(n.TypeArgs) > 0 {
				a.collectImplicitShapeParamsFromType(n.TypeArgs[0], seen, order)
			}
			if len(n.ValueArgs) > 0 {
				if name, ok := shapeNameFromValueExpr(n.ValueArgs[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		case "dstr":
			if len(n.ValueArgs) > 0 {
				if name, ok := shapeNameFromValueExpr(n.ValueArgs[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		}
	case *ast.GenericType:
		for _, arg := range n.Args {
			a.collectImplicitShapeParamsFromType(arg, seen, order)
		}
	}
}

func shapeNameFromTypeExpr(expr ast.TypeExpr) (string, bool) {
	name, ok := expr.(*ast.NamedType)
	if !ok {
		return "", false
	}
	return name.Name, true
}

func shapeNameFromValueExpr(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func isImplicitShapeWitnessName(name string) bool {
	if strings.HasPrefix(name, "shape_") || strings.HasPrefix(name, "s_") {
		return true
	}
	runes := []rune(name)
	if len(runes) != 1 {
		return false
	}
	return unicode.In(runes[0], unicode.Greek)
}
