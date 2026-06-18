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
	for {
		changed := false
		for _, fn := range funcs {
			pidx := idx[fn]
			add := func(srcName, tgtName string) {
				si, sok := pidx[srcName]
				ti, tok := pidx[tgtName]
				if !sok || !tok || si == ti || out[fn][si][ti] {
					return
				}
				// Only a source param that can carry region storage can dangle when stored.
				if !typeCarriesRegionStorage(a.paramStorageType(fn, si)) {
					return
				}
				out[fn][si][ti] = true
				changed = true
			}
			a.walkParamStores(fn.Body, pidx, out, add)
		}
		if !changed {
			break
		}
	}
	return out
}

// walkParamStores finds, in a function body, the param→param store relationships:
//   - a container-mutation `tgtParam.push(srcParam)` / put / add / insert / append / push_{front,back}
//   - a reassignment `tgtParam <- srcParam`, or a field store `tgtParam.field <- srcParam`
//   - a relayed store: a resolved callee that maps its position-p param into its position-q param,
//     where the call passes srcParam at p and tgtParam at q.
func (a *Analyzer) walkParamStores(body []ast.Stmt, pidx map[string]int, out map[*ast.FuncDecl][]map[int]bool, add func(src, tgt string)) {
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
						a.relayParamStores(n, ctargets, add)
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
func (a *Analyzer) relayParamStores(call *ast.CallExpr, calleeTargets []map[int]bool, add func(src, tgt string)) {
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
			if tgtName, ok := argName(q); ok {
				add(srcName, tgtName)
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
