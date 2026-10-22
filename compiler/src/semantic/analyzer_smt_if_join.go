package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// If-join of flow facts. A branch's own scope ends holding the facts that are true when control
// leaves it — its guard (`end >= 0`) and the exact equalities of the writes it made (`hi == end`).
// Those facts used to vanish with the branch scope, so after
//
//	hi: mutable i64 = 5
//	if end >= 0:
//	    hi <- end
//
// nothing tied `hi` to `end` (the else path's `not (end >= 0)` was lost too, and the entry fact
// `hi == 5` was invalidated by the write). recordIfJoinSMTFact records their DISJUNCTION
// `(end >= 0 and hi == end) or (not (end >= 0))` at the join.
//
// SOUNDNESS: control reaches the join through exactly one fall-through branch (branches that
// definitely exit are excluded by the caller), and that branch's end facts hold in the joined state
// because nothing runs between the branch end and the join. Every fact is checked to talk about the
// binding its names denote AT THE JOIN (record-time pins; a fact about a branch-local declaration
// is dropped from its conjunct, which only weakens that disjunct). The joined fact carries the pins
// and the union of dependency roots, so later writes and shadowing retire it like any other fact.
// A branch with no usable fact makes the disjunction trivially true, so nothing is recorded.

const ifJoinMaxFactsPerBranch = 24

// smtFactFrame is the visible flow-fact lists of every scope on the chain, by scope.
type smtFactFrame struct {
	scopes []*Scope
	facts  [][]smtFact
}

func (a *Analyzer) snapshotSMTFactFrame() smtFactFrame {
	depth := 0
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		depth++
	}
	frame := smtFactFrame{scopes: make([]*Scope, 0, depth), facts: make([][]smtFact, 0, depth)}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		frame.scopes = append(frame.scopes, sc)
		frame.facts = append(frame.facts, append([]smtFact(nil), sc.smtAssertFacts...))
	}
	return frame
}

// restoreSMTFactFrame returns the chain's facts as a branch left them and resets them to the entry
// frame, so the next branch is analyzed from the state it actually starts in: a sibling branch's
// writes never happen on its path, so their invalidations must not reach it.
func (a *Analyzer) restoreSMTFactFrame(entry smtFactFrame) smtFactFrame {
	// The frame's scope list is immutable; only the fact lists change between branches.
	survivors := smtFactFrame{scopes: entry.scopes, facts: make([][]smtFact, len(entry.scopes))}
	for i, sc := range entry.scopes {
		// The survivor list is read-only after this point. Restore below always installs
		// a fresh copy of entry.facts before another branch can append or invalidate facts.
		survivors.facts[i] = sc.smtAssertFacts
		sc.smtAssertFacts = append([]smtFact(nil), entry.facts[i]...)
	}
	return survivors
}

// joinIfSMTFacts sets the chain's facts after an if: the entry facts that survived EVERY
// fall-through branch hold outright; each branch's other survivors plus its own end facts are
// disjoined (recordIfJoinSMTFact). With no fall-through branch the code after the if is unreachable
// and the entry frame (already restored) stays.
func (a *Analyzer) joinIfSMTFacts(entry smtFactFrame, survivors []smtFactFrame, branchFacts [][]smtFact, disjoin bool) {
	if a == nil || len(survivors) == 0 {
		return
	}
	common := make([][]smtFact, len(entry.scopes))
	extras := make([][]smtFact, len(survivors))
	for i := range entry.scopes {
		for _, fact := range entry.facts[i] {
			inAll := true
			for _, branch := range survivors {
				if !smtFactListHas(branch.facts[i], fact) {
					inAll = false
					break
				}
			}
			if inAll {
				if common[i] == nil {
					capacity := len(entry.facts[i])
					for _, branch := range survivors {
						if len(branch.facts[i]) < capacity {
							capacity = len(branch.facts[i])
						}
					}
					// The current scope may receive one disjoined fact below.
					if disjoin && entry.scopes[i] == a.currentScope {
						capacity++
					}
					common[i] = make([]smtFact, 0, capacity)
				}
				common[i] = append(common[i], fact)
			}
		}
		for b, branch := range survivors {
			for _, fact := range branch.facts[i] {
				if !smtFactListHas(common[i], fact) {
					extras[b] = append(extras[b], fact)
				}
			}
		}
	}
	for i, sc := range entry.scopes {
		sc.smtAssertFacts = common[i]
	}
	if !disjoin {
		return
	}
	// extras and its slices are private to this join. Append branch-local facts
	// directly instead of copying the collected survivors into another builder.
	for b := range survivors {
		extras[b] = append(extras[b], branchFacts[b]...)
	}
	a.recordIfJoinSMTFact(extras)
}

// smtFactListHas matches by expression identity: entry facts are carried by value into each
// branch's lists, so a surviving fact keeps its Expr node.
func smtFactListHas(list []smtFact, fact smtFact) bool {
	for _, other := range list {
		if other.Expr == fact.Expr {
			return true
		}
	}
	return false
}

func (a *Analyzer) recordIfJoinSMTFact(branches [][]smtFact) {
	if a == nil || a.currentScope == nil || len(branches) == 0 {
		return
	}
	pins := map[string]*Symbol{}
	deps := map[string]bool{}
	var disjuncts []ast.Expr
	for _, facts := range branches {
		var conj ast.Expr
		kept := 0
		for _, fact := range facts {
			if fact.Expr == nil || fact.Binders != nil || kept >= ifJoinMaxFactsPerBranch {
				continue
			}
			current := true
			for name, pinned := range fact.Pins {
				now, _ := a.currentScope.Lookup(name)
				if now != pinned {
					current = false
					break
				}
				if prior, seen := pins[name]; seen && prior != pinned {
					current = false
					break
				}
			}
			if !current {
				continue
			}
			for name, pinned := range fact.Pins {
				pins[name] = pinned
			}
			for dep := range fact.Deps {
				deps[dep] = true
			}
			kept++
			if conj == nil {
				conj = fact.Expr
			} else {
				conj = &ast.BinaryExpr{Position: fact.Expr.Pos(), Op: lexer.TOKEN_AND, Left: conj, Right: fact.Expr}
			}
		}
		if conj == nil {
			return // this branch constrains nothing: the disjunction is trivially true
		}
		disjuncts = append(disjuncts, conj)
	}
	var joined ast.Expr
	for _, d := range disjuncts {
		if joined == nil {
			joined = d
		} else {
			joined = &ast.BinaryExpr{Position: d.Pos(), Op: lexer.TOKEN_OR, Left: joined, Right: d}
		}
	}
	if len(disjuncts) == 1 {
		// A single fall-through branch: its facts hold outright; record them as separate facts.
		for _, fact := range branches[0] {
			if fact.Expr == nil || fact.Binders != nil {
				continue
			}
			current := true
			for name, pinned := range fact.Pins {
				if now, _ := a.currentScope.Lookup(name); now != pinned {
					current = false
					break
				}
			}
			if current {
				a.currentScope.smtAssertFacts = append(a.currentScope.smtAssertFacts, fact)
			}
		}
		return
	}
	a.currentScope.smtAssertFacts = append(a.currentScope.smtAssertFacts, smtFact{Expr: joined, Deps: deps, Pins: pins})
}
