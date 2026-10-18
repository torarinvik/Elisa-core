package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"sort"
)

// Filled locals: body locals that start fresh and only ever receive, from a byte-free source,
// values that carry at most VIEWS of it -- so the local, and anything read out of it, carries at
// most views of the source (its own storage is fresh). This generalizes storeScanViewFilledLocals
// (pushes of view-typed values) to locals filled through helper calls, index stores of scalars,
// call results whose callees return only views, and aliases that are themselves filled.
//
// Computed as a greatest fixpoint: start from every local whose declarations are all initialized
// at most with views, then drop any local with a write (or an alias, or a binder over it) that
// may carry more. content is the store scan's taint set for the source.

func (a *Analyzer) storeScanFilledLocals(decl *ast.FuncDecl, content, views map[string]bool, seen map[storeScanKey]bool) map[string]bool {
	if views == nil {
		return nil // the source holds bytes: a view of it may point into its storage
	}
	params := map[string]bool{}
	for _, p := range decl.Params {
		params[p.Name] = true
	}
	idx := a.storeScanIndex(decl)
	candidates := map[string]bool{}
	var order []string // a fixed visiting order: summary cycles make the checks order-sensitive
	for name := range idx.decls {
		// This index is keyed by spelling rather than lexical binding identity. If a name is
		// declared more than once, the body scan can mix writes to shadowed locals, so do not
		// use that name as a proof candidate.
		if len(idx.decls[name]) == 1 && !params[name] && !idx.otherBound[name] {
			candidates[name] = true
			order = append(order, name)
		}
	}
	sort.Strings(order)
	for changed := true; changed; {
		changed = false
		for _, name := range order {
			if !candidates[name] {
				continue
			}
			if !a.storeScanLocalIsFilled(decl, name, content, views, candidates, seen) {
				if storeScanTrace {
					fmt.Fprintf(os.Stderr, "FILLDROP %s local=%s why=%s\n", decl.Name, name, storeScanFillWhy)
				}
				delete(candidates, name)
				changed = true
			}
		}
	}
	return candidates
}

// storeScanLocalIsFilled checks every way a value can reach local name, assuming the locals in
// filled are filled.
func (a *Analyzer) storeScanLocalIsFilled(decl *ast.FuncDecl, name string, content, views, filled map[string]bool, seen map[storeScanKey]bool) bool {
	atMost := func(v ast.Expr) bool { return a.storeScanAtMostViews(decl, v, content, views, filled, seen) }
	ok := true
	for _, node := range a.storeScanIndex(decl).nodes[name] {
		if !ok {
			break
		}
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				continue
			}
			if n.Name == name {
				if n.Value != nil && !atMost(n.Value) {
					ok = false
					storeScanFillWhy = "L72"
				}
				continue
			}
			// Another local bound to part of this one aliases its storage: it must be filled too.
			if n.Value != nil && storeScanRootedAt(n.Value, name) && !a.storeScanValueIsView(decl, n.Value, views) && !filled[n.Name] {
				ok = false
				storeScanFillWhy = "L78"
			}
		case *ast.AssignStmt:
			if n == nil {
				continue
			}
			if root := rootIdentExpr(n.Target); root != nil && root.Name == name {
				if !atMost(n.Value) {
					ok = false
					storeScanFillWhy = "L86"
				}
				continue
			}
			if storeScanRootedAt(n.Value, name) && !a.storeScanValueIsView(decl, n.Value, views) {
				if root := rootIdentExpr(n.Target); root == nil || !filled[root.Name] {
					ok = false
					storeScanFillWhy = "L92"
				}
			}
		case *ast.CallExpr:
			if n == nil {
				continue
			}
			args := returnBorrowCallArgs(n)
			if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(n.Args) {
				root := rootIdentExpr(field.Object)
				if root == nil || root.Name != name {
					// X as an argument of a method on something else: that receiver may keep it.
					for _, arg := range n.Args {
						if storeScanRootedAt(arg, name) && !a.storeScanValueIsView(decl, arg, views) {
							if r := rootIdentExpr(field.Object); r == nil || !filled[r.Name] {
								ok = false
								storeScanFillWhy = "L107"
							}
						}
					}
					continue
				}
				if !a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
					ok = false // a user method on X: unknown writes
					storeScanFillWhy = "L114"
					continue
				}
				if containerGrowthMethods[field.Field] {
					for _, arg := range n.Args {
						if !atMost(arg) {
							ok = false
							storeScanFillWhy = "L120"
						}
					}
				}
				continue
			}
			rooted := []int{}
			for i, arg := range args {
				if storeScanIdentCount(arg, name) == 0 {
					continue
				}
				if !storeScanRootedAt(arg, name) {
					// X inside a larger argument (a bundle): the callee may keep or write it.
					if !a.storeScanValueIsView(decl, arg, views) {
						ok = false
						storeScanFillWhy = "L134"
						continue
					}
					continue
				}
				rooted = append(rooted, i)
			}
			if len(rooted) == 0 {
				continue
			}
			callee, resolved := a.resolveReadOnlyScanCallee(n)
			if !resolved || callee == nil || !storeFlowCallFitsParams(callee, n) || funcHasArenaParam(callee) {
				ok = false
				storeScanFillWhy = "L146"
				continue
			}
			isRooted := map[int]bool{}
			writableX := []int{}
			for _, i := range rooted {
				isRooted[i] = true
				if a.declParamIsWritableRef(callee, i) {
					writableX = append(writableX, i)
				}
			}
			for j, arg := range args {
				if isRooted[j] || !a.declParamCarriesStorage(callee, j) {
					continue
				}
				// The callee may alias part of X into another writable argument (a header copy
				// shares X's storage): that argument must itself be filled.
				if a.declParamIsWritableRef(callee, j) {
					if root := rootIdentExpr(stripAddrAndParens(arg)); root == nil || !filled[root.Name] || !storeScanRootedAt(arg, root.Name) {
						ok = false
						storeScanFillWhy = "L165"
						continue
					}
				}
				if !ok || len(writableX) == 0 || atMost(arg) {
					continue // already rejected, nothing writable, or at most views reach X anyway
				}
				for _, xi := range writableX {
					stored := a.storeScanSettled(func() storeMode { return a.calleeStoreModeIntoSeen(callee, j, xi, seen) })
					if stored == storeNone || stored == storeViews {
						continue
					}
					ok = false
					storeScanFillWhy = "L177"
					break
				}
			}
		case *ast.ForStmt:
			// A counted loop binds an integer, whatever its bounds mention.
		case ast.Stmt:
			// A loop / match binder over X may alias its parts.
			if a.storeScanIndex(decl).headerMentions(n, map[string]bool{name: true}) && len(a.storeScanIndex(decl).boundNames(n)) > 0 {
				if _, isVar := node.(*ast.VarDeclStmt); !isVar && !a.storeScanBindsNoStorage(n) {
					ok = false
					storeScanFillWhy = "L187"
				}
			}
		}
	}
	return ok
}

// storeScanAtMostViews: does value carry, from the source (content), at most views?
func (a *Analyzer) storeScanAtMostViews(decl *ast.FuncDecl, value ast.Expr, content, views, filled map[string]bool, seen map[storeScanKey]bool) bool {
	if value == nil || !a.storeScanIndex(decl).mentions(value, content) {
		return true
	}
	inner := storeScanUnwrap(value)
	if a.storeScanValueIsView(decl, inner, views) || storeScanEmptyLiteral(inner) {
		return true
	}
	if t, ok := a.storeScanStaticType(decl, inner); ok && storeScanPlainScalarType(t) {
		return true
	}
	if root := rootIdentExpr(inner); root != nil && filled[root.Name] && storeScanRootedAt(inner, root.Name) {
		return true
	}
	switch e := inner.(type) {
	case *ast.StructLitExpr:
		if e == nil || e.CopyBase != nil || len(e.Spreads) > 0 {
			return false
		}
		for _, arg := range e.Args {
			if !a.storeScanAtMostViews(decl, arg, content, views, filled, seen) {
				return false
			}
		}
		return true
	case *ast.ListLitExpr:
		if e == nil {
			return false
		}
		for i, el := range e.Elems {
			if i < len(e.Spreads) && e.Spreads[i] {
				return false
			}
			if !a.storeScanAtMostViews(decl, el, content, views, filled, seen) {
				return false
			}
		}
		return true
	case *ast.CallExpr:
		if e == nil {
			return false
		}
		args := returnBorrowCallArgs(e)
		if field, isMethod := e.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(e.Args) {
			if containerGrowthMethods[field.Field] && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				if !a.storeScanAtMostViews(decl, field.Object, content, views, filled, seen) {
					return false
				}
				for _, arg := range e.Args {
					if !a.storeScanAtMostViews(decl, arg, content, views, filled, seen) {
						return false
					}
				}
				return true
			}
			return false
		}
		callee, ok := a.resolveReadOnlyScanCallee(e)
		if !ok || callee == nil || !storeFlowCallFitsParams(callee, e) || funcHasArenaParam(callee) || a.declReturnsVoid(callee) {
			return false
		}
		for i, arg := range args {
			if !storeScanMentions(arg, content) || !a.declParamCarriesStorage(callee, i) {
				continue
			}
			if a.storeScanAtMostViews(decl, arg, content, views, filled, seen) {
				continue
			}
			if a.storeScanSettled(func() storeMode { return a.calleeReturnModeOf(callee, i, seen) }) > storeViews {
				return false
			}
		}
		return true
	}
	return false
}

// storeScanRootedAt: value is the local name or a field / index chain into it.
func storeScanRootedAt(value ast.Expr, name string) bool {
	inner := storeScanUnwrap(stripAddrAndParens(value))
	for {
		switch e := inner.(type) {
		case *ast.Ident:
			return e != nil && e.Name == name
		case *ast.FieldExpr:
			if e == nil {
				return false
			}
			inner = storeScanUnwrap(e.Object)
		case *ast.IndexExpr:
			if e == nil {
				return false
			}
			inner = storeScanUnwrap(e.Object)
		default:
			return false
		}
	}
}

// lazyFilledLocalSet computes storeScanFilledLocals on first use (most values never need it).
type lazyFilledLocalSet struct {
	compute func() map[string]bool
	set     map[string]bool
	done    bool
}

func (a *Analyzer) lazyFilledLocals(decl *ast.FuncDecl, other int, content, views map[string]bool, seen map[storeScanKey]bool) *lazyFilledLocalSet {
	return &lazyFilledLocalSet{compute: func() map[string]bool {
		// content and views depend only on (decl, other); a result computed while a summary
		// cycle was open is provisional and not cached.
		key := storeScanKey{decl, other, -4}
		if cached, ok := a.storeScanFilledCache[key]; ok {
			return cached
		}
		saved := a.storeScanCycleHit
		a.storeScanCycleHit = false
		set := a.storeScanFilledLocals(decl, content, views, seen)
		if !a.storeScanCycleHit {
			if a.storeScanFilledCache == nil {
				a.storeScanFilledCache = map[storeScanKey]map[string]bool{}
			}
			a.storeScanFilledCache[key] = set
		}
		a.storeScanCycleHit = saved || a.storeScanCycleHit
		return set
	}}
}

// storeScanRootedAtFilled: value is a filled local or a field / index chain into one.
func storeScanRootedAtFilled(value ast.Expr, lazy *lazyFilledLocalSet) bool {
	if lazy == nil {
		return false
	}
	root := rootIdentExpr(storeScanUnwrap(stripAddrAndParens(value)))
	if root == nil || !storeScanRootedAt(value, root.Name) {
		return false
	}
	if !lazy.done {
		lazy.set = lazy.compute()
		lazy.done = true
	}
	return lazy.set[root.Name]
}

// storeScanFillWhy: trace only -- the last reason storeScanLocalIsFilled rejected a local.
var storeScanFillWhy string

// storeScanBindsNoStorage: a destructuring bind whose value's type shares no storage (`slot,
// found = probe(x.slots, ..)` binds two scalars): its binders alias nothing of the header.
func (a *Analyzer) storeScanBindsNoStorage(stmt ast.Stmt) bool {
	tb, ok := stmt.(*ast.TupleBindStmt)
	if !ok || tb == nil || tb.ArgManifest || tb.Value == nil {
		return false
	}
	t := a.exprTypes[tb.Value]
	if t == nil {
		if call, isCall := storeScanUnwrap(tb.Value).(*ast.CallExpr); isCall && call != nil {
			t = a.storeScanCallReturnType(call)
		}
	}
	return t != nil && !containsTypeParam(t) && !a.storeScanSharesCached(t)
}
