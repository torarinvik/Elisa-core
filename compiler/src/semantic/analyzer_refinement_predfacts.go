package semantic

import (
	"elisacore/src/ast"
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
	}
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
// integer constant, the variable is now known to equal that constant; any other target shape or a
// non-constant RHS clears the fact (the value is no longer statically known). Sound because the
// fact is invalidated again at the next mutation, and `mutable T&` pointees are non-aliased.
func (a *Analyzer) recordWrittenConstForTarget(target, value ast.Expr) {
	name, ok := target.(*ast.Ident)
	if !ok || name == nil {
		a.invalidateWrittenConst(rootIdentNameOrEmpty(target))
		return
	}
	c, ok := a.constIntValue(value)
	if !ok {
		a.invalidateWrittenConst(name.Name)
		return
	}
	if a.currentScope == nil {
		return
	}
	// Drop any shadowed parent entry first so the lookup finds this fresh value.
	a.invalidateWrittenConst(name.Name)
	if a.currentScope.writtenConst == nil {
		a.currentScope.writtenConst = map[string]int64{}
	}
	a.currentScope.writtenConst[name.Name] = c
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

// lookupWrittenConst returns the known integer value of a variable, if a written-constant fact for
// it is live in the active scope chain.
func (a *Analyzer) lookupWrittenConst(name string) (int64, bool) {
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenConst != nil {
			if v, ok := scope.writtenConst[name]; ok {
				return v, true
			}
		}
	}
	return 0, false
}

// gatherLawIsPredFact records a predicate fact for `if x is Law:` where Law is a BARE law (no
// bracket args) and x is a bare identifier — regardless of whether x is mutable. Mutable is safe
// here because any later mutation of x invalidates the fact via invalidatePredFacts. The integer
// range path (gatherLawIsRangeRefinement) handles the decidable `self OP const` fragment over
// immutable ints; this complements it for laws outside that fragment (e.g. `darray is NonEmpty`,
// whose body is `self.count > 0`) and for mutable subjects.
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
	if !ok || len(lawArgs) != 0 {
		return // only bare laws carry a predicate fact for now
	}
	recordPredFact(scope, ident.Name, lawName)
}

// tryProveRefinementByFactSet attempts to discharge `value is law` from a tracked predicate fact:
// when value is a bare identifier with a live "law holds" fact (gained from a flow narrowing and
// not since invalidated by a mutation), the obligation is proven with no runtime check. Bare laws
// only (predArgs must be empty), mirroring gatherLawIsPredFact.
func (a *Analyzer) tryProveRefinementByFactSet(value ast.Expr, lawName string, predArgs []ast.Expr) bool {
	if len(predArgs) != 0 || lawName == "" {
		return false
	}
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	return a.lookupPredFact(ident.Name, lawName)
}
