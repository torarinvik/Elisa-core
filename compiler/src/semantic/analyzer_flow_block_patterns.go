package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"maps"
	"slices"
)

func inferDArrayBuilderElemTypeFromKnownExpr(expr ast.Expr, known *darrayBuilderKnownTypes) ast.TypeExpr {
	typ := inferKnownDArrayBuilderExprType(expr, known)
	if typ == nil || IsInvalidType(typ) {
		return nil
	}
	return astTypeExprForBuiltinMethodRewrite(expr.Pos(), typ)
}

func inferKnownDArrayBuilderExprType(expr ast.Expr, known *darrayBuilderKnownTypes) Type {
	expr = stripParenExpr(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		return inferKnownIdentType(e.Name, known)
	case *ast.IndexExpr:
		source := inferKnownDArrayBuilderExprType(e.Object, known)
		if ref, ok := source.(*RefType); ok && ref != nil {
			source = ref.Elem
		}
		switch t := StripAggregateStateType(source).(type) {
		case *DArrayType:
			if t != nil {
				return t.Elem
			}
		case *ViewType:
			if t != nil {
				return t.Elem
			}
		case *ArrayType:
			if t != nil {
				return t.Elem
			}
		}
		return nil
	default:
		return nil
	}
}

func inferKnownIdentType(name string, known *darrayBuilderKnownTypes) Type {
	typ := known.get(name)
	if ref, ok := typ.(*RefType); ok && ref != nil {
		typ = ref.Elem
	}
	return typ
}

func inferDArrayExtendElemTypeFromKnownExpr(expr ast.Expr, known *darrayBuilderKnownTypes) ast.TypeExpr {
	expr = stripParenExpr(expr)
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return nil
	}
	typ := known.get(ident.Name)
	if ref, ok := typ.(*RefType); ok && ref != nil {
		typ = ref.Elem
	}
	switch t := StripAggregateStateType(typ).(type) {
	case *DArrayType:
		if t != nil {
			return astTypeExprForBuiltinMethodRewrite(expr.Pos(), t.Elem)
		}
	case *ViewType:
		if t != nil {
			return astTypeExprForBuiltinMethodRewrite(expr.Pos(), t.Elem)
		}
	case *ArrayType:
		if t != nil {
			return astTypeExprForBuiltinMethodRewrite(expr.Pos(), t.Elem)
		}
	}
	return nil
}

func inferSimpleLiteralTypeExpr(expr ast.Expr) ast.TypeExpr {
	expr = stripParenExpr(expr)
	pos := expr.Pos()
	switch lit := expr.(type) {
	case *ast.IntLit:
		switch lit.Suffix {
		case "":
			return &ast.NamedType{Position: pos, Name: "int"}
		case "u":
			return &ast.NamedType{Position: pos, Name: "usize"}
		case "i":
			return &ast.NamedType{Position: pos, Name: "int"}
		default:
			return &ast.NamedType{Position: pos, Name: lit.Suffix}
		}
	case *ast.FloatLit:
		if lit.Suffix != "" {
			return &ast.NamedType{Position: pos, Name: lit.Suffix}
		}
		return &ast.NamedType{Position: pos, Name: "f64"}
	case *ast.BoolLit:
		return &ast.NamedType{Position: pos, Name: "bool"}
	case *ast.CharLit:
		return &ast.NamedType{Position: pos, Name: "char"}
	}
	return nil
}

func (a *Analyzer) analyzeBlockWithRegionClone(stmts []ast.Stmt, scope *Scope) {
	a.analyzeBlockWithRegionScope(stmts, scope, false)
}

// analyzeBlockWithRegionCloneCapture analyzes a branch against a private copy of
// the region ownership state and returns the state at that branch's fallthrough.
// Region ownership is path-sensitive: restoring only the caller's state after a
// branch used to erase `destroy`/`adopt` effects and could make a dead region look
// live after control-flow joined.
func (a *Analyzer) analyzeBlockWithRegionCloneCapture(stmts []ast.Stmt, scope *Scope) map[*Symbol]regionState {
	return a.analyzeBlockWithRegionScope(stmts, scope, true)
}

func (a *Analyzer) analyzeBlockWithRegionScope(stmts []ast.Stmt, scope *Scope, capture bool) map[*Symbol]regionState {
	savedRegions := a.currentRegions
	savedRegionsBorrowed := a.currentRegionsBorrowed
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedRegionRefsBorrowed := a.currentRegionRefsBorrowed
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.borrowRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.borrowRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.analyzeBlockInScope(stmts, scope)
	var branchRegions map[*Symbol]regionState
	if capture {
		// Captured branches remain independently mutable. A branch that wrote already
		// owns its map; a read-only branch must detach before returning a snapshot.
		if a.currentRegionsBorrowed {
			branchRegions = a.cloneRegionStates()
		} else {
			branchRegions = a.currentRegions
		}
	}
	a.currentRegions = savedRegions
	a.currentRegionsBorrowed = savedRegionsBorrowed
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
	a.currentRegionRefsBorrowed = savedRegionRefsBorrowed
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	return branchRegions
}

type affineFlowSnapshot struct {
	Regions                    map[*Symbol]regionState
	Affine                     map[affineValueKey]affineValueState
	BorrowedOwnerRefs          map[*Symbol]borrowedOwnerRefState
	FunctionValues             map[*Symbol]*FuncType
	SpecializedValueTypes      map[*Symbol]Type
	ValueBindings              map[*Symbol]ast.Expr
	RangeFacts                 map[string]numRange
	StorageViewDeps            map[*Symbol]storageViewDependencyState
	AliasCarriers              map[string][]string
	AliasCarrierFieldOverrides map[string]map[string][]string
	// SMTFacts are the flow facts the block's own scope held at its end (its guard, the exact
	// assignment equalities of its writes); the if-join disjoins them (analyzer_smt_if_join.go).
	SMTFacts []smtFact
}

type borrowedOwnerRefSummaryTarget struct {
	ParamIndex int
	Path       []borrowReturnAnnotationStep
}

type borrowedOwnerRefSummary struct {
	HasDirect bool
	Direct    borrowedOwnerRefSummaryTarget
	Fields    map[string]borrowedOwnerRefSummary
}

func (a *Analyzer) analyzeBlockWithAffineClone(stmts []ast.Stmt, scope *Scope) affineFlowSnapshot {
	return a.analyzeBlockWithAffineClonePrepared(stmts, scope, nil)
}

func (a *Analyzer) analyzeBranchBlockWithAffineClone(stmts []ast.Stmt, scope *Scope) affineFlowSnapshot {
	savedRangeFacts := cloneScopeRangeFacts(scope)
	snapshot := a.analyzeBlockWithAffineClonePrepared(stmts, scope, nil)
	restoreScopeRangeFacts(savedRangeFacts)
	return snapshot
}

func (a *Analyzer) analyzeBlockWithConditionAffineClone(stmts []ast.Stmt, parent *Scope, cond ast.Expr, truthy bool) affineFlowSnapshot {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	savedRangeFacts := cloneScopeRangeFacts(scope)
	savedSizeGuards := len(a.overlaySizeGuards)
	snapshot := a.analyzeBlockWithAffineClonePrepared(stmts, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
		a.applyIndexBoundsFactsForCondition(cond, truthy)
		a.applyViewStaticLenForCondition(cond, truthy)
		// docs/107 increment 4: a truthy `if base.size[mem] >= N:` guard establishes a size
		// lower-bound that discharges `requires size >= N` overlay accesses in this branch body.
		if truthy {
			a.applyOverlaySizeGuardForCondition(cond)
		}
		// Record the branch-condition fact into the BRANCH scope, not the current (parent) scope —
		// currentScope is not switched to `scope` until the body is analyzed below, so using it here would
		// leak the condition past the branch (a then-branch `x > 10` reaching the fall-through, where it
		// contradicts the applied negation `x <= 10` and proves any postcondition).
		a.recordSMTAssertFactInScope(scope, smtFactExprForCondition(cond, truthy))
	})
	restoreScopeRangeFacts(savedRangeFacts)
	a.overlaySizeGuards = a.overlaySizeGuards[:savedSizeGuards]
	return snapshot
}

// optionalBindBoundType resolves the type bound by an `if VALUE is NAME:`
// condition. A slice operand (`if arr[a:b] is s:`) is a bounds-checked slice:
// it is marked checked (so codegen emits the runtime bounds test) and binds the
// bounded view, taking the else arm when out of range. All other operands fall
// back to the standard optional / nullable-reference unwrap.
func (a *Analyzer) optionalBindBoundType(value ast.Expr, valueType Type) (Type, bool) {
	if slice, ok := stripOptimizationParens(value).(*ast.SliceExpr); ok && slice != nil {
		a.checkedSliceExprs[slice] = true
		if opt, isOpt := valueType.(*OptionalType); isOpt && opt != nil {
			return opt.Value, true
		}
		return valueType, true
	}
	if bound, ok := conditionOptionalBindType(valueType); ok {
		return bound, true
	}
	// A place an assignment narrowed from `T?` to `T` is still DECLARED optional and its
	// storage still carries the present flag, so a refutable bind stays meaningful -- it
	// simply always succeeds. Re-ask against the declared type rather than rejecting the
	// bind outright, which is what made an optional unusable after its first write.
	if declared, narrowed := a.narrowedOptionalDeclaredType(value); narrowed {
		return conditionOptionalBindType(declared)
	}
	return nil, false
}

func conditionOptionalBindType(valueType Type) (Type, bool) {
	switch t := valueType.(type) {
	case *OptionalType:
		if t == nil || t.Value == nil {
			return nil, false
		}
		return t.Value, true
	case *RefType:
		if t == nil || t.State != RefStateNullable {
			return nil, false
		}
		return cloneRefTypeWithState(t, RefStateNonNull), true
	default:
		return nil, false
	}
}

func unwrapDirectConditionPattern(expr ast.Expr) (*ast.BinaryExpr, ast.Expr, ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapDirectConditionPattern(n.Inner)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_IS {
			return nil, nil, nil, false
		}
		right := n.Right
		if alias, ok := right.(*ast.IsAliasExpr); ok && alias != nil {
			right = alias.Target
		}
		switch testExpr := right.(type) {
		case *ast.StructTestExpr:
			if testExpr == nil || testExpr.Pattern == nil {
				return nil, nil, nil, false
			}
			return n, n.Left, testExpr.Pattern, true
		case *ast.VariantTestExpr:
			if testExpr == nil || testExpr.Pattern == nil {
				return nil, nil, nil, false
			}
			return n, n.Left, testExpr.Pattern, true
		default:
			return nil, nil, nil, false
		}
	default:
		return nil, nil, nil, false
	}
}

func (a *Analyzer) conditionAliasBindingType(leftExpr ast.Expr, target ast.Expr, leftType Type) (string, Type, bool) {
	alias, ok := target.(*ast.IsAliasExpr)
	if !ok || alias == nil || alias.Alias == "" || alias.Alias == "_" {
		return "", nil, false
	}
	if enumType, variant, ok := a.resolveEnumVariantIsTarget(alias.Target); ok && enumType != nil && variant != nil {
		matchableEnum, _, ok := resolveMatchableEnumType(leftType)
		if ok && matchableEnum != nil && matchableEnum.Name == enumType.Name {
			return alias.Alias, variant.PackedViewType(matchableEnum), true
		}
	}
	if targetBase, targetState, ok := a.resolveNamedStateIsTarget(alias.Target); ok && targetBase != nil && targetState != nil {
		leftBase, ok := namedStateStructBase(leftType)
		if ok && leftBase != nil && leftBase.Name == targetBase.Name {
			currentState, ok := namedStateCurrentArg(leftType)
			if !ok || currentState == nil {
				currentState = fullNamedStateType(leftBase)
			}
			refinedState := intersectNamedStateType(currentState, targetState, leftBase.NamedStateCases)
			if refinedState != nil {
				return alias.Alias, instantiateNamedStateStructLiteralType(leftBase, leftType, refinedState), true
			}
		}
	}
	// docs/77 §2: `e is Statement s` — a bare-category target gives the binder the NARROWED
	// category type, so it can be passed where the sub-category is required.
	if matchableEnum, _, ok := resolveMatchableEnumType(StripAggregateStateType(leftType)); ok && enumIsHierarchical(matchableEnum) {
		if category, ok := a.resolveEnumCategoryIsTarget(alias.Target); ok && enumDescendsFrom(category, matchableEnum) {
			return alias.Alias, category, true
		}
	}
	return alias.Alias, leftType, true
}

func (a *Analyzer) collectConditionStructPatternBindingTypes(pattern ast.MatchPattern, expected Type, out map[string]Type) {
	if a == nil || pattern == nil || expected == nil || out == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return
		}
		if a.analyzePredicateMatchPattern(p, expected, nil) {
			return
		}
		if prev, ok := out[p.Name]; ok {
			if !SameType(prev, expected) {
				a.errorf(p.Pos(), "condition binding %q has inconsistent types %s and %s", p.Name, prev, expected)
			}
			return
		}
		out[p.Name] = expected
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, expected) {
			return
		}
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.collectConditionStructPatternBindingTypes(arg.Pattern, fields[i].Type, out)
		}
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for _, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			a.collectConditionStructPatternBindingTypes(elem, elemType, out)
		}
	case *ast.MatchOrPattern:
		bindings, ok := a.collectOrPatternBindingTypes(p, expected)
		if !ok {
			return
		}
		for _, name := range slices.Sorted(maps.Keys(bindings)) { // map: sort for a stable report order
			typ := bindings[name]
			if prev, exists := out[name]; exists {
				if !SameType(prev, typ) {
					a.errorf(p.Pos(), "condition binding %q has inconsistent types %s and %s", name, prev, typ)
				}
				continue
			}
			out[name] = typ
		}
	case *ast.MatchVariantPattern:
		switch variantBase := conditionVariantPatternBase(expected).(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.collectConditionStructPatternBindingTypes(arg.Pattern, variant.Payload[i], out)
			}
		}
	}
}

func (a *Analyzer) collectOrPatternBindingTypes(pattern *ast.MatchOrPattern, expected Type) (map[string]Type, bool) {
	if pattern == nil {
		return nil, true
	}
	var baseline map[string]Type
	for i, option := range pattern.Options {
		current := map[string]Type{}
		a.collectConditionStructPatternBindingTypes(option, expected, current)
		if i == 0 {
			baseline = current
			continue
		}
		if !samePatternBindingTypeMap(baseline, current) {
			a.errorf(option.Pos(), "or-pattern alternatives must bind the same names with compatible types")
			return baseline, false
		}
	}
	if baseline == nil {
		baseline = map[string]Type{}
	}
	return baseline, true
}

func samePatternBindingTypeMap(left, right map[string]Type) bool {
	if len(left) != len(right) {
		return false
	}
	for name, leftType := range left {
		rightType, ok := right[name]
		if !ok || !SameType(leftType, rightType) {
			return false
		}
	}
	return true
}
