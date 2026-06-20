package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func (a *Analyzer) funcTypeFromDecl(name string, typeParams []string, genericParams []ast.GenericParam, regionParams []string, permissionParams []string, permissionRefs []ast.PermissionRef, ensures []ast.EnsuresClause, params []ast.ParamDecl, ret ast.TypeExpr, variadic bool) *FuncType {
	return a.funcTypeFromDeclWithFrame(name, typeParams, genericParams, regionParams, permissionParams, permissionRefs, ensures, nil, nil, params, ret, variadic, false)
}

// funcTypeFromExternDecl builds the signature of an `extern` function. It is identical to
// funcTypeFromDecl except that no implicit `preserve` poststates are synthesized: the native body is
// unverifiable, so callers must keep widening a borrowed stateful argument across the call.
func (a *Analyzer) funcTypeFromExternDecl(name string, typeParams []string, genericParams []ast.GenericParam, regionParams []string, permissionParams []string, permissionRefs []ast.PermissionRef, ensures []ast.EnsuresClause, params []ast.ParamDecl, ret ast.TypeExpr, variadic bool) *FuncType {
	return a.funcTypeFromDeclWithFrame(name, typeParams, genericParams, regionParams, permissionParams, permissionRefs, ensures, nil, nil, params, ret, variadic, true)
}

// funcTypeFromDeclWithFrame is funcTypeFromDecl plus the callee's frame clauses (docs/87 87-3), so
// the resulting FuncType carries an effective-frame summary call sites use to refine mutable-ref
// arguments. `changes` and `fulfills` are the only clauses that BOUND writes; `preserves` is a
// blacklist that does not, so it is not threaded here.
func (a *Analyzer) funcTypeFromDeclWithFrame(name string, typeParams []string, genericParams []ast.GenericParam, regionParams []string, permissionParams []string, permissionRefs []ast.PermissionRef, ensures []ast.EnsuresClause, changes []ast.EnsuresPath, fulfills []ast.FulfillsClause, params []ast.ParamDecl, ret ast.TypeExpr, variadic bool, isExtern bool) *FuncType {
	resolvedGenericParams := append([]ast.GenericParam(nil), genericParams...)
	for _, param := range resolvedGenericParams {
		if param.Kind != ast.GenericParamErrorSet {
			continue
		}
		shadowed, ok := a.namedTypes[param.Name]
		if !ok {
			shadowed, _, ok = a.lookupVisibleType(param.Name)
		}
		if errSet, isSet := shadowed.(*ErrorSetType); ok && isSet && errSet != nil && !errSet.HasParams() {
			a.warnOncef(param.Position, "errorset parameter %q on %q shadows the declared error set %q; every `error[%s]` in this signature means the parameter, not the set — rename the parameter", param.Name, name, param.Name, param.Name)
		}
	}
	for i, param := range resolvedGenericParams {
		if param.Kind != ast.GenericParamType || param.InterfaceBound == "" {
			continue
		}
		if iface, _, ok := a.lookupVisibleStaticInterface(param.InterfaceBound); ok && iface != nil {
			resolvedGenericParams[i].InterfaceBound = iface.Name
		}
	}
	explicitSpecs := a.expandExplicitParamSpecs(params, name)
	expandedExplicitParams := explicitParamDeclsFromSpecs(explicitSpecs)
	allParams := expandedExplicitParams
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
	var refinementEnsures []RefinementEnsure
	defaultExprs := make([]ast.Expr, len(expandedExplicitParams))
	hasDefaults := make([]bool, len(expandedExplicitParams))
	a.withGenericParams(resolvedGenericParams, nil, func() {
		a.withRegionParams(regionParams, func() {
			a.withPermissionParams(permissionParams, func() {
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
					if ret != nil {
						retType = a.resolveType(ret)
					}
					poststates = a.resolveFuncPoststates(name, allParams, ptypes, retType, ensures)
					refinementEnsures = a.resolveRefinementEnsures(name, allParams, ptypes, ensures)
					defaultExprs, hasDefaults = a.validateExpandedFuncParamDefaults(name, explicitSpecs, ptypes[:len(expandedExplicitParams)])
				})
			})
		})
	})
	frameWrites, frameBounded := a.resolveFrameSummary(allParams, changes, fulfills)
	ft := &FuncType{
		Name:                      name,
		FrameWrites:               frameWrites,
		FrameBounded:              frameBounded,
		TypeParams:                append([]string(nil), typeParams...),
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
		RefinementEnsures:         refinementEnsures,
		Params:                    ptypes,
		ExplicitParamCount:        len(expandedExplicitParams),
		ExplicitParamNames:        explicitNames,
		ExplicitParamDefaultExprs: append([]ast.Expr(nil), defaultExprs...),
		ExplicitParamHasDefault:   append([]bool(nil), hasDefaults...),
		Return:                    retType,
		Variadic:                  variadic,
		OwnedParams:               ownedParamFlags(allParams),
	}
	// Strict protocol balance (body-bearing functions only — never externs, whose native body cannot be
	// verified to preserve a borrowed state). Synthesized at signature-build time so every caller, in any
	// order, sees the preserve and stops widening across the call.
	if !isExtern {
		a.appendImplicitPreservePoststates(ft, allParams, ptypes)
	}
	return ft
}

// ownedParamFlags records which parameters are declared `owned <store>` so call
// sites can require/transfer ownership. Returns nil when none are owned (the
// common case) to avoid allocating.
func ownedParamFlags(params []ast.ParamDecl) []bool {
	var flags []bool
	for i, p := range params {
		if isOwnedTypeExpr(p.Type) {
			if flags == nil {
				flags = make([]bool, len(params))
			}
			flags[i] = true
		}
	}
	return flags
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
	resolved := make([]FuncPoststate, 0, len(ensures))
	type seenPoststateTarget struct {
		Condition FuncPoststateCondition
		Position  lexer.Pos
	}
	seenTargets := make(map[string][]seenPoststateTarget, len(ensures))
	for _, clause := range ensures {
		// Refinement postconditions (`ensures arr is NonEmpty`) ride the lightweight
		// RefinementEnsures channel (resolveRefinementEnsures), not the type-based poststate system.
		if clause.Kind == ast.EnsuresKindRefinement {
			continue
		}
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

// appendImplicitPreservePoststates implements STRICT PROTOCOL BALANCE: a `mutable T[S]&` parameter
// whose declared state is a single specific state, and which no explicit `ensures` already governs,
// gets an IMPLICIT `preserve` poststate. The resource must be handed back in the state it was lent in;
// a function that changes it must declare the new state (`ensures p => NewState`). This removes the
// `=> preserve` boilerplate for the common no-op-on-state case AND catches undeclared state mutations,
// and lets callers stop widening across such calls so protocol chains compose without annotation.
//
// It runs at BODY analysis (only body-bearing functions reach it), never for externs: an extern's
// native body is unverifiable, so assuming it preserves a borrowed state would be unsound — callers
// must keep widening across externs. Idempotent (re-analysis won't duplicate the synthesized clauses).
func (a *Analyzer) appendImplicitPreservePoststates(fnType *FuncType, params []ast.ParamDecl, paramTypes []Type) {
	if fnType == nil {
		return
	}
	covered := make(map[int]bool, len(fnType.Poststates))
	for _, ps := range fnType.Poststates {
		covered[ps.ParamIndex] = true
	}
	for i := range params {
		if i >= len(paramTypes) || covered[i] {
			continue
		}
		if !paramIsMutableRefToSingleNamedState(params[i], paramTypes[i]) {
			continue
		}
		fnType.Poststates = append(fnType.Poststates, FuncPoststate{
			Position:   params[i].Position,
			Condition:  FuncPoststateCondition{Kind: FuncPoststateConditionAlways},
			ParamIndex: i,
			Kind:       FuncPoststateKindPreserve,
			Implicit:   true,
		})
	}
}

// paramIsMutableRefToSingleNamedState reports whether a parameter is a MUTABLE reference to a
// named-state-bearing value whose state is pinned to exactly ONE case (e.g. `mutable File[Open]&`).
// Only this shape gets the implicit `preserve`: a by-value or immutable param can't have the caller's
// state mutated, and a multi-state declaration (`File[Open | Closed]&`) deliberately says "either
// state is fine", so pinning it to preserve would be a false constraint. Mutability may sit in the ref
// type (canonical `p: mutable T&`, RefType.Mutable) or on the ParamDecl (legacy `mutable p: T&`).
func paramIsMutableRefToSingleNamedState(param ast.ParamDecl, t Type) bool {
	ref, ok := poststateRefTargetType(t)
	if !ok || ref == nil {
		return false
	}
	if !ref.Mutable && !param.Mutable {
		return false
	}
	elem := ref.Elem
	for {
		agg, isAgg := elem.(*AggregateStateType)
		if !isAgg || agg == nil {
			break
		}
		elem = agg.Base
	}
	stateArg, ok := namedStateCurrentArg(elem)
	if !ok || stateArg == nil {
		return false
	}
	cases, _, ok := namedStateTypeCases(stateArg)
	return ok && len(cases) == 1
}

// resolveRefinementEnsures resolves `ensures <param> is <BareLaw>` postconditions (docs/85, mutable
// refinement flow brick 2) into the lightweight RefinementEnsure channel. Validated here: the
// target is a bare parameter (no field path) and the law is a real bare law whose subject accepts
// the param type. Each becomes a (ParamIndex, LawName) the call site uses to GAIN a predicate fact
// and the callee's returns use to discharge the postcondition.
func (a *Analyzer) resolveRefinementEnsures(name string, params []ast.ParamDecl, paramTypes []Type, ensures []ast.EnsuresClause) []RefinementEnsure {
	var out []RefinementEnsure
	for _, clause := range ensures {
		if clause.Kind != ast.EnsuresKindRefinement {
			continue
		}
		if len(clause.Target.Fields) != 0 {
			a.errorf(clause.Position, "ensures %q on function %q: refinement postconditions apply to a bare parameter, not a field path", clause.Target.Root, name)
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
		decl, ft, ok := a.lookupLaw(clause.RefinementLaw)
		if !ok || decl == nil {
			a.errorf(clause.Position, "ensures %q is not a law", clause.RefinementLaw)
			continue
		}
		// A law's first parameter is the subject; any remaining parameters are filled by the
		// postcondition's bracket args. A bare law has exactly one parameter (no args). A parametric
		// postcondition (`ensures x is Bounded[0, 500]`) must supply one arg per extra parameter.
		if len(decl.Params) == 0 {
			a.errorf(clause.Position, "ensures %q is not a valid refinement law (no subject parameter)", clause.RefinementLaw)
			continue
		}
		wantArgs := len(decl.Params) - 1
		if len(clause.RefinementArgs) != wantArgs {
			a.errorf(clause.Position, "ensures %q expects %d argument(s), got %d", clause.RefinementLaw, wantArgs, len(clause.RefinementArgs))
			continue
		}
		// Args must be compile-time constants so the obligation has a stable (law, args) identity
		// shared by the caller-gain fact key and the callee discharge.
		argsOK := true
		for _, arg := range clause.RefinementArgs {
			a.analyzeExpr(arg)
			if _, ok := a.evalConstExpr(arg); !ok {
				a.errorf(arg.Pos(), "ensures %q argument must be a compile-time constant", clause.RefinementLaw)
				argsOK = false
			}
		}
		if !argsOK {
			continue
		}
		if paramIndex < len(paramTypes) && ft != nil && len(ft.Params) >= 1 && len(decl.TypeParams) == 0 {
			subject := ft.Params[0]
			base := refinementSubjectBaseType(paramTypes[paramIndex])
			if base != nil && !AssignableTo(base, subject) && !AssignableTo(subject, base) {
				a.errorf(clause.Position, "ensures %q expects a subject of type %s, but parameter %q is %s", clause.RefinementLaw, typeString(subject), clause.Target.Root, typeString(base))
				continue
			}
		}
		out = append(out, RefinementEnsure{Position: clause.Position, ParamIndex: paramIndex, LawName: clause.RefinementLaw, Args: clause.RefinementArgs})
	}
	return out
}

// refinementSubjectBaseType strips a ref wrapper so a law over `darray[u8]` matches a
// `mutable darray[u8]&` parameter (the subject is the pointee).
func refinementSubjectBaseType(t Type) Type {
	if rt, ok := t.(*RefType); ok && rt != nil {
		return rt.Elem
	}
	return t
}
