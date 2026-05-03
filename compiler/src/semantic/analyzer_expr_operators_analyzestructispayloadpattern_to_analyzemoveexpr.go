package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

func (a *Analyzer) analyzeStructIsPayloadPattern(pattern ast.MatchPattern, expected Type) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		return
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "struct is field pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "struct is field pattern")
	case *ast.MatchVariantPattern:
		a.analyzeVariantIsPayloadPattern(p, expected)
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.analyzeStructIsPayloadPattern(arg.Pattern, fields[i].Type)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported struct is field pattern %T", pattern)
	}
}
func (a *Analyzer) resolveEnumVariantIsTarget(expr ast.Expr) (*EnumType, *EnumVariant, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.resolveEnumVariantIsTarget(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, false
		}
		base, _, ok := a.lookupVisibleType(testExpr.Pattern.EnumName)
		if !ok {
			return nil, nil, false
		}
		enumType, ok := base.(*EnumType)
		if !ok || enumType == nil {
			return nil, nil, false
		}
		variant, ok := enumType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return enumType, nil, false
		}
		return enumType, variant, true
	}
	if fieldExpr, ok := isEnumVariantExpr(expr); ok {
		enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
		if ok && variant != nil {
			return enumType, variant, true
		}
		return nil, nil, false
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	return a.enumVariantTargetFromNamedType(named)
}
func (a *Analyzer) resolveTreeVariantIsTarget(expr ast.Expr) (*TreeCategoryType, *EnumVariant, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.resolveTreeVariantIsTarget(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, false
		}
		base, _, ok := a.lookupVisibleType(testExpr.Pattern.EnumName)
		if !ok {
			return nil, nil, false
		}
		treeType, ok := base.(*TreeCategoryType)
		if !ok || treeType == nil {
			return nil, nil, false
		}
		variant, ok := treeType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return treeType, nil, false
		}
		return treeType, variant, true
	}
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(fieldExpr)
		if ok && variant != nil {
			return treeType, variant, true
		}
		return nil, nil, false
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	return a.treeVariantTargetFromNamedType(named)
}
func (a *Analyzer) enumVariantTargetFromNamedType(named *ast.NamedType) (*EnumType, *EnumVariant, bool) {
	if named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	enumName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := a.lookupVisibleType(enumName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok || variant == nil {
		return enumType, nil, false
	}
	return enumType, variant, true
}
func (a *Analyzer) treeVariantTargetFromNamedType(named *ast.NamedType) (*TreeCategoryType, *EnumVariant, bool) {
	if named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	treeName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := a.lookupVisibleType(treeName)
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*TreeCategoryType)
	if !ok || treeType == nil {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(variantName)
	if !ok || variant == nil {
		return treeType, nil, false
	}
	return treeType, variant, true
}
func resolveMatchableTreeCategoryType(actual Type) (*TreeCategoryType, *TreeVariantViewType, bool) {
	actual = StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *TreeCategoryType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, nil, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, nil, false
		}
		return tt.Category, tt, true
	default:
		return nil, nil, false
	}
}
func (a *Analyzer) resolveNamedStateIsTarget(expr ast.Expr) (*StructType, Type, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.resolveNamedStateIsTarget(paren.Inner)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	if named, ok := typedExpr.Type.(*ast.NamedType); ok && named != nil {
		if idx := strings.LastIndex(named.Name, "."); idx > 0 {
			if base, _, ok := a.lookupVisibleType(named.Name[:idx]); ok {
				switch base.(type) {
				case *EnumType, *TreeCategoryType:
					return nil, nil, false
				}
			}
		}
	}
	resolved := a.resolveType(typedExpr.Type)
	base, ok := namedStateStructBase(resolved)
	if !ok || base == nil {
		return nil, nil, false
	}
	stateArg, ok := namedStateCurrentArg(resolved)
	if !ok || stateArg == nil {
		return nil, nil, false
	}
	return base, stateArg, true
}
func isEnumVariantExpr(expr ast.Expr) (*ast.FieldExpr, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return isEnumVariantExpr(n.Inner)
	case *ast.FieldExpr:
		if _, ok := qualifiedTypePathFromExpr(n.Object); ok {
			return n, true
		}
	}
	return nil, false
}
func (a *Analyzer) analyzeUnaryExpr(expr *ast.UnaryExpr) Type {
	operand := a.analyzeExpr(expr.Operand)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		if !IsBoolType(operand) {
			a.errorf(expr.Pos(), "not operator requires bool operand")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_MINUS, lexer.TOKEN_TILDE:
		if !IsNumericType(operand) {
			a.errorf(expr.Pos(), "unary operator requires numeric operand")
		}
		if expr.Op == lexer.TOKEN_TILDE && !IsIntegralStorageType(operand) {
			a.errorf(expr.Pos(), "unary operator requires integral operand")
		}
		return operand
	case lexer.TOKEN_BANG:
		idType, ok := operand.(*IDType)
		if !ok {
			a.errorf(expr.Pos(), "id unwrap operator requires id[T] operand, got %s", operand)
			return invalidType
		}
		return idType.Storage
	default:
		return invalidType
	}
}
func (a *Analyzer) analyzeMoveExpr(expr *ast.MoveExpr) Type {
	if expr == nil {
		return invalidType
	}
	return a.analyzeExpr(expr.Operand)
}
