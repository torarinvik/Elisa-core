package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// invalidateRangeFacts drops the known integer range fact about `name` across the active scope chain.
// Called at every mutation site for `name`, mirroring invalidatePredFacts. Unlike predFacts there is
// NO dependent-fact cascade: a range fact is a concrete interval snapshot (even one seeded from another
// variable's range captured that variable's bound at seed time), so it has no live symbolic dependence
// on other variables — only a write to the SUBJECT itself can stale its interval (docs/90 brick 90-11).
func (a *Analyzer) invalidateRangeFacts(name string) {
	if name == "" {
		return
	}
	a.logRangeFactWrite(rangeFactWrite{name: name})
	// Consult each fact's own deps() via the unified predicate (analyzer_fact.go), mirroring assert-fact
	// invalidation. A range fact's sole dep is its subject variable, so this deletes exactly `name`.
	rootSet := map[string]bool{name: true}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts == nil {
			continue
		}
		for n, r := range scope.rangeFacts {
			if factInvalidatedBy(rangeHypothesisFact{name: n, r: r}, rootSet) {
				delete(scope.rangeFacts, n)
			}
		}
	}
}

// invalidateRangeFactsForFieldPath drops any range fact keyed on the exact path `path` (e.g.
// "s.n") across the active scope chain. Used when a direct field mutation `s.n <- …` occurs.
func (a *Analyzer) invalidateRangeFactsForFieldPath(path string) {
	if path == "" {
		return
	}
	a.logRangeFactWrite(rangeFactWrite{name: path, path: true})
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts != nil {
			delete(scope.rangeFacts, path)
		}
	}
}

// invalidateRangeFactsWithRootPrefix drops every range fact whose key equals `root` OR starts with
// `root + "."` — covering both a plain-ident fact and any field-path facts whose struct root is
// `root`. Called when the struct variable itself is mutated or reassigned (a write to `s` stales
// all "s.f" facts).
func (a *Analyzer) invalidateRangeFactsWithRootPrefix(root string) {
	if root == "" {
		return
	}
	a.logRangeFactWrite(rangeFactWrite{name: root, prefix: true})
	prefix := root + "."
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts == nil {
			continue
		}
		for k := range scope.rangeFacts {
			if k == root || len(k) > len(prefix) && k[:len(prefix)] == prefix {
				delete(scope.rangeFacts, k)
			}
		}
	}
}

// invalidateRangeFactsForTarget drops the range fact about the root variable of a mutation target
// expression (an identifier, or a field/index path rooted at one), mirroring invalidatePredFactsForTarget.
// For a field-path target `s.n`, it drops the exact path fact "s.n". For any mutation whose root
// is a plain identifier `s`, it drops `s` AND all field-path facts of the form "s.*" (because a
// write to the struct as a whole stales every field fact).
func (a *Analyzer) invalidateRangeFactsForTarget(target ast.Expr) {
	// If the target is a direct field expression `s.field`, drop only that exact path fact as well as
	// the root ident (via the existing plain-ident invalidation below). This is tighter than dropping
	// all "s.*" paths on a field write — only "s.field" is stale.
	if fe, ok := target.(*ast.FieldExpr); ok && fe != nil {
		if rootIdent, ok := fe.Object.(*ast.Ident); ok && rootIdent != nil {
			a.invalidateRangeFactsForFieldPath(rootIdent.Name + "." + fe.Field)
		}
	}
	for _, name := range a.mutationRootsForTarget(target) {
		a.invalidateRangeFacts(name)
		// Also invalidate any field-path facts keyed on this root (e.g. "name.f"), in case the
		// mutation target is the struct itself (not a specific field).
		a.invalidateRangeFactsWithRootPrefix(name)
	}
}

func (a *Analyzer) recordConstAssignmentRangeFact(target ast.Expr, value ast.Expr) {
	if a == nil || a.currentScope == nil {
		return
	}
	name, ok := rootIdentName(target)
	if !ok || name == "" {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	c, ok := a.constIntValue(value)
	if ok {
		if a.currentScope.rangeFacts == nil {
			a.currentScope.rangeFacts = map[string]numRange{}
		}
		a.currentScope.rangeFacts[name] = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
		return
	}
	// Ternary RHS: seed the join of the two branch ranges so that downstream proofs on `name`
	// can discharge bounds without re-examining the ternary expression structure.
	if tern, isTern := value.(*ast.TernaryExpr); isTern {
		if r, derived := a.tryDeriveTernaryRange(tern); derived {
			if a.currentScope.rangeFacts == nil {
				a.currentScope.rangeFacts = map[string]numRange{}
			}
			a.currentScope.rangeFacts[name] = r
		}
	}
}

// recordCastRangeFact propagates integer range facts from the source of a `.cast[T]` assignment
// `target = src.cast[T]` to `target` when the cast is provably value-preserving.
//
// `.cast[T]` is a bitwise reinterpretation (not a value conversion). For the range of `src` to
// be valid for `target` after the cast, the bit pattern must encode the same numeric value in T.
// This holds in two cases:
//
//	(a) Identity cast (src type == T): the bits are unchanged, so the range carries exactly.
//	(b) Sign-flip cast (same bit width, different sign, e.g. i64↔u64): value-preserving only
//	    when the proven range is fully bounded ([lo, hi] both known) and BOTH endpoints fit T.
//	    An open bound over a sign-flip means values in the out-of-range half could exist and
//	    would reinterpret to negative/positive values, so we decline.
//
// Any other integer cast is rejected by the compiler as a value conversion (must use constructors),
// so this function only needs to handle (a) and (b).
func (a *Analyzer) recordCastRangeFact(target ast.Expr, value ast.Expr) {
	if a == nil || a.currentScope == nil {
		return
	}
	castExpr, ok := unwrapParen(value).(*ast.CastExpr)
	if !ok || castExpr == nil {
		return
	}
	targetName, ok := rootIdentName(target)
	if !ok || targetName == "" {
		return
	}
	sym, ok := a.currentScope.Lookup(targetName)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	// Resolve the source operand to an identifier we can look up.
	srcIdent, ok := unwrapParen(castExpr.Operand).(*ast.Ident)
	if !ok || srcIdent == nil {
		return
	}
	// Look up the source variable's range fact (walks the full scope chain).
	srcRange, found := a.lookupRangeFact(srcIdent.Name)
	if !found || (!srcRange.loKnown && !srcRange.hiKnown) {
		return
	}
	// Resolve the destination type of the cast.
	dstType := a.resolveType(castExpr.Target)
	if dstType == nil {
		return
	}
	dstSigned, dstWidth, dstOk := BitIntInfo(dstType)
	if !dstOk {
		return
	}
	// Resolve the source variable's declared type to check cast kind.
	srcSym, srcFound := a.currentScope.Lookup(srcIdent.Name)
	if !srcFound || srcSym == nil {
		return
	}
	srcSigned, srcWidth, srcOk := BitIntInfo(srcSym.Type)
	if !srcOk {
		return
	}
	var r numRange
	if srcSigned == dstSigned && srcWidth == dstWidth {
		// Case (a): identity cast — bit pattern identical, range carries exactly.
		r = srcRange
	} else if srcWidth == dstWidth {
		// Case (b): sign-flip, same width (e.g. i64↔u64). Value-preserving only when the
		// proven range is fully bounded AND both endpoints fit T (no out-of-range half).
		if !srcRange.loKnown || !srcRange.hiKnown {
			return // open bound over a sign-flip: cannot guarantee fitness
		}
		if !IntegerTypeFitsValue(dstType, srcRange.lo) || !IntegerTypeFitsValue(dstType, srcRange.hi) {
			return // at least one endpoint wraps — range would be unsound
		}
		r = srcRange
	} else {
		// Different width: a narrowing numeric cast would be a value conversion (rejected by the
		// compiler), so this branch is only reachable for exotic non-integer or special casts that
		// are not numeric — decline conservatively.
		return
	}
	// Tighten by the target variable's declared type range (adds non-negativity etc.).
	if tr, ok2 := declaredTypeRange(sym.Type); ok2 {
		r = r.intersect(tr)
	}
	if !r.loKnown && !r.hiKnown {
		return
	}
	_ = dstSigned // both dstSigned and dstWidth were used in the case analysis above
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[targetName] = r
}

// seedIntegerMatchArmFact seeds the range fact `scrutinee == literal` into an integer match arm's
// scope when the scrutinee is an immutable integer variable and the arm pattern is a literal.
// This lets the flow prover discharge refinement obligations (e.g. `x is Five`) inside the arm
// body — `match k: 5:` knows `k ∈ [5,5]` for the arm body, just like `if k == 5:` does.
// The fact is scoped to the arm scope and therefore cannot leak to other arms or past the match.
func (a *Analyzer) seedIntegerMatchArmFact(scrutineeExpr ast.Expr, pattern ast.MatchPattern, armScope *Scope) {
	if a == nil || armScope == nil {
		return
	}
	litPat, ok := pattern.(*ast.MatchLiteralPattern)
	if !ok {
		return
	}
	name, ok := immutableIntIdentName(a, a.currentScope, scrutineeExpr)
	if !ok {
		return
	}
	c, ok := a.constIntValue(litPat.Value)
	if !ok {
		return
	}
	if armScope.rangeFacts == nil {
		armScope.rangeFacts = map[string]numRange{}
	}
	armScope.rangeFacts[name] = armScope.rangeFacts[name].intersect(numRange{loKnown: true, lo: c, hiKnown: true, hi: c})
}

// recordWideningCastRangeFact seeds a range fact for `target` when `value` is a chain of
// value-preserving widening casts from a source variable whose range is already known. For
// example, given `y: u64 = x.u64()` where `x` has proven range [0, 100], this records [0, 100]
// for `y` so that tier-2 proofs over `y` (or further casts of `y`) can carry the range
// through without re-doing the guard analysis.
//
// Sound: the same value-preservation check used by affineOf/CastExpr is applied here —
// IntegerTypeFitsValue on both bounds. A narrowing cast, sign-changing cast, or any cast
// whose source range is not fully known leaves no fact (fail-closed). Only immutable integer
// targets are handled, since a mutable target can be reassigned and would need invalidation.
func (a *Analyzer) recordWideningCastRangeFact(target ast.Expr, value ast.Expr, mutable bool) {
	if a == nil || a.currentScope == nil || mutable {
		return // mutable targets need invalidation; skip to avoid stale facts
	}
	name, ok := rootIdentName(target)
	if !ok || name == "" {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return
	}
	if IsFloatType(sym.Type) {
		return
	}
	if _, _, isInt := rangeIntInfo(sym.Type); !isInt {
		return
	}
	// Peel the cast chain, collecting each intermediate target type for value-preservation checks.
	expr := value
	var castTypes []Type
	for {
		ce, ok := expr.(*ast.CastExpr)
		if !ok {
			break
		}
		// Resolving the cast target is speculative here (we are only seeding an optimization
		// range fact). A postfix-shorthand cast `recv.Name()` whose `Name` is NOT a type is
		// really the UFCS call `Name(recv)` (re-interpreted later in analyzeExpr); resolving
		// `Name` as a type would emit a spurious "unknown type" error. Suppress diagnostics so
		// the non-integer/invalid result simply bails below, fail-closed, with no error.
		savedSuppress := a.suppressDiagnostics
		restoreReportProbe1 := a.beginProofReportProbe()
		defer restoreReportProbe1.restore()
		a.suppressDiagnostics = true
		t := a.resolveType(ce.Target)
		a.suppressDiagnostics = savedSuppress
		restoreReportProbe1.restore()
		if _, _, isInt := rangeIntInfo(t); !isInt {
			return // non-integer intermediate — cannot reason about value preservation
		}
		castTypes = append(castTypes, t)
		expr = ce.Operand
	}
	// No casts: a plain copy `m = n` of an integer identifier. The target is immutable, so the
	// source range at the binding point holds for the target's whole lifetime.
	// The innermost expr must be an immutable integer identifier with a known range.
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	r, ok := a.lookupRangeFact(ident.Name)
	if !ok {
		return // source has no known range — nothing to transfer
	}
	if !r.loKnown || !r.hiKnown {
		return // open source range — unsafe to transfer
	}
	// Validate that every cast in the chain is value-preserving (range fits in each target type).
	for _, t := range castTypes {
		if !rangeIntFits(t, r.lo) || !rangeIntFits(t, r.hi) {
			return // narrowing or sign-changing cast somewhere in the chain — decline
		}
	}
	// All casts are value-preserving: record the source range for the target variable.
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = r
}

// matchArmBodyExpr returns the single expression at the end of a match-arm body (the yield value),
// or nil when the body is empty, multi-statement (and the last isn't an ExprStmt), or definitely
// exits. Used by exprResultRange to compute the union of arm-value ranges.
func matchArmBodyExpr(body []ast.Stmt) ast.Expr {
	if len(body) == 0 {
		return nil
	}
	last := body[len(body)-1]
	if exprStmt, ok := last.(*ast.ExprStmt); ok {
		return exprStmt.Expr
	}
	return nil
}

// recordMatchExprRangeFact records a range fact for an integer local variable whose initializer is a
// match expression in value position. It calls exprResultRange (which now handles *ast.MatchExpr) to
// compute the JOIN of all arm-body ranges; if successful the fact is stored in the current scope so
// downstream refinement discharge (tryProveRefinementByFlow) can prove `x is SomeLaw`. Sound:
// exprResultRange is fail-closed (returns ok=false when any arm is outside the decidable fragment).
func (a *Analyzer) recordMatchExprRangeFact(name string, rhs ast.Expr, typ Type) {
	if a == nil || a.currentScope == nil || name == "" || rhs == nil {
		return
	}
	if !IsNumericType(typ) || IsFloatType(typ) {
		return
	}
	r, ok := a.exprResultRange(rhs, nil)
	if !ok {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = r
}

func declaredTypeRange(t Type) (numRange, bool) {
	signed, width, ok := BitIntInfo(t)
	if !ok || width <= 0 || width >= 63 {
		return numRange{}, false
	}
	if signed {
		return numRange{loKnown: true, lo: -(int64(1) << (width - 1)), hiKnown: true, hi: (int64(1) << (width - 1)) - 1}, true
	}
	return numRange{loKnown: true, lo: 0, hiKnown: true, hi: (int64(1) << width) - 1}, true
}

func sameNumRange(a, b numRange) bool {
	return a.loKnown == b.loKnown && a.lo == b.lo && a.hiKnown == b.hiKnown && a.hi == b.hi
}

func (a *Analyzer) mergePostIfRangeFacts(entry map[string]numRange, branches []map[string]numRange) {
	if a == nil || a.currentScope == nil || len(branches) == 0 {
		return
	}
	names := map[string]bool{}
	for name := range entry {
		names[name] = true
	}
	for _, br := range branches {
		for name := range br {
			names[name] = true
		}
	}
	for name := range names {
		base, hadBase := entry[name]
		changed := false
		for _, br := range branches {
			r, ok := br[name]
			if !ok {
				// Absent from a branch's full visible fact set: that branch invalidated it.
				changed = true
				break
			}
			if !hadBase || !sameNumRange(r, base) {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		var joined numRange
		joinedSet := false
		for _, br := range branches {
			r, ok := br[name]
			if !ok {
				// Invalidated on this path (or unknown there): only the declared type's range holds.
				if sym, found := a.currentScope.Lookup(name); found && sym != nil {
					if tr, found := declaredTypeRange(sym.Type); found {
						r = tr
					}
				}
			}
			if !ok {
				if sym, found := a.currentScope.Lookup(name); found && sym != nil {
					if tr, found := declaredTypeRange(sym.Type); found {
						r = tr
						ok = true
					}
				}
			}
			if !ok && !hadBase {
				r = numRange{}
			}
			if !joinedSet {
				joined, joinedSet = r, true
			} else {
				joined = joined.join(r)
			}
		}
		// The joined range replaces the entry range wherever it lives. Lookup intersects every
		// scope's fact, so an ancestor scope (e.g. outside the `can` block that holds this `if`)
		// keeping the pre-if range would silently override the widening.
		// The joined value is stored where the outermost such fact lived: it holds from this
		// point on, and that scope outlives the enclosing block.
		home := a.currentScope
		for sc := a.currentScope.Parent; sc != nil; sc = sc.Parent {
			if _, had := sc.rangeFacts[name]; had && a.rangeFactBindingCurrent(sc, name) {
				delete(sc.rangeFacts, name)
				home = sc
			}
		}
		if home != a.currentScope && a.currentScope.rangeFacts != nil {
			delete(a.currentScope.rangeFacts, name)
		}
		if home.rangeFacts == nil {
			home.rangeFacts = map[string]numRange{}
		}
		home.rangeFacts[name] = joined
	}
}

// writtenConstInt returns the exact integer value of a variable when a live written-constant fact
// pins it to a compile-time integer (an immutable local or a `<- const` write). The bridge between
// the written-const tracker and the interval prover.
func (a *Analyzer) writtenConstInt(name string) (int64, bool) {
	v, ok := a.lookupWrittenConst(name)
	if !ok || v == nil {
		return 0, false
	}
	return a.constIntValue(v)
}

// lawConstraint is one `self OP const` clause of a law body in the decidable fragment.
type lawConstraint struct {
	op lexer.TokenKind
	c  int64
}

// lawConstraints interprets a law body as a conjunction of `self OP const` constraints, or returns
// false if the body is outside the decidable fragment (then the flow prover declines and discharge
// falls back to a runtime check). `self` is the law's first parameter name; `paramConsts` binds the
// law's remaining (static `[..]`) params to the refinement's bracket-arg constants, so a body like
// `self >= lo and self <= hi` is interpreted against the actual bounds.
func (a *Analyzer) lawConstraints(decl *ast.FuncDecl, paramConsts map[string]int64) ([]lawConstraint, bool) {
	return a.lawConstraintsRanged(decl, paramConsts, nil)
}

// lawConstraintsRanged is lawConstraints with an extra `paramRanges` channel (docs/90 brick 90-9):
// a static law param bound to a known INTERVAL (rather than an exact constant) is resolved
// direction-aware — its lower bound for a `self >= param` constraint, its upper bound for a
// `self <= param` constraint — which is the only sound way to use a non-constant bracket argument
// (e.g. `cap_to(k)` with `k ∈ [0, 10]` yields `result <= 10`). paramConsts is consulted first;
// paramRanges (nil for the exact-constant callers) is the fallback.
func (a *Analyzer) lawConstraintsRanged(decl *ast.FuncDecl, paramConsts map[string]int64, paramRanges map[string]numRange) ([]lawConstraint, bool) {
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	self := decl.Params[0].Name
	var out []lawConstraint
	if !a.collectLawConstraints(ret.Value, self, paramConsts, paramRanges, &out) {
		return nil, false
	}
	return out, true
}
