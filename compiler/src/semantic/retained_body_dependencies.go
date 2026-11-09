package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

type retainedBodyDependencyPlan struct {
	removedLive []*ast.FuncDecl
	splices     []retainedSplice
	newTexts    map[*ast.FuncDecl]funcComparisonText
}

// retainedReanalyzeNameDependents schedules unchanged bodies whose name resolution changed.
func retainedReanalyzeNameDependents(r *RetainedAnalysis, a *Analyzer, paired retainedDeclarationPairing, comparison retainedComparison, liveFns map[fnKey][]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl, oldFunctionNames map[*ast.FuncDecl]string, splices []retainedSplice, pairedNew map[*ast.FuncDecl]*ast.FuncDecl, newTexts map[*ast.FuncDecl]funcComparisonText, usingChanges []retainedUsingChange) (retainedBodyDependencyPlan, error) {
	removed, added := comparison.removed, comparison.added
	overloadNames, usingsAdded, usingsRemoved := paired.overloadNames, paired.usingsAdded, paired.usingsRemoved
	var removedLive []*ast.FuncDecl
	if len(removed) != 0 {
		gone := map[string]bool{}
		goneFunctions := map[*ast.FuncDecl]bool{}
		for _, of := range removed {
			cands := liveFns[fnKey{retainedLineKey(of.Pos()), of.Name}]
			if len(cands) != 1 {
				return retainedBodyDependencyPlan{}, fmt.Errorf("%d analyzed candidates for removed %s", len(cands), of.Name)
			}
			live := cands[0]
			sym := a.funcDeclSymbols[live]
			// An overload set is removed whole, its survivors re-declared with it.
			if sym == nil || a.globalScope.Symbols[sym.Name] != sym || (len(a.ufcsFunctionsByName[sym.Name]) > 1 && !overloadNames[oldFunctionNames[of]]) {
				return retainedBodyDependencyPlan{}, fmt.Errorf("removed %s is not a plain global function", of.Name)
			}
			if _, known := scoped[live]; !known {
				return retainedBodyDependencyPlan{}, fmt.Errorf("removed %s is not a top-level analyzed function", of.Name)
			}
			removedLive = append(removedLive, live)
			gone[live.Name] = true
			goneFunctions[live] = true
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || goneFunctions[fn] {
				continue
			}
			if !retainedMentionsAny(fn, gone) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil {
				return retainedBodyDependencyPlan{}, fmt.Errorf("%s mentions a removed function but has no edited counterpart", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
		}
	}
	// A newly declared name can repair an unresolved call, or change overload
	// dispatch. Recheck every existing body that mentions it, including bodies
	// that were unchanged in text and currently only carry an undefined-name error.
	if len(added) != 0 {
		names := map[string]bool{}
		for _, fn := range added {
			names[fn.Name] = true
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			spliced[fn] = true
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || !retainedMentionsAny(fn, names) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return retainedBodyDependencyPlan{}, fmt.Errorf("%s mentions an added function but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
		}
	}
	// An added or removed `using`: its scope's declarations take their new usings, and the
	// functions there that mention a member of the used module are re-analyzed.
	if usingsAdded+usingsRemoved != 0 {
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			spliced[fn] = true // removed, or re-declared with a new signature
		}
		for _, c := range usingChanges {
			fn, ok := r.decls[c.index].Decl.(*ast.FuncDecl)
			if !ok || !c.mention || spliced[fn] {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil {
				return retainedBodyDependencyPlan{}, fmt.Errorf("%s mentions a newly used name but has no edited counterpart", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
		}
	}
	return retainedBodyDependencyPlan{removedLive: removedLive, splices: splices, newTexts: newTexts}, nil
}
