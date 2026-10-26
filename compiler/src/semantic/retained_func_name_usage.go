package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// rebuildRetainedFuncNameUsageIndex stores the value-position names contributed
// by each scoped declaration. Each declaration gets its own visit memo: AST
// nodes can be shared across owners, and removing one owner must not erase a
// name still contributed by another owner.
func (r *RetainedAnalysis) rebuildRetainedFuncNameUsageIndex(decls []scopedDecl) bool {
	if r == nil {
		return false
	}
	contributions := make(map[ast.Decl]map[string]bool, len(decls))
	ownerCounts := make(map[ast.Decl]int, len(decls))
	functionOwners := make(map[ast.Decl]string)
	valueCounts := map[string]int{}
	functionCounts := map[string]int{}
	total := 0
	for _, scoped := range decls {
		decl := scoped.Decl
		if decl == nil {
			return false
		}
		names, known := contributions[decl]
		if !known {
			names = map[string]bool{}
			scanValueUsedIdentsWithMemo(reflect.ValueOf(decl), names, newScanValueUsedIdentMemo())
			contributions[decl] = names
			if fn, ok := decl.(*ast.FuncDecl); ok && fn != nil {
				functionOwners[decl] = fn.Name
			}
		}
		ownerCounts[decl]++
		for name := range names {
			valueCounts[name]++
		}
		if name, ok := functionOwners[decl]; ok {
			functionCounts[name]++
		}
		total++
	}
	r.funcNameUsageContributions = contributions
	r.funcNameUsageOwnerCounts = ownerCounts
	r.funcNameUsageFunctionOwners = functionOwners
	r.funcNameUsageValueCounts = valueCounts
	r.funcNameUsageFunctionCounts = functionCounts
	r.funcNameUsageOwnerTotal = total
	r.funcNameUsageReady = true
	return true
}

// funcNameUsageOwnersMatch validates the cheap declaration graph identity
// before applying an owner-local update. It does not inspect any AST children.
func (r *RetainedAnalysis) funcNameUsageOwnersMatch(decls []scopedDecl) bool {
	if r == nil || !r.funcNameUsageReady || len(decls) != r.funcNameUsageOwnerTotal || len(r.funcNameUsageOwnerCounts) == 0 && len(decls) != 0 {
		return false
	}
	counts := make(map[ast.Decl]int, len(r.funcNameUsageOwnerCounts))
	for _, scoped := range decls {
		if scoped.Decl == nil {
			return false
		}
		counts[scoped.Decl]++
	}
	if len(counts) != len(r.funcNameUsageOwnerCounts) {
		return false
	}
	for decl, count := range counts {
		if r.funcNameUsageOwnerCounts[decl] != count {
			return false
		}
	}
	return true
}

func (r *RetainedAnalysis) ensureFuncNameUsageIndex(decls []scopedDecl) bool {
	if r.funcNameUsageOwnersMatch(decls) {
		return true
	}
	return r.rebuildRetainedFuncNameUsageIndex(decls)
}

func adjustUsageCount(counts map[string]int, name string, delta int) bool {
	if delta == 0 {
		return true
	}
	next := counts[name] + delta
	if next < 0 {
		return false
	}
	if next == 0 {
		delete(counts, name)
	} else {
		counts[name] = next
	}
	return true
}

func (r *RetainedAnalysis) replaceFuncNameUsageOwner(decl ast.Decl) bool {
	if r == nil || decl == nil {
		return false
	}
	weight, ok := r.funcNameUsageOwnerCounts[decl]
	oldNames, namesKnown := r.funcNameUsageContributions[decl]
	if !ok || weight <= 0 || !namesKnown {
		return false
	}
	for name := range oldNames {
		if !adjustUsageCount(r.funcNameUsageValueCounts, name, -weight) {
			return false
		}
	}
	if oldName, functionOwner := r.funcNameUsageFunctionOwners[decl]; functionOwner {
		if !adjustUsageCount(r.funcNameUsageFunctionCounts, oldName, -weight) {
			return false
		}
	}
	newNames := map[string]bool{}
	scanValueUsedIdentsWithMemo(reflect.ValueOf(decl), newNames, newScanValueUsedIdentMemo())
	for name := range newNames {
		if !adjustUsageCount(r.funcNameUsageValueCounts, name, weight) {
			return false
		}
	}
	r.funcNameUsageContributions[decl] = newNames
	if fn, isFunction := decl.(*ast.FuncDecl); isFunction && fn != nil {
		r.funcNameUsageFunctionOwners[decl] = fn.Name
		if !adjustUsageCount(r.funcNameUsageFunctionCounts, fn.Name, weight) {
			return false
		}
	} else {
		delete(r.funcNameUsageFunctionOwners, decl)
	}
	return true
}

// funcNameUsageForPost consumes the precise retained update plan installed by
// UpdatePrepared. Unknown or structurally changed owner sets rebuild from the
// current declarations. Without an update plan, use the original whole-program
// scanner so ad hoc post-phase callers cannot reuse stale facts.
func (r *RetainedAnalysis) funcNameUsageForPost(decls []scopedDecl) (map[string]int, map[string]bool) {
	if r == nil || !r.funcNameUsagePending {
		return scanFuncNameUsage(decls)
	}
	defer func() {
		r.funcNameUsagePending = false
		r.funcNameUsageRebuild = false
		r.funcNameUsageChangedOwners = nil
	}()

	rebuild := r.funcNameUsageRebuild || !r.funcNameUsageOwnersMatch(decls)
	if r.funcNameUsageTracking {
		if r.funcNameUsageUnknownTouched {
			rebuild = true
		}
		if !rebuild && len(r.funcNameUsageTouchedOwners) != 0 {
			seen := make(map[ast.Decl]bool, len(r.funcNameUsageChangedOwners)+len(r.funcNameUsageTouchedOwners))
			for _, decl := range r.funcNameUsageChangedOwners {
				seen[decl] = true
			}
			for decl := range r.funcNameUsageTouchedOwners {
				if decl != nil && !seen[decl] {
					r.funcNameUsageChangedOwners = append(r.funcNameUsageChangedOwners, decl)
					seen[decl] = true
				}
			}
		}
	}
	if !rebuild {
		seen := make(map[ast.Decl]bool, len(r.funcNameUsageChangedOwners))
		for _, decl := range r.funcNameUsageChangedOwners {
			if decl == nil || seen[decl] {
				continue
			}
			seen[decl] = true
			if !r.replaceFuncNameUsageOwner(decl) {
				rebuild = true
				break
			}
		}
	}
	if rebuild && !r.rebuildRetainedFuncNameUsageIndex(decls) {
		r.funcNameUsageReady = false
		return scanFuncNameUsage(decls)
	}
	valueNames := make(map[string]bool, len(r.funcNameUsageValueCounts))
	for name, count := range r.funcNameUsageValueCounts {
		if count > 0 {
			valueNames[name] = true
		}
	}
	return r.funcNameUsageFunctionCounts, valueNames
}

func retainedFuncNameUsageFunctionOwnerMap(decls []scopedDecl) map[*ast.FuncDecl]ast.Decl {
	owners := make(map[*ast.FuncDecl]ast.Decl)
	for _, scoped := range decls {
		switch decl := scoped.Decl.(type) {
		case *ast.FuncDecl:
			if decl != nil {
				owners[decl] = decl
			}
		case *ast.ImplDecl:
			for _, member := range decl.Members {
				if fn, ok := member.(*ast.FuncDecl); ok && fn != nil {
					owners[fn] = decl
				}
			}
		}
	}
	return owners
}

func (r *RetainedAnalysis) noteFuncNameUsageFunctionAnalysis(fn *ast.FuncDecl) {
	if r == nil || !r.funcNameUsageTracking || fn == nil {
		return
	}
	owner, ok := r.funcNameUsageOwnerLookup[fn]
	if !ok || owner == nil {
		r.funcNameUsageUnknownTouched = true
		return
	}
	if r.funcNameUsageTouchedOwners == nil {
		r.funcNameUsageTouchedOwners = map[ast.Decl]bool{}
	}
	r.funcNameUsageTouchedOwners[owner] = true
}

func (r *RetainedAnalysis) clearFuncNameUsageUpdateTracking() {
	if r == nil {
		return
	}
	r.funcNameUsageTracking = false
	r.funcNameUsageOwnerLookup = nil
	r.funcNameUsageTouchedOwners = nil
	r.funcNameUsageUnknownTouched = false
}

func (r *RetainedAnalysis) hasFuncNameUsageTransientState() bool {
	return r != nil && (r.funcNameUsagePending || r.funcNameUsageRebuild || r.funcNameUsageChangedOwners != nil || r.funcNameUsageTracking || r.funcNameUsageOwnerLookup != nil || r.funcNameUsageTouchedOwners != nil || r.funcNameUsageUnknownTouched)
}
