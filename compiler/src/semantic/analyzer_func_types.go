package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strconv"
	"strings"
)

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
