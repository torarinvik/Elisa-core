package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

// analyzeOperatorOverload dispatches a binary operator to a user protocol impl when the left operand's
// type provides the operator's dunder method (`+` -> `__add__`, …). It desugars `a OP b` to the static
// interface-method call `T.__add__(a, b)` and records it as `expr.LoweredCall`, so the backend emits the
// call and the effect/permission collectors thread the callee's effects (an allocating `__add__` on a
// user type makes `a + b` legitimately require `can Memory.Allocate`). Mirrors the built-in SpanLike
// path, generalized to any type carrying the dunder via `impl <SomeProtocol> for T`.
//
// This is DELIBERATELY value-type-first (docs: operator overloading): the flagship legitimate use is
// user arithmetic types (Vec3, Complex, fixed-point) whose ops are O(1). String `+` is intentionally NOT
// provided — f-strings own concatenation, and a naive `a + b + c` chain would be quadratic (Principle 1);
// were a string `Add` impl ever added, it should ship with a `-Wperf` chained-concat nudge and lower to a
// single presized fill (the `__fstr` shape), not N buffers.
// buildOverloadCall returns the synthesized static-impl call `T.method(left, right)` when the left
// operand's type carries `method` via a protocol impl (silent probe, nil pos), else ok=false. Numeric
// operands never reach here (the builtin arithmetic/comparison paths handle them first).
func (a *Analyzer) buildOverloadCall(expr *ast.BinaryExpr, methodName string, leftType Type) (*ast.CallExpr, bool) {
	if a == nil || expr == nil || leftType == nil || IsInvalidType(leftType) {
		return nil, false
	}
	var typePath ast.Expr
	if impl, ok := a.staticImplMethodForReceiver(leftType, methodName, nil); ok && impl != nil {
		// Concrete type carrying the dunder via `impl <Protocol> for T`.
		typePath = staticTypeExprForType(expr.Position, leftType)
	} else if tp, ok := StripAggregateStateType(unwrapReceiverRef(leftType)).(*TypeParamType); ok && tp != nil && tp.Name != "" {
		// A bounded type parameter whose interface declares the dunder — so `a + b` works INSIDE a
		// generic `def f[T: Add](a: T, b: T)`, resolving to the concrete impl at monomorphization.
		if iface, ok := a.lookupTypeParamInterface(tp.Name); ok && iface != nil {
			if m, ok := iface.Methods[methodName]; ok && m != nil {
				typePath = &ast.Ident{Position: expr.Position, Name: tp.Name}
			}
		}
	}
	if typePath == nil {
		return nil, false
	}
	field := &ast.FieldExpr{Position: expr.Position, Object: typePath, Field: methodName}
	return &ast.CallExpr{Position: expr.Position, Func: field, Args: []ast.Expr{expr.Left, expr.Right}}, true
}

// analyzeUnaryOverload dispatches an overloaded unary operator (`-x` -> `__neg__`) to the user type's
// dunder: `-x` desugars to `T.__neg__(x)`, recorded as expr.LoweredCall. Resolves through a concrete
// `impl Neg for T` OR a bounded type parameter (`def f[T: Neg]`), mirroring buildOverloadCall.
func (a *Analyzer) analyzeUnaryOverload(expr *ast.UnaryExpr, methodName string, operand Type) (Type, bool) {
	if a == nil || expr == nil || operand == nil || IsInvalidType(operand) {
		return nil, false
	}
	var typePath ast.Expr
	if impl, ok := a.staticImplMethodForReceiver(operand, methodName, nil); ok && impl != nil {
		typePath = staticTypeExprForType(expr.Position, operand)
	} else if tp, ok := StripAggregateStateType(unwrapReceiverRef(operand)).(*TypeParamType); ok && tp != nil && tp.Name != "" {
		if iface, ok := a.lookupTypeParamInterface(tp.Name); ok && iface != nil {
			if m, ok := iface.Methods[methodName]; ok && m != nil {
				typePath = &ast.Ident{Position: expr.Position, Name: tp.Name}
			}
		}
	}
	if typePath == nil {
		return nil, false
	}
	field := &ast.FieldExpr{Position: expr.Position, Object: typePath, Field: methodName}
	call := &ast.CallExpr{Position: expr.Position, Func: field, Args: []ast.Expr{expr.Operand}}
	result := a.analyzeExpr(call)
	expr.LoweredCall = call
	return result, true
}

// analyzeArithmeticOverload dispatches a value-returning binary operator (`+`/`-`/`*`) to its dunder
// (`__add__`/`__sub__`/`__mul__`). `a OP b` desugars to `T.OP(a, b)`, recorded as expr.LoweredCall so
// the backend emits the call and effect/region obligations thread to the operator site.
func (a *Analyzer) analyzeArithmeticOverload(expr *ast.BinaryExpr, methodName string, left Type) (Type, bool) {
	call, ok := a.buildOverloadCall(expr, methodName, left)
	if !ok {
		return nil, false
	}
	result := a.analyzeExpr(call)
	expr.LoweredCall = call
	return result, true
}

// analyzeComparisonOverload dispatches the comparison operators to a user type's `__eq__`/`__cmp__`:
//
//	a == b  ->  T.__eq__(a, b)                         (bool; recorded as LoweredCall)
//	a != b  ->  (T.__eq__(a, b)) == false              (rewritten in place, re-analyzed)
//	a < b   ->  (T.__cmp__(a, b)) < 0                   (and >, <=, >= — __cmp__ returns <0 / 0 / >0)
//
// The relational forms REWRITE the BinaryExpr's operands into a plain bool/numeric comparison and
// re-analyze it, so no new AST/backend machinery is needed — the __cmp__/__eq__ call's own effects
// thread through the rewritten operand. Value-type-first, same as arithmetic.
func (a *Analyzer) analyzeComparisonOverload(expr *ast.BinaryExpr, left Type) (Type, bool) {
	switch expr.Op {
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		call, ok := a.buildOverloadCall(expr, "__eq__", left)
		if !ok {
			return nil, false
		}
		if expr.Op == lexer.TOKEN_EQEQ {
			result := a.analyzeExpr(call)
			expr.LoweredCall = call
			return result, true
		}
		// `a != b`  ->  `(a.__eq__(b)) == false`
		expr.Left = call
		expr.Right = &ast.BoolLit{Position: expr.Position, Value: false}
		expr.Op = lexer.TOKEN_EQEQ
		return a.analyzeBinaryExpr(expr), true
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		call, ok := a.buildOverloadCall(expr, "__cmp__", left)
		if !ok {
			return nil, false
		}
		// `a OP b`  ->  `(a.__cmp__(b)) OP 0`  (op preserved; a plain integer comparison).
		expr.Left = call
		expr.Right = &ast.IntLit{Position: expr.Position, Value: "0"}
		return a.analyzeBinaryExpr(expr), true
	}
	return nil, false
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
	// This is an already-resolved canonical type name, not a source-level
	// chain of value fields. A FieldExpr chain would spell A.Point and reject
	// namespace A; the qualified identifier retains the selected type identity.
	for _, part := range strings.Split(name, ".") {
		if part == "" {
			return nil
		}
	}
	return &ast.Ident{Position: pos, Name: name}
}
func (a *Analyzer) analyzeMembershipExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	resultType := a.namedTypes["bool"]
	list, ok := a.membershipCandidateList(expr.Right)
	if !ok || list == nil {
		right := a.analyzeExpr(expr.Right)
		if setType, ok := StripAggregateStateType(right).(*SetType); ok && setType != nil {
			if !a.ensureRuntimeBackedSetSupported(expr.Right.Pos(), setType) {
				return resultType
			}
			if !AssignableTo(setType.Elem, left) {
				a.errorf(expr.Left.Pos(), "set membership expects %s, got %s", setType.Elem, left)
			}
			a.exprTypes[expr.Right] = right
			return resultType
		}
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
	if rangeExpr, ok := expr.(*ast.MembershipRangeExpr); ok {
		return &ast.ListLitExpr{Position: rangeExpr.Pos(), Elems: []ast.Expr{rangeExpr}}, true
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
	return AssignableTo(left, right) || AssignableTo(right, left) || refsComparableIgnoringMutability(left, right) || (IsNullType(left) && isNullComparablePointer(right)) || (IsNullType(right) && isNullComparablePointer(left))
}

func isNullComparablePointer(t Type) bool {
	if isRefLike(t) {
		return true
	}
	// `cstr` crosses the native boundary as a nullable pointer even though its
	// semantic carrier is CStrType. Contracts commonly spell that obligation as
	// `text != null`, and the backend already lowers it as pointer identity.
	str, ok := t.(*CStrType)
	return ok && str.SurfaceName == "cstr"
}

// IsAggregateValueEqualityUnsupported reports aggregates that have no value-equality
// lowering in the backend. Runtime string carriers and payload enums are intentionally
// absent because they each have an explicit equality path.
func IsAggregateValueEqualityUnsupported(t Type) bool {
	if runtimeStringKindOf(t) != runtimeStringNone {
		return false
	}
	switch value := StripAggregateStateType(t).(type) {
	case *ArrayType, *DArrayType, *TupleType, *ViewType, *DictType, *SetType, *DictEntryType, *StoreRowsViewType, *StoreRowViewType:
		return true
	case *StructType:
		return value != nil
	case *GenericInstanceType:
		_, ok := value.Base.(*StructType)
		return ok
	default:
		return false
	}
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
