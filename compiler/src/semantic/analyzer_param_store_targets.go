package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// Interprocedural store-TARGET summaries (docs/91 S4 W5). Where computeParamRetention answers "is
// param i retained at all?" (a bool), this answers the finer "param i is stored INTO sibling param j"
// — the precise fact a caller needs to decide whether passing a region-tainted by-value struct there
// is a use-after-free. paramStoreTargets[fn][i] is the set of OTHER parameter indices that param i may
// be stored into, directly (`dst.push(v)`, `dst <- v`, `dst.field <- v`) or relayed through a resolved
// callee that stores the corresponding argument into another argument.
//
// Purely STRUCTURAL: it walks the AST and uses only the resolved FuncType param types (paramStorageType,
// available after collectValueSymbols) to skip pointer-free params — no dependency on exprTypes, so it
// can run BEFORE body analysis, while the call-site check that consumes it (checkInterprocStoreEscape)
// runs DURING analysis when the interior-taint side-table is live. Monotone fixpoint over the call
// graph (targets only grow), so it terminates. Sibling-param targets only: a callee that instead
// RETURNS the value, or stores it into a GLOBAL, is covered by the existing return / stored-container
// escape checks, so this stays precise (zero over-rejection of same-region passes).
func (a *Analyzer) computeParamStoreTargets(funcs []*ast.FuncDecl) map[*ast.FuncDecl][]map[int]bool {
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
	locals := map[*ast.FuncDecl]map[string]bool{}
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		locals[fn] = collectLocalDeclNames(fn.Body)
	}
	for {
		changed := false
		for _, fn := range funcs {
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
				if locals[fn][tgtName] {
					return // stored into a callee-local: dies with the callee, cannot dangle a caller value
				}
				markTarget(srcName, storeTargetGlobal)
			}
			markGlobal := func(srcName string) { markTarget(srcName, storeTargetGlobal) }
			a.walkParamStores(fn.Body, pidx, out, addStore, markGlobal)
		}
		if !changed {
			break
		}
	}
	return out
}

// storeTargetGlobal is the sentinel target index meaning "stored into program-lifetime storage" (a
// global/const/perm container, or relayed into a callee param that itself escapes there). It outlives
// every local region, so a region-tainted by-value argument stored there always dangles.
const storeTargetGlobal = -1

// collectLocalDeclNames returns the set of names introduced by `let`/typed var declarations anywhere in
// a function body, used to tell a callee-local store target (safe — dies with the callee) from a
// global/captured one (program-lifetime — a region-tainted value stored there dangles).
func collectLocalDeclNames(body []ast.Stmt) map[string]bool {
	names := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if d, ok := v.Interface().(*ast.VarDeclStmt); ok && d != nil {
				names[d.Name] = true
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, s := range body {
		walk(reflect.ValueOf(s))
	}
	return names
}

// walkParamStores finds, in a function body, the param→param store relationships:
//   - a container-mutation `tgtParam.push(srcParam)` / put / add / insert / append / push_{front,back}
//   - a reassignment `tgtParam <- srcParam`, or a field store `tgtParam.field <- srcParam`
//   - a relayed store: a resolved callee that maps its position-p param into its position-q param,
//     where the call passes srcParam at p and tgtParam at q.
func (a *Analyzer) walkParamStores(body []ast.Stmt, pidx map[string]int, out map[*ast.FuncDecl][]map[int]bool, addStore func(src, tgt string), markGlobal func(src string)) {
	add := addStore
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.CallExpr:
				// Direct container-mutation store: tgt.push(src)
				if fe, ok := n.Func.(*ast.FieldExpr); ok && fe != nil && retainingMethods[fe.Field] {
					if tgt := paramRootName(fe.Object); tgt != "" {
						for _, arg := range n.Args {
							if src, ok := aliasSourceName(arg); ok {
								add(src, tgt)
							}
						}
					}
				}
				// Relayed store through a resolved callee.
				if callee, ok := a.resolveCalleeFuncDecl(n); ok && callee != nil {
					if ctargets, ok := out[callee]; ok {
						a.relayParamStores(n, ctargets, addStore, markGlobal)
					}
				}
			case *ast.AssignStmt:
				if src, ok := aliasSourceName(n.Value); ok {
					if tgt := paramRootName(n.Target); tgt != "" {
						add(src, tgt)
					}
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, s := range body {
		walk(reflect.ValueOf(s))
	}
}

// relayParamStores propagates a callee's param→param store relationships back to the caller's params:
// if the callee stores its position-p param into its position-q param, and the call passes the caller's
// param `src` at p and the caller's param `tgt` at q, then the caller stores src into tgt.
func (a *Analyzer) relayParamStores(call *ast.CallExpr, calleeTargets []map[int]bool, addStore func(src, tgt string), markGlobal func(src string)) {
	argName := func(pos int) (string, bool) {
		if pos < 0 || pos >= len(call.Args) {
			return "", false
		}
		return aliasSourceName(call.Args[pos])
	}
	for p, tset := range calleeTargets {
		if len(tset) == 0 {
			continue
		}
		srcName, ok := argName(p)
		if !ok {
			continue
		}
		for q := range tset {
			if q == storeTargetGlobal {
				// The callee stores its param p into program-lifetime storage; the argument we pass
				// at p therefore escapes there too — regardless of what arg sits at any position.
				markGlobal(srcName)
				continue
			}
			// The callee stores p into its sibling param q; re-classify the caller's arg at q in the
			// caller's own context (it may be a caller param, a caller local, or a caller global).
			if tgtName, ok := argName(q); ok {
				addStore(srcName, tgtName)
			}
		}
	}
}

// paramRootName returns the base identifier name of a store TARGET lvalue (an ident, or a field/index
// path rooted at an ident), or "" when the target is not a plain-rooted lvalue.
func paramRootName(expr ast.Expr) string {
	if root := rootIdentExpr(expr); root != nil {
		return root.Name
	}
	return ""
}
