package semantic

import (
	"math/big"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A view header does not make its backing immutable. Reuse a byte-array symbol
// only within an expression-only function that cannot modify or escape it.
func (tr *smtTranslator) immutableSviewArrayParameter(id *ast.Ident, env map[string]string) bool {
	if tr.a == nil || tr.a.currentScope == nil || tr.a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 {
		return false
	}
	if _, bound := env[id.Name]; bound {
		return false
	}
	if _, bound := env["\x00string:"+id.Name]; bound {
		return false
	}
	if !tr.a.immutableSviewParameter(id) {
		return false
	}
	if tr.a.currentFuncDecl.IsGhost {
		// Ghost functions are verification-only and their declaration validator rejects mutable
		// statements/effects. Their return-tree projection can therefore be treated as a stable
		// logical byte sequence even when it uses immutable local aliases or recursive calls.
		_, ok := tr.a.typedPureReturnExpr(tr.a.currentFuncDecl)
		return ok
	}
	body, ok := tr.a.typedPureReturnExpr(tr.a.currentFuncDecl)
	if ok && tr.a.pureSviewIndexExpression(body, 0) {
		return true
	}
	// A body that writes no memory cannot change the view's bytes (analyzer_smt_view_frame.go).
	return tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl)
}

func (a *Analyzer) immutableSviewParameter(id *ast.Ident) bool {
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Kind != SymbolParam || sym.Node != a.currentFuncDecl || symbolRootMutable(sym) {
		return false
	}
	if _, exact := sym.Type.(*SViewType); !exact {
		return false
	}
	for index, param := range a.currentFuncDecl.Params {
		if param.Name == id.Name {
			return sym.ParamIndex == index && !a.paramIsMutable(param)
		}
	}
	return false
}

func (a *Analyzer) pureSviewIndexExpression(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 64 {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit:
		return true
	case *ast.ParenExpr:
		return a.pureSviewIndexExpression(n.Inner, depth+1)
	case *ast.Ident:
		if _, ok := a.constIntValue(n); ok {
			return true
		}
		if _, ok := a.resolvedPrimitiveConstant(n); ok {
			return true
		}
		sym, found := a.currentScope.Lookup(n.Name)
		if !found || sym == nil || sym.Kind != SymbolParam || sym.Node != a.currentFuncDecl || symbolRootMutable(sym) {
			return false
		}
		if IsBoolType(sym.Type) {
			return true
		}
		_, _, integer := smtIntWidthSign(sym.Type)
		return integer
	case *ast.UnaryExpr:
		if n == nil || n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_NOT, lexer.TOKEN_MINUS, lexer.TOKEN_PLUS:
			return a.pureSviewIndexExpression(n.Operand, depth+1)
		}
	case *ast.BinaryExpr:
		return n != nil && n.LoweredCall == nil && a.pureSviewIndexExpression(n.Left, depth+1) && a.pureSviewIndexExpression(n.Right, depth+1)
	case *ast.FieldExpr:
		id, ok := stripOptimizationParens(n.Object).(*ast.Ident)
		return ok && (n.Field == "len" || n.Field == "count") && a.immutableSviewParameter(id)
	case *ast.CastExpr:
		return n != nil && a.pureSviewIndexExpression(n.Operand, depth+1)
	case *ast.TernaryExpr:
		return n != nil && a.pureSviewIndexExpression(n.Cond, depth+1) &&
			a.pureSviewIndexExpression(n.Value, depth+1) && a.pureSviewIndexExpression(n.Alt, depth+1)
	case *ast.CallExpr:
		if n == nil {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil || !decl.IsGhost {
			return false
		}
		for _, arg := range n.Args {
			if !a.pureSviewIndexExpression(arg, depth+1) {
				return false
			}
		}
		return true
	case *ast.IndexExpr:
		id, ok := stripOptimizationParens(n.Object).(*ast.Ident)
		return ok && n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback && n.AsBuiltinDictGet == nil && n.AsSpecialize == nil && n.AsOverlayCall == nil && a.immutableSviewParameter(id) && a.pureSviewIndexExpression(n.Index, depth+1)
	}
	return false
}

// ghostImmutableLocalEnv maps every immutable numeric local that `expr` mentions (transitively
// through their initializers) to the SMT term of its initializer, for canonical call keys inside a
// ghost function. A ghost body has no mutation, loops, or effects, so an immutable local denotes its
// initializer's value everywhere it is visible; keying `f(v, i + 1, next)` by `next`'s defining
// term lets it share one symbol with the spec's `f(v, i + 1, (s ^ v[i].u8().u64()) * P)` instead of
// leaving the solver to rediscover the equality through nonlinear modular arithmetic. Returns nil
// when nothing is expanded or any initializer falls outside the fragment (the caller then keys the
// call as before).
func (tr *smtTranslator) ghostImmutableLocalEnv(expr ast.Expr, depth int) map[string]string {
	a := tr.a
	if a == nil || a.currentScope == nil || a.currentFuncDecl == nil || !a.currentFuncDecl.IsGhost || tr.calleeBodyDepth != 0 || depth > 16 {
		return nil
	}
	env := map[string]string{}
	failed := false
	a.walkStaticExpr(expr, func(node ast.Expr) bool {
		id, ok := node.(*ast.Ident)
		if !ok || id == nil || failed {
			return false
		}
		if _, done := env[id.Name]; done {
			return false
		}
		sym, found := a.currentScope.Lookup(id.Name)
		if !found || sym == nil || sym.Mutable || sym.Kind != SymbolLocal || sym.Type == nil || !IsNumericType(sym.Type) {
			return false
		}
		vd, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || vd == nil || vd.Value == nil {
			return false
		}
		inner := tr.ghostImmutableLocalEnv(vd.Value, depth+1)
		if inner == nil {
			inner = map[string]string{}
		}
		term, ok := tr.termEnv(vd.Value, inner)
		if !ok {
			failed = true
			return true
		}
		env[id.Name] = term
		return false
	})
	if failed || len(env) == 0 {
		return nil
	}
	return env
}

// sliceLengthTerm models `.len` of an sview slice `obj[start:end]` EXACTLY as the runtime's
// string_view_slice computes it (it clamps, it does not trap): the bounds are passed as i64 (a usize
// >= 2^63 wraps negative), lo = clamp(start, 0, L), hi = (end >= 0 ? min(end, L) : L) raised to lo,
// and an empty/negative-length source yields an empty view. L is obj.len. Only sview objects are
// modeled; other sliceable kinds decline.
func (tr *smtTranslator) sliceLengthTerm(obj ast.Expr, env map[string]string) (string, bool) {
	if tr.a == nil {
		return "", false
	}
	if id, isIdent := stripOptimizationParens(obj).(*ast.Ident); isIdent && id != nil {
		if _, bound := env[id.Name]; bound || tr.calleeBodyDepth != 0 {
			return "", false
		}
		init, ok := tr.a.immutableSliceLocalInit(id)
		if !ok {
			return "", false
		}
		obj = init
	}
	slice, ok := stripOptimizationParens(obj).(*ast.SliceExpr)
	if !ok || slice == nil || slice.Start == nil || slice.End == nil {
		return "", false
	}
	if _, ok := stripRefForBounds(tr.a.exprTypes[slice.Object]).(*SViewType); !ok {
		return "", false
	}
	if _, ok := stripRefForBounds(tr.a.exprTypes[slice]).(*SViewType); !ok {
		return "", false
	}
	start, ok := tr.termEnv(slice.Start, env)
	if !ok {
		return "", false
	}
	end, ok := tr.termEnv(slice.End, env)
	if !ok {
		return "", false
	}
	length, ok := tr.termEnv(&ast.FieldExpr{Position: slice.Object.Pos(), Object: slice.Object, Field: "len"}, env)
	if !ok {
		return "", false
	}
	asI64 := func(t string) string {
		return "(ite (>= " + t + " 9223372036854775808) (- " + t + " 18446744073709551616) " + t + ")"
	}
	s, e, l := asI64(start), asI64(end), length
	lo0 := "(ite (> " + s + " 0) " + s + " 0)"
	lo := "(ite (> " + lo0 + " " + l + ") " + l + " " + lo0 + ")"
	hi0 := "(ite (>= " + e + " 0) " + e + " " + l + ")"
	hi1 := "(ite (> " + hi0 + " " + l + ") " + l + " " + hi0 + ")"
	hi := "(ite (< " + hi1 + " " + lo + ") " + lo + " " + hi1 + ")"
	return "(ite (<= " + l + " 0) 0 (- " + hi + " " + lo + "))", true
}

// immutableSliceLocalInit returns the initializer of an immutable sview local `name: sview =
// obj[start:end]` when re-evaluating that initializer at the use site yields the same value: every
// name it mentions resolves NOW to an immutable parameter of the current function or an immutable
// local declared before the slice local (so a later shadowing binding declines), and it contains
// only side-effect-free shapes.
func (a *Analyzer) immutableSliceLocalInit(id *ast.Ident) (*ast.SliceExpr, bool) {
	if a == nil || a.currentScope == nil || a.currentFuncDecl == nil {
		return nil, false
	}
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Kind != SymbolLocal || symbolRootMutable(sym) {
		return nil, false
	}
	if _, ok := sym.Type.(*SViewType); !ok {
		return nil, false
	}
	// One of the initializer's inputs was written after the slice was taken.
	if a.smtStaleLocalDefs[sym] {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || decl == nil || decl.Mutable || decl.Name != id.Name {
		return nil, false
	}
	slice, ok := stripOptimizationParens(decl.Value).(*ast.SliceExpr)
	if !ok || slice == nil || slice.Start == nil || slice.End == nil {
		return nil, false
	}
	declPos := decl.Position
	var stable func(e ast.Expr, depth int) bool
	stable = func(e ast.Expr, depth int) bool {
		if depth > 32 {
			return false
		}
		switch n := e.(type) {
		case *ast.IntLit, *ast.BoolLit, *ast.CharLit:
			return true
		case *ast.Ident:
			if n == nil {
				return false
			}
			if _, ok := a.constIntValue(n); ok {
				return true
			}
			s, found := a.currentScope.Lookup(n.Name)
			if !found || s == nil {
				return false
			}
			if symbolRootMutable(s) {
				// A by-value mutable local declared before the slice: its current value is the one
				// the slice read unless it was written since, and every write (assignment, `&x`,
				// mutable argument, loop-head havoc) marks this slice local stale (checked above).
				if s.Kind != SymbolLocal || !s.Mutable {
					return false
				}
				if _, isRef := s.Type.(*RefType); isRef {
					return false
				}
				d, ok := s.Node.(*ast.VarDeclStmt)
				if !ok || d == nil || d.Name != n.Name {
					return false
				}
				p := d.Position
				return p.File == declPos.File && (p.Line < declPos.Line || (p.Line == declPos.Line && p.Col < declPos.Col))
			}
			switch s.Kind {
			case SymbolParam:
				return s.Node == a.currentFuncDecl
			case SymbolLocal:
				d, ok := s.Node.(*ast.VarDeclStmt)
				if !ok || d == nil || d.Mutable || d.Name != n.Name {
					return false
				}
				p := d.Position
				return p.File == declPos.File && (p.Line < declPos.Line || (p.Line == declPos.Line && p.Col < declPos.Col))
			}
			return false
		case *ast.ParenExpr:
			return n != nil && stable(n.Inner, depth+1)
		case *ast.UnaryExpr:
			return n != nil && n.LoweredCall == nil && stable(n.Operand, depth+1)
		case *ast.BinaryExpr:
			return n != nil && n.LoweredCall == nil && stable(n.Left, depth+1) && stable(n.Right, depth+1)
		case *ast.CastExpr:
			return n != nil && isPureNumericValueCast(n) && stable(n.Operand, depth+1)
		case *ast.FieldExpr:
			return n != nil && !n.Safe && (n.Field == "len" || n.Field == "count") && stable(n.Object, depth+1)
		case *ast.SliceExpr:
			return n != nil && stable(n.Object, depth+1) && stable(n.Start, depth+1) && stable(n.End, depth+1)
		}
		return false
	}
	if !stable(slice, 0) {
		return nil, false
	}
	return slice, true
}

// implicitIntConversion makes an implicit integer conversion explicit for the prover. Codegen
// stores an assigned value of a different integer type by truncation / reinterpretation (an i64
// `sview.len` stored into a usize local is a bit copy, so -1 becomes 2^64-1); equating the target
// with the UNCONVERTED value would assert the value is already in the target's range -- an
// unsound assumption that excludes those executions. A value-preserving conversion (same sign and
// not narrower, or unsigned into strictly wider signed) needs no wrapper; any other is wrapped in a
// synthetic cast typed as the target, which the cast translator models exactly (or declines).
func (a *Analyzer) implicitIntConversion(value ast.Expr, valueType Type, targetType Type) ast.Expr {
	ssign, sbits, sok := smtIntWidthSign(valueType)
	dsign, dbits, dok := smtIntWidthSign(targetType)
	// `char` is an i64 value at runtime (smtIntWidthSign does not list it); storing it into a
	// narrower integer truncates exactly like any other i64.
	if source, isBuiltin := stripRefForBounds(valueType).(*BuiltinType); isBuiltin && source != nil && source.Name == "char" {
		ssign, sbits, sok = true, 64, true
	}
	if !sok || !dok {
		return value
	}
	if (ssign == dsign && dbits >= sbits) || (!ssign && dsign && dbits > sbits) {
		return value
	}
	// A value whose range is known (constants, ternaries/matches over constants) and fits the
	// target converts exactly.
	if r, ok := a.exprResultRange(value, nil); ok && r.loKnown && r.hiKnown &&
		IntegerTypeFitsValue(targetType, r.lo) && IntegerTypeFitsValue(targetType, r.hi) {
		return value
	}
	// Signed into an unsigned type at least as wide is the identity for every non-negative value, so
	// a known lower bound >= 0 suffices (`length: usize = view.len`).
	if ssign && !dsign && dbits >= sbits {
		if r, ok := a.exprResultRange(value, nil); ok && r.loKnown && r.lo >= 0 {
			return value
		}
		// A view's length is non-negative (the SMT preamble asserts it for every length symbol), so
		// its conversion into a wide unsigned type preserves the value.
		if f, ok := stripOptimizationParens(value).(*ast.FieldExpr); ok && f != nil && !f.Safe && f.Field == "len" && isStringViewType(stripRefForBounds(a.exprTypes[f.Object])) {
			return value
		}
	}
	cast := &ast.CastExpr{Position: value.Pos(), Operand: value}
	a.exprTypes[cast] = stripRefForBounds(targetType)
	return cast
}

// noteImplicitIntConversion records that the current function stores a value into a binding/result of
// a different integer type by a non-value-preserving implicit conversion. The untyped pure-return
// lowering (pureReturnExpr) inlines locals and return values WITHOUT that conversion, so such a
// function's lowered body would not equal its real result; typedPureReturnExpr declines it.
func (a *Analyzer) noteImplicitIntConversion(value ast.Expr, valueType Type, targetType Type) {
	if a == nil || a.currentFuncDecl == nil || value == nil {
		return
	}
	if a.implicitIntConversion(value, valueType, targetType) == value {
		return
	}
	if a.implicitIntConvFuncs == nil {
		a.implicitIntConvFuncs = map[*ast.FuncDecl]bool{}
	}
	a.implicitIntConvFuncs[a.currentFuncDecl] = true
}

// typedPureReturnExpr is pureReturnExpr for a function whose analyzed body has no implicit
// non-value-preserving integer conversion (see noteImplicitIntConversion).
func (a *Analyzer) typedPureReturnExpr(decl *ast.FuncDecl) (ast.Expr, bool) {
	if a != nil && a.implicitIntConvFuncs[decl] {
		return nil, false
	}
	return pureReturnExpr(decl)
}

// smtTermMentionsQuantifierBinder reports whether an SMT term names a quantifier binder (`q_…`).
func smtTermMentionsQuantifierBinder(term string) bool {
	if !strings.Contains(term, "q_") {
		return false
	}
	// Binders introduced INSIDE the term (`(forall ((q_k Int)) ...)`) are bound there; only a
	// binder with no enclosing quantifier in the term is free. Binder names carry a unique counter
	// suffix, so a name bound anywhere in the term is bound at all of its occurrences.
	bound := map[string]bool{}
	for _, q := range []string{"(forall (", "(exists ("} {
		rest := term
		for {
			i := strings.Index(rest, q)
			if i < 0 {
				break
			}
			rest = rest[i+len(q):]
			for strings.HasPrefix(rest, "(") {
				end := strings.IndexAny(rest[1:], " )")
				if end < 0 {
					break
				}
				bound[rest[1:1+end]] = true
				close := strings.Index(rest, ")")
				if close < 0 {
					break
				}
				rest = strings.TrimLeft(rest[close+1:], " ")
			}
		}
	}
	for tok := range strings.FieldsFuncSeq(term, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == '|' || r == ';' || r == '=' || r == ','
	}) {
		if strings.HasPrefix(tok, "q_") && !bound[tok] {
			return true
		}
	}
	return false
}

// mixedSignFitGuard decides whether an integer comparison mixes operand types such that codegen's
// evaluation (the RIGHT operand converted to the LEFT operand's type, then compared in that type:
// `usize_x <= view.len` compares unsigned, a length of -1 reading as 2^64-1; `i32 < u64` truncates)
// can differ from the plain-value comparison. It returns mixed=true and the SMT condition (over the
// right operand's term r) under which the two agree: r is representable in the left type. Constants
// that fit, value-preserving pairs, quantifier binders and operands of unknown type are not mixed.
func (tr *smtTranslator) mixedSignFitGuard(n *ast.BinaryExpr, r string) (string, bool) {
	if tr == nil || tr.a == nil || n == nil || n.LoweredCall != nil {
		return "", false
	}
	a := tr.a
	var leftType, rightType Type
	if tr.calleeBodyDepth != 0 {
		leftType, rightType = a.exprTypes[n.Left], a.exprTypes[n.Right]
	} else {
		if a.mentionsUnscopedName(n.Left) || a.mentionsUnscopedName(n.Right) {
			return "", false
		}
		leftType, rightType = a.smtOperandType(n.Left), a.smtOperandType(n.Right)
	}
	lsign, lbits, lok := smtIntWidthSign(leftType)
	rsign, rbits, rok := smtIntWidthSign(rightType)
	if !lok || !rok {
		return "", false
	}
	if (lsign == rsign && lbits >= rbits) || (!rsign && lsign && lbits > rbits) {
		return "", false
	}
	if c, ok := a.constIntValue(n.Right); ok && smtTypeFitsValue(leftType, c) {
		return "", false
	}
	lo := new(big.Int)
	hi := new(big.Int)
	if lsign {
		lo.Neg(new(big.Int).Lsh(big.NewInt(1), uint(lbits-1)))
		hi.Sub(new(big.Int).Lsh(big.NewInt(1), uint(lbits-1)), big.NewInt(1))
	} else {
		hi.Sub(new(big.Int).Lsh(big.NewInt(1), uint(lbits)), big.NewInt(1))
	}
	return "(and (<= " + smtBigInt(lo) + " " + r + ") (<= " + r + " " + hi.String() + "))", true
}

// mixedSignCompare is the comparison as codegen evaluates it. Where the right operand fits the left
// type it is the plain-value comparison. A signed right operand compared in a wider-or-equal unsigned
// left type reads a negative value as r + 2^bits, modeled exactly. Any other out-of-range case
// (truncation) is an unconstrained Bool: Unknown, never a value comparison.
func (tr *smtTranslator) mixedSignCompare(n *ast.BinaryExpr, l, r string) string {
	cmp := smtCompare(n.Op, l, r)
	guard, mixed := tr.mixedSignFitGuard(n, r)
	if !mixed {
		return cmp
	}
	var other string
	if wrap := tr.mixedSignWrapModulus(n); wrap != "" {
		other = smtCompare(n.Op, l, smtArith("+", r, wrap))
	} else if conv, ok := tr.mixedSignConvertedRight(n, r); ok {
		other = smtCompare(n.Op, l, conv)
	} else {
		other = tr.freshBoolAux()
	}
	return smtIteTerm(guard, cmp, other)
}

// mixedSignConvertedRight is the right operand exactly as codegen converts it to the left type
// (truncate, or sign/zero-extend, then reinterpret): m = r mod 2^bits, read as m - 2^bits when the
// left type is signed and m >= 2^(bits-1). A deterministic term, so equal comparisons translate equal.
func (tr *smtTranslator) mixedSignConvertedRight(n *ast.BinaryExpr, r string) (string, bool) {
	a := tr.a
	var leftType Type
	if tr.calleeBodyDepth != 0 {
		leftType = a.exprTypes[n.Left]
	} else {
		leftType = a.smtOperandType(n.Left)
	}
	lsign, lbits, ok := smtIntWidthSign(leftType)
	if !ok || lbits <= 0 || lbits > 128 {
		return "", false
	}
	mod := new(big.Int).Lsh(big.NewInt(1), uint(lbits)).String()
	m := smtModTerm(r, mod)
	if !lsign {
		return m, true
	}
	half := new(big.Int).Lsh(big.NewInt(1), uint(lbits-1)).String()
	return smtIteTerm(smtCompare(lexer.TOKEN_GTEQ, m, half), smtArith("-", m, mod), m), true
}

// mixedSignWrapModulus returns 2^bits when the left operand is unsigned with at least the right
// (signed) operand's width: the only mixed case where out-of-range means r < 0 and the converted
// value is exactly r + 2^bits.
func (tr *smtTranslator) mixedSignWrapModulus(n *ast.BinaryExpr) string {
	a := tr.a
	var leftType, rightType Type
	if tr.calleeBodyDepth != 0 {
		leftType, rightType = a.exprTypes[n.Left], a.exprTypes[n.Right]
	} else {
		leftType, rightType = a.smtOperandType(n.Left), a.smtOperandType(n.Right)
	}
	lsign, lbits, lok := smtIntWidthSign(leftType)
	rsign, rbits, rok := smtIntWidthSign(rightType)
	if !lok || !rok || lsign || !rsign || lbits < rbits {
		return ""
	}
	return new(big.Int).Lsh(big.NewInt(1), uint(lbits)).String()
}

// smtOperandType is an expression's analyzed type, falling back for synthetic/substituted nodes to
// the scope type of a bare identifier, `result`'s return type, the resolved type of a field read, or
// an sview's i64 `.len`.
func (a *Analyzer) smtOperandType(e ast.Expr) Type {
	if t := a.exprTypes[e]; t != nil {
		return t
	}
	switch op := stripOptimizationParens(e).(type) {
	case *ast.FieldExpr:
		if t, ok := a.fieldReadResolvedType(op); ok {
			return t
		}
		if op.Field == "len" {
			if _, isView := stripRefForBounds(a.smtOperandType(op.Object)).(*SViewType); isView {
				return a.scanTypeNamed("i64")
			}
		}
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(op.Name); ok && sym != nil {
				return sym.Type
			}
		}
		if op.Name == "result" && a.currentReturn != nil {
			return a.currentReturn
		}
	}
	return a.exprTypes[stripOptimizationParens(e)]
}

// mentionsUnscopedName reports whether an expression reads a name that is neither in scope, nor
// `result`, nor a constant: a quantifier binder (a mathematical integer, never a runtime value).
func (a *Analyzer) mentionsUnscopedName(e ast.Expr) bool {
	found := false
	var walk func(x ast.Expr, depth int)
	walk = func(x ast.Expr, depth int) {
		if found || x == nil || depth > 32 {
			return
		}
		switch n := x.(type) {
		case *ast.Ident:
			if n.Name == "result" {
				return
			}
			if _, ok := a.constIntValue(n); ok {
				return
			}
			if a.currentScope != nil {
				if sym, ok := a.currentScope.Lookup(n.Name); ok && sym != nil {
					return
				}
			}
			found = true
		case *ast.ParenExpr:
			walk(n.Inner, depth+1)
		case *ast.BinaryExpr:
			walk(n.Left, depth+1)
			walk(n.Right, depth+1)
		case *ast.UnaryExpr:
			walk(n.Operand, depth+1)
		case *ast.CastExpr:
			walk(n.Operand, depth+1)
		case *ast.FieldExpr:
			walk(n.Object, depth+1)
		case *ast.IndexExpr:
			walk(n.Object, depth+1)
			walk(n.Index, depth+1)
		}
	}
	walk(e, 0)
	return found
}

// smtTypeFitsValue reports that an integer constant is representable in an integer type, including
// the builtin usize/isize/char kinds (IntegerTypeFitsValue treats those as "fits anything").
func smtTypeFitsValue(t Type, value int64) bool {
	signed, bits, ok := smtIntWidthSign(t)
	if !ok {
		return false
	}
	if signed {
		if bits >= 64 {
			return true
		}
		return value >= -(int64(1)<<(bits-1)) && value <= (int64(1)<<(bits-1))-1
	}
	if value < 0 {
		return false
	}
	return bits >= 63 || value <= (int64(1)<<bits)-1
}

func smtBigInt(v *big.Int) string {
	if v.Sign() < 0 {
		return "(- " + new(big.Int).Neg(v).String() + ")"
	}
	return v.String()
}

// scanTypeNamed resolves a type NAME for the syntactic conversion scan: builtin integer/bool names
// directly, anything else through the declared named types.
func (a *Analyzer) scanTypeNamed(name string) Type {
	if name == "bool" || isIntegerConversionTargetName(name) {
		return &BuiltinType{Name: name}
	}
	return a.namedTypes[name]
}
