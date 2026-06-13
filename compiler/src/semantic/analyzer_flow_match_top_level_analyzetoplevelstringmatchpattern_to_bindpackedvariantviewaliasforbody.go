package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

func (a *Analyzer) analyzeTopLevelStringMatchPattern(pattern ast.MatchPattern, valueType Type, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchStringLiteralPattern:
		if !isStringMatchableType(valueType) {
			a.errorf(p.Pos(), "match arm expects a string value, got %s", valueType)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level string match arm must use a string literal or _")
		return false
	case *ast.MatchVariantPattern:
		a.errorf(p.Pos(), "top-level string match arm must use a string literal or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}
// analyzeTopLevelIntegerMatchPattern validates one arm of an integer `match`. Each arm must be an
// integer-literal pattern (`0xA9:`) or the wildcard `_`. The literal is analyzed against the
// scrutinee type so it lowers at the scrutinee's width (a bare `0xA9` becomes a u8 when matching a
// u8), and must be assignable to it. Returns true for the wildcard arm.
func (a *Analyzer) analyzeTopLevelIntegerMatchPattern(pattern ast.MatchPattern, valueType Type, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchLiteralPattern:
		armType := a.analyzeValueExpr(p.Value, valueType)
		if !IsIntegralType(StripAggregateStateType(armType)) {
			a.errorf(p.Pos(), "integer match arm expects an integer literal, got %s", armType)
		} else if !AssignableTo(valueType, armType) {
			a.errorf(p.Pos(), "integer match arm value of type %s is not assignable to the matched %s", armType, valueType)
		}
		return false
	case *ast.MatchStringLiteralPattern:
		a.errorf(p.Pos(), "top-level integer match arm must use an integer literal or _")
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level integer match arm must use an integer literal or _")
		return false
	case *ast.MatchVariantPattern:
		a.errorf(p.Pos(), "top-level integer match arm must use an integer literal or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeTopLevelStructMatchPattern(pattern ast.MatchPattern, valueType Type, valueExpr ast.Expr, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, valueType)
		if !ok {
			return false
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			a.analyzeNestedMatchPattern(arg.Pattern, fields[i].Type, fieldExpr, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level struct match arm must use Struct(...), or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported top-level struct match pattern %T", pattern)
		return false
	}
}
func (a *Analyzer) analyzeTopLevelTupleMatchPattern(pattern ast.MatchPattern, valueType Type, valueExpr ast.Expr, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchTuplePattern:
		fields, ok := a.resolveMatchTuplePattern(p, valueType)
		if !ok {
			return false
		}
		limit := len(p.Elems)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			fieldExpr := &ast.FieldExpr{Position: p.Elems[i].Pos(), Object: valueExpr, Field: fields[i].Name}
			a.analyzeNestedMatchPattern(p.Elems[i], fields[i].Type, fieldExpr, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level tuple match arm must use a tuple pattern or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported top-level tuple match pattern %T", pattern)
		return false
	}
}
func (a *Analyzer) analyzeTopLevelSequenceMatchPattern(pattern ast.MatchPattern, valueType Type, valueExpr ast.Expr, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(valueType)
		if !ok {
			a.errorf(p.Pos(), "list pattern requires an array, darray, view, or string-like value, got %s", valueType)
			return false
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				if i != len(p.Elems)-1 {
					a.errorf(elem.Pos(), "list pattern rest marker must be the final element")
				}
				continue
			}
			indexExpr := &ast.IndexExpr{
				Position: elem.Pos(),
				Object:   valueExpr,
				Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i), Suffix: "u"},
			}
			a.analyzeNestedMatchPattern(elem, elemType, indexExpr, scope)
		}
		return false
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, valueType) {
			return false
		}
		a.errorf(pattern.Pos(), "unsupported top-level sequence match pattern %T", pattern)
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level sequence match arm must use a list pattern or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported top-level sequence match pattern %T", pattern)
		return false
	}
}
func (a *Analyzer) reportNonExhaustiveStructMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive struct match expression; add a final _ arm")
}
func (a *Analyzer) reportNonExhaustiveTupleMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive tuple match expression; add a final _ arm")
}
func (a *Analyzer) bindPackedVariantViewAliasForBody(pattern ast.MatchPattern, enumType *EnumType, valueExpr ast.Expr, body []ast.Stmt, scope *Scope) {
	if a == nil || scope == nil || enumType == nil || !matchBodyReferencesVariantFields(body, valueExpr) {
		return
	}
	variantPattern, ok := pattern.(*ast.MatchVariantPattern)
	if !ok || variantPattern == nil || variantPattern.EnumName != enumType.Name {
		return
	}
	variant, ok := enumType.Variant(variantPattern.Variant)
	if !ok || variant == nil {
		return
	}
	ident, ok := unwrapPackedVariantViewExpr(valueExpr).(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	if _, exists := scope.Symbols[ident.Name]; exists {
		return
	}
	var aliasOf *Symbol
	if scope.Parent != nil {
		aliasOf, _ = scope.Parent.Lookup(ident.Name)
	}
	viewType := &PackedVariantViewType{Enum: enumType, Variant: variant}
	sym := &Symbol{Name: ident.Name, Kind: SymbolLocal, Type: viewType, Node: variantPattern, AliasOf: aliasOf, Mutable: false}
	a.defineLocalInScope(scope, sym, variantPattern.Pos())
	if a.currentPackedVariantViews != nil {
		a.currentPackedVariantViews[sym] = viewType
	}
}
