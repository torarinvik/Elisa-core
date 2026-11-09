package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"fmt"
	"strings"
)

type retainedEnumDependencyPlan struct {
	constEnumEdits            []retainedConstEnumEdit
	enumEdits, hierarchyEdits []retainedEnumEdit
	hierarchyMembers          []scopedDecl
	splices                   []retainedSplice
}

// retainedEnumDependencies plans body reanalysis for changed enum families.
func (r *RetainedAnalysis) retainedEnumDependencies(a *Analyzer, comparison retainedComparison, scratch *ast.File, newDecls []ast.Decl, removedLive []*ast.FuncDecl, splices []retainedSplice, pairedNew map[*ast.FuncDecl]*ast.FuncDecl, newTexts map[*ast.FuncDecl]funcComparisonText, spanOf func(ast.Decl) (string, string)) (retainedEnumDependencyPlan, error) {
	constEnumPairs, enumPairs, hierarchyPairs := comparison.constEnumPairs, comparison.enumPairs, comparison.hierarchyPairs
	newSpans := comparison.newSpans
	// Constant-enum values live in the type's member table. Keep that type's identity,
	// but reject declaration-phase value dependencies before mutating it.
	var constEnumEdits []retainedConstEnumEdit
	if len(constEnumPairs) != 0 {
		var why string
		if constEnumEdits, why = r.findLiveConstEnumEdits(constEnumPairs, scratch, newDecls, newSpans); why != "" {
			return retainedEnumDependencyPlan{}, fmt.Errorf("%s", why)
		}
		targets, names, members := map[Type]bool{}, map[string]bool{}, map[string]bool{}
		for _, e := range constEnumEdits {
			targets[e.et] = true
			names[e.live.Name] = true
			for _, d := range []*ast.ConstEnumDecl{e.live, e.edit} {
				for _, m := range d.Members {
					members[m.Name] = true
				}
			}
		}
		for name, t := range a.namedTypes {
			if targets[t] {
				names[name[strings.LastIndex(name, ".")+1:]] = true
			}
		}
		a.widenLayoutTargets(targets)
		// Layout queries name their operand without necessarily recording its type
		// as an expression type. Include containing records and their aliases.
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
		memo := map[Type]bool{}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			_, sourceText := spanOf(fn)
			if !retainedNameTextMatch(sourceText, names, namePattern) && !retainedConstEnumShorthand(pairedNew[fn], members) && !a.retainedUsesTypes(fn, targets, names, memo) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return retainedEnumDependencyPlan{}, fmt.Errorf("%s uses an edited constant enum but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
		}
		r.LastStats.EnumUsers = len(splices)
	}
	// An edited enum: every function whose types or text reach it is re-analyzed against the
	// new variant table.
	var enumEdits []retainedEnumEdit
	if len(enumPairs) != 0 {
		var why string
		if enumEdits, why = r.findLiveEnumEdits(enumPairs, scratch); why != "" {
			return retainedEnumDependencyPlan{}, fmt.Errorf("%s", why)
		}
		targets, names := map[Type]bool{}, map[string]bool{}
		for k, e := range enumEdits {
			targets[e.et] = true
			names[e.live.Name] = true
			for _, v := range enumPairs[k][0].Variants {
				names[v.Name] = true
			}
			for _, v := range enumPairs[k][1].Variants {
				names[v.Name] = true
			}
		}
		a.widenLayoutTargets(targets)
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		memo := map[Type]bool{}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || !a.retainedUsesTypes(fn, targets, names, memo) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return retainedEnumDependencyPlan{}, fmt.Errorf("%s uses an edited enum but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
		}
		r.LastStats.EnumUsers = len(splices)
	}
	// An edited refinement of an enum hierarchy: the hierarchy is recomputed, and the functions
	// whose text can observe the edit are re-analyzed.
	var hierarchyEdits []retainedEnumEdit
	var hierarchyMembers []scopedDecl
	if len(hierarchyPairs) != 0 {
		var why string
		if hierarchyEdits, hierarchyMembers, why = r.findLiveHierarchyEdits(hierarchyPairs, scratch); why != "" {
			return retainedEnumDependencyPlan{}, fmt.Errorf("%s", why)
		}
		edited, members := map[string]bool{}, map[string]bool{}
		editedTypes := map[Type]bool{}
		for _, e := range hierarchyEdits {
			editedTypes[e.et] = true
		}
		for name, t := range a.namedTypes {
			// The type's own name and any alias of it, as a bare token.
			short := name[strings.LastIndex(name, ".")+1:]
			if editedTypes[t] {
				edited[short] = true
			}
			if et, ok := t.(*EnumType); ok && et != nil {
				for _, sd := range hierarchyMembers {
					if sd.Decl == et.Decl {
						members[short] = true
					}
				}
			}
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			spliced[fn] = true
		}
		users := 0
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			oldSpan, newSpan := spanOf(fn)
			if pairedNew[fn] == nil {
				oldSpan = unparse.FormatDecl(fn)
			}
			if !retainedHierarchyUsers(oldSpan, edited, members) && !retainedHierarchyUsers(newSpan, edited, members) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return retainedEnumDependencyPlan{}, fmt.Errorf("%s uses an edited enum but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, retainedSplice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
			users++
			if oldSpan == newSpan {
				// Unchanged text re-analyzed for its match coverage: a variant it never
				// names cannot change how its body forwards borrows.
				r.rbSameFlow[fn] = true
			}
		}
		r.LastStats.EnumUsers += users
	}
	return retainedEnumDependencyPlan{constEnumEdits, enumEdits, hierarchyEdits, hierarchyMembers, splices}, nil
}
