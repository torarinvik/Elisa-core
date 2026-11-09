package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

type retainedValueDependencyPlan struct {
	constLive                            [][2]*ast.ConstDecl
	addedConstPrepared, removedConstLive []ast.Decl
	addedStructPrepared                  []*ast.StructDecl
	removedStructLive                    []scopedDecl
	splices                              []retainedSplice
}

// retainedValueDependencies plans declaration and body reanalysis for const and struct changes.
func (r *RetainedAnalysis) retainedValueDependencies(a *Analyzer, paired retainedDeclarationPairing, comparison retainedComparison, scratch *ast.File, newDecls []ast.Decl, splices []retainedSplice, pairedNew map[*ast.FuncDecl]*ast.FuncDecl, newTexts map[*ast.FuncDecl]funcComparisonText, spanOf func(ast.Decl) (string, string)) (retainedValueDependencyPlan, error) {
	constPairs := comparison.constPairs
	newSpans := comparison.newSpans
	addedConsts, removedConsts := paired.addedConsts, paired.removedConsts
	addedStructs, removedStructs := paired.addedStructs, paired.removedStructs
	// An edited const value: recheck direct users and callers of dependent value contracts.
	// Other declaration/signature dependencies retain their declaration-phase guard.
	var constLive [][2]*ast.ConstDecl // (live, scratch) per edited const
	var addedConstPrepared []ast.Decl
	var removedConstLive []ast.Decl
	var addedStructPrepared []*ast.StructDecl
	var removedStructLive []scopedDecl
	if len(constPairs)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) != 0 {
		names := map[string]bool{}
		edited := map[ast.Decl]bool{}
		for _, e := range constPairs {
			names[e[0].Name] = true
			edited[e[1]] = true
		}
		for _, d := range addedConsts {
			names[retainedValueDeclName(d)] = true
			edited[d] = true
		}
		for _, d := range removedConsts {
			names[retainedValueDeclName(d)] = true
		}
		for _, sd := range addedStructs {
			names[sd.Name] = true
			edited[sd] = true
		}
		for _, sd := range removedStructs {
			names[sd.Name] = true
		}
		pat := retainedNamesPattern(names)
		contractUsers := map[string]bool{}
		valueEditsOnly := len(constPairs) != 0 && len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) == 0
		for j, d := range newDecls {
			if edited[d] {
				continue
			}
			if fn, ok := d.(*ast.FuncDecl); ok {
				if pat.MatchString(retainedNormalized(retainedHeader(fn))) {
					if !valueEditsOnly || !retainedConstValueContractOnly(fn, pat) {
						return retainedValueDependencyPlan{}, fmt.Errorf("the signature of %s names an added or edited const or struct", fn.Name)
					}
					contractUsers[fn.Name] = true
				}
			} else if _, ns := d.(*ast.NamespaceDecl); !ns && pat.MatchString(newSpans[j]) {
				return retainedValueDependencyPlan{}, fmt.Errorf("declaration at %s names an added or edited const or struct", retainedLineKey(d.Pos()))
			}
		}
		// A changed contract value can retract a caller's proof even though the
		// function's syntax did not change. Recheck the whole caller closure.
		// Reuse captured name facts only for byte-identical function spans;
		// edited/new functions are scanned from their current syntax.
		unchangedUses := map[*ast.FuncDecl]retainedBodyUses{}
		if len(names) != 0 {
			for live, current := range pairedNew {
				if uses, ok := r.bodyUses[live]; ok {
					oldSpan, newSpan := spanOf(live)
					if oldSpan != "" && oldSpan == newSpan {
						unchangedUses[current] = uses
					}
				}
			}
		}
		mentionsCurrent := func(fn *ast.FuncDecl, selected map[string]bool) bool {
			if uses, ok := unchangedUses[fn]; ok {
				for _, name := range uses.names {
					if selected[name] {
						return true
					}
				}
				return false
			}
			return retainedMentionsAny(fn, selected)
		}
		for grew := len(contractUsers) != 0; grew; {
			grew = false
			for _, d := range newDecls {
				fn, ok := d.(*ast.FuncDecl)
				if ok && !contractUsers[fn.Name] && mentionsCurrent(fn, contractUsers) {
					contractUsers[fn.Name] = true
					grew = true
				}
			}
		}
		liveConsts := map[string]*ast.ConstDecl{}
		liveValues := map[string]ast.Decl{} // consts and globals
		for _, sd := range r.decls {
			switch n := sd.Decl.(type) {
			case *ast.ConstDecl:
				liveConsts[retainedLineKey(n.Pos())+"|"+n.Name] = n
				liveValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			case *ast.GlobalDecl:
				liveValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			}
		}
		scratchConsts := map[string]*ast.ConstDecl{}
		scratchValues := map[string]ast.Decl{} // consts and globals
		scratchStructs := map[string]*ast.StructDecl{}
		for _, d := range retainedWalkDecls(scratch) {
			switch n := d.(type) {
			case *ast.GlobalDecl:
				scratchValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			case *ast.ConstDecl:
				scratchConsts[retainedLineKey(n.Pos())+"|"+n.Name] = n
				scratchValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			case *ast.StructDecl:
				scratchStructs[retainedLineKey(n.Pos())+"|"+n.Name] = n
			}
		}
		removedTargets := map[Type]bool{}
		if len(removedStructs) != 0 {
			liveStructs := map[string]scopedDecl{}
			for _, sd := range r.decls {
				if st, ok := sd.Decl.(*ast.StructDecl); ok {
					liveStructs[retainedLineKey(st.Pos())+"|"+st.Name] = sd
				}
			}
			for _, ost := range removedStructs {
				sd, ok := liveStructs[retainedLineKey(ost.Pos())+"|"+ost.Name]
				st, _ := a.namedTypes[joinQualifiedName(sd.Namespace, ost.Name)].(*StructType)
				if !ok || st == nil || st.Builtin || len(st.TypeParams) != 0 {
					return retainedValueDependencyPlan{}, fmt.Errorf("removed struct %s not found in the analysis", ost.Name)
				}
				line := sd.Decl.Pos()
				for _, d := range a.diagnostics {
					if d.Pos.File == line.File && d.Pos.Line == line.Line {
						return retainedValueDependencyPlan{}, fmt.Errorf("removed struct %s has diagnostics", ost.Name)
					}
				}
				removedTargets[st] = true
				removedStructLive = append(removedStructLive, sd)
			}
		}
		for _, sd := range addedStructs {
			prepared := scratchStructs[retainedLineKey(sd.Pos())+"|"+sd.Name]
			if prepared == nil {
				return retainedValueDependencyPlan{}, fmt.Errorf("prepared form of added struct %s not found", sd.Name)
			}
			addedStructPrepared = append(addedStructPrepared, prepared)
		}
		for _, e := range constPairs {
			live := liveConsts[retainedLineKey(e[0].Pos())+"|"+e[0].Name]
			prepared := scratchConsts[retainedLineKey(e[1].Pos())+"|"+e[1].Name]
			if live == nil || prepared == nil {
				return retainedValueDependencyPlan{}, fmt.Errorf("const %s not found in the analysis", e[0].Name)
			}
			line := live.Pos()
			for _, d := range a.diagnostics {
				if d.Pos.File == line.File && d.Pos.Line == line.Line {
					return retainedValueDependencyPlan{}, fmt.Errorf("const %s has diagnostics", e[0].Name)
				}
			}
			constLive = append(constLive, [2]*ast.ConstDecl{live, prepared})
		}
		for _, cd := range removedConsts {
			name := retainedValueDeclName(cd)
			live := liveValues[retainedLineKey(cd.Pos())+"|"+name]
			if live == nil {
				return retainedValueDependencyPlan{}, fmt.Errorf("removed %s not found in the analysis", name)
			}
			line := live.Pos()
			for _, d := range a.diagnostics {
				if d.Pos.File == line.File && d.Pos.Line == line.Line {
					return retainedValueDependencyPlan{}, fmt.Errorf("removed %s has diagnostics", name)
				}
			}
			removedConstLive = append(removedConstLive, live)
		}
		for _, d := range addedConsts {
			prepared := scratchValues[retainedLineKey(d.Pos())+"|"+retainedValueDeclName(d)]
			if prepared == nil {
				return retainedValueDependencyPlan{}, fmt.Errorf("prepared form of added %s not found", retainedValueDeclName(d))
			}
			addedConstPrepared = append(addedConstPrepared, prepared)
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		// A struct is named by type expressions and literals, which the identifier scan
		// does not see, so the edited function's text is matched too.
		spanOf := map[ast.Decl]string{}
		if len(addedStructs)+len(removedStructs) != 0 {
			for j, d := range newDecls {
				spanOf[d] = newSpans[j]
			}
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			current := pairedNew[fn]
			if current == nil {
				current = fn
			}
			if !contractUsers[fn.Name] && !mentionsCurrent(current, names) && (len(addedStructs)+len(removedStructs) == 0 || !retainedNameTextMatch(spanOf[pairedNew[fn]], names, pat)) &&
				(len(removedTargets) == 0 || !a.retainedUsesTypes(fn, removedTargets, names, map[Type]bool{})) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return retainedValueDependencyPlan{}, fmt.Errorf("%s uses an added or edited name but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
		}
		r.LastStats.ConstUsers = len(splices)
	}
	return retainedValueDependencyPlan{constLive, addedConstPrepared, removedConstLive, addedStructPrepared, removedStructLive, splices}, nil
}
