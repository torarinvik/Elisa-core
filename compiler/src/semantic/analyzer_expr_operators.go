package semantic

import (
	"strconv"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeBinaryExpr(expr *ast.BinaryExpr) Type {
	if expr.Op == lexer.TOKEN_IS {
		return a.analyzeIsExpr(expr)
	}
	if expr.Op == lexer.TOKEN_IN {
		return a.analyzeMembershipExpr(expr)
	}
	leftShorthand, leftIsShorthand := contextualShorthandExpr(expr.Left)
	rightShorthand, rightIsShorthand := contextualShorthandExpr(expr.Right)
	var left Type
	var right Type
	switch {
	case leftIsShorthand && !rightIsShorthand:
		right = a.analyzeExpr(expr.Right)
		left = a.analyzeValueExpr(expr.Left, right)
	case rightIsShorthand && !leftIsShorthand:
		left = a.analyzeExpr(expr.Left)
		right = a.analyzeValueExpr(expr.Right, left)
	default:
		if leftIsShorthand && leftShorthand != nil {
			left = a.analyzeShorthandMemberExpr(leftShorthand, nil)
		} else {
			left = a.analyzeExpr(expr.Left)
		}
		if rightIsShorthand && rightShorthand != nil {
			right = a.analyzeShorthandMemberExpr(rightShorthand, nil)
		} else {
			right = a.analyzeExpr(expr.Right)
		}
	}
	switch expr.Op {
	case lexer.TOKEN_AND, lexer.TOKEN_OR:
		if !IsBoolType(left) || !IsBoolType(right) {
			a.errorf(expr.Pos(), "logical operator requires bool operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		if !typesComparableForEquality(left, right) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left, right)
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		if lref, ok := left.(*RefType); ok && IsIntegralStorageType(right) {
			return lref
		}
		if expr.Op == lexer.TOKEN_PLUS {
			if rref, ok := right.(*RefType); ok && IsIntegralStorageType(left) {
				return rref
			}
			if result, ok := a.analyzeSpanAlgebraExpr(expr, left, right); ok {
				return result
			}
		}
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	case lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
		lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		requiresIntegral := expr.Op == lexer.TOKEN_PERCENT || expr.Op == lexer.TOKEN_CARET || expr.Op == lexer.TOKEN_PIPE || expr.Op == lexer.TOKEN_AMPERSAND || expr.Op == lexer.TOKEN_LSHIFT || expr.Op == lexer.TOKEN_RSHIFT
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		if requiresIntegral && (!IsIntegralStorageType(left) || !IsIntegralStorageType(right)) {
			a.errorf(expr.Pos(), "operator requires integral operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeSpanAlgebraExpr(expr *ast.BinaryExpr, left Type, right Type) (Type, bool) {
	if expr == nil || expr.Op != lexer.TOKEN_PLUS {
		return nil, false
	}
	if left == nil || right == nil {
		return nil, false
	}
	if IsNumericType(left) || IsNumericType(right) || !SameType(left, right) {
		return nil, false
	}
	if result, ok := a.analyzeSpanLikeProtocolExpr(expr, left); ok {
		return result, true
	}
	helperName := ""
	switch left.String() {
	case "Span":
		helperName = "combine_span"
	case "LuaSpan":
		helperName = "lua_span_union"
	default:
		if _, _, ok := a.lookupVisibleGlobal("combine_span"); ok {
			helperName = "combine_span"
		} else {
			return nil, false
		}
	}
	call := &ast.CallExpr{
		Position: expr.Position,
		Func:     &ast.Ident{Position: expr.Position, Name: helperName},
		Args:     []ast.Expr{expr.Left, expr.Right},
	}
	result := a.analyzeExpr(call)
	if !IsInvalidType(result) && !AssignableTo(left, result) {
		a.errorf(expr.Pos(), "span algebra helper %q returned %s, expected %s", helperName, result, left)
		return invalidType, true
	}
	expr.LoweredCall = call
	return result, true
}

func (a *Analyzer) analyzeSpanLikeProtocolExpr(expr *ast.BinaryExpr, spanType Type) (Type, bool) {
	if a == nil || expr == nil || spanType == nil {
		return nil, false
	}
	iface, interfaceName, ok := a.lookupVisibleStaticInterface("SpanLike")
	if !ok || iface == nil {
		return nil, false
	}
	impl, ok := LookupStaticImpl(a.staticImpls, interfaceName, spanType)
	if !ok || impl == nil {
		return nil, false
	}
	if _, ok := impl.Methods["combine"]; !ok {
		return nil, false
	}
	typePath := staticTypeExprForType(expr.Position, spanType)
	if typePath == nil {
		return nil, false
	}
	field := &ast.FieldExpr{
		Position: expr.Position,
		Object:   typePath,
		Field:    "combine",
	}
	call := &ast.CallExpr{
		Position: expr.Position,
		Func:     field,
		Args:     []ast.Expr{expr.Left, expr.Right},
	}
	result := a.analyzeExpr(call)
	if !IsInvalidType(result) && !AssignableTo(spanType, result) {
		a.errorf(expr.Pos(), "SpanLike.combine returned %s, expected %s", result, spanType)
		return invalidType, true
	}
	expr.LoweredCall = call
	return result, true
}

func staticTypeExprForType(pos lexer.Pos, typ Type) ast.Expr {
	if typ == nil {
		return nil
	}
	name := typ.String()
	if name == "" || strings.ContainsAny(name, "[]&*? ") {
		return nil
	}
	parts := strings.Split(name, ".")
	var expr ast.Expr
	for _, part := range parts {
		if part == "" {
			return nil
		}
		if expr == nil {
			expr = &ast.Ident{Position: pos, Name: part}
			continue
		}
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: part}
	}
	return expr
}

func (a *Analyzer) analyzeMembershipExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	resultType := a.namedTypes["bool"]
	list, ok := expr.Right.(*ast.ListLitExpr)
	if !ok || list == nil {
		right := a.analyzeExpr(expr.Right)
		a.errorf(expr.Right.Pos(), "membership operator currently requires a list literal on the right-hand side, got %s", right)
		return resultType
	}

	var elemType Type
	for _, elem := range list.Elems {
		itemType := a.analyzeValueExpr(elem, left)
		if !typesComparableForEquality(left, itemType) {
			a.errorf(elem.Pos(), "cannot compare %s against membership candidate %s", left, itemType)
			continue
		}
		a.consumeAffineValueExpr(elem, itemType, "move into membership candidate")
		if elemType == nil {
			elemType = itemType
			continue
		}
		merged := MergeTypes(elemType, itemType)
		if IsInvalidType(merged) {
			merged = left
		}
		if !IsInvalidType(merged) {
			elemType = merged
		}
	}
	if elemType == nil {
		elemType = left
	}
	a.recordAnalyzedExprType(list, &ArrayType{Elem: elemType, Size: strconv.Itoa(len(list.Elems)), HasConstSize: true, ConstSize: int64(len(list.Elems))})
	return resultType
}

func typesComparableForEquality(left Type, right Type) bool {
	if runtimeStringComparable(left, right) {
		return true
	}
	if IsNumericType(left) && IsNumericType(right) {
		return true
	}
	return AssignableTo(left, right) || AssignableTo(right, left) || refsComparableIgnoringMutability(left, right) || (IsNullType(left) && isRefLike(right)) || (IsNullType(right) && isRefLike(left))
}

func refsComparableIgnoringMutability(left Type, right Type) bool {
	leftRef, ok := left.(*RefType)
	if !ok || leftRef == nil {
		return false
	}
	rightRef, ok := right.(*RefType)
	if !ok || rightRef == nil {
		return false
	}
	leftClone := cloneRefType(leftRef)
	rightClone := cloneRefType(rightRef)
	leftClone.Mutable = false
	rightClone.Mutable = false
	return AssignableTo(leftClone, rightClone) || AssignableTo(rightClone, leftClone)
}

func appendIsTargetExprs(out []ast.Expr, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return out
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return appendIsTargetExprs(out, n.Inner)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			out = appendIsTargetExprs(out, target)
		}
		return out
	default:
		return append(out, expr)
	}
}

func flattenIsTargetExprs(expr ast.Expr) []ast.Expr {
	return appendIsTargetExprs(nil, expr)
}

func isComparableValueTarget(left Type, right Type) bool {
	return runtimeStringComparable(left, right) ||
		(IsNumericType(left) && IsNumericType(right)) ||
		AssignableTo(left, right) ||
		AssignableTo(right, left) ||
		refsComparableIgnoringMutability(left, right) ||
		(IsNullType(left) && isRefLike(right)) ||
		(IsNullType(right) && isRefLike(left))
}

func (a *Analyzer) analyzeIsComparableTarget(left Type, target ast.Expr) bool {
	if typedExpr, ok := target.(*ast.TypeExprExpr); ok && typedExpr != nil && typedExpr.Type != nil {
		a.resolveType(typedExpr.Type)
		a.errorf(target.Pos(), "is target must be a variant, a named-state target, or a comparable value")
		return false
	}
	targetShorthand, targetIsShorthand := contextualShorthandExpr(target)
	var right Type
	if targetIsShorthand && targetShorthand != nil {
		right = a.analyzeValueExpr(target, left)
	} else {
		right = a.analyzeExpr(target)
	}
	if !isComparableValueTarget(left, right) {
		a.errorf(target.Pos(), "is expects a comparable value alternative, got %s", right)
		return false
	}
	return true
}

func (a *Analyzer) analyzeIsExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	targets := flattenIsTargetExprs(expr.Right)
	for _, target := range targets {
		if enumType, variant, ok := a.resolveEnumVariantIsTarget(target); ok {
			if _, _, ok := resolveMatchableEnumType(left); !ok {
				a.errorf(expr.Left.Pos(), "is requires an enum value for variant tests, got %s", left)
				continue
			}
			matchableEnum, _, _ := resolveMatchableEnumType(left)
			if matchableEnum == nil || enumType == nil || matchableEnum.Name != enumType.Name {
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
		if treeType, variant, ok := a.resolveTreeVariantIsTarget(target); ok {
			if _, _, ok := resolveMatchableTreeCategoryType(left); !ok {
				a.errorf(expr.Left.Pos(), "is requires an enum or tree-category value for variant tests, got %s", left)
				continue
			}
			matchableTree, _, _ := resolveMatchableTreeCategoryType(left)
			if matchableTree == nil || treeType == nil || matchableTree.Name != treeType.Name {
				expected := "<invalid>"
				if matchableTree != nil {
					expected = matchableTree.Name
				}
				got := "<invalid>"
				if treeType != nil && variant != nil {
					got = treeType.Name + "." + variant.Name
				}
				a.errorf(expr.Pos(), "is expects a variant of tree category %q, got %s", expected, got)
			}
			if pattern, ok := a.treeVariantIsTargetPattern(target, treeType, variant); ok && pattern != nil {
				a.validateTreeVariantIsTargetPattern(pattern, treeType, variant)
			}
			continue
		}
		if pattern, ok := a.structIsTargetPattern(target); ok && pattern != nil {
			a.validateStructIsTargetPattern(pattern, left)
			continue
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

func (a *Analyzer) structIsTargetPattern(expr ast.Expr) (*ast.MatchStructPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.structIsTargetPattern(paren.Inner)
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

func (a *Analyzer) treeVariantIsTargetPattern(expr ast.Expr, treeType *TreeCategoryType, variant *EnumVariant) (*ast.MatchVariantPattern, bool) {
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	if treeType == nil || variant == nil {
		return nil, false
	}
	return &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: treeType.Name, Variant: variant.Name}, true
}

func (a *Analyzer) validateTreeVariantIsTargetPattern(pattern *ast.MatchVariantPattern, treeType *TreeCategoryType, variant *EnumVariant) {
	if pattern == nil || treeType == nil || variant == nil {
		return
	}
	orderedArgs := a.resolvePartialMatchPatternArgs(pattern, variant, treeType.Name+"."+pattern.Variant, false)
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
	case *ast.MatchStructPattern:
		a.analyzeStructIsPayloadPattern(p, expected)
	case *ast.MatchVariantPattern:
		switch target := expected.(type) {
		case *EnumType:
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
		case *TreeCategoryType:
			if p.EnumName != target.Name {
				a.errorf(p.Pos(), "nested variant is pattern expects tree category %q, got %q", target.Name, p.EnumName)
				return
			}
			variant, ok := target.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "tree category %q has no variant %q", target.Name, p.Variant)
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
			a.errorf(p.Pos(), "nested variant is pattern %q requires an enum or tree-category payload, got %s", p.EnumName+"."+p.Variant, expected)
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
	case *ast.MatchStructPattern:
		a.analyzeStructIsPayloadPattern(p, expected)
	case *ast.MatchVariantPattern:
		enumType, ok := expected.(*EnumType)
		if !ok {
			a.errorf(p.Pos(), "nested variant is pattern %q requires an enum payload, got %s", p.EnumName+"."+p.Variant, expected)
			return
		}
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
