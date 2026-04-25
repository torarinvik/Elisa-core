package semantic

import (
	"fmt"
	"strconv"
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
		a.errorf(pos, "%s", DuplicateDeclarationMessage(existing.Name, existing.Kind))
	}
}

func (a *Analyzer) defineLocal(sym *Symbol, pos lexer.Pos) {
	if a.currentScope == nil {
		return
	}
	if existing, ok := a.currentScope.Define(sym); !ok {
		a.errorf(pos, "%s", DuplicateLocalMessage(existing.Name, existing.Kind))
		return
	}
	a.trackAffineValueSymbol(sym)
	a.recordSpecializedValueTypeBinding(sym, sym.Type)
}

func (a *Analyzer) defineLocalInScope(scope *Scope, sym *Symbol, pos lexer.Pos) {
	if scope == nil {
		return
	}
	if existing, ok := scope.Define(sym); !ok {
		a.errorf(pos, "%s", DuplicateLocalMessage(existing.Name, existing.Kind))
		return
	}
	a.trackAffineValueSymbol(sym)
	a.recordSpecializedValueTypeBinding(sym, sym.Type)
}

func (a *Analyzer) funcTypeFromDecl(name string, typeParams []string, refStorageParams []string, refStateParams []string, genericParams []ast.GenericParam, regionParams []string, permissionParams []string, effectAliasPos lexer.Pos, effectAlias string, effects []ast.SignatureEffectItem, permissionRefs []ast.PermissionRef, ensures []ast.EnsuresClause, params []ast.ParamDecl, paramPacks []ast.ParamPackUse, paramItemOrder []ast.ParamSigItem, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, ret ast.TypeExpr, variadic bool) *FuncType {
	resolvedGenericParams := append([]ast.GenericParam(nil), genericParams...)
	for i, param := range resolvedGenericParams {
		if param.Kind != ast.GenericParamType || param.InterfaceBound == "" {
			continue
		}
		if iface, _, ok := a.lookupVisibleStaticInterface(param.InterfaceBound); ok && iface != nil {
			resolvedGenericParams[i].InterfaceBound = iface.Name
		}
	}
	explicitSpecs := a.expandExplicitParamSpecs(params, paramPacks, paramItemOrder, name)
	expandedExplicitParams := explicitParamDeclsFromSpecs(explicitSpecs)
	expandedImplicitParams, implicitNames := a.expandImplicitParamDecls(expandedExplicitParams, implicitParams, implicitBundles, implicitItemOrder, name)
	allParams := append(append([]ast.ParamDecl(nil), expandedExplicitParams...), expandedImplicitParams...)
	explicitNames := make([]string, 0, len(expandedExplicitParams))
	for _, p := range expandedExplicitParams {
		explicitNames = append(explicitNames, p.Name)
	}
	ptypes := make([]Type, 0, len(allParams))
	retType := a.namedTypes["void"]
	shapeParams := a.collectImplicitShapeParams(allParams, ret)
	var resolvedPermissionRefs []ast.PermissionRef
	var permissions []string
	var poststates []FuncPoststate
	defaultExprs := make([]ast.Expr, len(expandedExplicitParams))
	hasDefaults := make([]bool, len(expandedExplicitParams))
	a.withGenericParams(resolvedGenericParams, nil, func() {
		a.withRegionParams(regionParams, func() {
			a.withPermissionParams(permissionParams, func() {
				ret, permissionRefs = a.expandSignatureEffects(ret, permissionRefs, effectAlias, effectAliasPos, effects)
				resolvedPermissionRefs = a.resolvePermissionRefs(permissionRefs, true)
				permissions = a.resolvePermissionFamilies(permissionRefs, true)
				a.withShapeParams(shapeParams, func() {
					for _, spec := range explicitSpecs {
						if spec.HasResolvedType {
							ptypes = append(ptypes, spec.ResolvedType)
							continue
						}
						ptypes = append(ptypes, a.resolveType(spec.Decl.Type))
					}
					for _, p := range expandedImplicitParams {
						ptypes = append(ptypes, a.resolveType(p.Type))
					}
					if ret != nil {
						retType = a.resolveType(ret)
					}
					poststates = a.resolveFuncPoststates(name, allParams, ptypes, retType, ensures)
					defaultExprs, hasDefaults = a.validateExpandedFuncParamDefaults(name, explicitSpecs, ptypes[:len(expandedExplicitParams)])
				})
			})
		})
	})
	return &FuncType{
		Name:                      name,
		TypeParams:                append([]string(nil), typeParams...),
		RefStorageParams:          append([]string(nil), refStorageParams...),
		RefStateParams:            append([]string(nil), refStateParams...),
		RegionParams:              append([]string(nil), regionParams...),
		PermissionParams:          append([]string(nil), permissionParams...),
		GenericParams:             append([]ast.GenericParam(nil), resolvedGenericParams...),
		UsedPermissionParams:      append([]string(nil), a.permissionParamsInRefs(permissionRefs)...),
		DeclaredPermissionRefs:    append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
		DeclaredPermissions:       append([]string(nil), permissions...),
		PermissionRefs:            append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
		Permissions:               permissions,
		ShapeParams:               shapeParams,
		FreshReturnShapeParams:    knownFreshReturnShapeParams(name, retType),
		InlineMode:                FuncInlineModeDefault,
		HasNoRecurse:              false,
		TemperatureMode:           FuncTemperatureModeDefault,
		Poststates:                poststates,
		Params:                    ptypes,
		ExplicitParamCount:        len(expandedExplicitParams),
		ExplicitParamNames:        explicitNames,
		ExplicitParamDefaultExprs: append([]ast.Expr(nil), defaultExprs...),
		ExplicitParamHasDefault:   append([]bool(nil), hasDefaults...),
		ImplicitParamNames:        implicitNames,
		Return:                    retType,
		Variadic:                  variadic,
	}
}

func ensuresClauseSteps(clause ast.EnsuresClause) []borrowReturnAnnotationStep {
	if len(clause.Target.Fields) == 0 {
		return nil
	}
	steps := make([]borrowReturnAnnotationStep, len(clause.Target.Fields))
	for i, field := range clause.Target.Fields {
		steps[i] = borrowReturnAnnotationStep{Field: field}
	}
	return steps
}

func poststatePathKey(paramIndex int, steps []borrowReturnAnnotationStep) string {
	if len(steps) == 0 {
		return strconv.Itoa(paramIndex)
	}
	parts := make([]string, 0, len(steps)+1)
	parts = append(parts, strconv.Itoa(paramIndex))
	for _, step := range steps {
		switch {
		case step.Field != "":
			parts = append(parts, "."+step.Field)
		case step.Wildcard:
			parts = append(parts, "[*]")
		case step.Index != nil:
			parts = append(parts, "["+strconv.FormatInt(*step.Index, 10)+"]")
		default:
			parts = append(parts, "<?>")
		}
	}
	return strings.Join(parts, "")
}

func funcPoststateConditionFromAST(condition ast.EnsuresCondition) FuncPoststateCondition {
	switch condition.Kind {
	case ast.EnsuresConditionReturnBool:
		return FuncPoststateCondition{Kind: FuncPoststateConditionReturnBool, ReturnBool: condition.ReturnBool}
	default:
		return FuncPoststateCondition{Kind: FuncPoststateConditionAlways}
	}
}

func funcPoststateConditionKey(condition FuncPoststateCondition) string {
	switch condition.Kind {
	case FuncPoststateConditionReturnBool:
		if condition.ReturnBool {
			return "return:true"
		}
		return "return:false"
	default:
		return "always"
	}
}

func funcPoststateConditionLabel(condition FuncPoststateCondition) string {
	switch condition.Kind {
	case FuncPoststateConditionReturnBool:
		if condition.ReturnBool {
			return "return true"
		}
		return "return false"
	default:
		return "always"
	}
}

func funcPoststateConditionsOverlap(left FuncPoststateCondition, right FuncPoststateCondition) bool {
	if left.Kind == FuncPoststateConditionAlways || right.Kind == FuncPoststateConditionAlways {
		return true
	}
	if left.Kind != right.Kind {
		return false
	}
	return left.ReturnBool == right.ReturnBool
}

func poststateNamedStateTargetBase(t Type) (*StructType, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return poststateNamedStateTargetBase(tt.Base)
	case *RefType:
		return poststateNamedStateTargetBase(tt.Elem)
	default:
		return namedStateStructBase(t)
	}
}

func poststateRefTargetType(t Type) (*RefType, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return poststateRefTargetType(tt.Base)
	case *RefType:
		return tt, tt != nil
	default:
		return nil, false
	}
}

func poststateTargetTrackable(t Type) bool {
	if _, ok := poststateNamedStateTargetBase(t); ok {
		return true
	}
	_, ok := poststateRefTargetType(t)
	return ok
}

func (a *Analyzer) projectFuncPoststateTargetType(current Type, step borrowReturnAnnotationStep) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		return a.projectFuncPoststateTargetType(tt.Base, step)
	case *RefType:
		return a.projectFuncPoststateTargetType(tt.Elem, step)
	}
	if step.Field == "" {
		return nil, false
	}
	if fieldType, ok := a.lookupResolvedFieldType(current, step.Field); ok {
		return fieldType, true
	}
	return nil, false
}

func (a *Analyzer) resolveFuncPoststates(name string, params []ast.ParamDecl, paramTypes []Type, returnType Type, ensures []ast.EnsuresClause) []FuncPoststate {
	if len(ensures) == 0 {
		return nil
	}
	resolved := make([]FuncPoststate, 0, len(ensures))
	type seenPoststateTarget struct {
		Condition FuncPoststateCondition
		Position  lexer.Pos
	}
	seenTargets := make(map[string][]seenPoststateTarget, len(ensures))
	for _, clause := range ensures {
		condition := funcPoststateConditionFromAST(clause.Condition)
		if condition.Kind == FuncPoststateConditionReturnBool && !IsBoolType(returnType) {
			a.errorf(clause.Position, "ensures %s on function %q requires a bool return type, got %s", funcPoststateConditionLabel(condition), name, returnType)
			continue
		}
		paramIndex := -1
		for i, param := range params {
			if param.Name == clause.Target.Root {
				paramIndex = i
				break
			}
		}
		if paramIndex < 0 {
			a.errorf(clause.Position, "ensures on function %q references unknown parameter %q", name, clause.Target.Root)
			continue
		}
		if paramIndex >= len(paramTypes) {
			continue
		}
		steps := ensuresClauseSteps(clause)
		key := poststatePathKey(paramIndex, steps)
		targetName := clause.Target.Root + borrowAnnotationPathSuffix(steps)
		overlapped := false
		for _, seen := range seenTargets[key] {
			if !funcPoststateConditionsOverlap(seen.Condition, condition) {
				continue
			}
			a.errorf(clause.Position, "overlapping ensures target %q for %s on function %q (first seen at %s:%d:%d as %s)", targetName, funcPoststateConditionLabel(condition), name, seen.Position.File, seen.Position.Line, seen.Position.Col, funcPoststateConditionLabel(seen.Condition))
			overlapped = true
			break
		}
		if overlapped {
			continue
		}
		seenTargets[key] = append(seenTargets[key], seenPoststateTarget{Condition: condition, Position: clause.Position})
		targetType := paramTypes[paramIndex]
		ok := true
		for _, step := range steps {
			targetType, ok = a.projectFuncPoststateTargetType(targetType, step)
			if !ok {
				a.errorf(clause.Position, "ensures on function %q references unknown target path %q", name, clause.Target.Root+borrowAnnotationPathSuffix(steps))
				break
			}
		}
		if !ok {
			continue
		}
		poststate := FuncPoststate{Position: clause.Position, Condition: condition, ParamIndex: paramIndex, Path: steps}
		switch clause.Kind {
		case ast.EnsuresKindNamedState:
			base, ok := poststateNamedStateTargetBase(targetType)
			if !ok || base == nil {
				a.errorf(clause.Position, "ensures on function %q requires %q to resolve to a named-state-bearing target, got %s", name, clause.Target.Root+borrowAnnotationPathSuffix(steps), targetType)
				continue
			}
			seenCases := map[string]bool{}
			stateCases := make([]string, 0, len(clause.StateCases))
			valid := true
			for _, stateCase := range clause.StateCases {
				if seenCases[stateCase] {
					continue
				}
				seenCases[stateCase] = true
				allowed := false
				for _, candidate := range base.NamedStateCases {
					if candidate == stateCase {
						allowed = true
						break
					}
				}
				if !allowed {
					a.errorf(clause.Position, "ensures on function %q uses unknown state %q for %q", name, stateCase, clause.Target.Root+borrowAnnotationPathSuffix(steps))
					valid = false
					break
				}
				stateCases = append(stateCases, stateCase)
			}
			if !valid || len(stateCases) == 0 {
				continue
			}
			poststate.Kind = FuncPoststateKindNamedState
			poststate.StateCases = canonicalizeNamedStateCases(base.NamedStateCases, stateCases)
		case ast.EnsuresKindRefState:
			if _, ok := poststateRefTargetType(targetType); !ok {
				a.errorf(clause.Position, "ensures on function %q requires %q to resolve to a ref target for refstate effects, got %s", name, clause.Target.Root+borrowAnnotationPathSuffix(steps), targetType)
				continue
			}
			poststate.Kind = FuncPoststateKindRefState
			poststate.RefState = RefState(clause.RefState)
		case ast.EnsuresKindPreserve:
			if !poststateTargetTrackable(targetType) {
				a.errorf(clause.Position, "ensures on function %q requires %q to resolve to a trackable poststate target, got %s", name, clause.Target.Root+borrowAnnotationPathSuffix(steps), targetType)
				continue
			}
			poststate.Kind = FuncPoststateKindPreserve
		default:
			a.errorf(clause.Position, "ensures on function %q uses an unsupported poststate effect", name)
			continue
		}
		resolved = append(resolved, poststate)
	}
	return resolved
}

func (a *Analyzer) resolveType(expr ast.TypeExpr) Type {
	switch n := expr.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "dstr":
			return &DStrType{Shape: &WildcardShape{}, SurfaceName: "dstr"}
		case "sview":
			return &SViewType{}
		case "DStr":
			a.errorLegacyBuiltinReplacement(n.Pos(), "DStr", "dstr")
			return invalidType
		case "dstring":
			a.errorLegacyBuiltinReplacement(n.Pos(), "dstring", "dstr")
			return invalidType
		}
		a.maybeRejectRuntimeCarrierTypeUse(n.Pos(), n.Name)
		if t, ok := a.lookupTypeParam(n.Name); ok {
			return t
		}
		if t, ok := a.lookupInterfaceAssocType(n.Name); ok {
			return t
		}
		if t, _, ok := a.lookupVisibleType(n.Name); ok {
			return DefaultStatefulType(t)
		}
		if t, ok := a.resolveProjectedAssociatedType(n); ok {
			return t
		}
		if t, ok := a.resolveNamedVariantWitnessType(n); ok {
			return t
		}
		a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.Name))
		return invalidType
	case *ast.StateSetTypeExpr:
		a.errorf(n.Pos(), "state unions like %q are only valid as named struct state arguments", strings.Join(n.Cases, " | "))
		return invalidType
	case *ast.AggregateStateTypeExpr:
		baseType := StripAggregateStateType(a.resolveType(n.Base))
		count := AggregateStateParamCount(baseType)
		if count == 0 {
			a.errorf(n.Pos(), "type %q does not declare an aggregate state parameter", baseType)
			return invalidType
		}
		states := astAggregateStates(n)
		if len(states) != count {
			a.errorf(n.Pos(), "type %q expects %d aggregate state arguments, got %d", baseType, count, len(states))
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
			a.errorf(n.Pos(), "error union expects an error set on the right-hand side, got %s", errorType)
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
			a.errorf(n.Pos(), "value optionals cannot wrap references; use &? instead of %s?", valueType)
			return invalidType
		}
		return &OptionalType{Value: valueType}
	case *ast.TupleTypeExpr:
		fields := make([]TupleField, 0, len(n.Fields))
		for _, field := range n.Fields {
			fields = append(fields, TupleField{Name: field.Name, Type: a.resolveType(field.Type)})
		}
		return &TupleType{Fields: fields}
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
			a.errorf(n.Pos(), "references to values containing affine handles are not supported; got %s&", elemType)
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
		expandedImplicitParams, implicitNames := a.expandImplicitParamDecls(nil, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, "func")
		for _, param := range expandedImplicitParams {
			ptypes = append(ptypes, a.resolveType(param.Type))
		}
		retExpr, permissionRefs := a.expandSignatureEffects(n.Return, n.Permissions, n.EffectAlias, n.EffectAliasPos, n.Effects)
		resolvedPermissionRefs := a.resolvePermissionRefs(permissionRefs, true)
		permissions := a.resolvePermissionFamilies(permissionRefs, true)
		retType := a.namedTypes["void"]
		if retExpr != nil {
			retType = a.resolveType(retExpr)
		}
		return &FuncType{
			Name:                      "func",
			RefStorageParams:          nil,
			RefStateParams:            nil,
			UsedPermissionParams:      append([]string(nil), a.permissionParamsInRefs(permissionRefs)...),
			DeclaredPermissionRefs:    append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
			DeclaredPermissions:       append([]string(nil), permissions...),
			PermissionRefs:            append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
			Permissions:               permissions,
			Params:                    ptypes,
			ExplicitParamCount:        len(n.Params),
			ExplicitParamNames:        nil,
			ExplicitParamDefaultExprs: make([]ast.Expr, len(n.Params)),
			ExplicitParamHasDefault:   make([]bool, len(n.Params)),
			ImplicitParamNames:        implicitNames,
			Return:                    retType,
			Variadic:                  n.Variadic,
		}
	case *ast.MutableType:
		elemType := a.resolveType(n.Elem)
		if ref, ok := elemType.(*RefType); ok {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			return cloned
		}
		return elemType
	case *ast.TailType:
		elemType := a.resolveType(n.Elem)
		if a.containsAffineHandleValues(elemType, map[string]bool{}) && !isBorrowableAffineOwnerType(elemType) {
			a.errorf(n.Pos(), "references to values containing affine handles are not supported; got %s&", elemType)
		}
		return &RefType{Elem: elemType, State: RefStateNonNull, Storage: RefStorageAny}
	case *ast.GenericType:
		if shaped, ok := a.resolveDynamicShapeType(n); ok {
			return shaped
		}
		if arrayExpr, ok := a.genericTypeAsArrayType(n); ok {
			return a.resolveArrayType(arrayExpr)
		}
		a.maybeRejectRuntimeCarrierTypeUse(n.Pos(), n.Name)
		args := make([]Type, 0, len(n.Args))
		base, _, ok := a.lookupVisibleType(n.Name)
		if !ok {
			a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.Name))
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
		case *TreeStoreType:
			if len(n.Args) != 1 {
				a.errorf(n.Pos(), "tree store type %q expects 1 state argument, got %d", n.Name, len(args))
				return invalidType
			}
			args = append(args, a.resolveType(n.Args[0]))
			return TreeStoreWithState(base, args[0])
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
					a.errorf(n.Pos(), "atomic payload type must satisfy atomic_safe(T), got %s", args[0])
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
		var resolved Type
		resolved, _, ok = a.lookupVisibleType(tag.SetName)
		if ok {
			t = resolved
		}
	}
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

func (a *Analyzer) resolvePackedVariantViewSurfaceType(expr ast.TypeExpr, pos lexer.Pos) Type {
	named, ok := expr.(*ast.NamedType)
	if !ok {
		a.errorf(pos, "packedview expects a packed enum variant like packedview[Expr.Lit]")
		return invalidType
	}
	lastDot := strings.LastIndex(named.Name, ".")
	if lastDot <= 0 || lastDot >= len(named.Name)-1 {
		a.errorf(pos, "packedview expects a packed enum variant like packedview[Expr.Lit]")
		return invalidType
	}
	enumName := named.Name[:lastDot]
	variantName := named.Name[lastDot+1:]
	base, ok := a.namedTypes[enumName]
	if !ok {
		a.errorf(pos, "unknown packed enum %q in packedview type", enumName)
		return invalidType
	}
	enumType, ok := base.(*EnumType)
	if !ok {
		a.errorf(pos, "packedview expects a packed enum variant like packedview[Expr.Lit]")
		return invalidType
	}
	if !enumType.Packed {
		a.errorf(pos, "packedview requires a packed enum variant, got ordinary enum %q", enumType.Name)
		return invalidType
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		a.errorf(pos, "enum %q has no variant %q", enumType.Name, variantName)
		return invalidType
	}
	viewType := &PackedVariantViewType{Enum: enumType, Variant: variant}
	return viewType
}

func (a *Analyzer) resolveTreeVariantViewSurfaceType(expr ast.TypeExpr, pos lexer.Pos) Type {
	named, ok := expr.(*ast.NamedType)
	if !ok {
		a.errorf(pos, "treeview expects a tree variant like Lua.Expr.Binary")
		return invalidType
	}
	categoryType, variant, ok := a.treeVariantTargetFromNamedType(named)
	if !ok || categoryType == nil || variant == nil {
		if categoryType == nil {
			a.errorf(pos, "treeview expects a tree variant like Lua.Expr.Binary")
			return invalidType
		}
		a.errorf(pos, "tree category %q has no variant %q", categoryType.Name, named.Name[strings.LastIndex(named.Name, ".")+1:])
		return invalidType
	}
	return categoryType.VariantViewType(variant)
}

func (a *Analyzer) resolveNamedVariantWitnessType(named *ast.NamedType) (Type, bool) {
	if named == nil {
		return nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, false
	}
	baseName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, false
	}
	switch tt := base.(type) {
	case *TreeCategoryType:
		variant, ok := tt.Variant(variantName)
		if !ok || variant == nil {
			a.errorf(named.Pos(), "tree category %q has no variant %q", tt.Name, variantName)
			return invalidType, true
		}
		return tt.VariantViewType(variant), true
	case *EnumType:
		variant, ok := tt.Variant(variantName)
		if !ok || variant == nil {
			a.errorf(named.Pos(), "enum %q has no variant %q", tt.Name, variantName)
			return invalidType, true
		}
		if !tt.Packed {
			a.errorf(named.Pos(), "bare variant type %q requires a packed enum or tree category; ordinary enum variants are not first-class types", named.Name)
			return invalidType, true
		}
		return variant.PackedViewType(tt), true
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
	case "packedview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "packedview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return a.resolvePackedVariantViewSurfaceType(expr.TypeArgs[0], expr.Pos())
	case "treeview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "treeview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return a.resolveTreeVariantViewSurfaceType(expr.TypeArgs[0], expr.Pos())
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
	if a.containsAffineHandleValues(keyType, map[string]bool{}) {
		a.errorf(keyExpr.Pos(), "dict keys cannot contain affine handles, got %s", keyType)
		return invalidType
	}
	return &DictType{Key: keyType, Value: valueType, SurfaceName: surfaceName}
}

func dictRuntimeBackedKeyType(keyType Type) bool {
	switch StripAggregateStateType(keyType).(type) {
	case *DStrType, *TypeParamType:
		return true
	default:
		return false
	}
}

func dictSupportsRuntimeBackedOps(dictType *DictType) bool {
	return dictType != nil && dictRuntimeBackedKeyType(dictType.Key)
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
	case *StructType, *OpaqueType, *PackedEnumStoreType, *TreeStoreType:
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
	params := make([]ast.GenericParam, 0, len(base.TypeParams)+1)
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	if len(base.NamedStateCases) != 0 {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), base.NamedStateCases...), StateOwner: base.Name})
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

func (a *Analyzer) typeSatisfiesStaticInterface(candidate Type, iface *StaticInterface) bool {
	if a == nil || candidate == nil || iface == nil {
		return false
	}
	if typeParam, ok := candidate.(*TypeParamType); ok && typeParam != nil {
		boundIface, ok := a.lookupTypeParamInterface(typeParam.Name)
		return ok && boundIface != nil && boundIface.Name == iface.Name
	}
	_, ok := LookupStaticImpl(a.staticImpls, iface.Name, candidate)
	return ok
}

func (a *Analyzer) resolveGenericArgForParam(expr ast.TypeExpr, param ast.GenericParam) Type {
	switch param.Kind {
	case ast.GenericParamState:
		return a.resolveNamedStateGenericArg(expr, param)
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
		a.errorf(expr.Pos(), "generic argument %q for refstorage parameter %q must be a refstorage literal or parameter", resolved, param.Name)
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
		a.errorf(expr.Pos(), "generic argument %q for refstate parameter %q must be a refstate literal or parameter", resolved, param.Name)
		return invalidType
	default:
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStorageParamType); ok {
			a.errorf(expr.Pos(), "refstorage parameter %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if _, ok := resolved.(*RefStorageValueType); ok {
			a.errorf(expr.Pos(), "refstorage literal %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if _, ok := resolved.(*RefStateParamType); ok {
			a.errorf(expr.Pos(), "refstate parameter %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if _, ok := resolved.(*RefStateValueType); ok {
			a.errorf(expr.Pos(), "refstate literal %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if param.InterfaceBound != "" {
			iface := a.staticInterfaces[param.InterfaceBound]
			if iface == nil {
				var ok bool
				iface, _, ok = a.lookupVisibleStaticInterface(param.InterfaceBound)
				if !ok || iface == nil {
					a.errorf(expr.Pos(), "%s", UnknownInterfaceMessage(param.InterfaceBound))
					return invalidType
				}
			}
			if !a.typeSatisfiesStaticInterface(resolved, iface) {
				a.errorf(expr.Pos(), interfaceConformanceFactMessage(resolved.String(), iface.Name, "type argument"))
				return invalidType
			}
		}
		return resolved
	}
}

func (a *Analyzer) resolveNamedStateGenericArg(expr ast.TypeExpr, param ast.GenericParam) Type {
	allowed := make(map[string]bool, len(param.StateCases))
	for _, name := range param.StateCases {
		allowed[name] = true
	}
	collect := func(names []string) Type {
		for _, name := range names {
			if !allowed[name] {
				a.errorf(expr.Pos(), "generic argument %q is not a declared state of %q", name, param.StateOwner)
				return invalidType
			}
		}
		resolved := newNamedStateType(param.StateOwner, param.StateCases, names)
		if resolved == nil {
			a.errorf(expr.Pos(), "named state argument for %q cannot be empty", param.StateOwner)
			return invalidType
		}
		return resolved
	}
	switch n := expr.(type) {
	case *ast.NamedType:
		return collect([]string{n.Name})
	case *ast.StateSetTypeExpr:
		return collect(n.Cases)
	default:
		resolved := a.resolveType(expr)
		a.errorf(expr.Pos(), "generic argument %q for named struct state parameter of %q must be a declared state name or union", resolved, param.StateOwner)
		return invalidType
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
	case *ast.FloatLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				return t
			}
		}
		return a.namedTypes["f64"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	case *ast.CharLit:
		return a.namedTypes["char"]
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
	if AssignableTo(dst, src) {
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
	case *ast.FieldExpr:
		object := a.exprSummary(n.Object)
		if object == "?" {
			return "?"
		}
		return fmt.Sprintf("%s.%s", object, n.Field)
	case *ast.IndexExpr:
		object := a.exprSummary(n.Object)
		index := a.exprSummary(n.Index)
		if object == "?" || index == "?" {
			return "?"
		}
		if n.Fallback == nil {
			return fmt.Sprintf("%s[%s]", object, index)
		}
		fallback := a.exprSummary(n.Fallback)
		if fallback == "?" {
			return "?"
		}
		return fmt.Sprintf("%s[%s] else %s", object, index, fallback)
	case *ast.BinaryExpr:
		return fmt.Sprintf("%s%s%s", a.exprSummary(n.Left), lexer.TokenName(n.Op), a.exprSummary(n.Right))
	case *ast.ParenExpr:
		return a.exprSummary(n.Inner)
	case *ast.CastExpr:
		return a.exprSummary(n.Operand)
	case *ast.MoveExpr:
		return a.exprSummary(n.Operand)
	case *ast.CanExpr:
		return a.exprSummary(n.Expr)
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
	typeInterfaces := make(map[string]*StaticInterface)
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
			if param.InterfaceBound != "" {
				iface, _, ok := a.lookupVisibleStaticInterface(param.InterfaceBound)
				if !ok || iface == nil {
					a.errorf(param.Position, "%s", UnknownInterfaceMessage(param.InterfaceBound))
				} else {
					typeInterfaces[param.Name] = iface
				}
			}
		case ast.GenericParamRefStorage:
			refStorageNames = append(refStorageNames, param.Name)
			refStorageArgs = append(refStorageArgs, arg)
		case ast.GenericParamRefState:
			refStateNames = append(refStateNames, param.Name)
			refStateArgs = append(refStateArgs, arg)
		}
	}
	a.withTypeParamInterfaces(typeInterfaces, func() {
		a.withTypeParams(typeNames, typeArgs, func() {
			a.withRefStorageParams(refStorageNames, refStorageArgs, func() {
				a.withRefStateParams(refStateNames, refStateArgs, fn)
			})
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
		value := a.substituteTypeWithDepth(n.Value, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(value) {
			return invalidType
		}
		return &ErrorUnionType{Value: value, Errors: n.Errors}
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
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &RefType{Elem: elem, Mutable: n.Mutable, State: state, StateParam: stateParam, Storage: storage, StorageParam: storageParam, Region: region, ExplicitStorage: n.ExplicitStorage}
	case *ArrayType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &ArrayType{Elem: elem, Size: n.Size, HasConstSize: n.HasConstSize, ConstSize: n.ConstSize, SurfaceName: n.SurfaceName}
	case *DArrayType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &DArrayType{Elem: elem, Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName}
	case *ViewType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &ViewType{Elem: elem, Begin: n.Begin, End: n.End}
	case *DArrayViewType:
		elem := a.substituteTypeWithDepth(n.Elem, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(elem) {
			return invalidType
		}
		return &DArrayViewType{Elem: elem, Begin: n.Begin, End: n.End, SurfaceName: n.SurfaceName}
	case *DStrType:
		return &DStrType{Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName}
	case *DictType:
		key := a.substituteTypeWithDepth(n.Key, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(key) {
			return invalidType
		}
		value := a.substituteTypeWithDepth(n.Value, bindings, shapeBindings, regionBindings, permissionBindings, depth+1)
		if IsInvalidType(value) {
			return invalidType
		}
		return &DictType{Key: key, Value: value, SurfaceName: n.SurfaceName}
	case *SViewType:
		return &SViewType{Begin: n.Begin, End: n.End}
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
		return &FuncType{Name: n.Name, TypeParams: append([]string(nil), n.TypeParams...), RefStorageParams: append([]string(nil), n.RefStorageParams...), RefStateParams: append([]string(nil), n.RefStateParams...), RegionParams: append([]string(nil), n.RegionParams...), PermissionParams: append([]string(nil), n.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), n.GenericParams...), UsedPermissionParams: usedPermissionParams, DeclaredPermissionRefs: declaredRefs, DeclaredPermissions: permissionFamiliesFromRefs(declaredRefs), PermissionRefs: refs, Permissions: permissionFamiliesFromRefs(refs), ShapeParams: append([]string(nil), n.ShapeParams...), FreshReturnShapeParams: append([]string(nil), n.FreshReturnShapeParams...), InlineMode: n.InlineMode, HasInlineMode: n.HasInlineMode, HasNoRecurse: n.HasNoRecurse, TemperatureMode: n.TemperatureMode, HasTemperatureMode: n.HasTemperatureMode, GuardEffects: cloneFuncGuardEffects(n.GuardEffects), Poststates: cloneFuncPoststates(n.Poststates), Params: params, ExplicitParamCount: n.ExplicitParamCount, ExplicitParamNames: append([]string(nil), n.ExplicitParamNames...), ExplicitParamDefaultExprs: append([]ast.Expr(nil), n.ExplicitParamDefaultExprs...), ExplicitParamHasDefault: append([]bool(nil), n.ExplicitParamHasDefault...), ImplicitParamNames: append([]string(nil), n.ImplicitParamNames...), Return: returnType, Variadic: n.Variadic, SinkParams: append([]bool(nil), n.SinkParams...), SinkParamsKnown: n.SinkParamsKnown, ReturnProvenance: cloneRegionRefState(n.ReturnProvenance), ReturnProvenanceKnown: n.ReturnProvenanceKnown, ReturnBorrowedOwnerRefs: cloneBorrowedOwnerRefSummary(n.ReturnBorrowedOwnerRefs), ReturnBorrowedOwnerRefsKnown: n.ReturnBorrowedOwnerRefsKnown, ReturnIsolation: n.ReturnIsolation, ReturnIsolationKnown: n.ReturnIsolationKnown}
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
	mutableType, ok := param.Type.(*ast.MutableType)
	if !ok {
		return false
	}
	_, refLike := mutableType.Elem.(*ast.RefType)
	return !refLike
}

func isLegacyOutParamName(name string) bool {
	return name == "out" || strings.HasSuffix(name, "_out")
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
	case *ast.TupleTypeExpr:
		for _, field := range n.Fields {
			a.collectImplicitShapeParamsFromType(field.Type, seen, order)
		}
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
