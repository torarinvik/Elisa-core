package semantic

import "elisacore/src/ast"

type reindexedPermissionFunctions struct {
	fresh map[*ast.FuncDecl]*FuncType
	reset []*ast.FuncDecl
}

// Reindex only verified surviving equations. A removed row's surviving readers
// must all have fresh bodies; otherwise a cached binding could still refer to
// the old callable. New equations must own distinct, genuinely new types.
// Validation deliberately runs in full after this more general graph change.
func (p *retainedPermissions) reindexedFunctions(a *Analyzer, before []scopedDecl, beforeTypes []*FuncType, knownTypes map[*FuncType]bool, current, changed []scopedDecl) (*reindexedPermissionFunctions, bool) {
	if p.readers == nil || p.rows == nil || p.initial == nil || knownTypes == nil || len(beforeTypes) != len(before) {
		return nil, false
	}
	oldIndex := map[ast.Decl]int{}
	oldTypes := map[*FuncType]bool{}
	for i, sd := range before {
		if _, duplicate := oldIndex[sd.Decl]; duplicate {
			return nil, false
		}
		oldIndex[sd.Decl] = i
		if ft := beforeTypes[i]; ft != nil {
			if oldTypes[ft] {
				return nil, false
			}
			oldTypes[ft] = true
		}
	}
	edited := map[ast.Decl]bool{}
	for _, sd := range changed {
		edited[sd.Decl] = true
	}
	index := map[ast.Decl]int{}
	fresh := map[*ast.FuncDecl]*FuncType{}
	freshTypes := map[*FuncType]bool{}
	for i, sd := range current {
		fn, ok := sd.Decl.(*ast.FuncDecl)
		if !ok {
			return nil, false
		}
		if _, duplicate := index[fn]; duplicate {
			return nil, false
		}
		index[fn] = i
		ft := permissionDeclType(a, sd)
		if old, survives := oldIndex[fn]; survives {
			previous := before[old]
			if sd.Namespace != previous.Namespace || !sameStringSlice(sd.Usings, previous.Usings) || ft != beforeTypes[old] {
				return nil, false
			}
			if ft != nil {
				if _, known := p.rows[ft]; !known {
					return nil, false
				}
			}
			continue
		}
		if !edited[fn] || ft == nil || freshTypes[ft] || knownTypes[ft] || len(p.readers[ft]) != 0 {
			return nil, false
		}
		if _, known := p.rows[ft]; known {
			return nil, false
		}
		fresh[fn], freshTypes[ft] = ft, true
	}
	var roots []int
	for i, sd := range before {
		if _, survives := index[sd.Decl]; survives {
			continue
		}
		for reader := range p.readers[beforeTypes[i]] {
			if reader < 0 || reader >= len(before) {
				return nil, false
			}
			readerDecl := before[reader].Decl
			if _, survives := index[readerDecl]; survives && !edited[readerDecl] {
				return nil, false
			}
			roots = append(roots, reader)
		}
	}
	// Replacing a callable can lose an effect without changing any surviving
	// row on the first evaluation: a recursive caller can keep the old effect
	// alive through its peers. Restart the entire old reader cone from declared
	// rows, not the old solution, and enqueue every surviving equation in it.
	cone := map[int]bool{}
	var reset []*ast.FuncDecl
	for len(roots) != 0 {
		i := roots[len(roots)-1]
		roots = roots[:len(roots)-1]
		if i < 0 || i >= len(before) {
			return nil, false
		}
		if cone[i] {
			continue
		}
		cone[i] = true
		ft := beforeTypes[i]
		if _, survives := index[before[i].Decl]; survives && ft != nil {
			if _, known := p.declared[ft]; !known {
				return nil, false
			}
			reset = append(reset, before[i].Decl.(*ast.FuncDecl))
		}
		for reader := range p.readers[ft] {
			roots = append(roots, reader)
		}
	}
	readers := map[*FuncType]map[int]bool{}
	for ft, previous := range p.readers {
		for old := range previous {
			if old < 0 || old >= len(before) {
				return nil, false
			}
			if next, survives := index[before[old].Decl]; survives {
				if readers[ft] == nil {
					readers[ft] = map[int]bool{}
				}
				readers[ft][next] = true
			}
		}
	}
	// Commit only after every guard has passed. Declaration/initial rows stay
	// available to body preparation; the marked reader cone restarts at its
	// declarations. Missing diagnostic entries force full validation.
	for i, sd := range before {
		if _, survives := index[sd.Decl]; !survives {
			delete(p.rows, beforeTypes[i])
		}
	}
	p.readers, p.diags = readers, nil
	return &reindexedPermissionFunctions{fresh: fresh, reset: reset}, true
}
