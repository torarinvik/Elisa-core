package semantic

import (
	"encoding/hex"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// boolTerm lowers a boolean-valued expression: comparisons, and/or/not, parens, and bool literals.
// `env` maps an Elisa identifier (notably the law's `self`) to a pre-built SMT term.
func (tr *smtTranslator) boolTerm(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.IndexExpr:
		return tr.booleanArrayIndexTerm(n, env)
	case *ast.ExprBlock:
		return tr.runtimeRangeQuantifierTerm(n, env)
	case *ast.TernaryExpr:
		// A bool-valued conditional `v if c else w` (the folded form of guard-return chains).
		if n == nil || n.Cond == nil || n.Value == nil || n.Alt == nil {
			return "", false
		}
		c, ok := tr.boolTerm(n.Cond, env)
		if !ok {
			return "", false
		}
		v, ok := tr.boolTerm(n.Value, env)
		if !ok {
			return "", false
		}
		w, ok := tr.boolTerm(n.Alt, env)
		if !ok {
			return "", false
		}
		return "(ite " + c + " " + v + " " + w + ")", true
	case *ast.MatchExpr:
		if term, ok := tr.enumMatchTerm(n, env, tr.boolTerm); ok {
			return term, true
		}
		if term, ok := tr.constEnumMatchTerm(n, env, tr.boolTerm); ok {
			return term, true
		}
		return tr.booleanMatchTerm(n, env)
	case *ast.ParenExpr:
		return tr.boolTerm(n.Inner, env)
	case *ast.QuantifierExpr:
		if n.In != nil {
			// `forall x in s` / `exists x in s` over a SET: bind `x` to a fresh key symbol of the set's
			// key sort, guard membership with `(select s x)`, and emit a `:pattern` on that select so z3
			// has a deterministic E-matching trigger (mirrors the array path; MBQI completes the rest).
			if ssym, ksort, ok := tr.setTermEnv(n.In, env); ok {
				if len(n.Vars) != 1 {
					return "", false
				}
				kSym := "q_" + n.Vars[0] + "_k"
				qenv := make(map[string]string, len(env)+1)
				for k, v := range env {
					qenv[k] = v
				}
				qenv[n.Vars[0]] = kSym
				body, ok := tr.boolTerm(n.Body, qenv)
				if !ok {
					return "", false
				}
				mem := "(select " + ssym + " " + kSym + ")"
				if n.Exists {
					return "(exists ((" + kSym + " " + ksort + ")) (! (and " + mem + " " + body + ") :pattern (" + mem + ")))", true
				}
				return "(forall ((" + kSym + " " + ksort + ")) (! (or (not " + mem + ") " + body + ") :pattern (" + mem + ")))", true
			}
			// `forall (key,v) in d` / `forall key in d` over a DICT: bind the key to a fresh key symbol
			// and (when a value binder is present) the value to `(select d_vals key)`. Membership
			// `(select d_keys key)` guards the body, and is the `:pattern` trigger.
			if dsym, ksort, ok := tr.dictTermEnv(n.In, env); ok {
				if len(n.Vars) != 1 && len(n.Vars) != 2 {
					return "", false
				}
				kSym := "q_" + n.Vars[0] + "_k"
				qenv := make(map[string]string, len(env)+2)
				for k, v := range env {
					qenv[k] = v
				}
				qenv[n.Vars[0]] = kSym
				if len(n.Vars) == 2 {
					qenv[n.Vars[1]] = "(select " + dsym + "_vals " + kSym + ")"
				}
				body, ok := tr.boolTerm(n.Body, qenv)
				if !ok {
					return "", false
				}
				mem := "(select " + dsym + "_keys " + kSym + ")"
				if n.Exists {
					return "(exists ((" + kSym + " " + ksort + ")) (! (and " + mem + " " + body + ") :pattern (" + mem + ")))", true
				}
				return "(forall ((" + kSym + " " + ksort + ")) (! (or (not " + mem + ") " + body + ") :pattern (" + mem + ")))", true
			}
			if len(n.Vars) != 1 {
				return "", false
			}
			arr, ok := tr.arrayTermEnv(n.In, env)
			if !ok {
				return "", false
			}
			idxSym := "q_" + n.Vars[0] + "_idx"
			qenv := make(map[string]string, len(env)+1)
			for k, v := range env {
				qenv[k] = v
			}
			inputType := tr.arrayReadOperandType(n.In)
			if inputType == nil {
				return "", false
			}
			element := "(select " + arr + " " + idxSym + ")"
			if smtBooleanArrayType(inputType) {
				element = "(not (= " + element + " 0))"
			}
			qenv[n.Vars[0]] = element
			body, ok := tr.boolTerm(n.Body, qenv)
			if !ok {
				return "", false
			}
			lenSym := smtArrayLenOf(arr)
			tr.lenDecls[lenSym] = true
			guard := "(and (<= 0 " + idxSym + ") (< " + idxSym + " " + lenSym + "))"
			// E-matching trigger (docs/90 brick 90-16): a `forall x in xs` / `forall i in xs.indices`
			// quantifier binds the index `idxSym` and models the element as `(select arr idxSym)`. That
			// select term is the canonical instantiation pattern, so emit it as `:pattern`. Previously this
			// `In`-source path went out as a BARE quantifier, leaving z3 to auto-pattern infer (or loop on a
			// bad inferred trigger). Soundness/completeness hold: the trigger only guides E-matching, and
			// MBQI (preamble-forced on) still completes any goal the trigger alone would miss.
			elemSel := "(select " + arr + " " + idxSym + ")"
			if n.Exists {
				return "(exists ((" + idxSym + " Int)) (! (and " + guard + " " + body + ") :pattern (" + elemSel + ")))", true
			}
			return "(forall ((" + idxSym + " Int)) (! (or (not " + guard + ") " + body + ") :pattern (" + elemSel + ")))", true
		}
		// Bind each quantifier variable to a fresh SMT Int symbol (prefix "q_" so it never collides
		// with a free variable's "v_" symbol), then translate the body under the extended environment.
		qenv := make(map[string]string, len(env)+len(n.Vars))
		for k, v := range env {
			qenv[k] = v
		}
		decls := make([]string, 0, len(n.Vars))
		for _, v := range n.Vars {
			sym := "q_" + v
			qenv[v] = sym
			decls = append(decls, "("+sym+" Int)")
		}
		body, ok := tr.boolTerm(n.Body, qenv)
		if !ok {
			if body, ok = tr.opaqueGuardedQuantifierBody(n, qenv); !ok {
				return "", false
			}
		}
		q := "forall"
		if n.Exists {
			q = "exists"
		}
		// Attach an E-matching trigger (docs/90 brick 90-16): for a quantifier over array contents, the
		// `(select <arr> <idx>)` subterms whose index mentions a bound variable are the canonical
		// instantiation pattern. Emitting them as `(! body :pattern (...))` gives z3 a deterministic,
		// cheap instantiation strategy instead of relying on auto-pattern inference. Soundness/
		// completeness are preserved: triggers only guide E-matching, and z3's MBQI (on by default)
		// still completes any goal the trigger alone would miss. A purely arithmetic quantifier (no
		// select term mentioning a binder) gets no pattern — there is no good ground trigger, so it is
		// left to MBQI exactly as before.
		if triggers := tr.collectSelectTriggers(n.Body, qenv, n.Vars); len(triggers) > 0 {
			body = "(! " + body + " :pattern (" + strings.Join(triggers, " ") + "))"
		}
		return "(" + q + " (" + strings.Join(decls, " ") + ") " + body + ")", true
	case *ast.BoolLit:
		if n.Value {
			return "true", true
		}
		return "false", true
	case *ast.Ident:
		if value, constant := tr.a.resolvedPrimitiveConstant(n); constant && value.Kind == ConstBool {
			return strconv.FormatBool(value.Bool), true
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
		if c, ok := tr.a.evalConstBoolExpr(n); ok {
			if c {
				return "true", true
			}
			return "false", true
		}
		// A free IMMUTABLE bool-typed identifier (a bool param/immutable local) → a stable SMT Bool
		// const, so a boolean postcondition over it (`ensure result == (p != q)`) can discharge. Only
		// immutable bindings qualify (a reassigned bool would make the const stale).
		if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil && !sym.Mutable && IsBoolType(sym.Type) {
			name := smtBoolVar(n.Name)
			tr.boolDecls[name] = true
			if sym.Kind == SymbolLocal {
				if tr.boolLocalRefs == nil {
					tr.boolLocalRefs = make(map[string]*Symbol)
				}
				tr.boolLocalRefs[name] = sym
				if tr.onBoolIdentifier != nil {
					tr.onBoolIdentifier(name)
				}
			}
			return name, true
		}
		// A by-value MUTABLE bool local/param denotes its value at this proof point, exactly like a
		// mutable integer local in termEnv: every flow fact about it is retired at each write (the
		// mutation chokepoint), and no defining equation is ever asserted for a mutable binding.
		if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil && sym.Mutable && (sym.Kind == SymbolLocal || sym.Kind == SymbolParam) && IsBoolType(sym.Type) {
			if _, isRef := sym.Type.(*RefType); !isRef {
				name := smtBoolVar(n.Name)
				tr.boolDecls[name] = true
				return name, true
			}
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
		if term, handled, ok := tr.recordElementFieldTerm(n, env, "Bool"); handled {
			return term, ok
		}
		if _, nested := stripOptimizationParens(n.Object).(*ast.FieldExpr); nested && !boundRoot {
			if value, found := tr.a.nestedLiteralFieldValue(n, 0); found {
				if _, constant := stripOptimizationParens(value).(*ast.BoolLit); constant {
					return tr.boolTerm(value, env)
				}
			}
		}
		if !boundRoot {
			if rerooted, ok := tr.a.rerootLiteralProjection(n); ok {
				return tr.boolTerm(rerooted, env)
			}
		}
		if tuple, ok := stripOptimizationParens(n.Object).(*ast.TupleExpr); ok {
			if value, found := tr.a.tupleLiteralFieldValue(tuple, n.Field); found {
				return tr.boolTerm(value, env)
			}
		}
		// A constructor projection is its field argument, including after result
		// substitution has replaced the result identifier with the literal.
		if literal, ok := stripOptimizationParens(n.Object).(*ast.StructLitExpr); ok {
			if value, found := tr.a.structLitFieldMap(literal)[n.Field]; found {
				return tr.boolTerm(value, env)
			}
		}
		if tern, ok := stripOptimizationParens(n.Object).(*ast.TernaryExpr); ok && tern != nil {
			return tr.ternaryProjectionBool(n, tern, env)
		}
		if tr.resultFields != nil && tr.calleeBodyDepth == 0 && !tr.envBindsRoot(&ast.Ident{Name: "result"}, env) {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && id.Name == "result" {
				if value, found := tr.resultFields[n.Field]; found {
					return tr.boolTerm(value, env)
				}
			}
		}
		if term, ok, handled := tr.aggregateProjectionBool(n, env); handled {
			return term, ok
		}
		fieldType := tr.a.exprType(n)
		if fieldType == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				fieldType = resolved
			}
		}
		if IsBoolType(fieldType) {
			key, ok := tr.fieldProjectionKey(n, env)
			if !ok {
				return "", false
			}
			pred := smtBoolVar(key)
			tr.boolDecls[pred] = true
			return pred, true
		}
		return "", false
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			inner, ok := tr.boolTerm(n.Operand, env)
			if !ok {
				return "", false
			}
			if inner == "true" {
				return "false", true
			}
			if inner == "false" {
				return "true", true
			}
			return "(not " + inner + ")", true
		}
		return "", false
	case *ast.BinaryExpr:
		if term, ok := tr.sviewContentEqualityTerm(n, env); ok {
			return term, true
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			l, ok := tr.boolTerm(n.Left, env)
			if !ok {
				return "", false
			}
			if n.Op == lexer.TOKEN_OR && l == "true" {
				return "true", true
			}
			if n.Op == lexer.TOKEN_AND && l == "false" {
				return "false", true
			}
			r, ok := tr.boolTerm(n.Right, env)
			if !ok {
				return "", false
			}
			if n.Op == lexer.TOKEN_OR {
				if r == "true" {
					return "true", true
				}
				if l == "false" {
					return r, true
				}
			}
			if n.Op == lexer.TOKEN_AND {
				if r == "false" {
					return "false", true
				}
				if l == "true" {
					return r, true
				}
			}
			conn := "and"
			if n.Op == lexer.TOKEN_OR {
				conn = "or"
			}
			return "(" + conn + " " + l + " " + r + ")", true
		case lexer.TOKEN_IN:
			// Membership `p in s` / `k in d` (docs/90 set/dict modeling). A set is its membership
			// predicate directly; a dict's membership is its `<d>_keys` array. `not in` already lowers to
			// `not (x in y)` at parse time, so only the positive form is handled here.
			if ssym, ksort, ok := tr.setTermEnv(n.Right, env); ok {
				elem, ok := tr.keyTermEnv(n.Left, ksort, env)
				if !ok {
					return "", false
				}
				return "(select " + ssym + " " + elem + ")", true
			}
			if dsym, ksort, ok := tr.dictTermEnv(n.Right, env); ok {
				key, ok := tr.keyTermEnv(n.Left, ksort, env)
				if !ok {
					return "", false
				}
				return "(select " + dsym + "_keys " + key + ")", true
			}
			return tr.literalMembershipTerm(n.Left, n.Right, env)
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
			l, ok := tr.termEnv(n.Left, env)
			if !ok {
				return "", false
			}
			r, ok := tr.termEnv(n.Right, env)
			if !ok {
				return "", false
			}
			if cmp, ok := tr.bitvectorCompare(n, l, r, env); ok {
				return cmp, true
			}
			return tr.mixedSignCompare(n, l, r), true
		case lexer.TOKEN_IS:
			return tr.enumIsTerm(n, env, false)
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			if eq, ok := tr.enumEqualityTerm(n, env); ok {
				return eq, true
			}
			if eq, ok := tr.constantStringEquality(n.Left, n.Right, env, 0); ok {
				if n.Op == lexer.TOKEN_BANGEQ {
					return "(not " + eq + ")", true
				}
				return eq, true
			}
			if n.LoweredCall != nil {
				// A user equality operator compares values, not pointer identity.
				return tr.boolTerm(n.LoweredCall, env)
			}
			// Pointer-null test (`p == null` / `p != null`). Model null-ness as a Bool predicate keyed by
			// the pointer's syntactic path, so the same pointer's null-ness is consistent between a branch
			// guard and the obligation. This lets `ensure (p != null) or (result == false)` discharge: the
			// fall-through guard `p != null` and the disjunct refer to the same predicate.
			if ptr := nullComparePointer(n); ptr != nil {
				if tr.a != nil && tr.a.shadowingActive() {
					return "", false
				}
				if tr.pointerHasUnboundCalleeAddress(ptr, env) {
					return "", false
				}
				ptr = tr.pointerLiteralOperand(ptr, env, 0)
				if _, null := stripOptimizationParens(ptr).(*ast.NullLit); null {
					return strconv.FormatBool(n.Op == lexer.TOKEN_EQEQ), true
				}
				if tr.calleeFreeProjection(ptr, env) {
					return "", false
				}
				pred := "isnull_" + smtProjectionName(stripOptimizationParens(ptr))
				tr.boolDecls[pred] = true
				if n.Op == lexer.TOKEN_EQEQ {
					return pred, true
				}
				return "(not " + pred + ")", true
			}
			// Pointer identity implies equal nullness, but the converse only holds
			// when both pointers are null. Keep identity as a separate predicate:
			// distinct non-null pointers must remain a possible counterexample.
			if tr.a != nil {
				leftType := tr.pointerIdentityOperandType(n.Left)
				rightType := tr.pointerIdentityOperandType(n.Right)
				if tr.a.exprIsPointerLike(leftType) && tr.a.exprIsPointerLike(rightType) {
					if tr.a.shadowingActive() || smtEnvHasQuantifierBinder(env) || tr.calleeFreeProjection(n.Left, env) || tr.calleeFreeProjection(n.Right, env) {
						return "", false
					}
					if tr.pointerHasUnboundCalleeAddress(n.Left, env) || tr.pointerHasUnboundCalleeAddress(n.Right, env) {
						if tr.pointerIdentityReadStable(n.Left) && tr.pointerIdentityReadStable(n.Right) && tr.samePointerIdentityExpr(n.Left, n.Right) {
							return strconv.FormatBool(n.Op == lexer.TOKEN_EQEQ), true
						}
						return "", false
					}
					left := tr.pointerLiteralOperand(n.Left, env, 0)
					right := tr.pointerLiteralOperand(n.Right, env, 0)
					if tr.calleeFreeProjection(left, env) || tr.calleeFreeProjection(right, env) {
						return "", false
					}
					if !tr.a.shadowingActive() && tr.pointerIdentityReadStable(left) && tr.pointerIdentityReadStable(right) && tr.samePointerIdentityExpr(left, right) {
						return strconv.FormatBool(n.Op == lexer.TOKEN_EQEQ), true
					}
					lPred := "isnull_" + smtProjectionName(left)
					rPred := "isnull_" + smtProjectionName(right)
					tr.boolDecls[lPred] = true
					tr.boolDecls[rPred] = true
					if rPred < lPred {
						lPred, rPred = rPred, lPred
					}
					eq := "|@pointer_eq_" + hex.EncodeToString([]byte(strconv.Itoa(len(lPred))+":"+lPred+rPred)) + "|"
					if tr.ghostUFDecls == nil {
						tr.ghostUFDecls = map[string]bool{}
					}
					if !tr.ghostUFDecls[eq] {
						tr.ghostUFDecls[eq] = true
						tr.auxDecls = append(tr.auxDecls,
							"(declare-const "+eq+" Bool)\n",
							"(assert (=> "+eq+" (= "+lPred+" "+rPred+")))\n",
							"(assert (=> (and "+lPred+" "+rPred+") "+eq+"))\n")
					}
					if n.Op == lexer.TOKEN_BANGEQ {
						return "(not " + eq + ")", true
					}
					return eq, true
				}
			}
			// Numeric equality is the common case; try integer terms first.
			if l, lok := tr.termEnv(n.Left, env); lok {
				if r, rok := tr.termEnv(n.Right, env); rok {
					if l == r {
						if n.Op == lexer.TOKEN_EQEQ {
							return "true", true
						}
						return "false", true
					}
					return tr.mixedSignCompare(n, l, r), true
				}
			}
			// Boolean equality: both sides are bool-valued (e.g. `result == ((x % m) == 0)`). SMT `=`
			// is polymorphic, so equate the two Bool terms directly (negated for `!=`).
			if bl, blok := tr.boolTerm(n.Left, env); blok {
				if br, brok := tr.boolTerm(n.Right, env); brok {
					if bl == br {
						if n.Op == lexer.TOKEN_EQEQ {
							return "true", true
						}
						return "false", true
					}
					eq := "(= " + bl + " " + br + ")"
					if n.Op == lexer.TOKEN_BANGEQ {
						return "(not " + eq + ")", true
					}
					return eq, true
				}
			}
			return "", false
		default:
			return "", false
		}
	case *ast.CallExpr:
		// SURFACE dict membership `d.contains(k)` maps to the dict key array. This must run BEFORE the
		// structural-predicate path, which would otherwise canonicalize `arena_dict_contains` into an
		// opaque predicate + defining equation, severing it from the dict model (so a `requires
		// d.contains(k)` guard would no longer constrain the modeled key set).
		if sel, ok := tr.dictMethodCall(n, "keys", env); ok {
			return sel, true
		}
		if !tr.opaqueCallQuantifierIndependent(n, env) {
			return tr.scalarCallUF(n, env, "Bool")
		}
		// Try structural-predicate canonicalization first (a `ghost def ... -> bool` over a recursive
		// datatype canonicalizes to one opaque Bool symbol + its bounded structural defining equation —
		// see analyzer_structural_induction.go); fall back to the general ghost bool-call path for
		// non-predicate bool-returning calls so the docs/101 ghost-beyond-int behavior is preserved.
		if sym, ok := tr.boolPredicateCallSym(n, env); ok {
			tr.linkScalarCallUF(n, env, sym, "Bool")
			return sym, true
		}
		return tr.callResultBoolTermEnv(n, env)
	default:
		return "", false
	}
}
