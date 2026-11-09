package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) analyzeIsExpr(expr *ast.BinaryExpr) Type {
	// docs/85 §2: `subject is Law` is law application — UFCS first-arg binding. Desugar to the
	// call `Law(subject)` and reuse all call analysis/codegen. Bare-law single-target form.
	if a.tryAnalyzeLawIsExpr(expr) {
		return a.namedTypes["bool"]
	}
	left := a.analyzeExpr(expr.Left)
	targets := flattenIsTargetExprs(expr.Right)
	for _, target := range targets {
		if a.rejectDotModulePathIsTarget(target) {
			continue
		}
		if enumType, variant, ok := a.resolveEnumVariantIsTarget(target); ok {
			if _, _, ok := resolveMatchableEnumType(left); !ok {
				a.errorf(expr.Left.Pos(), "is requires an enum value for variant tests, got %s", left)
				continue
			}
			matchableEnum, _, _ := resolveMatchableEnumType(left)
			// docs/77: `e is BinaryExpression.Add` is valid when the tested enum is the scrutinee's
			// enum OR any refinement of it (BinaryExpression <: Expression). enumDescendsFrom collapses
			// to name-equality for a plain enum, so this is a strict generalization.
			if matchableEnum == nil || enumType == nil || !enumDescendsFrom(enumType, matchableEnum) {
				expected := "<invalid>"
				if matchableEnum != nil {
					expected = matchableEnum.Name
				}
				got := "<invalid>"
				if enumType != nil && variant != nil {
					got = enumType.Name + "." + variant.Name
				}
				a.errorf(expr.Pos(), "is expects a variant of enum %q, got %s", expected, got)
			}
			if pattern, ok := a.enumVariantIsTargetPattern(target, enumType, variant); ok && pattern != nil {
				a.validateEnumVariantIsTargetPattern(pattern, variant)
			}
			continue
		}
		if pattern, ok := a.structIsTargetPattern(target); ok && pattern != nil {
			a.validateStructIsTargetPattern(pattern, left)
			continue
		}
		// docs/77: a bare-category test `e is BinaryExpression` (no `.Variant`) on a hierarchy
		// scrutinee. Gated to hierarchical enums so flat-enum `is` behavior is unchanged.
		if matchableEnum, _, mok := resolveMatchableEnumType(left); mok && enumIsHierarchical(matchableEnum) {
			if category, ok := a.resolveEnumCategoryIsTarget(target); ok {
				if !enumDescendsFrom(category, matchableEnum) && !enumDescendsFrom(matchableEnum, category) {
					a.errorf(expr.Pos(), "is expects %q or a refinement of it, got unrelated enum %q", matchableEnum.Name, category.Name)
				}
				continue
			}
		}
		if targetBase, _, ok := a.resolveNamedStateIsTarget(target); ok {
			leftBase, ok := namedStateStructBase(left)
			if !ok || leftBase == nil {
				a.errorf(expr.Left.Pos(), "is requires a named-state struct value for type-state tests, got %s", left)
				continue
			}
			if leftBase.Name != targetBase.Name {
				a.errorf(expr.Pos(), "is expects a state of struct %q, got state target for %q", leftBase.Name, targetBase.Name)
			}
			continue
		}
		a.analyzeIsComparableTarget(left, target)
	}
	return a.namedTypes["bool"]
}

// resolveEnumCategoryIsTarget recognizes a bare enum-category target in an `is` test (docs/77): the
// target is a plain type name (no `.Variant`) that resolves to an enum. Returns that enum.
func (a *Analyzer) resolveEnumCategoryIsTarget(expr ast.Expr) (*EnumType, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		if e == nil {
			return nil, false
		}
		return a.resolveEnumCategoryIsTarget(e.Inner)
	case *ast.IsAliasExpr:
		if e == nil {
			return nil, false
		}
		return a.resolveEnumCategoryIsTarget(e.Target)
	case *ast.TypeExprExpr:
		if e == nil {
			return nil, false
		}
		named, ok := e.Type.(*ast.NamedType)
		if !ok || named == nil || strings.Contains(named.Name, ".") {
			return nil, false // dotted ⇒ a variant target, handled elsewhere
		}
		base, _, ok := a.lookupVisibleType(named.Name)
		if !ok {
			return nil, false
		}
		enumType, ok := base.(*EnumType)
		return enumType, ok && enumType != nil
	case *ast.Ident:
		if e == nil || e.Name == "" {
			return nil, false
		}
		base, _, ok := a.lookupVisibleType(e.Name)
		if !ok {
			return nil, false
		}
		enumType, ok := base.(*EnumType)
		return enumType, ok && enumType != nil
	}
	return nil, false
}

func (a *Analyzer) structIsTargetPattern(expr ast.Expr) (*ast.MatchStructPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.structIsTargetPattern(paren.Inner)
	}
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return a.structIsTargetPattern(alias.Target)
	}
	if testExpr, ok := expr.(*ast.StructTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	return nil, false
}
func (a *Analyzer) validateStructIsTargetPattern(pattern *ast.MatchStructPattern, actual Type) {
	if pattern == nil {
		return
	}
	fields, orderedArgs, ok := a.resolveMatchStructPattern(pattern, actual)
	if !ok {
		return
	}
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		a.analyzeStructIsPayloadPattern(arg.Pattern, fields[i].Type)
	}
}
func (a *Analyzer) enumVariantIsTargetPattern(expr ast.Expr, enumType *EnumType, variant *EnumVariant) (*ast.MatchVariantPattern, bool) {
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return a.enumVariantIsTargetPattern(alias.Target, enumType, variant)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	if enumType == nil || variant == nil {
		return nil, false
	}
	return &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: enumType.Name, Variant: variant.Name}, true
}
func (a *Analyzer) validateEnumVariantIsTargetPattern(pattern *ast.MatchVariantPattern, variant *EnumVariant) {
	if pattern == nil || variant == nil {
		return
	}
	orderedArgs := a.resolvePartialMatchPatternArgs(pattern, variant, pattern.EnumName+"."+pattern.Variant, false)
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		a.analyzeVariantIsPayloadPattern(arg.Pattern, variant.Payload[i])
	}
}
func (a *Analyzer) analyzeVariantIsPayloadPattern(pattern ast.MatchPattern, expected Type) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		return
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "variant is payload pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "variant is payload pattern")
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			a.analyzeVariantIsPayloadPattern(option, expected)
		}
	case *ast.MatchStructPattern:
		a.analyzeStructIsPayloadPattern(p, expected)
	case *ast.MatchVariantPattern:
		switch target := expected.(type) {
		case *EnumType:
			p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, target.Name)
			if p.EnumName != target.Name {
				a.errorf(p.Pos(), "nested variant is pattern expects enum %q, got %q", target.Name, p.EnumName)
				return
			}
			variant, ok := target.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", target.Name, p.Variant)
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, target.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.analyzeVariantIsPayloadPattern(arg.Pattern, variant.Payload[i])
			}
		default:
			a.errorf(p.Pos(), "nested variant is pattern %q requires an enum payload, got %s", p.EnumName+"."+p.Variant, expected)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported variant is payload pattern %T", pattern)
	}
}
func (a *Analyzer) analyzeEnumIsPayloadPattern(pattern ast.MatchPattern, expected Type) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		return
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "variant is payload pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "variant is payload pattern")
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			a.analyzeEnumIsPayloadPattern(option, expected)
		}
	case *ast.MatchStructPattern:
		a.analyzeStructIsPayloadPattern(p, expected)
	case *ast.MatchVariantPattern:
		enumType, ok := expected.(*EnumType)
		if !ok {
			a.errorf(p.Pos(), "nested variant is pattern %q requires an enum payload, got %s", p.EnumName+"."+p.Variant, expected)
			return
		}
		p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, enumType.Name)
		if p.EnumName != enumType.Name {
			a.errorf(p.Pos(), "nested variant is pattern expects enum %q, got %q", enumType.Name, p.EnumName)
			return
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
			return
		}
		orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, enumType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.analyzeEnumIsPayloadPattern(arg.Pattern, variant.Payload[i])
		}
	default:
		a.errorf(pattern.Pos(), "unsupported variant is payload pattern %T", pattern)
	}
}

// plainStructValueType reports the struct type when t is a struct VALUE — not a reference to
// one, and not a runtime string (sview/dstr/cstr are struct-shaped but content-compare
// through their own path, which runs before this).
func plainStructValueType(t Type) *StructType {
	if runtimeStringKindOf(t) != runtimeStringNone {
		return nil
	}
	switch v := t.(type) {
	case *StructType:
		return v
	case *GenericInstanceType:
		if st, ok := v.Base.(*StructType); ok {
			return st
		}
	}
	return nil
}

// comparesNarrowedOptionalAgainstNull reports whether one side of an equality is `null`
// and the other is a place whose DECLARED type is optional but which an assignment
// narrowed to the payload type. See Scope.narrowedOptionals.
func (a *Analyzer) comparesNarrowedOptionalAgainstNull(expr *ast.BinaryExpr, left Type, right Type) bool {
	if a.currentScope == nil {
		return false
	}
	var place ast.Expr
	switch {
	case IsNullType(right):
		place = expr.Left
	case IsNullType(left):
		place = expr.Right
	default:
		return false
	}
	key, ok := exprRefinementKey(place)
	if !ok {
		return false
	}
	_, narrowed := a.currentScope.LookupNarrowedOptional(key)
	return narrowed
}

func shortCircuitPrimitiveType(t Type) bool {
	t = valueContextOperandType(t)
	return t != nil && (IsNumericType(t) || IsBoolType(t))
}

// Speculative return inference must not rebind a declaration's constant reads
// through caller locals. Ordinary reanalysis removes an obsolete annotation.
func (a *Analyzer) recordResolvedPrimitiveConstantName(ident *ast.Ident, symbol *Symbol) {
	if a == nil || a.suppressDiagnostics || a.resolvedValueNames == nil || a.globalScope == nil {
		return
	}
	if symbol != nil && symbol.Kind == SymbolConst {
		if global, known := a.globalScope.Lookup(symbol.Name); known && global == symbol {
			a.resolvedValueNames[ident] = symbol.Name
			return
		}
	}
	if name, annotated := a.resolvedValueNames[ident]; annotated {
		if prior, known := a.globalScope.Lookup(name); known && prior.Kind == SymbolConst {
			delete(a.resolvedValueNames, ident)
		}
	}
}
