package semantic

import (
	"fmt"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeBlock(stmts []ast.Stmt) {
	saved := a.currentScope
	a.analyzeBlockInScope(stmts, NewScope(saved))
}

func (a *Analyzer) analyzeBlockInScope(stmts []ast.Stmt, scope *Scope) {
	saved := a.currentScope
	a.currentScope = scope
	a.withLocalParamPackFrame(func() {
		for _, stmt := range stmts {
			a.analyzeStmt(stmt)
		}
	})
	a.currentScope = saved
}

func (a *Analyzer) analyzeBlockWithRegionClone(stmts []ast.Stmt, scope *Scope) {
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.analyzeBlockInScope(stmts, scope)
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

type affineFlowSnapshot struct {
	Affine                map[affineValueKey]affineValueState
	BorrowedOwnerRefs     map[*Symbol]borrowedOwnerRefState
	FunctionValues        map[*Symbol]*FuncType
	SpecializedValueTypes map[*Symbol]Type
	ValueBindings         map[*Symbol]ast.Expr
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

func (a *Analyzer) analyzeBlockWithConditionAffineClone(stmts []ast.Stmt, parent *Scope, cond ast.Expr, truthy bool) affineFlowSnapshot {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeBlockWithAffineClonePrepared(stmts, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
	})
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
		switch testExpr := n.Right.(type) {
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
		if prev, ok := out[p.Name]; ok {
			if !SameType(prev, expected) {
				a.errorf(p.Pos(), "condition binding %q has inconsistent types %s and %s", p.Name, prev, expected)
			}
			return
		}
		out[p.Name] = expected
	case *ast.MatchStructPattern:
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
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
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
		case *TreeCategoryType:
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

func (a *Analyzer) collectGuaranteedTruthyConditionBindingTypes(expr ast.Expr) map[string]Type {
	if a == nil || expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectGuaranteedTruthyConditionBindingTypes(n.Inner)
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return nil
		}
		valueType := a.exprTypes[n.Value]
		if valueType == nil {
			valueType = a.analyzeExpr(n.Value)
		}
		boundType, ok := conditionOptionalBindType(valueType)
		if !ok {
			return nil
		}
		return map[string]Type{n.Name: boundType}
	case *ast.UnaryExpr:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.collectGuaranteedTruthyConditionBindingTypes(n.Left)
			right := a.collectGuaranteedTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 {
				return right
			}
			if len(right) == 0 {
				return left
			}
			out := make(map[string]Type, len(left)+len(right))
			for name, typ := range left {
				out[name] = typ
			}
			for name, typ := range right {
				if prev, ok := out[name]; ok && !SameType(prev, typ) {
					a.errorf(n.Pos(), "condition binding %q has inconsistent types %s and %s", name, prev, typ)
					continue
				}
				out[name] = typ
			}
			return out
		case lexer.TOKEN_OR:
			left := a.collectGuaranteedTruthyConditionBindingTypes(n.Left)
			right := a.collectGuaranteedTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 || len(right) == 0 {
				return nil
			}
			out := map[string]Type{}
			for name, leftType := range left {
				rightType, ok := right[name]
				if !ok || !SameType(leftType, rightType) {
					continue
				}
				out[name] = leftType
			}
			if len(out) == 0 {
				return nil
			}
			return out
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	a.collectConditionStructPatternBindingTypes(pattern, valueType, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) collectPossibleTruthyConditionBindingTypes(expr ast.Expr) map[string]Type {
	if a == nil || expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectPossibleTruthyConditionBindingTypes(n.Inner)
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return nil
		}
		valueType := a.exprTypes[n.Value]
		if valueType == nil {
			valueType = a.analyzeExpr(n.Value)
		}
		boundType, ok := conditionOptionalBindType(valueType)
		if !ok {
			return nil
		}
		return map[string]Type{n.Name: boundType}
	case *ast.UnaryExpr:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			left := a.collectPossibleTruthyConditionBindingTypes(n.Left)
			right := a.collectPossibleTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 {
				return right
			}
			if len(right) == 0 {
				return left
			}
			out := make(map[string]Type, len(left)+len(right))
			for name, typ := range left {
				out[name] = typ
			}
			for name, typ := range right {
				if _, ok := out[name]; !ok {
					out[name] = typ
				}
			}
			return out
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	a.collectConditionStructPatternBindingTypes(pattern, valueType, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) recordConditionalBindingHints(scope *Scope, expr ast.Expr, truthy bool) {
	if a == nil || scope == nil || !truthy || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.recordConditionalBindingHints(scope, n.Inner, truthy)
		return
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.recordConditionalBindingHints(scope, n.Operand, !truthy)
		}
		return
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.recordConditionalBindingHints(scope, n.Left, true)
			a.recordConditionalBindingHints(scope, n.Right, true)
			return
		case lexer.TOKEN_OR:
			leftPossible := a.collectPossibleTruthyConditionBindingTypes(n.Left)
			rightPossible := a.collectPossibleTruthyConditionBindingTypes(n.Right)
			guaranteed := a.collectGuaranteedTruthyConditionBindingTypes(n)
			allNames := map[string]bool{}
			for name := range leftPossible {
				allNames[name] = true
			}
			for name := range rightPossible {
				allNames[name] = true
			}
			for name := range allNames {
				if _, ok := guaranteed[name]; ok {
					continue
				}
				leftType, leftOK := leftPossible[name]
				rightType, rightOK := rightPossible[name]
				hint := ""
				switch {
				case leftOK && rightOK && !SameType(leftType, rightType):
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch binds it as %s, while right branch binds it as %s; use different bind names or restructure the condition", name, leftType.String(), rightType.String())
				case leftOK && !rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch binds it as %s, while right branch does not bind it", name, leftType.String())
				case !leftOK && rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch does not bind it, while right branch binds it as %s", name, rightType.String())
				case leftOK || rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` condition bindings are only introduced when every successful branch binds that name", name)
				}
				if hint != "" {
					scope.ConditionalBindingHints[name] = hint
				}
			}
			a.recordConditionalBindingHints(scope, n.Left, true)
			a.recordConditionalBindingHints(scope, n.Right, true)
			return
		}
	}
}

func (a *Analyzer) bindConditionPatternLocals(scope *Scope, expr ast.Expr, truthy bool) {
	if a == nil || scope == nil || !truthy {
		return
	}
	a.recordConditionalBindingHints(scope, expr, truthy)
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.bindConditionPatternLocals(scope, n.Inner, truthy)
		return
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return
		}
		valueType := a.exprTypes[n.Value]
		if valueType == nil {
			valueType = a.analyzeExprInScope(n.Value, scope)
		}
		boundType, ok := conditionOptionalBindType(valueType)
		if !ok {
			return
		}
		sym := &Symbol{Name: n.Name, Kind: SymbolLocal, Type: boundType, Node: n, Mutable: false}
		a.defineLocalInScope(scope, sym, n.Pos())
		return
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.bindConditionPatternLocals(scope, n.Operand, !truthy)
		}
		return
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.bindConditionPatternLocals(scope, n.Left, true)
			a.bindConditionPatternLocals(scope, n.Right, true)
			return
		case lexer.TOKEN_OR:
			for name, typ := range a.collectGuaranteedTruthyConditionBindingTypes(n) {
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: n, Mutable: false}
				a.defineLocalInScope(scope, sym, n.Pos())
			}
			return
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExprInScope(valueExpr, scope)
	}
	a.bindConditionStructPatternLocals(scope, pattern, valueType, valueExpr)
}

func (a *Analyzer) bindConditionStructPatternLocals(scope *Scope, pattern ast.MatchPattern, expected Type, valueExpr ast.Expr) {
	if a == nil || scope == nil || pattern == nil || expected == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "_" {
			return
		}
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}
		a.defineLocalInScope(scope, sym, p.Pos())
		if valueExpr != nil {
			a.recordValueBinding(sym, valueExpr)
			a.recordBorrowedOwnerRefBinding(sym, valueExpr)
			a.recordFunctionValueBinding(sym, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
			a.recordRegionRefBinding(sym, valueExpr)
		}
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			var fieldExpr ast.Expr
			if valueExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			}
			a.bindConditionStructPatternLocals(scope, arg.Pattern, fields[i].Type, fieldExpr)
		}
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
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
				var payloadExpr ast.Expr
				if valueExpr != nil {
					resolvedExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
					if ok {
						payloadExpr = resolvedExpr
					}
				}
				a.bindConditionStructPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		case *TreeCategoryType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				var payloadExpr ast.Expr
				if valueExpr != nil {
					resolvedExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
					if ok {
						payloadExpr = resolvedExpr
					}
				}
				a.bindConditionStructPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		}
	}
}

func (a *Analyzer) analyzeBlockWithAffineClonePrepared(stmts []ast.Stmt, scope *Scope, prepare func()) affineFlowSnapshot {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedPackedVariantViews := a.currentPackedVariantViews
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	if prepare != nil {
		prepare()
	}
	a.analyzeBlockWithRegionClone(stmts, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentPackedVariantViews = savedPackedVariantViews
	return snapshot
}
