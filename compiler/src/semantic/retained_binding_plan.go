package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

type retainedBindingPlan struct {
	newOf              func(ast.Decl) ast.Decl
	spanOf             func(ast.Decl) (string, string)
	usingChanges       []retainedUsingChange
	functionKeys       map[*ast.FuncDecl]string
	reanalyzedBodyKeys map[string]bool
}

// planRetainedBindings maps declarations and identifies bodies affected by using changes.
func (r *RetainedAnalysis) planRetainedBindings(file *ast.File, oldDecls, newDecls []ast.Decl, paired retainedDeclarationPairing, comparison retainedComparison, added []*ast.FuncDecl, splices []retainedSplice) (retainedBindingPlan, error) {
	pairs := paired.pairs
	oldSpans, newSpans := comparison.oldSpans, comparison.newSpans
	usingsAdded, usingsRemoved := paired.usingsAdded, paired.usingsRemoved
	addedConsts, addedStructs := paired.addedConsts, paired.addedStructs
	// Build the declaration pairing and using-dependent reanalysis plan before
	// comparing resolved type-name rows. Those planned owners are the only
	// unchanged bodies whose rows a using edit may legitimately change.
	keyOf := func(d ast.Decl) string { return fmt.Sprintf("%s|%T", retainedLineKey(d.Pos()), d) }
	newOfOld := make(map[string]int, len(pairs))
	for _, pair := range pairs {
		newOfOld[keyOf(oldDecls[pair[0]])] = pair[0]<<32 | pair[1]
	}
	lookup := func(d ast.Decl) (int, int, bool) {
		v, ok := newOfOld[keyOf(d)]
		return v >> 32, v & (1<<32 - 1), ok
	}
	newOf := func(d ast.Decl) ast.Decl {
		if _, j, ok := lookup(d); ok {
			return newDecls[j]
		}
		return nil
	}
	spanOf := func(d ast.Decl) (string, string) {
		if i, j, ok := lookup(d); ok {
			return oldSpans[i], newSpans[j]
		}
		return "", ""
	}
	var usingChanges []retainedUsingChange
	if usingsAdded+usingsRemoved != 0 {
		var extra []string
		for _, nf := range added {
			extra = append(extra, nf.Name)
		}
		for _, d := range addedConsts {
			extra = append(extra, retainedValueDeclName(d))
		}
		for _, st := range addedStructs {
			extra = append(extra, st.Name)
		}
		var why string
		if usingChanges, why = r.findUsingChanges(file, newOf, spanOf, extra); why != "" {
			return retainedBindingPlan{}, fmt.Errorf("%s", why)
		}
	}
	functionKeysBeforeReplay := retainedPreBodyFunctionKeys(r.decls)
	reanalyzedBodyKeys := make(map[string]bool, len(splices))
	for _, splice := range splices {
		if key := functionKeysBeforeReplay[splice.live]; key != "" {
			reanalyzedBodyKeys[key] = true
		}
	}
	for _, change := range usingChanges {
		fn, ok := r.decls[change.index].Decl.(*ast.FuncDecl)
		if !ok || !change.mention {
			continue
		}
		if key := functionKeysBeforeReplay[fn]; key != "" {
			reanalyzedBodyKeys[key] = true
		}
	}
	return retainedBindingPlan{newOf, spanOf, usingChanges, functionKeysBeforeReplay, reanalyzedBodyKeys}, nil
}
