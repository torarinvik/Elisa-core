package semantic

import (
	"math/big"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (tr *smtTranslator) termEnv(expr ast.Expr, env map[string]string) (string, bool) {
	if n, ok := expr.(*ast.MatchExpr); ok {
		if term, ok := tr.enumMatchTerm(n, env, tr.termEnv); ok {
			return term, true
		}
		if term, ok := tr.constEnumMatchTerm(n, env, tr.termEnv); ok {
			return term, true
		}
		return tr.scalarMatchTerm(n, env, tr.termEnv)
	}

	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.termEnv(n.Inner, env)
	case *ast.IntLit:
		// Unsigned literals must enter SMT as their mathematical value, not the
		// signed bit pattern used by the general constant folder.
		signed, _, typed := smtIntWidthSign(tr.a.exprType(n))
		// Contract substitution clones AST nodes without exprTypes entries; the
		// literal's preserved explicit suffix remains authoritative there.
		unsignedSuffix := n.Suffix == "u" || n.Suffix == "u8" || n.Suffix == "u16" || n.Suffix == "u32" || n.Suffix == "u64" || n.Suffix == "usize"
		if (typed && !signed) || unsignedSuffix {
			if v, ok := tr.a.unsignedConstBound(n); ok {
				return v.String(), true
			}
		}
		if c, ok := tr.a.constIntValue(n); ok {
			return tr.constIntTerm(n, c), true
		}
		// A u64-range literal (e.g. 0xFFFFFFFFFFFFFFFF) does not fit int64 so constIntValue declines; emit
		// its exact non-negative value as an SMT Int so unsigned upper-bound preconditions translate.
		if v, ok := tr.a.unsignedConstBound(n); ok {
			return v.String(), true
		}
		return "", false
	case *ast.CharLit:
		// A char literal is the byte codegen emits for it (ParseCharLiteral, emitCharLiteral).
		if v, ok := ParseCharLiteral(n); ok {
			return smtInt(v), true
		}
		return "", false
	case *ast.Ident:
		if value, constant := tr.a.resolvedPrimitiveConstant(n); constant && value.Kind == ConstInt {
			return tr.constIntTerm(n, value.Int), true
		}
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				if strings.HasPrefix(bound, dtTokenPrefix) {
					return "", false
				}
				return bound, true
			}
		}
		if tr.calleeBodyDepth > 0 {
			return "", false
		}
		if c, ok := tr.paramConsts[n.Name]; ok {
			return smtInt(c), true
		}
		if c, ok := tr.a.constIntValue(n); ok {
			return tr.constIntTerm(n, c), true
		}
		if _, ok := immutableIntIdentName(tr.a, tr.a.currentScope, n); ok {
			name := smtVar(n.Name)
			tr.decls[n.Name] = true
			// Emit the identifier's true type bounds (unsigned `[0, 2^w)`, signed `[-2^(w-1), 2^(w-1))`).
			// Without these an immutable unsigned param is an unconstrained Int, so a goal like
			// `a - b <= a` (which needs `b >= 0`) cannot discharge. The bounds are guaranteed by the type
			// and therefore always sound (asserting more true facts only ever proves more, never less).
			if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil {
				tr.markIntVar(n.Name, sym.Type)
			}
			return name, true
		}
		if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil && (sym.Kind == SymbolLocal || sym.Kind == SymbolParam) {
			if t, ok := smtNumericValueType(sym.Type); ok {
				name := smtVar(n.Name)
				tr.decls[n.Name] = true
				tr.markIntVar(n.Name, t)
				return name, true
			}
		}
		return "", false
	case *ast.CallExpr:
		return tr.callResultTermEnv(n, env)
	case *ast.StructLitExpr:
		if call, ok := tr.a.proofCallExpr(n); ok {
			return tr.callResultTerm(call)
		}
		return "", false
	case *ast.IndexExpr:
		if term, ok := tr.stableSviewByteIndex(n, env); ok {
			return term, true
		}
		// A read of a constant integer table (`__classtable_f[c]`, a folded classifier) is the
		// table's value at the index (analyzer_smt_const_table.go).
		if term, ok := tr.constTableIndexTerm(n, env); ok {
			return term, true
		}
		// Array element access `arr[idx]` → `(select <arr> <idx>)` over SMT array theory (docs/90
		// brick 90-5). The element value is an Int; out-of-range indices are an arbitrary-but-total
		// value, which a quantifier's range guard constrains away.
		if arr, ok := tr.arrayTermEnv(n.Object, env); ok {
			idx, ok := tr.termEnv(n.Index, env)
			if !ok {
				return "", false
			}
			return "(select " + arr + " " + idx + ")", true
		}
		// Dict lookup `d[k]` -> `(select <d>_vals <k>)` over the dict's value array (docs/90 set/dict
		// modeling). The key term is modeled in the dict's key sort. Out-of-domain lookups select an
		// arbitrary-but-total value; a `k in d` guard (asserting `(select d_keys k)`) is what constrains
		// the proof to in-domain keys, so e.g. `requires k in d; assert d[k] > 0` discharges.
		if dsym, ksort, ok := tr.dictTermEnv(n.Object, env); ok {
			key, ok := tr.keyTermEnv(n.Index, ksort, env)
			if !ok {
				return "", false
			}
			return "(select " + dsym + "_vals " + key + ")", true
		}
		return "", false
	case *ast.FieldExpr:
		if env != nil {
			if bound, ok := env[smtProjectionName(n)]; ok {
				return bound, true
			}
		}
		if tr.calleeFreeProjection(n, env) {
			return "", false
		}
		boundRoot := tr.envBindsRoot(n.Object, env)
		if n.Field == "len" {
			if base, k, ok := tr.containerViewIndex(n.Object, env); ok {
				_, _, length := tr.containerViewElement(base, k)
				return length, true
			}
		}
		if term, handled, ok := tr.recordElementFieldTerm(n, env, "Int"); handled {
			return term, ok
		}
		if _, nested := stripOptimizationParens(n.Object).(*ast.FieldExpr); nested && !boundRoot {
			if value, found := tr.a.nestedLiteralFieldValue(n, 0); found {
				if _, constant := stripOptimizationParens(value).(*ast.IntLit); constant {
					return tr.termEnv(value, env)
				}
			}
		}
		if !boundRoot {
			if rerooted, ok := tr.a.rerootLiteralProjection(n); ok {
				return tr.termEnv(rerooted, env)
			}
		}
		if tuple, ok := stripOptimizationParens(n.Object).(*ast.TupleExpr); ok {
			if value, found := tr.a.tupleLiteralFieldValue(tuple, n.Field); found {
				return tr.termEnv(value, env)
			}
		}
		// A constructor projection is its (re-evaluable) field argument, including after result
		// substitution has replaced the result identifier with the literal.
		if literal, ok := stripOptimizationParens(n.Object).(*ast.StructLitExpr); ok {
			if value, found := tr.a.structLitFieldMap(literal)[n.Field]; found {
				return tr.termEnv(value, env)
			}
		}
		// Constant enum members retain their exact backing value, not an unconstrained projection.
		if c, ok := tr.a.constIntValue(n); ok && !boundRoot {
			return tr.constIntTerm(n, c), true
		}
		// `(X if c else Y).f` is `X.f` where c holds and `Y.f` elsewhere.
		if tern, ok := stripOptimizationParens(n.Object).(*ast.TernaryExpr); ok && tern != nil {
			return tr.ternaryProjectionTerm(n, tern, env)
		}
		// A projection of an aggregate-valued call or call-initialized local (analyzer_smt_call_projection.go).
		if term, ok, handled := tr.aggregateProjectionTerm(n, env); handled {
			return term, ok
		}
		// `result.field` where `result` is bound to a struct literal → that field's construction
		// argument, so `ensure result.a == 1` for `return Pair(1, 2)` proves.
		if tr.resultFields != nil && tr.calleeBodyDepth == 0 && !tr.envBindsRoot(&ast.Ident{Name: "result"}, env) {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && id.Name == "result" {
				if arg, ok := tr.resultFields[n.Field]; ok {
					return tr.termEnv(arg, env)
				}
			}
		}
		// `arr.count` / `arr.len` → a per-array length Int symbol (derived from the array's SMT symbol,
		// so it resolves through `env` for `self.count`), asserted >= 0 in the preamble.
		// SPECIAL CASE: if the entire field expression evaluates to a compile-time constant (e.g.
		// `"abc".len == 3`), emit the integer directly so the proof discharges at the const tier
		// rather than introducing an opaque length symbol.
		// A tuple/struct field that happens to be NAMED `count`/`len` is an ordinary field read
		// (handled by the projection below), never a container length.
		if (n.Field == "count" || n.Field == "len") && !tr.a.recordFieldNamed(n.Object, n.Field) {
			if c, ok := tr.a.constIntValue(n); ok && !boundRoot {
				return tr.constIntTerm(n, c), true
			}
			if term, ok := tr.fixedArraySliceLengthTerm(n.Object, env); ok {
				return term, true
			}
			// An sview slice's `.len` is modeled exactly (string_view_slice clamps).
			if term, ok := tr.sliceLengthTerm(n.Object, env); ok {
				return term, true
			}
			// The scope-keyed view length names the CURRENT function's binding; a name the env binds
			// (a substituted callee formal) must resolve through the env instead, or a callee's
			// `other.len` would read the caller's same-named view.
			if n.Field == "len" && !tr.envBindsRoot(n.Object, env) {
				if key, ok := immutableSViewLengthProjection(tr.a, tr.a.currentScope, n); ok {
					lenSym := smtSViewLengthSymbol(key)
					tr.lenDecls[lenSym] = true
					return lenSym, true
				}
			}
			arr, ok := tr.arrayTermEnv(n.Object, env)
			if !ok {
				// `arrayTermEnv` models only INTEGER-element arrays (it lowers `arr[k]` to a `(select …)`),
				// so a darray of STRUCTS (`self.dmem_areas: darray[PhysicalMemoryArea]`) declines there. But
				// the LENGTH `.count`/`.len` is just a `usize` regardless of element type, and a measure /
				// guard only needs that scalar — never the element theory. When the object is provably an
				// array/darray of ANY element type, mint the length symbol keyed by the object's syntactic
				// projection (the same key arrayTermEnv would use for an Ident/Field path), so it resolves
				// to a CONSISTENT symbol between measure and measure[body]. This is what lets the emulator's
				// struct-element field-shift loops (`decreases self.xs.count - j`) discharge.
				lenSym, lok := tr.arrayLenSymForAnyElem(n.Object, env)
				if !lok {
					return "", false
				}
				tr.lenDecls[lenSym] = true
				return lenSym, true
			}
			lenSym := smtArrayLenOf(arr)
			if c, ok := tr.lenConsts[lenSym]; ok {
				return smtInt(c), true
			}
			tr.lenDecls[lenSym] = true
			return lenSym, true
		}
		// Field type from exprTypes (set once analyzed) OR resolved from the object's struct type when
		// the node is synthetic/cloned (e.g. a substituted derived-state condition, whose field reads
		// were never analyzed so their per-node type is unset).
		fieldType := tr.a.exprType(n)
		if fieldType == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				fieldType = resolved
			}
		}
		if t, ok := smtNumericValueType(fieldType); ok && !IsFloatType(t) {
			// Numeric struct-field reads are modeled as fresh-ish projection symbols keyed by
			// their syntactic path (`self.total`, `area.size`). We assert only facts guaranteed
			// by the field type (e.g. unsigned >= 0 in factPreamble), not any relation to other
			// fields or heap state. That is sound and is enough for practical contracts like
			// `ensure result <= self.total` on unsigned storage.
			name, ok := tr.fieldProjectionKey(n, env)
			if !ok {
				return "", false
			}
			tr.decls[name] = true
			tr.markIntVar(name, t)
			return smtVar(name), true
		}
		return "", false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return "", false
		}
		inner, ok := tr.termEnv(n.Operand, env)
		if !ok {
			return "", false
		}
		return "(- " + inner + ")", true
	case *ast.CastExpr:
		if term, ok := tr.sameWidthSignRoundTripEnv(n, env); ok {
			return term, true
		}
		// A value-preserving integer conversion — widening or same-width, SAME signedness
		// (`x.u64()`, `x.usize()`, an i32 used as i64) — is the IDENTITY in the unbounded-Int
		// model, so the prover sees THROUGH the conversion and refinement bounds survive it
		// (docs/85 gap #2). Narrowing and sign changes can wrap; the cases below model their bits
		// explicitly rather than treating them as identity.
		sourceType := tr.castOperandType(n)
		ssign, sbits, sok := smtIntWidthSign(sourceType)
		dsign, dbits, dok := smtIntWidthSign(tr.a.exprType(n))
		if !dok {
			// The result type is unset for a synthetic/cloned cast node (e.g. the loop-termination
			// prover's measure[body], whose `field.usize()` cast was produced by substitution and never
			// analyzed). Resolve the destination width/sign from the postfix cast TARGET NAME — the same
			// information the analyzer would have recorded — so a field-read cast in the measure cancels
			// against its analyzed twin. Declines for any non-integer / compound target (sound).
			dsign, dbits, dok = postfixCastTargetWidthSign(n)
		}
		// A literal that fits the destination is unchanged even by a narrowing
		// cast. Do not route this through float-based constant conversion: large
		// integer literals must retain their exact value in a proof.
		literal := stripOptimizationParens(n.Operand)
		negated := false
		if unary, ok := literal.(*ast.UnaryExpr); ok && unary.Op == lexer.TOKEN_MINUS && unary.LoweredCall == nil {
			literal = stripOptimizationParens(unary.Operand)
			negated = true
		}
		if _, integer := literal.(*ast.IntLit); integer && sok && dok && dbits > 0 && dbits <= 64 && (!negated || ssign) {
			if value, known := tr.a.constIntValue(n.Operand); known && (ssign || value >= 0) {
				limit := new(big.Int).Lsh(big.NewInt(1), uint(dbits))
				minimum := new(big.Int)
				if dsign {
					limit.Rsh(limit, 1)
					minimum.Neg(limit)
				}
				constant := big.NewInt(value)
				if constant.Cmp(minimum) >= 0 && constant.Cmp(limit) < 0 {
					return tr.termEnv(n.Operand, env)
				}
			}
		}
		// `char` is represented as an i64 value, while string indexing produces a
		// byte-sized code unit. Its conversion to u8 is LLVM truncation, exactly
		// unsigned modulo 256 for every possible char value. Model that conversion
		// explicitly instead of treating char as an 8-bit integer globally: char
		// has other i64-producing sources and may carry values outside the byte range.
		if source, ok := stripRefForBounds(sourceType).(*BuiltinType); ok && source != nil && source.Name == "char" && dok && !dsign && dbits == 8 {
			inner, ok := tr.termEnv(n.Operand, env)
			if !ok {
				return "", false
			}
			return smtModTerm(inner, "256"), true
		}
		// A char uses the runtime's signed i64 representation. Converting to
		// u64 preserves those bits, including negative values; it is not an
		// unbounded integer identity. This also models byte reads inside callee
		// clauses, where the caller-only stable-view shortcut is unavailable.
		if source, ok := sourceType.(*BuiltinType); ok && source != nil && source.Name == "char" && isPureNumericValueCast(n) && dok && !dsign && dbits == 64 {
			if read, ok := stripOptimizationParens(n.Operand).(*ast.IndexExpr); ok {
				if term, stable := tr.stableSviewByteIndex(read, env); stable {
					return term, true
				}
			}
			inner, ok := tr.termEnv(n.Operand, env)
			if !ok {
				return "", false
			}
			return smtSameWidthReinterpret(inner, false, 64), true
		}
		// `char` is represented as a signed 64-bit value. Widening it to an
		// equal-width signed integer is therefore the identity, including inside
		// substituted callee contracts where generic width lookup intentionally
		// does not borrow a same-named caller type.
		if source, ok := stripRefForBounds(sourceType).(*BuiltinType); ok && source != nil && source.Name == "char" && dok && dsign && dbits == 64 {
			return tr.termEnv(n.Operand, env)
		}
		if !sok || !dok {
			return "", false
		}
		if ssign == dsign && dbits >= sbits {
			return tr.termEnv(n.Operand, env)
		}
		// Unsigned -> strictly wider signed (`(u8 b).i32()`): the source is non-negative and fits the
		// wider signed range, so the conversion is the identity on the value.
		if !ssign && dsign && dbits > sbits {
			return tr.termEnv(n.Operand, env)
		}
		// Same-width sign reinterpretation preserves the bits. Narrowing uses LLVM truncation,
		// preserving only the low destination-width bits, irrespective of source signedness.
		// In both cases, read those bits with the destination's signed/unsigned interpretation.
		if sbits == dbits || (dbits > 0 && dbits < sbits && sbits <= 64) {
			inner, ok := tr.termEnv(n.Operand, env)
			if !ok {
				return "", false
			}
			return smtSameWidthReinterpret(inner, dsign, dbits), true
		}
		return "", false
	case *ast.GetExpr:
		// `get d[k] else f` is the explicit, total unwrap of a dict lookup (the `else f` is consumed at
		// the postfix `[k]` level): the stored value when k is a member, the fallback otherwise. Modeled
		// exactly as an `ite` over the membership array, so it needs no separate `k in d` hypothesis to
		// be sound. (A bare `d.get(k)` is a `T&?` and has no value to compare until unwrapped.)
		idx, ok := stripOptimizationParens(n.Value).(*ast.IndexExpr)
		if !ok || idx.Fallback == nil || n.Fallback != nil || n.Recovery != nil || idx.Index2 != nil {
			return "", false
		}
		dsym, ksort, ok := tr.dictTermEnv(idx.Object, env)
		if !ok {
			return "", false
		}
		key, ok := tr.keyTermEnv(idx.Index, ksort, env)
		if !ok {
			return "", false
		}
		alt, ok := tr.termEnv(idx.Fallback, env)
		if !ok {
			return "", false
		}
		return "(ite (select " + dsym + "_keys " + key + ") (select " + dsym + "_vals " + key + ") " + alt + ")", true
	case *ast.TernaryExpr:
		cond, ok := tr.boolTerm(n.Cond, env)
		if !ok {
			return "", false
		}
		if cond == "true" {
			return tr.termEnv(n.Value, env)
		}
		if cond == "false" {
			return tr.termEnv(n.Alt, env)
		}
		v, ok := tr.termEnv(n.Value, env)
		if !ok {
			return "", false
		}
		alt, ok := tr.termEnv(n.Alt, env)
		if !ok {
			return "", false
		}
		return "(ite " + cond + " " + v + " " + alt + ")", true
	case *ast.BinaryExpr:
		var op string
		switch n.Op {
		case lexer.TOKEN_PLUS:
			op = "+"
		case lexer.TOKEN_MINUS:
			op = "-"
		case lexer.TOKEN_STAR:
			op = "*"
		case lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			// SMT-LIB `div`/`mod` are Euclidean, while Elisa integer division truncates toward zero.
			// Model truncating division explicitly from abs/sign, and model remainder as x-y*q. Still
			// require a provably non-zero divisor: SMT-LIB division is total at zero, which could
			// otherwise fabricate proofs for source programs that may divide by zero at runtime.
			// When the divisor is NOT provably non-zero we do not give up on the whole clause —
			// that would lose a guarding `or` (`alignment == 0 or (x % alignment) == 0`) or an
			// outer disjunct that makes the clause hold regardless of this sub-term. Instead we
			// mint a fresh under-constrained symbol: a free integer, asserted `>= 0` only when the
			// dividend is provably non-negative (true of unsigned `%`/`/`). That is sound — it
			// states nothing false — so the result is just opaque, never a fabricated proof.
			if !tr.a.provablyNonZero(n.Right) {
				return tr.freshAux(tr.a.provablyNonNeg(n.Left)), true
			}
			l, ok := tr.termEnv(n.Left, env)
			if !ok {
				return "", false
			}
			r, ok := tr.termEnv(n.Right, env)
			if !ok {
				return "", false
			}
			// Preferred path: a non-negative dividend with a positive divisor (the overwhelmingly
			// common integer case — unsigned `%`/`/`, page/alignment math). Use SMT-LIB's NATIVE
			// `mod`/`div` directly: they are Euclidean, and Euclidean == truncating for non-negative
			// operands, so this is exact (no abs/sign dance). It also engages z3's dedicated div/mod
			// theory, which discharges divisibility goals like `(value - value%alignment) %
			// alignment == 0` instantly where the nested-`div`/fresh-symbol encodings stall. Identical
			// `(mod l r)` sub-terms are syntactically shared, so a `rem = value % alignment`
			// hypothesis and a later `value % alignment` obligation refer to the same term.
			if tr.a.provablyNonNeg(n.Left) && tr.a.provablyPositive(n.Right) {
				if n.Op == lexer.TOKEN_PERCENT {
					return smtModTerm(l, r), true
				}
				return smtDivTerm(l, r), true
			}
			// SOUNDNESS: signed `INT_MIN / -1` (and `INT_MIN % -1`) OVERFLOWS — the true result -INT_MIN is
			// not representable. On the arm64 target it wraps to INT_MIN (no hardware trap), so the exact
			// unbounded-ℤ model (smtTruncDiv gives -INT_MIN, positive) would prove a false `result >= 0`.
			// Unless that overflow is provably impossible (divisor cannot be -1, or dividend cannot be
			// INT_MIN), abstract the result as a fresh under-constrained symbol — sound (states nothing
			// false), at worst declines the proof (audit cluster F).
			if !tr.signedDivCannotOverflow(n.Left, n.Right) {
				return tr.freshAux(tr.a.provablyNonNeg(n.Left)), true
			}
			if n.Op == lexer.TOKEN_PERCENT {
				q := smtTruncDiv(l, r)
				return smtArith("-", l, smtArith("*", r, q)), true
			}
			return smtTruncDiv(l, r), true
		case lexer.TOKEN_AMPERSAND, lexer.TOKEN_PIPE, lexer.TOKEN_CARET,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			// Bitwise/shift ops are modeled exactly via the SMT bitvector theory (see bitwiseTerm).
			return tr.bitwiseTerm(n, env)
		default:
			return "", false
		}
		l, ok := tr.termEnv(n.Left, env)
		if !ok {
			return "", false
		}
		r, ok := tr.termEnv(n.Right, env)
		if !ok {
			return "", false
		}
		return tr.wrapMachineArith(n, op, l, r), true
	default:
		return "", false
	}
}

// wrapMachineArith models the machine semantics of `+`/`-`/`*` on a fixed-width integer result, which
// WRAP modulo 2^width (two's complement for signed). Modeling them as unbounded-ℤ operations is
// UNSOUND for postconditions a wrap can violate — e.g. `ensure result <= a` for unsigned `a - b` holds
// in ℤ (b ≥ 0) but is FALSE when b > a (underflow), and `ensure result >= a` for signed `a + b` holds
// in ℤ (b ≥ 0) but is FALSE on signed overflow. When the result is PROVABLY in range (affine interval
// within the type, or a no-underflow subtraction) the ℤ term equals the machine value, so we emit it
// clean — keeping divisibility/alignment goals tractable. Otherwise we emit the exact wrapped value:
// `(mod raw 2^W)` for unsigned (Euclidean mod == unsigned wrap), and a recentered mod for signed. The
// prover only ever concludes on `unsat`, so a wrap can never fabricate a proof — at worst it declines.
func (tr *smtTranslator) wrapMachineArith(n *ast.BinaryExpr, op, l, r string) string {
	raw := smtArith(op, l, r)
	signed, bits, ok := smtIntWidthSign(tr.a.exprType(n))
	if !ok || bits <= 0 || bits > 64 {
		return raw
	}
	if tr.a.provablyNoArithWrap(n, signed, bits) {
		// Provably in range: the ℤ value equals the machine value — emit it clean (keeps divisibility /
		// alignment goals tractable and lets bounded arithmetic prove).
		return raw
	}
	if signed {
		// SOUNDNESS: signed `+`/`-`/`*` that can overflow WRAPS two's-complement at runtime — there is no
		// codegen trap backing the old "assume no overflow" stance, so on the target `i64max + 1` becomes
		// i64min, and the clean ℤ value would prove a false `ensure result > 0`. Model the exact wrapped
		// value: recenter into [0, 2^W), take the unsigned wrap, then shift back into the signed range —
		// `((raw + 2^(W-1)) mod 2^W) - 2^(W-1)`. The prover only concludes on `unsat`, so this can only
		// decline an unprovable goal, never fabricate one.
		return smtSignedWrap(raw, bits)
	}
	// Unsigned wraps modulo 2^W with well-defined two's-complement semantics (and is frequently an
	// intentional idiom), so it is modeled exactly: `(mod raw 2^W)` is the wrapped value. This is what
	// keeps `ensure result <= total; return total - usage` honest (declines without a guard).
	return smtModTerm(raw, smtPow2(bits))
}

// smtSignedWrap renders the two's-complement wrap of a `bits`-wide signed result: the machine value of
// `raw` is `((raw + 2^(W-1)) mod 2^W) - 2^(W-1)`, which maps any ℤ value into [-2^(W-1), 2^(W-1)).
func smtSignedWrap(raw string, bits int) string {
	half := smtPow2(bits - 1)
	full := smtPow2(bits)
	return smtArith("-", smtModTerm(smtArith("+", raw, half), full), half)
}
