package semantic

import (
	"elisacore/src/ast"
)

func (s *localContainerElementScan) classifyIdent(ident *ast.Ident, parent ast.Node, field string) {
	name := ident.Name
	s.mentioned[name] = true
	if s.opaque > 0 {
		s.ineligible[name] = true
		return
	}
	switch p := parent.(type) {
	case *ast.FieldExpr:
		if field != "Object" {
			break
		}
		switch p.Field {
		case "count", "len", "is_empty":
			return
		}
		if s.acceptedReceivers[p] {
			return
		}
	case *ast.IndexExpr:
		if field != "Object" || p.Index2 != nil {
			break
		}
		if s.acceptedReads[p] {
			s.reads = append(s.reads, localContainerElementRead{name: name, node: p, loops: append([]ast.Node(nil), s.loops...)})
			return
		}
	case *ast.IterForStmt:
		if field == "Source" && p.Mode == ast.IterBindValue {
			if ast.Expr(ident) == p.Source {
				s.binderReads = append(s.binderReads, localContainerElementRead{name: name, loop: p, loops: append([]ast.Node(nil), s.loops...)})
			}
			return
		}
	case *ast.CallExpr:
		// The source of an accepted statement `ys.extend(xs)` is only read: extend copies xs's
		// elements by value into ys (recordLocalContainerElementPush merges xs's tracked state).
		if callee, ok := p.Func.(*ast.FieldExpr); ok && field == "Args" && callee.Field == "extend" && s.acceptedReceivers[callee] && len(p.Args) == 1 && p.Args[0] == ast.Expr(ident) {
			s.extendSources = append(s.extendSources, localContainerExtendSource{call: p, name: name, loops: append([]ast.Node(nil), s.loops...)})
			return
		}
		if field == "Args" && len(p.ArgNames) == 0 && !p.HasArgForward && s.argReadOnly != nil {
			// A receiver-form call `recv.f(xs)` is rewritten to `f(recv, xs)` only during the
			// body's analysis; judge it as that call here. A prediction the rewrite does not
			// confirm is harmless: the flow judges the analyzed call and stops tracking any
			// container it cannot describe (recordCallFilledElementStates).
			judged, shift := p, 0
			if s.ufcsCall != nil {
				if rewritten := s.ufcsCall(p); rewritten != nil {
					judged, shift = rewritten, 1
				}
			}
			for index, arg := range p.Args {
				if arg == ast.Expr(ident) {
					if s.argReadOnly(judged, index+shift) {
						return
					}
					if s.argFillable != nil && s.argFillable(judged, index+shift) {
						// The call adds elements, like a push: a read in an enclosing loop
						// may see the previous iteration's fill.
						s.markPushed(name)
						s.modLoops[p] = append([]ast.Node(nil), s.loops...)
						return
					}
					break
				}
			}
		}
	case *ast.AddrOfExpr:
		// `f(&xs)` is the bare argument `xs`.
		if site, ok := s.addrArgs[p]; ok && field == "Operand" && len(site.call.ArgNames) == 0 && !site.call.HasArgForward && s.argReadOnly != nil {
			if s.argReadOnly(site.call, site.index) {
				return
			}
			if s.argFillable != nil && s.argFillable(site.call, site.index) {
				s.markPushed(name)
				s.modLoops[site.call] = append([]ast.Node(nil), s.loops...)
				return
			}
		}
	case *ast.AssignStmt:
		// `xs <- f(args)` replaces every element with the call result's, whose provenance the
		// callee's return-element summary describes; the flow records it (or stops tracking).
		if field == "Target" && !p.Optional && ast.Expr(ident) == p.Target {
			if _, isCall := stripParenExpr(p.Value).(*ast.CallExpr); isCall {
				s.markPushed(name)
				s.modLoops[p] = append([]ast.Node(nil), s.loops...)
				return
			}
		}
	case *ast.ReturnStmt:
		// Returning the container hands its elements to the caller; the return-element summary
		// reads its tracked state.
		if field == "Value" {
			return
		}
	case *ast.QueryExpr:
		// `any x in xs where ...`: the binder is a by-value element copy.
		if field == "Source" {
			return
		}
	case *ast.ListComprehensionExpr:
		// `[f(x) for x in xs]` only reads xs: its binder is a by-value element copy, and the new
		// list's element provenance is computed from the binder, not from xs's tracked state.
		if field == "Source" && p.Owner == nil {
			return
		}
	}
	s.ineligible[name] = true
}

// recordLocalContainerElementInit starts element tracking for a local container declared
// with a list literal: its elements' provenance is the merge of the literal elements'.
func (a *Analyzer) recordLocalContainerElementInit(sym *Symbol, value ast.Expr) {
	if a.currentElementStates == nil || sym == nil || sym.Kind != SymbolLocal {
		return
	}
	if comp, ok := value.(*ast.ListComprehensionExpr); ok {
		state, recorded := a.comprehensionElementStates[comp]
		if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !recorded || !isDArray || comp.Owner != nil || comp.Parallel {
			delete(a.currentElementStates, sym)
			return
		}
		a.currentElementStates[sym] = cloneRegionRefState(state)
		a.traceElemState(sym, 602)
		return
	}
	if tern, ok := stripParenExpr(value).(*ast.TernaryExpr); ok {
		// `f(args) if c else []`: the elements are one branch's, so their provenance is the merge.
		state, known := a.ternaryInitElementState(tern)
		if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !known || !isDArray {
			delete(a.currentElementStates, sym)
			return
		}
		a.currentElementStates[sym] = state
		a.traceElemState(sym, 612)
		return
	}
	if call, ok := stripParenExpr(value).(*ast.CallExpr); ok {
		// A call result: its elements are what the callee's return-element summary says.
		state, known := a.callReturnedElementState(call)
		if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !known || !isDArray {
			delete(a.currentElementStates, sym)
			return
		}
		a.currentElementStates[sym] = state
		a.traceElemState(sym, 622)
		return
	}
	lit, ok := value.(*ast.ListLitExpr)
	if !ok || lit.Brace || lit.Owner != nil || lit.Keys != nil {
		delete(a.currentElementStates, sym)
		return
	}
	if _, ok := stripRefForBounds(sym.Type).(*DArrayType); !ok {
		return
	}
	for _, spread := range lit.Spreads {
		if spread {
			return
		}
	}
	states := make([]regionRefState, 0, len(lit.Elems))
	for _, elem := range lit.Elems {
		state, ok, known := a.elementStorageState(elem)
		if !known {
			delete(a.currentElementStates, sym)
			return
		}
		if ok {
			states = append(states, state)
		}
	}
	merged, _ := mergeRegionRefStates(states...)
	a.currentElementStates[sym] = merged
	a.traceElemState(sym, 650)
}

// ternaryInitElementState describes a ternary initializer whose branches are each a call
// with a return-element summary, `[]`, or such a ternary.
func (a *Analyzer) ternaryInitElementState(tern *ast.TernaryExpr) (regionRefState, bool) {
	states := make([]regionRefState, 0, 2)
	for _, branch := range []ast.Expr{tern.Value, tern.Alt} {
		switch b := stripParenExpr(branch).(type) {
		case *ast.CallExpr:
			state, known := a.callReturnedElementState(b)
			if !known {
				return regionRefState{}, false
			}
			states = append(states, state)
		case *ast.ListLitExpr:
			if len(b.Elems) != 0 || b.Brace || b.Owner != nil || b.Keys != nil {
				return regionRefState{}, false
			}
		case *ast.TernaryExpr:
			state, known := a.ternaryInitElementState(b)
			if !known {
				return regionRefState{}, false
			}
			states = append(states, state)
		default:
			return regionRefState{}, false
		}
	}
	provenanced := states[:0]
	for _, state := range states {
		if hasRegionProvenance(state) {
			provenanced = append(provenanced, state)
		}
	}
	if len(provenanced) == 0 {
		// Every branch is `[]` or a call whose elements carry no region provenance.
		return regionRefState{}, true
	}
	return mergeRegionRefStates(provenanced...)
}

// recordComprehensionElementState records the provenance of a list comprehension's element
// expression while its binders are in scope; one without provenance records the empty state.
func (a *Analyzer) recordComprehensionElementState(expr *ast.ListComprehensionExpr) {
	if a.currentElementStates == nil {
		return
	}
	state, _, known := a.elementStorageState(expr.Value)
	if a.comprehensionElementStates == nil {
		a.comprehensionElementStates = map[*ast.ListComprehensionExpr]regionRefState{}
	}
	if !known {
		delete(a.comprehensionElementStates, expr)
		return
	}
	a.comprehensionElementStates[expr] = state
}

// recordLocalContainerElementAssign replaces a tracked local container's element state on a
// whole assignment `xs <- f(args)` with the callee's summarized return-element provenance;
// a call without a summary stops tracking the container.
func (a *Analyzer) recordLocalContainerElementAssign(n *ast.AssignStmt) {
	if a.currentElementStates == nil || a.currentScope == nil || n == nil || n.Optional {
		return
	}
	ident, ok := n.Target.(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	if _, tracked := a.currentElementStates[sym]; !tracked {
		return
	}
	call, isCall := stripParenExpr(n.Value).(*ast.CallExpr)
	state, known := regionRefState{}, false
	if isCall {
		state, known = a.callReturnedElementState(call)
	}
	if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !known || !isDArray {
		a.checkElementLoopAssumptions(sym, n, regionRefState{}, false)
		delete(a.currentElementStates, sym)
		return
	}
	a.checkElementLoopAssumptions(sym, n, state, true)
	a.currentElementStates[sym] = cloneRegionRefState(state)
	a.traceElemState(sym, 738)
}

// recordLocalContainerElementPush merges a pushed value's provenance into the receiver's
// element state; a bulk push (or extend) from anything but a parameter stops tracking the receiver.
func (a *Analyzer) recordLocalContainerElementPush(receiver ast.Expr, arg ast.Expr, bulk bool) {
	if a.currentElementStates == nil || a.currentScope == nil {
		return
	}
	ident, ok := receiver.(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	existing, tracked := a.currentElementStates[sym]
	if !tracked {
		return
	}
	bulkParamIndex := -1
	if bulk {
		// Only a parameter source is modelled: its elements are no shorter-lived than its region,
		// as for a by-value read `src[i]`. A local source's elements may be shorter-lived than the
		// container that holds them.
		if comp, isComp := stripParenExpr(arg).(*ast.ListComprehensionExpr); isComp {
			// `xs.extend([f(x) for x in src])`: the comprehension's elements are fresh by-value
			// copies whose provenance was recorded from its element expression.
			state, recorded := a.comprehensionElementStates[comp]
			if !recorded || comp.Owner != nil || comp.Parallel {
				a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
				a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
				delete(a.currentElementStates, sym)
				return
			}
			merged, _ := mergeRegionRefStates(existing, state)
			a.checkElementLoopAssumptions(sym, arg, merged, true)
			a.currentElementStates[sym] = merged
			a.traceElemState(sym, 776)
			return
		}
		source, isIdent := stripParenExpr(arg).(*ast.Ident)
		if !isIdent {
			a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			return
		}
		sourceSym, found := a.currentScope.Lookup(source.Name)
		if found && sourceSym != nil && sourceSym.Kind != SymbolParam && a.currentElementExtendSources[arg] {
			// `xs.extend(ys)` from a TRACKED local copies ys's elements by value, so xs's
			// elements gain exactly ys's tracked element provenance — not ys's backing
			// buffer, which is never retained. An untracked local source keeps the
			// conservative fallback below.
			if sourceState, tracked := a.currentElementStates[sourceSym]; tracked {
				if a.currentElementLoop != nil && a.currentElementLoop.reads[arg] != nil {
					a.currentElementLoop.assume(sourceSym, sourceState, arg)
				}
				merged, _ := mergeRegionRefStates(existing, sourceState)
				a.checkElementLoopAssumptions(sym, arg, merged, true)
				a.currentElementStates[sym] = merged
				a.traceElemState(sym, 797)
				return
			}
		}
		if !found || sourceSym == nil || sourceSym.Kind != SymbolParam {
			a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			return
		}
		bulkParamIndex = sourceSym.ParamIndex
	}
	if bulk && bulkParamIndex >= 0 {
		if container, ok := stripRefForBounds(sym.Type).(*DArrayType); ok && container != nil && a.typeMayOwnArenaStorage(container.Elem, map[string]bool{}) {
			// `extend(param)` copies the parameter's elements, not the parameter's
			// backing buffer. Their unknown nested references are conservatively
			// bounded by the source parameter's lifetime. Recording a concrete
			// symbolic-region dependency here loses the return summary for region-
			// polymorphic copies (especially when the element type comes from an
			// imported module and its payload provenance is opaque).
			state := regionRefStateFromParamDependency(bulkParamIndex)
			merged, _ := mergeRegionRefStates(existing, state)
			a.checkElementLoopAssumptions(sym, arg, merged, true)
			a.currentElementStates[sym] = merged
			a.traceElemState(sym, 819)
			return
		}
	}
	state, ok, known := a.elementStorageState(arg)
	if !known {
		a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
		delete(a.currentElementStates, sym)
		return
	}
	if !ok {
		return
	}
	merged, _ := mergeRegionRefStates(existing, state)
	a.checkElementLoopAssumptions(sym, arg, merged, true)
	a.currentElementStates[sym] = merged
	a.traceElemState(sym, 834)
}

// localContainerElementState returns the tracked element provenance for a by-value read
// `xs[i]` of an eligible local container.
func (a *Analyzer) localContainerElementState(n *ast.IndexExpr) (regionRefState, bool) {
	if a.currentScope == nil {
		return regionRefState{}, false
	}
	loopRead := false
	if !a.currentElementReads[n] {
		if a.currentElementLoop == nil || a.currentElementLoop.reads[n] == nil {
			return regionRefState{}, false
		}
		loopRead = true
	}
	ident, ok := n.Object.(*ast.Ident)
	if !ok {
		return regionRefState{}, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return regionRefState{}, false
	}
	state, ok := a.currentElementStates[sym]
	if ok && loopRead {
		a.currentElementLoop.assume(sym, state, n)
	}
	return state, ok
}
