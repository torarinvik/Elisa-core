package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// returnBorrowFlowForComprehension is the flow of `[v for x in src]`: the elements hold what the
// value (and a dict key) holds, with the binder holding what the source's elements hold, as a
// value `for` binding does. A range binder is an integer and holds nothing.
func (a *Analyzer) returnBorrowFlowForComprehension(n *ast.ListComprehensionExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	var binderFlow returnBorrowFlow
	switch {
	case n.RangeEnd != nil:
	default:
		binderFlow = a.returnBorrowIterValueFlow(n.Source, aliases, active, localBindings)
	}
	for _, outer := range []ast.Expr{n.Source, n.RangeEnd, n.RangeStep, n.Owner} {
		a.noteReturnBorrowCallArgumentStores(outer, aliases, active, localBindings)
	}
	env := a.beginReturnBorrowBranch(aliases)
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	a.defineReturnBorrowBinding(env, n.Name, n, nil, binderFlow)
	a.defineReturnBorrowBinding(env, n.SecondName, n, nil, binderFlow)
	for _, inner := range []ast.Expr{n.Filter, n.Value, n.Key} {
		a.noteReturnBorrowCallArgumentStores(inner, env, active, localBindings)
	}
	flow := a.returnBorrowFlowForExpr(n.Value, env, active, localBindings)
	flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(n.Key, env, active, localBindings))
	a.endReturnBorrowBranch(aliases)
	a.currentScope = outerScope
	return flow
}

// returnBorrowFlowForQuery is the flow of a single-binder query `first/each x in src [where p]
// [-> proj]`: the binder holds what the source's elements hold, as a value `for` binding does, and
// the result holds the projection (or the binder). The other kinds yield scalars. The filter's and
// projection's stores are noted with the binder in scope.
func (a *Analyzer) returnBorrowFlowForQuery(n *ast.QueryExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	a.noteReturnBorrowCallArgumentStores(n.Source, aliases, active, localBindings)
	a.noteReturnBorrowCallArgumentStores(n.Owner, aliases, active, localBindings)
	binderFlow := a.returnBorrowIterValueFlow(n.Source, aliases, active, localBindings)
	env := a.beginReturnBorrowBranch(aliases)
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	defer func() {
		a.endReturnBorrowBranch(aliases)
		a.currentScope = outerScope
	}()
	a.defineReturnBorrowBinding(env, n.Name, n, nil, binderFlow)
	a.noteReturnBorrowCallArgumentStores(n.Filter, env, active, localBindings)
	a.noteReturnBorrowCallArgumentStores(n.Projection, env, active, localBindings)
	switch n.Kind {
	case ast.QueryExprFirst, ast.QueryExprEach:
		if n.Projection != nil {
			return a.returnBorrowFlowForExpr(n.Projection, env, active, localBindings)
		}
		return a.returnBorrowFlowForExpr(&ast.Ident{Position: n.Pos(), Name: n.Name}, env, active, localBindings)
	}
	return returnBorrowFlow{}
}

// noteReturnBorrowConditionStores notes the stores of a condition, with each and-chain operand's
// unwraps in scope for the operands after it.
func (a *Analyzer) noteReturnBorrowConditionStores(cond ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	binders := map[ast.Node]bool{}
	returnBorrowMarkConditionBinders(cond, binders)
	if len(binders) == 0 {
		a.noteReturnBorrowCallArgumentStores(cond, aliases, active, localBindings)
		return
	}
	env := a.beginReturnBorrowBranch(aliases)
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	a.noteReturnBorrowAndChainStores(cond, env, active, localBindings)
	a.currentScope = outerScope
	a.joinReturnBorrowBranch(aliases, a.endReturnBorrowBranch(aliases), nil)
}

func (a *Analyzer) noteReturnBorrowAndChainStores(cond ast.Expr, env map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		a.noteReturnBorrowAndChainStores(n.Inner, env, active, localBindings)
		return
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			a.noteReturnBorrowAndChainStores(n.Left, env, active, localBindings)
			a.defineReturnBorrowConditionBindings(n.Left, env, active, localBindings)
			a.noteReturnBorrowAndChainStores(n.Right, env, active, localBindings)
			return
		}
	}
	a.noteReturnBorrowCallArgumentStores(cond, env, active, localBindings)
}

// returnBorrowFlowForMatchExpr is the flow of a match expression: each arm's binders hold what the
// subject holds (a view binder also its storage), as in a match statement, and the value is the join
// of the arms' final expressions. The arms' stores are noted as they are walked.
func (a *Analyzer) returnBorrowFlowForMatchExpr(n *ast.MatchExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
	a.noteReturnBorrowCallArgumentStores(n.Store, aliases, active, localBindings)
	subjectFlow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
	var flow returnBorrowFlow
	armEnvs := make([]map[string]returnBorrowFlow, 0, len(n.Arms))
	var armChanges [][]returnBorrowBranchChange
	for _, arm := range n.Arms {
		binders := returnBorrowMatchPatternBinders(arm.Pattern, nil)
		// A view binder's flow reads the subject's address in the environment BEFORE this arm's
		// binders, so such an arm keeps a private clone; any other arm walks in place.
		cloned := false
		for _, binder := range binders {
			cloned = cloned || binder.view
		}
		var armEnv map[string]returnBorrowFlow
		if cloned {
			armEnv = cloneReturnBorrowAliases(aliases)
		} else {
			armEnv = a.beginReturnBorrowBranch(aliases)
		}
		outerScope := a.currentScope
		a.currentScope = NewScope(outerScope)
		for _, binder := range binders {
			binderFlow := subjectFlow
			if binder.view {
				binderFlow = mergeReturnBorrowFlow(binderFlow, a.returnBorrowAddressFlow(n.Value, aliases, active, localBindings))
			}
			a.defineReturnBorrowBinding(armEnv, binder.name, binder.node, nil, binderFlow)
		}
		a.noteReturnBorrowCallArgumentStores(arm.Guard, armEnv, active, localBindings)
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForStatements(arm.Body, armEnv, active, localBindings, true))
		a.currentScope = outerScope
		if cloned {
			armEnvs = append(armEnvs, armEnv)
		} else {
			armChanges = append(armChanges, a.endReturnBorrowBranch(aliases))
		}
	}
	// Each arm starts from the environment before the match; their writes join afterwards.
	for _, env := range armEnvs {
		a.mergeReturnBorrowAliasMaps(aliases, env)
	}
	for _, changes := range armChanges {
		a.joinReturnBorrowBranch(aliases, changes, nil)
	}
	return flow
}

// returnBorrowFlowForUnwrap is the flow of `get v` / `try v`: the unwrapped payload holds what v
// holds (absence or an error propagates out instead), and an `else` supplies its fallback value.
// A recovery block (or a value reading the error binding) is not walked, so its value is
// conservative.
func (a *Analyzer) returnBorrowFlowForUnwrap(expr ast.Expr, value, fallback ast.Expr, recovery *ast.RecoveryClause, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	flow := a.returnBorrowFlowForExpr(value, aliases, active, localBindings)
	flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(fallback, aliases, active, localBindings))
	if recovery == nil {
		return flow
	}
	switch {
	case recovery.Kind == ast.RecoveryBlock || len(recovery.Body) > 0 || (recovery.Kind == ast.RecoveryValue && recovery.Binding != ""):
		return mergeReturnBorrowFlow(flow, a.returnBorrowConservativeFlow(expr, nil))
	case recovery.Kind == ast.RecoveryValue:
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(recovery.Value, aliases, active, localBindings))
	}
	// `else return` / `else raise` leave the function instead of producing a value.
	return flow
}

// returnBorrowFlowForFunc summarizes which parameters (and whether frame-local storage) can flow
// into fn's result. Every callee summary is memoized -- without it the walk re-enters each callee
// once per call path, which is exponential in call depth. A cut-free summary is cached across
// queries once fn's body was analyzed; otherwise it is memoized for the current top-level query
// under the dependency rule on returnBorrowQueryEntry.
func (a *Analyzer) returnBorrowFlowForFunc(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	if fn == nil {
		return returnBorrowFlow{}
	}
	if r := a.retained; r != nil {
		r.noteReturnBorrowDep(fn)
	}
	if summary, ok := a.returnBorrowFuncSummaries[fn]; ok {
		return summary
	}
	if len(active) == 0 {
		savedMemo, savedLog := a.returnBorrowQueryMemo, a.returnBorrowCutLog
		a.returnBorrowQueryMemo = a.returnBorrowRootMemo(fn)
		a.returnBorrowCutLog = nil
		defer func() { a.returnBorrowQueryMemo, a.returnBorrowCutLog = savedMemo, savedLog }()
	} else if entry, ok := a.returnBorrowQueryMemo[fn]; ok && a.returnBorrowQueryEntryValid(entry, active) {
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, entry.cutOn...)
		return entry.flow
	}
	if active[fn] {
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, fn)
		if assumed, ok := a.returnBorrowCycleAssume[fn]; ok {
			return assumed
		}
		return a.allBorrowedParamFlow(fn)
	}
	if len(active) >= semanticTraversalDepthLimit {
		// nil marks a depth-limit cut.
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, nil)
		return a.allBorrowedParamFlow(fn)
	}
	logStart := len(a.returnBorrowCutLog)
	if r := a.retained; r != nil {
		r.pushReturnBorrowOwner(fn)
	}
	// This walk may quietly type checked-get expressions with semantic
	// currentFuncDecl cleared. Its lexical proof owner is the callee being
	// scanned, not the requesting caller or a global initializer.
	restoreProofOwner := a.enterIndexProofOwner(fn, false)
	defer restoreProofOwner()
	summary := a.computeReturnBorrowFlowForFunc(fn, active)
	if r := a.retained; r != nil {
		r.popReturnBorrowOwner()
	}
	entry := returnBorrowQueryEntry{flow: summary}
	seen := map[*ast.FuncDecl]bool{fn: true} // a cut on fn itself is resolved inside this summary
	for _, cut := range a.returnBorrowCutLog[logStart:] {
		switch {
		case cut == nil:
			entry.depthCut = true
		case !seen[cut]:
			seen[cut] = true
			entry.cutOn = append(entry.cutOn, cut)
		}
	}
	// Callers inherit the cuts this summary still depends on; fn's own cut is resolved here.
	a.returnBorrowCutLog = append(a.returnBorrowCutLog[:logStart], entry.cutOn...)
	if entry.depthCut {
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, nil)
	}
	if !entry.depthCut && len(entry.cutOn) == 0 && a.returnBorrowFuncAnalyzed[fn] && fn != a.currentFuncDecl {
		if a.returnBorrowFuncSummaries == nil {
			a.returnBorrowFuncSummaries = map[*ast.FuncDecl]returnBorrowFlow{}
		}
		a.returnBorrowFuncSummaries[fn] = summary
	} else if a.returnBorrowQueryMemo != nil {
		a.returnBorrowQueryMemo[fn] = entry
	}
	return summary
}

// returnBorrowWalkReturnType is the declared return type of the function being summarized.
func (a *Analyzer) returnBorrowWalkReturnType() Type {
	if a.returnBorrowWalkFn == nil {
		return nil
	}
	if sym := a.funcDeclSymbols[a.returnBorrowWalkFn]; sym != nil {
		if fnType, ok := sym.Type.(*FuncType); ok {
			return fnType.Return
		}
	}
	return nil
}

// returnBorrowRootMemo is the query memo for top-level queries rooted at root. Every such query
// has the same active set at each callee it reaches, so an entry one query stored is exactly what
// the next would recompute -- while the analyzed bodies stay the same, which is why the memos are
// dropped once another function's analysis begins (a callee analyzed since then summarizes more
// precisely). Without it every deferred check and every recording pass re-walked the whole
// recursive cluster its function belongs to: ~130k summary walks for the parser.
func (a *Analyzer) returnBorrowRootMemo(root *ast.FuncDecl) map[*ast.FuncDecl]returnBorrowQueryEntry {
	if a.returnBorrowRootMemos == nil || a.returnBorrowRootMemosFor != a.currentFuncDecl {
		a.returnBorrowRootMemos = map[*ast.FuncDecl]map[*ast.FuncDecl]returnBorrowQueryEntry{}
		a.returnBorrowRootMemosFor = a.currentFuncDecl
	}
	memo := a.returnBorrowRootMemos[root]
	if memo == nil {
		memo = map[*ast.FuncDecl]returnBorrowQueryEntry{}
		a.returnBorrowRootMemos[root] = memo
	}
	return memo
}

func (a *Analyzer) returnBorrowQueryEntryValid(entry returnBorrowQueryEntry, active map[*ast.FuncDecl]bool) bool {
	if entry.depthCut {
		return false
	}
	for _, cut := range entry.cutOn {
		if !active[cut] {
			return false
		}
	}
	return true
}

// returnBorrowCycleIterationLimit bounds the fixpoint iteration of a recursive summary. The flow
// lattice is finite (Local plus two index sets over the parameters), so the iteration converges
// long before this; the limit only guards a non-monotone walk, which falls back to the old cut.
const returnBorrowCycleIterationLimit = 64

// computeReturnBorrowFlowForFunc summarizes fn. A recursive call back into fn is answered from the
// current approximation of fn's own summary, starting from the empty flow and growing it until
// the body's summary adds nothing: the least fixpoint of the (monotone) flow equations, so every
// finite recursion path is covered. Assuming "borrows every parameter" at the cycle instead made
// the whole parser's recursive expression cluster claim to return views of its Parser argument.
func (a *Analyzer) computeReturnBorrowFlowForFunc(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	if a.returnBorrowCycleAssume == nil {
		a.returnBorrowCycleAssume = map[*ast.FuncDecl]returnBorrowFlow{}
	}
	if _, nested := a.returnBorrowCycleAssume[fn]; nested {
		return a.computeReturnBorrowFlowForFuncOnce(fn, active)
	}
	assumed := returnBorrowFlow{}
	a.returnBorrowCycleAssume[fn] = assumed
	defer delete(a.returnBorrowCycleAssume, fn)
	for iteration := 0; ; iteration++ {
		logStart := len(a.returnBorrowCutLog)
		summary := a.computeReturnBorrowFlowForFuncOnce(fn, active)
		cutSelf := false
		for _, cut := range a.returnBorrowCutLog[logStart:] {
			if cut == fn {
				cutSelf = true
				break
			}
		}
		if !cutSelf {
			return summary
		}
		merged := mergeReturnBorrowFlow(assumed, summary)
		if !returnBorrowFlowGrew(assumed, merged) {
			return merged
		}
		if iteration+1 >= returnBorrowCycleIterationLimit {
			return mergeReturnBorrowFlow(merged, a.allBorrowedParamFlow(fn))
		}
		assumed = merged
		a.returnBorrowCycleAssume[fn] = assumed
		a.returnBorrowCutLog = a.returnBorrowCutLog[:logStart]
		// Memoized callee summaries that read the old approximation are stale.
		for callee, entry := range a.returnBorrowQueryMemo {
			for _, cut := range entry.cutOn {
				if cut == fn {
					delete(a.returnBorrowQueryMemo, callee)
					break
				}
			}
		}
	}
}

func (a *Analyzer) computeReturnBorrowFlowForFuncOnce(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	active[fn] = true
	defer delete(active, fn)
	savedWalkFn := a.returnBorrowWalkFn
	a.returnBorrowWalkFn = fn
	defer func() { a.returnBorrowWalkFn = savedWalkFn }()

	savedNamespace, savedUsings, savedScope := a.currentNamespace, a.currentUsings, a.currentScope
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if index := lastDotIndex(sym.Name); index >= 0 {
			a.currentNamespace = sym.Name[:index]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.declarationUsings(fn)...)
	defer func() {
		a.currentNamespace, a.currentUsings, a.currentScope = savedNamespace, savedUsings, savedScope
	}()

	aliases := map[string]returnBorrowFlow{}
	params := a.readOnlyExpandedFuncDeclParams(fn)
	var fnType *FuncType
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		fnType, _ = sym.Type.(*FuncType)
	}
	// A function returning a reference TO a reference/view (`-> sview&`) hands out the address of
	// the field it reads, so its reads are not content reads.
	savedContentReads := a.returnBorrowContentReads
	a.returnBorrowContentReads = true
	if fnType != nil {
		if ref, ok := fnType.Return.(*RefType); ok && (ref.Elem == nil || isBorrowLikeType(ref.Elem)) {
			a.returnBorrowContentReads = false
		}
	} else {
		a.returnBorrowContentReads = false
	}
	defer func() { a.returnBorrowContentReads = savedContentReads }()
	a.currentScope = NewScope(a.globalScope)
	for index, param := range params {
		var paramType Type
		if fnType != nil && index < len(fnType.Params) {
			paramType = fnType.Params[index]
		}
		if paramType == nil {
			paramType = a.resolveType(param.Type)
		}
		a.currentScope.Define(&Symbol{Name: param.Name, Kind: SymbolParam, Type: paramType, ParamIndex: index, Node: fn})
		if a.typeCarriesBorrowedStorage(paramType, map[Type]bool{}) {
			aliases[param.Name] = returnBorrowFlow{Params: map[int]bool{index: true}}
		}
	}
	return a.returnBorrowFlowForStatements(fn.Body, aliases, active, map[*Symbol]bool{}, true)
}

// defineReturnBorrowConditionBindings binds the optional unwraps a true condition guarantees
// (`if let v = x`, `if x is v`, through `and` and parentheses) to the unwrapped value's flow: the
// payload is a copy of what x holds. Other condition bindings stay unbound, so a read of one
// keeps its conservative flow.
func (a *Analyzer) defineReturnBorrowConditionBindings(cond ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		a.defineReturnBorrowConditionBindings(n.Inner, aliases, active, localBindings)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.defineReturnBorrowConditionBindings(n.Left, aliases, active, localBindings)
			a.defineReturnBorrowConditionBindings(n.Right, aliases, active, localBindings)
		case lexer.TOKEN_IS:
			a.defineReturnBorrowPatternTestBindings(n, aliases, active, localBindings)
		case lexer.TOKEN_OR:
			// `x is P(b) or y is Q(b)`: the shared binder is the payload of whichever
			// alternative matched, so it holds the union of the alternatives' flows.
			var tests []*ast.BinaryExpr
			collectOrPatternTests(n, &tests)
			merged := map[string]returnBorrowFlow{}
			nodes := map[string]ast.Node{}
			var order []string
			for _, test := range tests {
				_, subject, pattern, ok := unwrapDirectConditionPattern(test)
				if !ok || pattern == nil {
					continue
				}
				subjectFlow := a.returnBorrowFlowForExpr(subject, aliases, active, localBindings)
				for _, binder := range returnBorrowMatchPatternBinders(pattern, nil) {
					flow := subjectFlow
					if binder.view {
						flow = mergeReturnBorrowFlow(flow, a.returnBorrowAddressFlow(subject, aliases, active, localBindings))
					}
					if _, seen := merged[binder.name]; !seen {
						order = append(order, binder.name)
						nodes[binder.name] = n
					}
					merged[binder.name] = mergeReturnBorrowFlow(merged[binder.name], flow)
				}
			}
			for _, name := range order {
				a.defineReturnBorrowBinding(aliases, name, nodes[name], nil, merged[name])
			}
		}
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return
		}
		flow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
		a.defineReturnBorrowBinding(aliases, n.Name, n, nil, flow)
	}
}

// defineReturnBorrowPatternTestBindings defines the binders of `subject is Variant(binder, ...)`:
// they are the subject's payload, as in a match arm.
func (a *Analyzer) defineReturnBorrowPatternTestBindings(test *ast.BinaryExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	_, subject, pattern, ok := unwrapDirectConditionPattern(test)
	if !ok || pattern == nil {
		return
	}
	binders := returnBorrowMatchPatternBinders(pattern, nil)
	if len(binders) == 0 {
		return
	}
	subjectFlow := a.returnBorrowFlowForExpr(subject, aliases, active, localBindings)
	for _, binder := range binders {
		flow := subjectFlow
		if binder.view {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowAddressFlow(subject, aliases, active, localBindings))
		}
		a.defineReturnBorrowBinding(aliases, binder.name, binder.node, nil, flow)
	}
}

func (a *Analyzer) allBorrowedParamFlow(fn *ast.FuncDecl) returnBorrowFlow {
	flow := returnBorrowFlow{}
	if fn == nil {
		return flow
	}
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if fnType, ok := sym.Type.(*FuncType); ok {
			for index, paramType := range fnType.Params {
				if a.typeCarriesBorrowedStorage(paramType, map[Type]bool{}) {
					if flow.Params == nil {
						flow.Params = map[int]bool{}
						flow.Contents = map[int]bool{}
					}
					// Contents is a reference parameter's POINTEE: a by-value parameter's value is
					// already its Params flow. The reference itself -- the caller's address -- can
					// reach the result only if the result can hold a borrow into the referent.
					if ref, isRef := paramType.(*RefType); isRef {
						flow.Contents[index] = true
						if ref.Elem != nil && fnType.Return != nil && !a.returnBorrowResultMayTarget(fnType.Return, ref.Elem) {
							continue
						}
					}
					flow.Params[index] = true
				}
			}
		}
	}
	return flow
}
