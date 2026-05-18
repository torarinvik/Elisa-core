package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
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
		left = valueContextOperandType(left)
		right = valueContextOperandType(right)
		if !IsBoolType(left) || !IsBoolType(right) {
			a.errorf(expr.Pos(), "logical operator requires bool operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		compareLeft := left
		compareRight := right
		if !typesComparableForEquality(compareLeft, compareRight) && !IsNullType(left) && !IsNullType(right) {
			valueLeft := valueContextOperandType(left)
			valueRight := valueContextOperandType(right)
			if typesComparableForEquality(valueLeft, valueRight) {
				compareLeft = valueLeft
				compareRight = valueRight
			}
		}
		if !typesComparableForEquality(compareLeft, compareRight) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left, right)
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		left = valueContextOperandType(left)
		right = valueContextOperandType(right)
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		left = mutableScalarRefValueContextOperandType(left)
		right = mutableScalarRefValueContextOperandType(right)
		if lref, ok := left.(*RefType); ok && IsIntegralStorageType(right) {
			if a.enforceUnsafePermissions {
				a.recordFunctionPermissionRefs(unsafePointerArithmeticRefs(expr.Position))
			}
			return lref
		}
		if expr.Op == lexer.TOKEN_PLUS {
			if rref, ok := right.(*RefType); ok && IsIntegralStorageType(left) {
				if a.enforceUnsafePermissions {
					a.recordFunctionPermissionRefs(unsafePointerArithmeticRefs(expr.Position))
				}
				return rref
			}
			if result, ok := a.analyzeSpanAlgebraExpr(expr, left, right); ok {
				return result
			}
		}
		left = valueContextOperandType(left)
		right = valueContextOperandType(right)
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	case lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
		lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		left = valueContextOperandType(left)
		right = valueContextOperandType(right)
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

func valueContextOperandType(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil {
		if IsNumericType(ref.Elem) || IsBoolType(ref.Elem) {
			return ref.Elem
		}
	}
	return t
}

func mutableScalarRefValueContextOperandType(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil {
		if ref.Mutable && (IsNumericType(ref.Elem) || IsBoolType(ref.Elem)) && !isBytePointerArithmeticRef(ref) {
			return ref.Elem
		}
	}
	return t
}

func isBytePointerArithmeticRef(ref *RefType) bool {
	if ref == nil {
		return false
	}
	if builtin, ok := ref.Elem.(*BuiltinType); ok && builtin != nil {
		return builtin.Name == "u8"
	}
	return false
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
	list, ok := a.membershipCandidateList(expr.Right)
	if !ok || list == nil {
		right := a.analyzeExpr(expr.Right)
		if _, isPackedStore := right.(*PackedEnumStoreType); isPackedStore {
			a.errorf(expr.Right.Pos(), "if pattern binder requires `as Enum.Variant(...)` after store expression")
			return resultType
		}
		a.errorf(expr.Right.Pos(), "membership operator requires a list literal or tokenset on the right-hand side, got %s", right)
		return resultType
	}
	expr.Right = list

	var elemType Type
	for i, elem := range list.Elems {
		if i < len(list.Spreads) && list.Spreads[i] {
			a.errorf(elem.Pos(), "membership candidate lists do not support spread elements")
			continue
		}
		if rangeExpr, ok := elem.(*ast.MembershipRangeExpr); ok {
			itemType := a.analyzeMembershipRangeExpr(rangeExpr, left)
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
			continue
		}
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
func (a *Analyzer) analyzeMembershipRangeExpr(expr *ast.MembershipRangeExpr, left Type) Type {
	if expr == nil {
		return invalidType
	}
	if ce, ok := left.(*ConstEnumType); ok && ce != nil {
		if ident, ok := expr.Start.(*ast.Ident); ok {
			if _, ok := ce.Member(ident.Name); ok {
				expr.Start = &ast.ShorthandMemberExpr{Position: ident.Position, Parts: []string{ident.Name}}
			}
		}
		if ident, ok := expr.End.(*ast.Ident); ok {
			if _, ok := ce.Member(ident.Name); ok {
				expr.End = &ast.ShorthandMemberExpr{Position: ident.Position, Parts: []string{ident.Name}}
			}
		}
	}
	startType := a.analyzeValueExpr(expr.Start, left)
	endType := a.analyzeValueExpr(expr.End, left)
	if !isMembershipRangeBoundType(left) || !isMembershipRangeBoundType(startType) || !isMembershipRangeBoundType(endType) {
		a.errorf(expr.Pos(), "membership ranges require integer- or const-enum-compatible bounds compatible with %s", left)
		return invalidType
	}
	if !typesComparableForEquality(left, startType) {
		a.errorf(expr.Start.Pos(), "cannot compare %s against membership range start %s", left, startType)
	}
	if !typesComparableForEquality(left, endType) {
		a.errorf(expr.End.Pos(), "cannot compare %s against membership range end %s", left, endType)
	}
	a.consumeAffineValueExpr(expr.Start, startType, "move into membership range start")
	a.consumeAffineValueExpr(expr.End, endType, "move into membership range end")
	merged := MergeTypes(startType, endType)
	if IsInvalidType(merged) {
		return left
	}
	return merged
}
func isMembershipRangeBoundType(t Type) bool {
	if t == nil {
		return false
	}
	if IsIntegralStorageType(t) {
		return true
	}
	if ce, ok := t.(*ConstEnumType); ok && ce != nil {
		return IsIntegralStorageType(ce.Storage)
	}
	return false
}

func (a *Analyzer) membershipCandidateList(expr ast.Expr) (*ast.ListLitExpr, bool) {
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return list, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	var sym *Symbol
	var found bool
	if a.currentScope != nil {
		sym, found = a.currentScope.Lookup(ident.Name)
	}
	if !found {
		sym, _, found = a.lookupVisibleGlobal(ident.Name)
	}
	if !found || sym == nil {
		return nil, false
	}
	switch decl := sym.Node.(type) {
	case *ast.TokenSetDecl:
		if decl == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.CharsetDecl:
		return a.charsetMembershipList(decl), true
	default:
		return nil, false
	}
}

func (a *Analyzer) charsetMembershipList(decl *ast.CharsetDecl) *ast.ListLitExpr {
	if decl == nil {
		return nil
	}
	elems := a.charsetMembershipElems(decl, decl.Terms, map[*ast.CharsetDecl]bool{decl: true})
	return &ast.ListLitExpr{Position: decl.Position, Elems: elems}
}

func (a *Analyzer) charsetMembershipElems(owner *ast.CharsetDecl, terms []ast.LexerCharClassTerm, visiting map[*ast.CharsetDecl]bool) []ast.Expr {
	elems := make([]ast.Expr, 0, len(terms))
	for _, term := range terms {
		if term.Ref {
			ref, ok := a.lookupCharsetRef(owner, term)
			if !ok || visiting[ref] {
				continue
			}
			visiting[ref] = true
			elems = append(elems, a.charsetMembershipElems(owner, ref.Terms, visiting)...)
			delete(visiting, ref)
			continue
		}
		start := &ast.CharLit{Position: term.Position, Value: term.Start}
		if term.Range {
			elems = append(elems, &ast.MembershipRangeExpr{Position: term.Position, Start: start, End: &ast.CharLit{Position: term.Position, Value: term.End}, Op: lexer.TOKEN_RANGE})
			continue
		}
		elems = append(elems, start)
	}
	return elems
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
	case *ast.IsAliasExpr:
		return appendIsTargetExprs(out, n.Target)
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
			a.recordImplicitTreeStoreUseForType(left)
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
func (a *Analyzer) treeVariantIsTargetPattern(expr ast.Expr, treeType *TreeCategoryType, variant *EnumVariant) (*ast.MatchVariantPattern, bool) {
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return a.treeVariantIsTargetPattern(alias.Target, treeType, variant)
	}
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
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			a.analyzeVariantIsPayloadPattern(option, expected)
		}
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
