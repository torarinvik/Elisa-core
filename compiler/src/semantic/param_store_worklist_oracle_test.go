package semantic

import "elisacore/src/ast"

// Deliberately simple all-functions fixed point is an independent scheduling
// oracle. It shares transfer rules so these tests qualify propagation, not alias
// analysis soundness.
func (a *Analyzer) computeParamStoreTargetsFixedPoint(funcs []*ast.FuncDecl) map[*ast.FuncDecl][]map[int]bool {
	out := map[*ast.FuncDecl][]map[int]bool{}
	params := map[*ast.FuncDecl][]ast.ParamDecl{}
	idx := map[*ast.FuncDecl]map[string]int{}
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		p := a.expandedFuncDeclParams(fn)
		params[fn] = p
		ts := make([]map[int]bool, len(p))
		for i := range ts {
			ts[i] = map[int]bool{}
		}
		out[fn] = ts
		m := map[string]int{}
		for i := range p {
			m[p[i].Name] = i
		}
		idx[fn] = m
	}
	// Every emitted relationship passes markTarget's source-storage gate. If
	// no expanded parameter can carry storage, no body syntax or callee summary
	// can add an edge for this function. Derive eligibility from current types
	// each analysis; never replay it across type changes.
	active := make([]*ast.FuncDecl, 0, len(funcs))
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		for i := range params[fn] {
			if typeCarriesRegionStorage(a.paramStorageType(fn, i)) {
				active = append(active, fn)
				break
			}
		}
	}
	for {
		changed := false
		for _, fn := range active {
			pidx := idx[fn]
			// markTarget records that source param `srcName` is stored into target index `ti`
			// (a sibling param index, or the sentinel storeTargetGlobal for program-lifetime storage).
			markTarget := func(srcName string, ti int) {
				si, sok := pidx[srcName]
				if !sok || out[fn][si][ti] {
					return
				}
				if !typeCarriesRegionStorage(a.paramStorageType(fn, si)) {
					return
				}
				out[fn][si][ti] = true
				changed = true
			}
			add := func(srcName, tgtName string) {
				if ti, ok := pidx[tgtName]; ok {
					if si, sok := pidx[srcName]; !sok || si == ti {
						return
					}
					markTarget(srcName, ti)
				}
			}
			// addStore classifies a store of `srcName` into the lvalue rooted at `tgtName`: a sibling
			// param is a precise region-comparable target; a body-local dies with the callee (safe,
			// ignored); anything else is a program-lifetime global/captured target — sound to reject.
			addStore := func(srcName, tgtName string) {
				if _, isParam := pidx[tgtName]; isParam {
					add(srcName, tgtName)
					return
				}
				markTarget(srcName, storeTargetGlobal)
			}
			markGlobal := func(srcName string) { markTarget(srcName, storeTargetGlobal) }
			a.walkParamStores(fn.Body, pidx, out, addStore, markGlobal, nil)
		}
		if !changed {
			break
		}
	}
	return out
}
