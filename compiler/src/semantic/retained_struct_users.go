package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"strings"
)

// retainedStructUsers schedules unchanged functions affected by edited struct layouts.
func (r *RetainedAnalysis) retainedStructUsers(a *Analyzer, structPairs [][2]*ast.StructDecl, removedLive []*ast.FuncDecl, splices []retainedSplice, pairedNew map[*ast.FuncDecl]*ast.FuncDecl, newTexts map[*ast.FuncDecl]funcComparisonText, spanOf func(ast.Decl) (string, string)) ([]retainedStructEdit, map[*ast.FuncDecl]bool, []retainedSplice, error) {
	// An edited struct: every function whose types or text reach it is re-analyzed from its
	// prepared body against the new field table.
	var structEdits []retainedStructEdit
	var structOnly map[*ast.FuncDecl]bool // struct users spliced only for the struct edit
	if len(structPairs) != 0 {
		var why string
		if structEdits, why = r.findLiveStructEdits(structPairs); why != "" {
			return nil, nil, nil, fmt.Errorf("%s", why)
		}
		targets, names := map[Type]bool{}, map[string]bool{}
		for _, e := range structEdits {
			targets[e.st] = true
			names[e.live.Name] = true
			for _, field := range e.st.Fields {
				if group, ok := field.Type.(*BitGroupType); ok {
					targets[group] = true
				}
			}
		}
		additive := true
		for _, e := range structEdits {
			if !a.retainedAdditiveScalarEdit(e.live, e.edit) {
				additive = false
				break
			}
		}
		a.widenLayoutTargets(targets)
		for name, typ := range a.namedTypes {
			if targets[typ] {
				names[name[strings.LastIndex(name, ".")+1:]] = true
			}
		}
		namePattern := retainedNamesPattern(names)
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			// Removed, or re-declared with a new signature: splicing the new body under
			// the old header would check it against the old parameter list.
			spliced[fn] = true
		}
		memo := map[Type]bool{}
		var stores map[string]bool
		if additive {
			stores = a.retainedPackedStoresReaching(targets, memo)
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			oldText, newText := spanOf(fn)
			namedInSource := retainedNameTextMatch(oldText, names, namePattern)
			if !namedInSource && newText != oldText {
				namedInSource = retainedNameTextMatch(newText, names, namePattern)
			}
			if !namedInSource && !a.retainedUsesTypes(fn, targets, names, memo) {
				continue
			}
			if additive && newText == oldText && !a.retainedObservesFieldSet(fn, targets, stores, memo) {
				r.LastStats.StructUnobserved++
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil {
				return nil, nil, nil, fmt.Errorf("%s uses an edited struct but has no edited counterpart", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
			if structOnly == nil {
				structOnly = map[*ast.FuncDecl]bool{}
			}
			structOnly[fn] = true
		}
		r.LastStats.StructUsers = len(splices)
	}
	return structEdits, structOnly, splices, nil
}
