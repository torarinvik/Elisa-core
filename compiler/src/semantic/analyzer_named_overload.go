package semantic

import "elisacore/src/ast"

// namedOverloadReceiver binds explicit arguments without evaluating or reordering
// them. A candidate is eligible only when its complete call shape is valid.
// Returning -1 leaves defaulted receivers and malformed calls to normal checking.
func namedOverloadReceiver(call *ast.CallExpr, fn *FuncType) int {
	if call == nil || fn == nil || call.HasArgForward || fn.Variadic {
		return -1
	}
	count := funcTypeExplicitParamCount(fn)
	if count == 0 || len(fn.ExplicitParamNames) != count {
		return -1
	}
	filled := make([]bool, count)
	receiver, next := -1, 0
	sawNamed := false
	for _, item := range orderedCallArgItems(call) {
		if item.ArgIndex < 0 || item.ArgIndex >= len(call.Args) {
			return -1
		}
		name := call.ArgName(item.ArgIndex)
		index := -1
		if name == "" {
			if sawNamed || next >= count {
				return -1
			}
			index = next
			next++
		} else {
			sawNamed = true
			for i, param := range fn.ExplicitParamNames {
				if param == name {
					index = i
					break
				}
			}
		}
		if index < 0 || filled[index] {
			return -1
		}
		filled[index] = true
		if index == 0 {
			receiver = item.ArgIndex
		}
	}
	for i, present := range filled {
		if !present && (i >= len(fn.ExplicitParamHasDefault) || !fn.ExplicitParamHasDefault[i]) {
			return -1
		}
	}
	return receiver
}

// Select a named call's receiver-ranked overload without converting a named
// argument to positional syntax. The regular argument binder retains authority
// over all diagnostics and default expansion once a unique winner is known.
func (a *Analyzer) rewriteNamedReceiverOverload(call *ast.CallExpr, ident *ast.Ident) {
	if call == nil || ident == nil || call.NamedArgCount() == 0 || call.HasArgForward {
		return
	}
	// A local function value shadows the global overload set.
	for scope := a.currentScope; scope != nil && scope != a.globalScope; scope = scope.Parent {
		if _, found := scope.Symbols[ident.Name]; found {
			return
		}
	}
	seen := map[*Symbol]bool{}
	probed := map[int]Type{}
	var best *Symbol
	bestRank := 0
	ambiguous := false
	for _, name := range a.visibleNameCandidates(ident.Name) {
		candidates := a.ufcsFunctionsByName[name]
		if len(candidates) == 0 {
			if sym, ok := a.globalScope.Lookup(name); ok {
				candidates = []*Symbol{sym}
			}
		}
		for _, sym := range candidates {
			if sym == nil || seen[sym] || (sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc) {
				continue
			}
			seen[sym] = true
			fn, ok := sym.Type.(*FuncType)
			if !ok || fn == nil || len(fn.Params) == 0 {
				continue
			}
			index := namedOverloadReceiver(call, fn)
			if index < 0 {
				continue
			}
			actual, seenType := probed[index]
			if !seenType {
				saved := a.suppressDiagnostics
				probe := a.beginProofReportProbe()
				a.suppressDiagnostics = true
				actual = a.analyzeExpr(call.Args[index])
				a.suppressDiagnostics = saved
				probe.restore()
				probed[index] = actual
			}
			if actual == nil || IsInvalidType(actual) {
				continue
			}
			rank := a.receiverMatchRank(fn.Params[0], actual)
			if rank == 0 {
				continue
			}
			if rank > bestRank {
				best, bestRank, ambiguous = sym, rank, false
			} else if rank == bestRank {
				ambiguous = true
			}
		}
	}
	if best == nil || ambiguous {
		return
	}
	rewritten := &ast.Ident{Position: ident.Position, Name: best.Name}
	if specialized, ok := call.Func.(*ast.SpecializeExpr); ok {
		specialized.Operand = rewritten
	} else {
		call.Func = rewritten
	}
}
