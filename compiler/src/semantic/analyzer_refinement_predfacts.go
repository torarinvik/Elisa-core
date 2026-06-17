package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// Predicate-fact tracking (docs/85: mutable refinement flow).
//
// The integer range prover (analyzer_refinement_flow.go) is deliberately IMMUTABLE-ONLY: a range
// fact about a mutable variable could be invalidated by a later write, so it is never carried.
// Predicate facts make mutable tracking SOUND by doing the opposite of refusing them — they are
// carried but INVALIDATED at every mutation site. A fact "predicate P holds on x" is gained from a
// flow narrowing (`if x is P:`), used to discharge a downstream obligation on x without a runtime
// check, and dropped the moment x is mutated (assigned, or passed by `&`/mutable to a call). The
// drop is the soundness core: a mutation can break the predicate (e.g. `pop` on a `NonEmpty`
// darray), so the compiler stops trusting the fact exactly where the mutation happens — which is
// the "loses the refinement where it is called" behavior. Dropping is always sound (it only adds
// runtime checks, never removes them); only bare laws (no bracket args) participate for now.

// recordPredFact remembers that bare law-predicate `pred` holds on variable `name` within `scope`.
func recordPredFact(scope *Scope, name, pred string) {
	if scope == nil || name == "" || pred == "" {
		return
	}
	if scope.predFacts == nil {
		scope.predFacts = map[string]map[string]bool{}
	}
	set := scope.predFacts[name]
	if set == nil {
		set = map[string]bool{}
		scope.predFacts[name] = set
	}
	set[pred] = true
}

// recordPredFactWithDeps remembers fact `key` on variable `name`, recording the dependency roots its
// bounds read (docs/85 §5.3). When deps is non-empty the fact is DEPENDENT: it survives only while
// every dep root is frozen, and mutating any of them drops it via invalidatePredFacts.
func recordPredFactWithDeps(scope *Scope, name, key string, deps []string) {
	if scope == nil || name == "" || key == "" {
		return
	}
	recordPredFact(scope, name, key)
	if len(deps) == 0 {
		return
	}
	if scope.predFactDeps == nil {
		scope.predFactDeps = map[string]map[string][]string{}
	}
	byKey := scope.predFactDeps[name]
	if byKey == nil {
		byKey = map[string][]string{}
		scope.predFactDeps[name] = byKey
	}
	byKey[key] = append([]string(nil), deps...)
}

// lookupPredFact reports whether bare law-predicate `pred` is known to hold on `name` anywhere in
// the active scope chain.
func (a *Analyzer) lookupPredFact(name, pred string) bool {
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.predFacts != nil {
			if set := scope.predFacts[name]; set != nil && set[pred] {
				return true
			}
		}
	}
	return false
}

// invalidatePredFacts drops EVERY predicate fact about `name` across the whole active scope chain.
// Called at each mutation site (assignment, ref-call). Deleting upward through parent scopes is the
// sound direction: a real mutation of x invalidates whatever a branch or the enclosing scope
// believed about x, and over-dropping only forces more runtime checks, never fewer. This also gives
// correct merge behavior for free — a fact gained inside one `if` branch and mutated there is gone
// at the merge point.
func (a *Analyzer) invalidatePredFacts(name string) {
	if name == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.predFacts != nil {
			delete(scope.predFacts, name)
		}
		// Dependence-freeze (docs/85 §5.3): mutating `name` also breaks any OTHER variable's dependent
		// fact whose bounds read `name` (e.g. `i is Bounded[0, name.count]`). Drop those facts and
		// their dependency records so a stale length bound can never discharge an obligation.
		if scope.predFactDeps != nil {
			delete(scope.predFactDeps, name)
			for holder, byKey := range scope.predFactDeps {
				for key, deps := range byKey {
					if containsString(deps, name) {
						delete(byKey, key)
						if scope.predFacts[holder] != nil {
							delete(scope.predFacts[holder], key)
						}
					}
				}
				if len(byKey) == 0 {
					delete(scope.predFactDeps, holder)
				}
			}
		}
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// invalidatePredFactsForTarget drops predicate facts about the root variable of a mutation target
// expression (an identifier, or a field/index path rooted at one). Conservative: any write under a
// root invalidates the root's facts.
func (a *Analyzer) invalidatePredFactsForTarget(target ast.Expr) {
	if name, ok := rootIdentName(target); ok {
		a.invalidatePredFacts(name)
	}
}

// rootIdentName walks a field/index path down to its root identifier name.
func rootIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Inner)
	case *ast.FieldExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Object)
	case *ast.IndexExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Object)
	case *ast.UnaryExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Operand)
	case *ast.AddrOfExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Operand)
	default:
		return "", false
	}
}

// recordWrittenConstForTarget records (or clears) the written-constant fact for an assignment
// `target <- value` / `target = value`. For a bare-identifier target whose RHS is a compile-time
// constant of any const-evaluable kind (int, bool, enum, float, char, string), the variable is now
// known to equal that constant expr; any other target shape or a non-constant RHS clears the fact
// (the value is no longer statically known). Sound because the fact is invalidated again at the next
// mutation, `mutable T&` pointees are non-aliased, and the stored RHS references only literals /
// immutable consts (evalConstExpr never resolves a mutable local) so re-evaluation is stable.
func (a *Analyzer) recordWrittenConstForTarget(target, value ast.Expr) {
	name, ok := target.(*ast.Ident)
	if !ok || name == nil {
		a.invalidateWrittenConst(rootIdentNameOrEmpty(target))
		return
	}
	if _, ok := a.evalConstExpr(value); !ok {
		a.invalidateWrittenConst(name.Name)
		return
	}
	if a.currentScope == nil {
		return
	}
	// Drop any shadowed parent entry first so the lookup finds this fresh value.
	a.invalidateWrittenConst(name.Name)
	if a.currentScope.writtenConst == nil {
		a.currentScope.writtenConst = map[string]ast.Expr{}
	}
	a.currentScope.writtenConst[name.Name] = value
}

func rootIdentNameOrEmpty(expr ast.Expr) string {
	if name, ok := rootIdentName(expr); ok {
		return name
	}
	return ""
}

// invalidateWrittenConst drops the written-constant fact for a variable across the scope chain,
// mirroring invalidatePredFacts. Called at every mutation site that does not record a new constant.
func (a *Analyzer) invalidateWrittenConst(name string) {
	if name == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenConst != nil {
			delete(scope.writtenConst, name)
		}
	}
}

// lookupWrittenConst returns the known constant-value expr of a variable, if a written-constant fact
// for it is live in the active scope chain. The expr is the original const RHS (any kind), suitable
// for use as a const-eval subject.
func (a *Analyzer) lookupWrittenConst(name string) (ast.Expr, bool) {
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenConst != nil {
			if v, ok := scope.writtenConst[name]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

// callPreservesArgRefinements reports whether passing an argument as ref-param `paramIndex` of
// `call` (callee type `appliedType`) provably leaves the argument's refinements intact, so the
// caller can KEEP its predicate/written-const facts across the call (docs/85 brick 3, preserve
// credit). Two sound signals: (1) the callee declares `ensures <param> preserve` (a
// FuncPoststateKindPreserve over the whole target — its ParamIndex aligns with the call loop index,
// same as funcPoststatesForParam); (2) the parameter is an IMMUTABLE borrow (the callee decl's
// ParamDecl.Mutable is false), so the callee cannot write through it and Elisa's borrow rules
// forbid a concurrent mutable alias. Anything unprovable returns false → the brick-1 drop stands.
func (a *Analyzer) callPreservesArgRefinements(call *ast.CallExpr, appliedType *FuncType, paramIndex int) bool {
	if appliedType != nil {
		for _, ps := range appliedType.Poststates {
			if ps.ParamIndex == paramIndex && ps.Kind == FuncPoststateKindPreserve && len(ps.Path) == 0 {
				return true
			}
		}
		// An IMMUTABLE borrow `T&` (non-mutable pointee) cannot be written through, so the argument's
		// refinements survive. Reading this from the resolved FuncType — rather than the call's decl —
		// makes the signal work for EVERY call shape (method `obj.m(arg)`, indirect/closure calls),
		// not just direct by-name calls. Index aligns with the call's param loop, same as Poststates.
		if paramIndex >= 0 && paramIndex < len(appliedType.Params) {
			if rt, ok := appliedType.Params[paramIndex].(*RefType); ok && rt != nil && !rt.Mutable {
				return true
			}
		}
	}
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		if paramIndex >= 0 && paramIndex < len(decl.Params) && paramIsImmutableBorrow(decl.Params[paramIndex]) {
			return true
		}
	}
	return false
}

// paramIsImmutableBorrow reports whether a parameter is an immutable borrow `T&` — a ref through
// which the callee cannot write, so an argument's refinements survive the call. The canonical
// mutable borrow is `p: mutable i64&` (a MutableType wrapping the ref); the legacy form is a leading
// `mutable` keyword (ParamDecl.Mutable). Both are rejected here; only a plain `*ast.RefType` whose
// pointee is not itself mutable counts as immutable. Conservative: any non-ref or mutable shape is
// not a preserving borrow.
func paramIsImmutableBorrow(p ast.ParamDecl) bool {
	if p.Mutable {
		return false
	}
	rt, ok := p.Type.(*ast.RefType)
	if !ok || rt == nil {
		return false
	}
	if _, mut := rt.Elem.(*ast.MutableType); mut {
		return false
	}
	return true
}

// factKey builds the canonical predicate-fact key for a law applied to constant arguments. A bare
// law is just its name; a parametric law `Bounded[0, 500]` whose args are all compile-time integer
// constants is `Bounded[0,500]`. Returns false when any argument is not a constant integer (such a
// fact can't be matched against an obligation soundly, so it is not tracked). Keying by exact arg
// values means a fact only discharges an obligation with the SAME bounds — `Bounded[0,500]` never
// proves `Bounded[10,20]` (no cross-bounds entailment here; that is the range prover's job).
func (a *Analyzer) factKey(lawName string, argExprs []ast.Expr) (string, bool) {
	if lawName == "" {
		return "", false
	}
	if len(argExprs) == 0 {
		return lawName, true
	}
	key := lawName + "["
	for i, arg := range argExprs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return "", false
		}
		if i > 0 {
			key += ","
		}
		key += strconv.FormatInt(c, 10)
	}
	return key + "]", true
}

// factKeyWithDeps builds a predicate-fact key for a law whose args may be CONSTANT or DEPENDENT
// (docs/85 §5.3 dependence-freeze). A constant integer arg is keyed by value (exact-match, as
// factKey). A dependent arg — a pure, side-effect-free path/arithmetic over variables (`xs.count`,
// `xs.count - 1`, `r.vw`) — is keyed by its canonical text, and the root variables it reads are
// returned as `deps`; such a fact is sound only while those roots are frozen (a mutation of any of
// them invalidates it). Returns ok=false for any non-freezable arg (a call, an impure or
// unrecognized expr) so the fact is simply not tracked. deps is nil for an all-constant fact, making
// this a superset of factKey with identical keys in the constant case.
func (a *Analyzer) factKeyWithDeps(lawName string, argExprs []ast.Expr) (string, []string, bool) {
	if lawName == "" {
		return "", nil, false
	}
	if len(argExprs) == 0 {
		return lawName, nil, true
	}
	var deps []string
	seen := map[string]bool{}
	key := lawName + "["
	for i, arg := range argExprs {
		if i > 0 {
			key += ","
		}
		if c, ok := a.constIntValue(arg); ok {
			key += strconv.FormatInt(c, 10)
			continue
		}
		if !isFreezableDependentArg(arg) {
			return "", nil, false
		}
		key += unparse.FormatExpr(arg)
		collectFreezableArgRoots(arg, seen, &deps)
	}
	return key + "]", deps, true
}

// isFreezableDependentArg reports whether a dependent-bound argument is a pure, re-evaluation-stable
// path/arithmetic expression: identifiers, field paths (incl. `.count`), constant-or-path indexing,
// integer literals, parens, unary +/-/~, and arithmetic binary ops over such operands. Anything that
// could observe or cause a side effect (a call) or that this checker doesn't recognize is rejected,
// so a fact is tracked only when its dependence on frozen variables is explicit.
func isFreezableDependentArg(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n != nil
	case *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return n != nil && isFreezableDependentArg(n.Inner)
	case *ast.FieldExpr:
		return n != nil && isFreezableDependentArg(n.Object)
	case *ast.IndexExpr:
		return n != nil && isFreezableDependentArg(n.Object) && isFreezableDependentArg(n.Index)
	case *ast.UnaryExpr:
		return n != nil && isFreezableDependentArg(n.Operand)
	case *ast.BinaryExpr:
		if n == nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			return isFreezableDependentArg(n.Left) && isFreezableDependentArg(n.Right)
		}
		return false
	default:
		return false
	}
}

// collectFreezableArgRoots adds the root variable names a freezable dependent arg reads (the bases of
// its paths and the operands of its arithmetic) into deps, deduped via seen.
func collectFreezableArgRoots(expr ast.Expr, seen map[string]bool, deps *[]string) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil && !seen[n.Name] {
			seen[n.Name] = true
			*deps = append(*deps, n.Name)
		}
	case *ast.ParenExpr:
		if n != nil {
			collectFreezableArgRoots(n.Inner, seen, deps)
		}
	case *ast.FieldExpr:
		if n != nil {
			collectFreezableArgRoots(n.Object, seen, deps)
		}
	case *ast.IndexExpr:
		if n != nil {
			collectFreezableArgRoots(n.Object, seen, deps)
			collectFreezableArgRoots(n.Index, seen, deps)
		}
	case *ast.UnaryExpr:
		if n != nil {
			collectFreezableArgRoots(n.Operand, seen, deps)
		}
	case *ast.BinaryExpr:
		if n != nil {
			collectFreezableArgRoots(n.Left, seen, deps)
			collectFreezableArgRoots(n.Right, seen, deps)
		}
	}
}

// gatherLawIsPredFact records a predicate fact for `if x is Law:` / `if x is Law[consts]:` where x
// is a bare identifier — regardless of whether x is mutable. Mutable is safe here because any later
// mutation of x invalidates the fact via invalidatePredFacts. The integer range path
// (gatherLawIsRangeRefinement) handles the decidable `self OP const` fragment over immutable ints;
// this complements it for laws outside that fragment (e.g. `darray is NonEmpty`, body `self.count >
// 0`), for mutable subjects, and for parametric laws applied to constant bounds.
func (a *Analyzer) gatherLawIsPredFact(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	ident, ok := n.Left.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	key, deps, ok := a.factKeyWithDeps(lawName, lawArgs)
	if !ok {
		return // non-constant, non-freezable parametric args can't be tracked soundly
	}
	// A dependent fact (deps non-empty, docs/85 §5.3) also drops when any frozen dependency mutates.
	recordPredFactWithDeps(scope, ident.Name, key, deps)
}

// tryProveRefinementByFactSet attempts to discharge `value is law[args]` from a tracked predicate
// fact: when value is a bare identifier with a live fact for the SAME (law, constant args) — gained
// from a flow narrowing and not since invalidated by a mutation — the obligation is proven with no
// runtime check.
func (a *Analyzer) tryProveRefinementByFactSet(value ast.Expr, lawName string, predArgs []ast.Expr, allowDependentFacts bool) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	// Build the key the same way the narrowing site recorded it (constant OR dependent §5.3). A live
	// dependent fact has already survived the freeze invariant — any mutation of its deps would have
	// dropped it — so a hit discharges the obligation with no runtime check.
	key, deps, ok := a.factKeyWithDeps(lawName, predArgs)
	if !ok {
		return false
	}
	// A dependent key names variables; matching one across a function boundary (a call argument, whose
	// callee param refinement spells names in the CALLEE's scope) would conflate distinct variables
	// that happen to share a spelling. Dependent facts therefore discharge only same-function
	// obligations (var decl, return, ensures); constant facts (no deps) are absolute and always match.
	if len(deps) != 0 && !allowDependentFacts {
		return false
	}
	return a.lookupPredFact(ident.Name, key)
}
