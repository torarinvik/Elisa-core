package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeBinaryExpr(expr *ast.BinaryExpr) Type {
	if expr.Op == lexer.TOKEN_IS {
		return a.analyzeIsExpr(expr)
	}
	if expr.Op == lexer.TOKEN_IN {
		return a.analyzeMembershipExpr(expr)
	}
	if expr.Op == lexer.TOKEN_AND || expr.Op == lexer.TOKEN_OR {
		return a.analyzeShortCircuitChain(expr)
	}
	leftShorthand, leftIsShorthand := contextualShorthandExpr(expr.Left)
	rightShorthand, rightIsShorthand := contextualShorthandExpr(expr.Right)
	leftIntegerLiteral := isUnsuffixedIntegerValueExpr(expr.Left)
	rightIntegerLiteral := isUnsuffixedIntegerValueExpr(expr.Right)
	var left Type
	var right Type
	switch {
	case leftIsShorthand && !rightIsShorthand:
		right = a.analyzeExpr(expr.Right)
		left = a.analyzeValueExpr(expr.Left, right)
	case rightIsShorthand && !leftIsShorthand:
		left = a.analyzeExpr(expr.Left)
		right = a.analyzeValueExpr(expr.Right, left)
	case isNumericComparisonOperator(expr.Op) && leftIntegerLiteral && !rightIntegerLiteral:
		right = a.analyzeExpr(expr.Right)
		if expected := valueContextOperandType(right); IsIntegralStorageType(expected) {
			left = a.analyzeValueExpr(expr.Left, expected)
		} else {
			left = a.analyzeExpr(expr.Left)
		}
	case isNumericComparisonOperator(expr.Op) && rightIntegerLiteral && !leftIntegerLiteral:
		left = a.analyzeExpr(expr.Left)
		if expected := valueContextOperandType(left); IsIntegralStorageType(expected) {
			right = a.analyzeValueExpr(expr.Right, expected)
		} else {
			right = a.analyzeExpr(expr.Right)
		}
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
		// User types: `a == b` -> `T.__eq__(a, b)` (`!=` -> `not __eq__`) when the type impls `__eq__`.
		if result, ok := a.analyzeComparisonOverload(expr, left); ok {
			return result
		}
		// Optionals only compare against null (present-flag check). `opt == 5`
		// would hide an implicit presence branch; require an explicit unwrap so
		// the null case is visible at the comparison site.
		if !IsNullType(left) && !IsNullType(right) {
			_, leftIsOptional := left.(*OptionalType)
			_, rightIsOptional := right.(*OptionalType)
			if leftIsOptional || rightIsOptional {
				a.errorf(expr.Pos(), "cannot compare %s and %s: unwrap the optional first (compare against null, or bind the value with `if EXPR is NAME:`)", left, right)
				return a.namedTypes["bool"]
			}
		}
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
		if !typesComparableForEquality(compareLeft, compareRight) && a.comparesNarrowedOptionalAgainstNull(expr, left, right) {
			// The place was narrowed non-null by an assignment, but it is still declared
			// optional and its storage still carries the present flag. Asking is legal;
			// the answer just happens to be statically known.
			return a.namedTypes["bool"]
		}
		if !typesComparableForEquality(compareLeft, compareRight) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left, right)
		} else if st := plainStructValueType(compareLeft); st != nil && plainStructValueType(compareRight) != nil {
			// A struct VALUE with no `__eq__`. AssignableTo(P, P) makes this look comparable,
			// but the backend has no lowering for it: emitBinaryExpr builds an `icmp` for
			// every non-float operand type, so the module failed LLVM's verifier with
			// "Invalid operand types for ICmp instruction" naming an internal temp. Say what
			// is actually wrong, at the comparison.
			a.errorf(expr.Pos(), "struct %s has no == operator; implement Eq (`def __eq__(self, other) -> bool`) to compare values of this type", st.Name)
		} else if IsAggregateValueEqualityUnsupported(compareLeft) && IsAggregateValueEqualityUnsupported(compareRight) {
			// Generic assignability can make aggregate values appear comparable. They do
			// not have scalar LLVM equality lowering, though: falling through to the
			// integer comparison emitter would construct an invalid icmp over an
			// aggregate (most visibly for darray). Reject it at the source boundary.
			a.errorf(expr.Pos(), "aggregate values do not support ==; compare their contents explicitly")
		}
		// Footgun guard: comparing two raw `u8&` C-string pointers with == / !=
		// compares addresses, not contents (unlike cstr/sview/dstr, which content-
		// compare). This silently does the wrong thing in ported C code.
		if !IsNullType(left) && !IsNullType(right) &&
			runtimeStringKindOf(compareLeft) == runtimeStringRaw &&
			runtimeStringKindOf(compareRight) == runtimeStringRaw {
			// Suppress when both operands fold to compile-time constants
			// (e.g. `target.arch == "x86_64"`): const-eval compares by content,
			// so only a genuine *runtime* address compare is a footgun.
			_, lConst := a.evalConstExpr(expr.Left)
			_, rConst := a.evalConstExpr(expr.Right)
			if !(lConst && rConst) && !a.inCompileTimeQueryPredicate {
				a.warnf(expr.Pos(), "%s on two raw u8& C-string pointers compares addresses, not contents; use streq() or compare via cstr/sview/dstr for content equality", lexer.TokenName(expr.Op))
			}
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		// User types: `a < b` -> `(a.__cmp__(b)) < 0` (and >, <=, >=) when the type impls `__cmp__`.
		if result, ok := a.analyzeComparisonOverload(expr, left); ok {
			return result
		}
		left = valueContextOperandType(left)
		right = valueContextOperandType(right)
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		if nullableScalarValueRef(left) || nullableScalarValueRef(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		left = scalarRefValueContextOperandType(left)
		right = scalarRefValueContextOperandType(right)
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
		// User arithmetic types: `a + b` -> `T.__add__(a, b)` / `a - b` -> `T.__sub__(a, b)`.
		{
			method := "__add__"
			if expr.Op == lexer.TOKEN_MINUS {
				method = "__sub__"
			}
			if result, ok := a.analyzeArithmeticOverload(expr, method, left); ok {
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
		// User arithmetic types: `a * b` -> `Mul.__mul__`, `a / b` -> `Div.__div__` (mod/bitwise stay
		// numeric).
		if expr.Op == lexer.TOKEN_STAR {
			if result, ok := a.analyzeArithmeticOverload(expr, "__mul__", left); ok {
				return result
			}
		}
		if expr.Op == lexer.TOKEN_SLASH {
			if result, ok := a.analyzeArithmeticOverload(expr, "__div__", left); ok {
				return result
			}
		}
		left = valueContextOperandType(left)
		right = valueContextOperandType(right)
		requiresIntegral := expr.Op == lexer.TOKEN_PERCENT || expr.Op == lexer.TOKEN_CARET || expr.Op == lexer.TOKEN_PIPE || expr.Op == lexer.TOKEN_AMPERSAND || expr.Op == lexer.TOKEN_LSHIFT || expr.Op == lexer.TOKEN_RSHIFT
		if requiresIntegral {
			leftStorage := integralOperatorOperandType(left)
			rightStorage := integralOperatorOperandType(right)
			if !IsNumericType(leftStorage) || !IsNumericType(rightStorage) {
				a.errorf(expr.Pos(), "operator requires numeric operands")
				return invalidType
			}
			if !IsIntegralStorageType(leftStorage) || !IsIntegralStorageType(rightStorage) {
				a.errorf(expr.Pos(), "operator requires integral operands")
				return invalidType
			}
			return CommonNumericType(leftStorage, rightStorage)
		}
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	default:
		return invalidType
	}
}

// Walk the left-associated spine once. Each evaluated operand contributes its
// own fact, so mutations can invalidate preceding facts without reconstructing
// stale prefix expressions. The child scope ends with this expression.
func (a *Analyzer) analyzeShortCircuitChain(expr *ast.BinaryExpr) Type {
	// Nested logical analyses share one classification session, regardless of
	// whether the subtree is behind parens, unary operators or a pure call.
	if a.shortCircuitAnalysisDepth > 0 && !a.suppressDiagnostics && a.shortCircuitStability == nil {
		a.shortCircuitStability = make(map[shortCircuitStabilityKey]bool)
	}
	a.shortCircuitAnalysisDepth++
	defer func() {
		a.shortCircuitAnalysisDepth--
		if a.shortCircuitAnalysisDepth == 0 {
			a.shortCircuitStability = nil
		}
	}()
	chain := []*ast.BinaryExpr{expr}
	first := expr.Left
	for {
		nested, ok := first.(*ast.BinaryExpr)
		if !ok || nested.Op != expr.Op {
			break
		}
		chain = append(chain, nested)
		first = nested.Left
	}
	left := valueContextOperandType(a.analyzeExpr(first))
	scope := NewScope(a.currentScope)
	previous := first
	for i := len(chain) - 1; i >= 0; i-- {
		node := chain[i]
		if a.shortCircuitStablePredicate(previous) {
			a.recordSMTAssertFactInScope(scope, smtFactExprForCondition(previous, expr.Op == lexer.TOKEN_AND))
		}
		right := valueContextOperandType(a.analyzeExprInScope(node.Right, scope))
		if !IsBoolType(left) || !IsBoolType(right) {
			a.errorf(node.Pos(), "logical operator requires bool operands")
		}
		left = a.namedTypes["bool"]
		if node != expr {
			a.exprTypes[node] = left
			a.recordExprOptimizationFacts(node, left)
		}
		previous = node.Right
	}
	return left
}

// These expressions read current values without mutating them. A helper must
// have a closed, primitive return expression; its return type alone is not
// evidence of purity.
// Share classifications across nested parenthesized chains, but never across
// separate analyses or lexical scopes. Speculative analysis does not cache.
type shortCircuitStabilityKey struct {
	scopeVersion  uint64
	globalVersion uint64
	typeEpoch     uint64
	expr          ast.Expr
	scope         *Scope
}

func (a *Analyzer) shortCircuitStablePredicate(expr ast.Expr) bool {
	if a.suppressDiagnostics {
		a.shortCircuitStability = nil
	}
	_, typed := a.lookupExprType(expr)
	if a.shortCircuitStability == nil || a.suppressDiagnostics || !typed {
		return a.shortCircuitStablePredicateUncached(expr)
	}
	// RHS bindings live in child scopes; intermediate lexical parents stay
	// unchanged during this session. Stamp mutable local/global binding tables
	// and inferred symbol types without walking the parent chain per AST node.
	var scopeVersion, globalVersion uint64
	if a.currentScope != nil {
		scopeVersion = a.currentScope.symbolsVersion
	}
	if a.globalScope != nil {
		globalVersion = a.globalScope.symbolsVersion
	}
	key := shortCircuitStabilityKey{expr: expr, scope: a.currentScope, scopeVersion: scopeVersion, globalVersion: globalVersion, typeEpoch: a.symbolTypeEpoch}
	if stable, found := a.shortCircuitStability[key]; found {
		return stable
	}
	stable := a.shortCircuitStablePredicateUncached(expr)
	a.shortCircuitStability[key] = stable
	return stable
}

func (a *Analyzer) shortCircuitStablePredicateUncached(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok {
		if _, constant := a.resolvedPrimitiveConstant(ident); constant {
			return true
		}
	}
	// Shared global storage is not an evaluated value snapshot. Another task
	// may change it before the RHS, so do not turn that read into a hypothesis.
	if root, global := a.globalStorageRoot(expr); global && (root.Mutable || root.Kind == SymbolExternVar) {
		return false
	}
	if ref, borrowed := a.exprType(expr).(*RefType); borrowed && ref.Storage == RefStorageStatic {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.Ident:
		return true
	case *ast.ParenExpr:
		return n != nil && a.shortCircuitStablePredicate(n.Inner)
	case *ast.FieldExpr:
		return n != nil && a.shortCircuitStablePredicate(n.Object)
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && n.Op != lexer.TOKEN_STAR && shortCircuitPrimitiveType(a.exprType(n.Operand)) && a.shortCircuitStablePredicate(n.Operand)
	case *ast.BinaryExpr:
		return n != nil && shortCircuitPrimitiveType(a.exprType(n.Left)) && shortCircuitPrimitiveType(a.exprType(n.Right)) && a.shortCircuitStablePredicate(n.Left) && a.shortCircuitStablePredicate(n.Right)
	case *ast.CastExpr:
		return n != nil && shortCircuitPrimitiveType(a.exprType(n.Operand)) && shortCircuitPrimitiveType(a.exprType(n)) && a.shortCircuitStablePredicate(n.Operand)
	case *ast.CallExpr:
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil || !a.functionDefiningEquationEligible(decl) {
			return false
		}
		body, ok := a.typedPureReturnExpr(decl)
		if !ok {
			return false
		}
		parameters := make(map[string]bool, len(decl.Params))
		for _, parameter := range decl.Params {
			parameters[parameter.Name] = true
		}
		// Exclude free state and nested calls, including recursive helpers.
		if a.walkStaticExpr(body, func(child ast.Expr) bool {
			switch child := child.(type) {
			case *ast.Ident:
				_, constant := a.resolvedPrimitiveConstant(child)
				return !parameters[child.Name] && !constant
			case *ast.CallExpr, *ast.FieldExpr, *ast.IndexExpr:
				return true
			}
			return false
		}) || !a.shortCircuitStablePredicate(body) {
			return false
		}
		for _, argument := range n.Args {
			if !a.shortCircuitStablePredicate(argument) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// Use the declaration's recorded binding, never the caller's lexical spelling.
func (a *Analyzer) resolvedPrimitiveConstant(ident *ast.Ident) (ConstValue, bool) {
	if a == nil || ident == nil || a.globalScope == nil {
		return ConstValue{}, false
	}
	name, resolved := a.resolvedValueNames[ident]
	if !resolved {
		return ConstValue{}, false
	}
	symbol, ok := a.globalScope.Lookup(name)
	if !ok || symbol.Kind != SymbolConst || symbol.Mutable || !shortCircuitPrimitiveType(symbol.Type) {
		return ConstValue{}, false
	}
	value, ok := a.constValues[name]
	return value, ok && (value.Kind == ConstInt || value.Kind == ConstBool)
}

func isNumericComparisonOperator(op lexer.TokenKind) bool {
	switch op {
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ,
		lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		return true
	default:
		return false
	}
}

// isUnsuffixedIntegerValueExpr identifies integer literals that can take their
// comparison operand's type. An explicit numeric suffix remains authoritative.
func isUnsuffixedIntegerValueExpr(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Suffix == ""
	case *ast.ParenExpr:
		return isUnsuffixedIntegerValueExpr(n.Inner)
	case *ast.UnaryExpr:
		return n.Op == lexer.TOKEN_MINUS && isUnsuffixedIntegerValueExpr(n.Operand)
	default:
		return false
	}
}

// valueContextOperandType reads a scalar reference operand as its referent value.
// Only a PROVEN non-null reference has a value to read; a `T&?` stays a reference,
// so the operator rejects it and the caller must unwrap it first.
func valueContextOperandType(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil && ref.State == RefStateNonNull {
		if IsNumericType(ref.Elem) || IsBoolType(ref.Elem) {
			return ref.Elem
		}
	}
	return t
}

func integralOperatorOperandType(t Type) Type {
	if storage, ok := ConstEnumStorageType(t); ok {
		return storage
	}
	return t
}

// scalarRefValueContextOperandType deref's a scalar reference to its referent
// value for +/- arithmetic, so the operator means value arithmetic, not pointer
// arithmetic on the address. This applies to both mutable and immutable scalar
// refs (numeric/bool) — previously only MUTABLE refs were deref'd, so an
// immutable scalar ref (e.g. the `i64&` bound by `for ref x in nums`) fell
// through to the pointer-arithmetic path and silently used the address bits,
// a silent wrong-result footgun. `u8&` refs are excluded: they are the genuine
// C-string / byte-pointer case where `+ n` is intended pointer stepping.
func scalarRefValueContextOperandType(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil && ref.State == RefStateNonNull {
		if (IsNumericType(ref.Elem) || IsBoolType(ref.Elem)) && !isBytePointerArithmeticRef(ref) {
			return ref.Elem
		}
	}
	return t
}

// nullableScalarValueRef reports a numeric/bool reference that is not proven non-null
// and is not a byte pointer. Such an operand used to be read as its value; now it has
// no value until unwrapped, and it must not fall through to pointer arithmetic either
// (that would silently change `r + 1` from value arithmetic to address stepping).
func nullableScalarValueRef(t Type) bool {
	ref, ok := t.(*RefType)
	if !ok || ref == nil || ref.State == RefStateNonNull {
		return false
	}
	return (IsNumericType(ref.Elem) || IsBoolType(ref.Elem)) && !isBytePointerArithmeticRef(ref)
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
