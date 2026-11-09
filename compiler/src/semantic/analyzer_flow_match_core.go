package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

// analyzeMatchArmGuard type-checks the docs/122 §5.1 arm-header guard (`Pattern if
// cond:`) inside the arm's pattern scope, so pattern bindings are visible to the
// guard expression. No-op for unguarded arms.
func (a *Analyzer) analyzeMatchArmGuard(guard ast.Expr, scope *Scope) {
	if guard == nil {
		return
	}
	saved := a.currentScope
	a.currentScope = scope
	condType := a.analyzeCondExpr(guard)
	a.currentScope = saved
	if !IsBoolType(condType) {
		a.errorf(guard.Pos(), "match arm guard must be bool, got %s", condType)
	}
}

// matchArmCoverageSink implements the docs/122 §5.1 exhaustiveness rule: a guarded
// arm can fail at runtime, so it never discharges coverage. Guarded arms write their
// variant marks into a throwaway map; callers must also ignore their wildcard signal.
func matchArmCoverageSink(guard ast.Expr, covered map[string]bool) map[string]bool {
	if guard == nil {
		return covered
	}
	return map[string]bool{}
}

// matchPatternHasNamedBindings is used where alternatives are analyzed independently. The
// bootstrap analyzer currently cannot soundly merge alias/borrow facts from distinct top-level
// alternatives, so those forms are rejected instead of inheriting facts from only the first
// branch. Binding-free alternatives can be validated branch by branch without losing state.
func matchPatternHasNamedBindings(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		return p != nil && (p.Name != "" && p.Name != "_" || p.Binder != "")
	case *ast.MatchRestPattern:
		return p != nil && p.Name != ""
	case *ast.MatchVariantPattern:
		if p == nil {
			return false
		}
		if p.As != "" {
			return true
		}
		for _, arg := range p.Args {
			if matchPatternHasNamedBindings(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchStructPattern:
		if p == nil {
			return false
		}
		if p.As != "" {
			return true
		}
		for _, arg := range p.Args {
			if matchPatternHasNamedBindings(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchTuplePattern:
		if p != nil {
			for _, elem := range p.Elems {
				if matchPatternHasNamedBindings(elem) {
					return true
				}
			}
		}
	case *ast.MatchListPattern:
		if p != nil {
			for _, elem := range p.Elems {
				if matchPatternHasNamedBindings(elem) {
					return true
				}
			}
		}
	case *ast.MatchOrPattern:
		if p != nil {
			for _, option := range p.Options {
				if matchPatternHasNamedBindings(option) {
					return true
				}
			}
		}
	}
	return false
}

// analyzeTopLevelOrPattern runs the ordinary top-level validator for every alternative so type,
// constructor, and coverage checks are identical to writing separate arms. Alternatives get
// isolated scopes because a refinement or packed-payload view from one branch is not valid for the
// shared body on every other branch. Binding patterns stay rejected until their branch merge is
// modeled explicitly.
func (a *Analyzer) analyzeTopLevelOrPattern(pattern *ast.MatchOrPattern, analyze func(ast.MatchPattern, *Scope) bool) bool {
	if pattern == nil {
		return false
	}
	if len(pattern.Options) == 0 {
		a.errorf(pattern.Pos(), "or-pattern requires at least one alternative")
		return false
	}
	for _, option := range pattern.Options {
		if matchPatternHasNamedBindings(option) {
			a.errorf(pattern.Pos(), "top-level or-pattern alternatives that bind names are not supported")
			return false
		}
	}
	wildcard := false
	baseScope := a.currentScope
	packedViews := a.currentPackedVariantViews
	for _, option := range pattern.Options {
		if option == nil {
			a.errorf(pattern.Pos(), "or-pattern alternative is missing")
			continue
		}
		// Each alternative is only a possible reason for entering the shared body. Analyze it in
		// an isolated child scope and discard branch-only refinements afterwards; otherwise the
		// last option could lend its enum narrowing or packed-payload view to executions that
		// matched an earlier option. Named bindings are rejected above until their branch merge is
		// modeled explicitly.
		analysisScope := NewScope(baseScope)
		a.currentPackedVariantViews = packedViews
		a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
		isWildcard := analyze(option, analysisScope)
		a.currentPackedVariantViews = packedViews
		if isWildcard {
			wildcard = true
		}
	}
	return wildcard
}

func (a *Analyzer) analyzeMatchStmt(stmt *ast.MatchStmt) {
	valueType := a.analyzeExpr(stmt.Value)
	enumType, _, ok := resolveMatchableEnumType(valueType)
	if ok {
		a.analyzeEnumMatchStmt(stmt, valueType, enumType)
		return
	}
	constEnumType, ok := resolveMatchableConstEnumType(valueType)
	if ok {
		a.analyzeConstEnumMatchStmt(stmt, valueType, constEnumType)
		return
	}
	errorSetType, ok := resolveMatchableErrorSetType(valueType)
	if ok {
		a.analyzeErrorSetMatchStmt(stmt, valueType, errorSetType)
		return
	}
	if optionalType, ok := valueType.(*OptionalType); ok {
		a.analyzeOptionalMatchStmt(stmt, optionalType)
		return
	}
	if isStringMatchableType(valueType) {
		a.analyzeStringMatchStmt(stmt, valueType)
		return
	}
	if isIntegerMatchableType(valueType) {
		a.analyzeIntegerMatchStmt(stmt, valueType)
		return
	}
	if _, ok := StripAggregateStateType(valueType).(*TupleType); ok {
		a.analyzeTupleMatchStmt(stmt, valueType)
		return
	}
	if _, ok := SequenceMatchElementType(valueType); ok {
		a.analyzeSequenceMatchStmt(stmt, valueType)
		return
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		a.analyzeStructMatchStmt(stmt, valueType)
		return
	}
	if !IsInvalidType(valueType) {
		a.errorf(stmt.Pos(), "match requires an enum, const enum, error set, optional, integer, string, tuple, sequence, or struct value, got %s", valueType)
	}
	for _, arm := range stmt.Arms {
		a.analyzeBlockWithRegionClone(arm.Body, NewScope(a.currentScope))
	}
}

func (a *Analyzer) analyzeExpectPatternStmt(stmt *ast.ExpectPatternStmt) {
	if stmt == nil {
		return
	}
	arms := make([]ast.MatchArm, 0, len(stmt.Patterns)+1)
	for _, pattern := range stmt.Patterns {
		arms = append(arms, ast.MatchArm{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.PassStmt{Position: pattern.Pos()}}})
	}
	arms = append(arms, ast.MatchArm{
		Position: stmt.Position,
		Pattern:  &ast.MatchWildcardPattern{Position: stmt.Position},
		Body:     []ast.Stmt{&ast.PanicStmt{Position: stmt.Position, Message: &ast.StringLit{Position: stmt.Position, Value: "expect pattern failed"}}},
	})
	a.analyzeMatchStmt(&ast.MatchStmt{Position: stmt.Position, Value: stmt.Value, Arms: arms})
	if len(stmt.Patterns) == 0 {
		return
	}
	valueType := a.exprType(stmt.Value)
	if valueType == nil {
		valueType = a.analyzeExpr(stmt.Value)
	}
	if len(stmt.Patterns) != 1 {
		for _, pattern := range stmt.Patterns {
			bindings := map[string]Type{}
			a.collectConditionStructPatternBindingTypes(pattern, valueType, bindings)
			if len(bindings) != 0 {
				a.errorf(pattern.Pos(), "blockless expect with multiple patterns cannot bind names")
				return
			}
		}
		return
	}
	a.bindExpectPatternLocals(a.currentScope, stmt.Patterns[0], valueType, stmt.Value)
}

func (a *Analyzer) bindExpectPatternLocal(scope *Scope, name string, typ Type, node ast.Node, pos lexer.Pos, valueExpr ast.Expr) {
	if a == nil || scope == nil || name == "" || name == "_" || typ == nil {
		return
	}
	if existing, ok := scope.Symbols[name]; ok {
		if !SameType(existing.Type, typ) {
			existing.Type = typ
			scope.symbolsVersion++
			a.symbolTypeEpoch++
		}
		if valueExpr != nil {
			a.recordValueBinding(existing, valueExpr)
			a.recordBorrowedOwnerRefBinding(existing, valueExpr)
			a.recordFunctionValueBinding(existing, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(existing, valueExpr)
			a.recordRegionRefBinding(existing, valueExpr)
			a.recordPatternStorageViewBinding(existing, valueExpr)
		}
		return
	}
	sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: node, Mutable: false}
	a.defineLocalInScope(scope, sym, pos)
	if valueExpr != nil {
		a.recordValueBinding(sym, valueExpr)
		a.recordBorrowedOwnerRefBinding(sym, valueExpr)
		a.recordFunctionValueBinding(sym, valueExpr)
		a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		a.recordRegionRefBinding(sym, valueExpr)
		a.recordPatternStorageViewBinding(sym, valueExpr)
	}
}

func (a *Analyzer) bindExpectPatternLocals(scope *Scope, pattern ast.MatchPattern, expected Type, valueExpr ast.Expr) {
	if a == nil || scope == nil || pattern == nil || expected == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if a.analyzePredicateMatchPattern(p, expected, valueExpr) {
			return
		}
		a.bindExpectPatternLocal(scope, p.Name, expected, p, p.Pos(), valueExpr)
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
			var fieldExpr ast.Expr
			if valueExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			}
			a.bindExpectPatternLocals(scope, arg.Pattern, fields[i].Type, fieldExpr)
		}
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			var indexExpr ast.Expr
			if valueExpr != nil {
				indexExpr = &ast.IndexExpr{
					Position: elem.Pos(),
					Object:   valueExpr,
					Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i)},
				}
			}
			a.bindExpectPatternLocals(scope, elem, elemType, indexExpr)
		}
	case *ast.MatchOrPattern:
		if _, ok := a.collectOrPatternBindingTypes(p, expected); !ok || len(p.Options) == 0 {
			return
		}
		a.bindExpectPatternLocals(scope, p.Options[0], expected, valueExpr)
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
				payloadExpr, restore := a.enterVariantPayloadPattern(valueExpr, p, moveBindVariantFieldKey(variant, i))
				a.bindExpectPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
				restore()
			}
		}
	}
}
