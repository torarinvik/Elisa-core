package semantic

import (
	"elisacore/src/ast"
)

// SMT symbols are keyed by SPELLING (`v_y`), while Elisa lets a block shadow an outer binding
// (`y: i64 = x; if y > 0: y: i64 = 0 - x`). Every hypothesis therefore has to be about the binding
// that the spelling denotes at the proof point; otherwise two bindings collapse into one symbol and
// their facts can contradict (`v_y > 0` from the outer guard, `v_y = 0 - x` from the inner
// declaration), which proves anything. The helpers below keep each hypothesis source bound to the
// binding it was stated about, and drop it (sound: fewer assumptions) when a shadowing binding now
// owns one of its names.

// smtFactPins records the symbol each name of a flow fact resolves to at record time, resolved from
// the scope the fact is recorded INTO (a loop fact names the loop variable of the loop scope; a branch
// scope is still empty when its guard is recorded, so it resolves like its parent).
func (a *Analyzer) smtFactPins(scope *Scope, expr ast.Expr) map[string]*Symbol {
	if a == nil || scope == nil || expr == nil {
		return nil
	}
	names := smtFactDeps(expr)
	pins := make(map[string]*Symbol, len(names))
	add := func(name string) {
		if _, done := pins[name]; done {
			return
		}
		sym, _ := scope.Lookup(name)
		pins[name] = sym
	}
	for name := range names {
		add(name)
	}
	for _, name := range exprIdentNames(expr) {
		add(name)
	}
	return pins
}

// hypothesisFactBindingsCurrent reports whether a fact found in scope sc still talks about the
// bindings its names denote at the current proof point.
//   - An assert fact carries record-time pins: each name must still resolve to its pinned symbol.
//     (A fact recorded in a scope BEFORE a same-scope shadowing declaration — a branch guard on the
//     outer `y`, then `y: i64 = ...` in the branch body — fails this check.)
//   - A range fact is keyed by name in its scope; it is current when the name resolves to the same
//     symbol from the proof point as from that scope. Stale same-scope range facts are removed when a
//     binding is declared (forgetShadowedRangeFact).
func (a *Analyzer) hypothesisFactBindingsCurrent(sc *Scope, f hypothesisFact) bool {
	if a == nil || a.currentScope == nil {
		return true
	}
	switch fact := f.(type) {
	case assertHypothesisFact:
		for name, pinned := range fact.fact.Pins {
			now, _ := a.currentScope.Lookup(name)
			if now != pinned {
				return false
			}
		}
		return true
	case rangeHypothesisFact:
		if sc == nil {
			return true
		}
		now, _ := a.currentScope.Lookup(fact.name)
		then, _ := sc.Lookup(fact.name)
		return now == then
	}
	return true
}

// rangeFactBindingCurrent reports whether a range fact stored under name in scope is about the
// binding name denotes at the proof point.
func (a *Analyzer) rangeFactBindingCurrent(scope *Scope, name string) bool {
	if a == nil || a.currentScope == nil || scope == nil {
		return true
	}
	now, _ := a.currentScope.Lookup(name)
	then, _ := scope.Lookup(name)
	return now == then
}

// forgetShadowedRangeFact drops a scope's range fact for a name that is being (re)bound in that
// scope: the fact was established for the binding the name denoted before this declaration.
func forgetShadowedRangeFact(scope *Scope, name string) {
	if scope == nil || scope.rangeFacts == nil {
		return
	}
	delete(scope.rangeFacts, name)
}

// shadowedParam reports whether the current function's parameter `name` is shadowed at the proof
// point, so a function-level hypothesis over it (`requires`, a param `where`, a non-null ref fact)
// no longer describes the binding `v_name` denotes.
func (a *Analyzer) shadowedParam(name string) bool {
	if a == nil || a.currentScope == nil || a.currentFuncDecl == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(name)
	if !found || sym == nil {
		return false
	}
	return sym.Kind != SymbolParam || sym.Node != a.currentFuncDecl
}

// shadowingActive reports whether, at the proof point, some local binding of the current function
// shadows another local or parameter of it. Whole-body tiers (WP transport, the pure return tree)
// build a term over the function's ENTRY bindings and check it against the proof point's
// hypotheses; that is only sound while every spelling denotes one binding.
func (a *Analyzer) shadowingActive() bool {
	if a == nil || a.currentScope == nil || a.currentFuncDecl == nil {
		return false
	}
	for sc := a.currentScope; sc != nil && sc.Parent != nil; sc = sc.Parent {
		for name, sym := range sc.Symbols {
			if sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam) {
				continue
			}
			if outer, found := sc.Parent.Lookup(name); found && outer != nil && outer != sym &&
				(outer.Kind == SymbolLocal || (outer.Kind == SymbolParam && outer.Node == a.currentFuncDecl)) {
				return true
			}
		}
	}
	return false
}

// mentionsShadowedParam reports whether a function-level clause names a shadowed parameter.
func (a *Analyzer) mentionsShadowedParam(clause ast.Expr) bool {
	if a == nil || a.currentFuncDecl == nil || clause == nil {
		return false
	}
	names := map[string]bool{}
	for name := range smtFactDeps(clause) {
		names[name] = true
	}
	for _, name := range exprIdentNames(clause) {
		names[name] = true
	}
	for _, param := range a.currentFuncDecl.Params {
		if names[param.Name] && a.shadowedParam(param.Name) {
			return true
		}
	}
	return false
}

// immutableLocalDefinitionCurrent reports whether `name = value` (the defining equation of the
// immutable local sym, declared in scope declScope) can be stated over the current symbols: every
// name the initializer mentions must denote, from the proof point, the binding it denoted at the
// declaration — not the local itself (`y: i64 = y + 1` reads the OUTER y), not a binding declared
// later in the declaring scope, and not one shadowed between the proof point and that scope.
func (a *Analyzer) immutableLocalDefinitionCurrent(declScope *Scope, sym *Symbol, value ast.Expr) bool {
	if a == nil || a.currentScope == nil || declScope == nil || sym == nil || value == nil {
		return false
	}
	// The initializer captured its inputs' values at the declaration; one of them was written since.
	if a.smtStaleLocalDefs[sym] {
		return false
	}
	declPos := sym.Node.Pos()
	names := map[string]bool{}
	for name := range smtFactDeps(value) {
		names[name] = true
	}
	for _, name := range exprIdentNames(value) {
		names[name] = true
	}
	for name := range names {
		if name == sym.Name {
			return false
		}
		then, _ := declScope.Lookup(name)
		now, _ := a.currentScope.Lookup(name)
		if then != now {
			return false
		}
		if then != nil && then.Node != nil && then.Kind == SymbolLocal {
			if p := then.Node.Pos(); p.File == declPos.File && p.Offset > declPos.Offset {
				return false // declared after the initializer was evaluated
			}
		}
		// A mutable global can change in any callee, so no defining equation over it survives.
		if then != nil && then.Kind == SymbolGlobal && then.Mutable {
			return false
		}
	}
	return true
}

// markLocalDefinitionsStale records a write to the places rooted at roots (nil: an unidentifiable
// place, which may be any of them). Every immutable local visible here whose initializer read one
// of those roots loses its defining equation for the rest of the function: `y: i64 = x + 1;
// x <- 5` must not prove `y == 6`. It runs from the same mutation chokepoint that drops assert
// facts (assignments, compound assignments, `&x`/receiver/mutable-argument calls, and the loop-head
// havoc of every root a loop body writes, which covers the back edge), so a local declared before
// a loop whose body writes its input is stale inside and after the loop. Locals declared after the
// write are not yet visible and keep their equation, which holds from their declaration on.
func (a *Analyzer) markLocalDefinitionsStale(roots map[string]bool) {
	if a == nil {
		return
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, sym := range sc.Symbols {
			if sym == nil || sym.Mutable || sym.Kind != SymbolLocal || a.smtStaleLocalDefs[sym] {
				continue
			}
			vd, ok := sym.Node.(*ast.VarDeclStmt)
			if !ok || vd == nil || vd.Value == nil {
				continue
			}
			hit := roots == nil
			if !hit {
				for _, name := range a.smtLocalDefinitionDeps(sym, vd) {
					if roots[name] {
						hit = true
						break
					}
				}
			}
			if hit {
				if a.smtStaleLocalDefs == nil {
					a.smtStaleLocalDefs = map[*Symbol]bool{}
				}
				a.smtStaleLocalDefs[sym] = true
			}
		}
	}
}

// smtLocalDefinitionDeps preserves the full syntactic dependency union used by stale-definition
// invalidation. Compute it once per immutable local: this path runs after many different writes,
// while the initializer AST stays fixed for the lifetime of the Analyzer.
func (a *Analyzer) smtLocalDefinitionDeps(sym *Symbol, vd *ast.VarDeclStmt) []string {
	if a == nil || sym == nil || vd == nil {
		return nil
	}
	if deps, ok := a.smtLocalDefDeps[sym]; ok {
		return deps
	}
	depSet := smtFactDeps(vd.Value)
	allNames := exprIdentNames(vd.Value)
	deps := make([]string, 0, len(depSet)+len(allNames))
	for name := range depSet {
		deps = append(deps, name)
	}
	for _, name := range allNames {
		if depSet[name] {
			continue
		}
		if depSet == nil {
			depSet = map[string]bool{}
		}
		depSet[name] = true
		deps = append(deps, name)
	}
	if a.smtLocalDefDeps == nil {
		a.smtLocalDefDeps = map[*Symbol][]string{}
	}
	a.smtLocalDefDeps[sym] = deps
	return deps
}
