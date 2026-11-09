package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"maps"
	"slices"
)

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
		valueType := a.exprType(n.Value)
		if valueType == nil {
			valueType = a.analyzeExpr(n.Value)
		}
		boundType, ok := a.optionalBindBoundType(n.Value, valueType)
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
			for _, name := range slices.Sorted(maps.Keys(right)) { // map: sort for a stable report order
				typ := right[name]
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
		if n.Op == lexer.TOKEN_IS {
			valueType := a.exprType(n.Left)
			if valueType == nil {
				valueType = a.analyzeExpr(n.Left)
			}
			if name, typ, ok := a.conditionAliasBindingType(n.Left, n.Right, valueType); ok && typ != nil {
				return map[string]Type{name: typ}
			}
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprType(valueExpr)
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil {
		if name, typ, ok := a.conditionAliasBindingType(valueExpr, binary.Right, valueType); ok && typ != nil {
			out[name] = typ
		}
	}
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
		valueType := a.exprType(n.Value)
		if valueType == nil {
			valueType = a.analyzeExpr(n.Value)
		}
		boundType, ok := a.optionalBindBoundType(n.Value, valueType)
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
		if n.Op == lexer.TOKEN_IS {
			valueType := a.exprType(n.Left)
			if valueType == nil {
				valueType = a.analyzeExpr(n.Left)
			}
			if name, typ, ok := a.conditionAliasBindingType(n.Left, n.Right, valueType); ok && typ != nil {
				return map[string]Type{name: typ}
			}
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprType(valueExpr)
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil {
		if name, typ, ok := a.conditionAliasBindingType(valueExpr, binary.Right, valueType); ok && typ != nil {
			out[name] = typ
		}
	}
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
					scope.setConditionalBindingHint(name, hint)
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
		valueType := a.exprType(n.Value)
		if valueType == nil {
			valueType = a.analyzeExprInScope(n.Value, scope)
		}
		boundType, ok := a.optionalBindBoundType(n.Value, valueType)
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
			truthy := a.collectGuaranteedTruthyConditionBindingTypes(n)
			for _, name := range slices.Sorted(maps.Keys(truthy)) { // map: sort for a stable report order
				typ := truthy[name]
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: n, Mutable: false}
				a.defineLocalInScope(scope, sym, n.Pos())
			}
			return
		}
		if n.Op == lexer.TOKEN_IS {
			valueType := a.exprType(n.Left)
			if valueType == nil {
				valueType = a.analyzeExprInScope(n.Left, scope)
			}
			if name, typ, ok := a.conditionAliasBindingType(n.Left, n.Right, valueType); ok && typ != nil {
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: n.Right, Mutable: false}
				a.defineLocalInScope(scope, sym, n.Right.Pos())
				a.recordValueBinding(sym, n.Left)
				a.recordBorrowedOwnerRefBinding(sym, n.Left)
				a.recordFunctionValueBinding(sym, n.Left)
				a.recordImmutableSymbolOptimizationFacts(sym, n.Left)
				a.recordRegionRefBinding(sym, n.Left)
				a.recordStorageViewBinding(sym, n.Left)
			}
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return
	}
	valueType := a.exprType(valueExpr)
	if valueType == nil {
		valueType = a.analyzeExprInScope(valueExpr, scope)
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil {
		if name, typ, ok := a.conditionAliasBindingType(valueExpr, binary.Right, valueType); ok && typ != nil {
			sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: binary.Right, Mutable: false}
			a.defineLocalInScope(scope, sym, binary.Right.Pos())
			if valueExpr != nil {
				a.recordValueBinding(sym, valueExpr)
				a.recordBorrowedOwnerRefBinding(sym, valueExpr)
				a.recordFunctionValueBinding(sym, valueExpr)
				a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
				a.recordRegionRefBinding(sym, valueExpr)
				a.recordPatternStorageViewBinding(sym, valueExpr)
			}
		}
	}
	a.bindConditionStructPatternLocals(scope, pattern, valueType, valueExpr)
}
