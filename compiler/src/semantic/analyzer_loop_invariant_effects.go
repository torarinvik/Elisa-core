package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// loopRecordLocalAdmissible admits an immutable body local whose definition reads only pre-body
// state: no variable or array the body already wrote, no other local, and no name the loop state
// or the enclosing scope already binds (shadowing would change what later reads mean).
func (a *Analyzer) loopRecordLocalAdmissible(n *ast.VarDeclStmt, locals map[string]ast.Expr, assigned, storedArrays map[string]bool) bool {
	if n == nil || n.Name == "" || n.Mutable || n.Ghost || n.Value == nil || n.Owner != nil {
		return false
	}
	if _, dup := locals[n.Name]; dup || assigned[n.Name] || storedArrays[n.Name] {
		return false
	}
	if sym, found := a.currentScope.Lookup(n.Name); found && sym != nil {
		return false
	}
	if !a.seqLoopValueAdmissible(n.Value) {
		return false
	}
	refs := map[string]bool{}
	collectExprIdentNames(n.Value, refs)
	for r := range refs {
		if _, isLocal := locals[r]; isLocal || assigned[r] || storedArrays[r] {
			return false
		}
	}
	return true
}

// loopRecordStoreValue admits `array[index] <- value` storing a whole record into a bare array of
// records. The value is a read-only expression of pre-body state (locals read through their
// definitions); the array is stored once and never read by its own index or value after a write.
func (a *Analyzer) loopRecordStoreValue(target *ast.IndexExpr, value ast.Expr, locals map[string]ast.Expr, assigned, storedArrays map[string]bool) (ast.Expr, bool) {
	arr, ok := stripOptimizationParens(target.Object).(*ast.Ident)
	if !ok || arr == nil || storedArrays[arr.Name] {
		return nil, false
	}
	sym, found := a.currentScope.Lookup(arr.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || !isDArrayTypeMaybeRef(sym.Type) {
		return nil, false
	}
	element := darrayElementTypeMaybeRef(sym.Type)
	if _, record := StripAggregateStateType(element).(*StructType); !record {
		return nil, false
	}
	if !exprIsPureArith(target.Index) {
		return nil, false
	}
	refs := map[string]bool{}
	collectArithIdents(target.Index, refs)
	for r := range refs {
		if assigned[r] {
			return nil, false
		}
	}
	if !a.loopRecordValueAdmissible(value) {
		return nil, false
	}
	refs = map[string]bool{}
	collectExprIdentNames(value, refs)
	for r := range refs {
		if definition, isLocal := locals[r]; isLocal {
			collectExprIdentNames(definition, refs)
		}
	}
	for r := range refs {
		if assigned[r] || storedArrays[r] {
			return nil, false
		}
	}
	value, ok = a.loopRecordValueOverPreState(value, locals)
	if !ok {
		return nil, false
	}
	// The body is captured before it is analyzed: type the pre-state value so the translator can
	// bind its record and bool arguments.
	a.typeSequentialPathsQuietly([]loopBodyPath{{subst: map[string]ast.Expr{"": value}}})
	return value, true
}

// loopRecordValueAdmissible admits a stored record value: an admissible read or call, or a
// named-field literal of admissible values.
func (a *Analyzer) loopRecordValueAdmissible(value ast.Expr) bool {
	lit, ok := stripOptimizationParens(value).(*ast.StructLitExpr)
	if !ok {
		return a.seqLoopValueAdmissible(value)
	}
	if lit == nil || len(lit.Spreads) != 0 || lit.CopyBase != nil || len(lit.ArgNames) != len(lit.Args) {
		return false
	}
	for _, arg := range lit.Args {
		if !a.loopRecordValueAdmissible(arg) {
			return false
		}
	}
	return true
}

// loopRecordValueOverPreState reads body locals through their definitions. A named-field literal
// is rebuilt field by field.
func (a *Analyzer) loopRecordValueOverPreState(value ast.Expr, locals map[string]ast.Expr) (ast.Expr, bool) {
	if len(locals) == 0 {
		return value, true
	}
	lit, isLiteral := stripOptimizationParens(value).(*ast.StructLitExpr)
	if !isLiteral {
		substituted, ok := a.substituteTypedIdents(value, locals, 0)
		return substituted, ok && substituted != nil
	}
	rebuilt := *lit
	rebuilt.Args = make([]ast.Expr, len(lit.Args))
	for i, arg := range lit.Args {
		field, ok := a.loopRecordValueOverPreState(arg, locals)
		if !ok {
			return nil, false
		}
		rebuilt.Args[i] = field
	}
	return &rebuilt, true
}

func darrayElementTypeMaybeRef(t Type) Type {
	if r, ok := t.(*RefType); ok {
		t = r.Elem
	}
	if d, ok := StripAggregateStateType(t).(*DArrayType); ok && d != nil {
		return d.Elem
	}
	return nil
}

func (a *Analyzer) loopRecordStored(stores []loopArrayStore, name string) bool {
	for _, st := range stores {
		if st.record && st.arrayName == name {
			return true
		}
	}
	return false
}

func exprIsSideEffectFreeForTermination(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit, *ast.Ident:
		return true
	case *ast.ParenExpr:
		return n != nil && exprIsSideEffectFreeForTermination(n.Inner)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && exprIsSideEffectFreeForTermination(n.Object)
	case *ast.IndexExpr:
		return n != nil &&
			n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback &&
			n.AsSpecialize == nil && n.AsOverlayCall == nil &&
			exprIsSideEffectFreeForTermination(n.Object) && exprIsSideEffectFreeForTermination(n.Index)
	case *ast.SliceExpr:
		return n != nil &&
			exprIsSideEffectFreeForTermination(n.Object) &&
			(n.Start == nil || exprIsSideEffectFreeForTermination(n.Start)) &&
			(n.End == nil || exprIsSideEffectFreeForTermination(n.End))
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && exprIsSideEffectFreeForTermination(n.Operand)
	case *ast.BinaryExpr:
		return n != nil && exprIsSideEffectFreeForTermination(n.Left) && exprIsSideEffectFreeForTermination(n.Right)
	case *ast.CastExpr:
		return n != nil && exprIsSideEffectFreeForTermination(n.Operand)
	case *ast.TernaryExpr:
		return n != nil &&
			exprIsSideEffectFreeForTermination(n.Cond) &&
			exprIsSideEffectFreeForTermination(n.Value) &&
			exprIsSideEffectFreeForTermination(n.Alt)
	default:
		return false
	}
}

// loopBodyAssertIsPure recognizes only an unshadowed builtin `assert(cond)` whose condition is
// side-effect-free. Loop proof models may ignore such a statement because it does not mutate the
// modeled state. They must not assume cond: optimized builds can erase the runtime assertion, so
// treating it as a path guard could make a false loop invariant appear proven.
func (a *Analyzer) loopBodyAssertIsPure(stmt *ast.ExprStmt) bool {
	if a == nil || stmt == nil {
		return false
	}
	cond, ok := a.assertedCondition(stmt.Expr)
	return ok && a.loopAssertConditionIsSideEffectFree(cond)
}

// loopAssertConditionIsSideEffectFree admits the ordinary read-only expression forms used by
// loop assertions and also direct ghost-function calls. Ghost calls are erased from runtime code;
// ordinary calls stay rejected because they may have observable effects even when their result is
// used only by an assertion.
func (a *Analyzer) loopAssertConditionIsSideEffectFree(expr ast.Expr) bool {
	return a.loopAssertConditionIsSideEffectFreeWithGate(expr, &sviewFrameGate{a: a, typeScope: a.currentScope})
}

// The scope stays fixed while walking a condition. Reuse its visible types instead of
// rebuilding the entire scope map for every expression node.
func (a *Analyzer) loopAssertConditionIsSideEffectFreeWithGate(expr ast.Expr, operatorGate *sviewFrameGate) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit:
		return true
	case *ast.Ident:
		return n != nil
	case *ast.ParenExpr:
		return n != nil && a.loopAssertConditionIsSideEffectFreeWithGate(n.Inner, operatorGate)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && a.loopAssertConditionIsSideEffectFreeWithGate(n.Object, operatorGate)
	case *ast.IndexExpr:
		return n != nil && n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback &&
			n.AsSpecialize == nil && n.AsOverlayCall == nil && n.AsBuiltinDictGet == nil &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Object, operatorGate) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Index, operatorGate)
	case *ast.SliceExpr:
		return n != nil && a.loopAssertConditionIsSideEffectFreeWithGate(n.Object, operatorGate) &&
			(n.Start == nil || a.loopAssertConditionIsSideEffectFreeWithGate(n.Start, operatorGate)) &&
			(n.End == nil || a.loopAssertConditionIsSideEffectFreeWithGate(n.End, operatorGate))
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op != lexer.TOKEN_AMPERSAND && operatorGate.unaryHasNoOverload(n) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Operand, operatorGate)
	case *ast.BinaryExpr:
		return n != nil && n.LoweredCall == nil && operatorGate.binaryHasNoOverload(n) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Left, operatorGate) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Right, operatorGate)
	case *ast.CastExpr:
		return n != nil && isPureNumericValueCast(n) && a.loopAssertConditionIsSideEffectFreeWithGate(n.Operand, operatorGate)
	case *ast.TernaryExpr:
		return n != nil && a.loopAssertConditionIsSideEffectFreeWithGate(n.Cond, operatorGate) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Value, operatorGate) && a.loopAssertConditionIsSideEffectFreeWithGate(n.Alt, operatorGate)
	case *ast.CallExpr:
		if n == nil || n.Safe {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil || !decl.IsGhost {
			return false
		}
		for _, arg := range n.Args {
			if !a.loopAssertConditionIsSideEffectFreeWithGate(arg, operatorGate) {
				return false
			}
		}
		return true
	case *ast.QuantifierExpr:
		return n != nil && n.Body != nil && (n.In == nil || a.loopAssertConditionIsSideEffectFreeWithGate(n.In, operatorGate)) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(n.Body, operatorGate)
	case *ast.ExprBlock:
		// A range quantifier the parser lowered to a flag-and-loop block evaluates its bounds and
		// predicate and writes only its own flag.
		loop, predicate, _, ok := runtimeRangeQuantifierParts(n)
		return ok && a.loopAssertConditionIsSideEffectFreeWithGate(loop.Start, operatorGate) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(loop.End, operatorGate) &&
			a.loopAssertConditionIsSideEffectFreeWithGate(predicate, operatorGate)
	default:
		return false
	}
}

// loopStoreTargetBaseKey returns the normalized base key of an array-element store target
// `BASE[idx]`, plus whether the base is admissible for the current capture mode.
//
//   - The STRICT (invariant) path requires BASE to be a bare array variable — exactly the
//     prior behaviour; a field path is rejected so the `(store arr idx val)` SMT model only ever
//     keys on a plain Ident array symbol.
//   - The TERMINATION path additionally admits a stable struct-FIELD path lvalue rooted at a single
//     `*ast.Ident` receiver through `*ast.FieldExpr` hops only — `self.xs`, `self.a.b`. No calls,
//     no index/subscript, no deref, no safe-navigation in the path; anything else declines.
//
// The returned key is a canonical string for the base path (`self`, `self.xs`), so the
// caller's stored-once / not-also-reassigned tracking treats two syntactically-equal paths as the
// same array (conservative aliasing: identical paths alias; distinct paths we cannot prove disjoint
// are kept apart only by syntactic string, and a store is permitted at most ONCE per key — so the
// simultaneous-substitution invariant cannot be broken even if two distinct strings happened to
// alias, because each store is independently discarded and changes no scalar).
//
// The IndexExpr itself must be an ordinary single-subscript array index: any analyzer-rewritten or
// sugared form (two-operand overlay accessor, `else` fallback, value specialization, overlay call)
// is rejected — those do not lower to a plain element store.
func loopStoreTargetBaseKey(target *ast.IndexExpr, forTermination bool) (string, bool) {
	if target == nil {
		return "", false
	}
	if target.Index2 != nil || target.Fallback != nil || target.LegacyElseFallback ||
		target.AsSpecialize != nil || target.AsOverlayCall != nil {
		return "", false
	}
	if arrIdent, ok := target.Object.(*ast.Ident); ok && arrIdent != nil {
		return arrIdent.Name, true
	}
	// Struct-field array paths (`s.data[i] <- v`) are admitted on BOTH the invariant path and the
	// termination path. The normalized path key (e.g. `s.data`) matches the projection name
	// that smtProjectionName / arrayTermEnv uses, so proveLoopPreservationSMT injects
	//   env["s.data"] = "(store v_s.data i v)"
	// and arrayTermEnv's FieldExpr env-lookup picks it up when translating `s.data[k]` in the
	// quantified invariant. Soundness constraints remain identical: stored-once per path,
	// never whole-reassigned, pure-arith index/value.
	key, ok := normalizeFieldPathLValue(target.Object)
	return key, ok
}

// normalizeFieldPathLValue returns a canonical key string for a stable field-path lvalue rooted at a
// single `*ast.Ident` receiver through `*ast.FieldExpr` hops ONLY, and whether `expr` is exactly such
// a path. It walks `obj.a.b…` down to the root identifier; ANYTHING ELSE in the chain — a call, an
// index, an address-of, a cast, a parenthesized non-field, or a safe (`?.`) navigation that could be
// null — declines (returns ok=false). The key is the dotted path joined by a separator that cannot
// appear in an identifier, so two equal paths produce one key and unequal paths produce distinct keys.
func normalizeFieldPathLValue(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		// A `?.` safe-navigation field is NOT a stable lvalue store base (the object may be null), so
		// reject it; only an unconditional `.field` projection of a stable path is admissible.
		if n == nil || n.Safe {
			return "", false
		}
		base, ok := normalizeFieldPathLValue(n.Object)
		if !ok {
			return "", false
		}
		return base + smtFieldSeparator + n.Field, true
	default:
		return "", false
	}
}
