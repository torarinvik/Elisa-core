package semantic

import (
	"elisacore/src/ast"
)

// computeStoreFlowKinds: one round of the kind analysis of decl under its callees' current
// kinds. Names that may share storage are unioned into classes (a local bound to, or holding, a
// part of another name's storage joins its class: a store into either is a store into both).
// Each class also records the parameters whose copied ELEMENT values it may hold. It is
// flow-insensitive and iterated to a local fixpoint over every node.
func (a *Analyzer) computeStoreFlowKinds(decl *ast.FuncDecl, s *storeFlowSummary) ([]storeFlowEdgeKind, [][]storeFlowEdgeKind) {
	decl = a.readDecl(decl)
	n := len(decl.Params)
	top := func() ([]storeFlowEdgeKind, [][]storeFlowEdgeKind) {
		ret := make([]storeFlowEdgeKind, n)
		reach := make([][]storeFlowEdgeKind, n)
		for i := range ret {
			ret[i] = storeFlowEdgeHeader
			reach[i] = make([]storeFlowEdgeKind, n)
			for j := range reach[i] {
				reach[i][j] = storeFlowEdgeHeader
			}
		}
		return ret, reach
	}
	// Shapes the local analysis does not model keep every edge at Header.
	if len(decl.LmutThreadSlots) != 0 || (decl.ReturnType != nil && !a.declReturnsVoid(decl) && !storeFlowEndsInReturn(decl.Body)) {
		return top()
	}
	idx := a.storeScanIndex(decl)
	for _, node := range idx.flat {
		switch node.(type) {
		case *ast.LambdaExpr, *ast.DeferStmt, *ast.AdoptStmt, *ast.PromoteStmt, *ast.ParallelForStmt, *ast.PoolStmt, *ast.LockStmt:
			return top()
		}
	}
	savedFlowDecl := a.storeFlowCurrentDecl
	a.storeFlowCurrentDecl = decl
	defer func() { a.storeFlowCurrentDecl = savedFlowDecl }()
	// Resolve the body's calls in decl's own module (as computeStoreFlow does).
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		if index := lastDotIndex(sym.Name); index >= 0 {
			a.currentNamespace = sym.Name[:index]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.declarationUsings(decl)...)
	defer func() { a.currentNamespace, a.currentUsings = savedNamespace, savedUsings }()

	scalars := a.storeScanScalarLocals(decl)
	const retName = "\x00ret"
	known := map[string]bool{retName: true}
	for _, p := range decl.Params {
		known[p.Name] = true
	}
	for name := range idx.locals {
		known[name] = true
	}
	// Names are keyed per lexical binding (see storeFlowKindScopes); scalars and parameter
	// projections are facts about every binding of a name, read by its base name.
	sc := a.storeFlowKindScopesFor(decl, idx)
	for _, key := range sc.keys {
		known[key] = true
	}
	for name := range sc.opaque {
		known[name] = true
	}
	keyOf := func(id *ast.Ident) string {
		if id == nil {
			return ""
		}
		return sc.key(id)
	}
	isScalar := func(key string) bool { return scalars[storeFlowBaseName(key)] }
	parent := map[string]string{}
	var find func(x string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok || p == x {
			return x
		}
		r := find(p)
		parent[x] = r
		return r
	}
	elem := map[string]map[int]bool{} // class root -> parameters whose copied elements it holds
	// joined: names in a class some union made (a class of one name never unioned holds only
	// the copied elements put into it).
	joined := map[string]bool{}
	changed := false
	union := func(x, y string) {
		rx, ry := find(x), find(y)
		if rx == ry {
			return
		}
		joined[rx], joined[ry] = true, true
		parent[rx] = ry
		if e := elem[rx]; len(e) > 0 {
			if elem[ry] == nil {
				elem[ry] = map[int]bool{}
			}
			for i := range e {
				elem[ry][i] = true
			}
		}
		delete(elem, rx)
		changed = true
	}
	paramIndex := map[string]int{}
	for i, p := range decl.Params {
		paramIndex[p.Name] = i
	}
	empty := func() storeFlowKindValue { return storeFlowKindValue{} }
	merge := func(dst *storeFlowKindValue, src storeFlowKindValue) {
		for h := range src.hdr {
			if dst.hdr == nil {
				dst.hdr = map[string]bool{}
			}
			dst.hdr[h] = true
		}
		for i := range src.el {
			if dst.el == nil {
				dst.el = map[int]bool{}
			}
			dst.el[i] = true
		}
	}
	// lower: what reading element / field values whose type shares no storage out of v yields.
	lower := func(v storeFlowKindValue, to storeFlowEdgeKind) storeFlowKindValue {
		if to == storeFlowEdgeHeader {
			return v
		}
		out := storeFlowKindValue{el: map[int]bool{}}
		for i := range v.el {
			out.el[i] = true
		}
		for h := range v.hdr {
			root := find(h)
			for i, p := range decl.Params {
				if find(p.Name) == root {
					out.el[i] = true
				}
			}
			for i := range elem[root] {
				out.el[i] = true
			}
		}
		return out
	}
	put := func(name string, v storeFlowKindValue) {
		if name == "" || isScalar(name) || !known[name] {
			return
		}
		for h := range v.hdr {
			union(name, h)
		}
		if len(v.el) > 0 {
			root := find(name)
			if elem[root] == nil {
				elem[root] = map[int]bool{}
			}
			for i := range v.el {
				if !elem[root][i] {
					elem[root][i] = true
					changed = true
				}
			}
		}
	}
	headerOf := func(tokens []string) storeFlowKindValue {
		out := empty()
		for _, t := range tokens {
			if known[t] && !isScalar(t) && t != retName {
				if out.hdr == nil {
					out.hdr = map[string]bool{}
				}
				out.hdr[t] = true
			}
		}
		return out
	}
	mentioned := func(expr ast.Expr) storeFlowKindValue {
		if expr == nil {
			return empty()
		}
		return headerOf(storeScanMentionTokensKeyed(expr, sc))
	}
	// elemKind: the kind of an element / field value read out of a container: Elements when its
	// static type can share no storage (views, scalars), else Header.
	elemKind := func(expr ast.Expr, container ast.Expr) storeFlowEdgeKind {
		t, ok := a.storeScanStaticType(decl, storeScanUnwrap(expr))
		if id, isIdent := storeScanUnwrap(expr).(*ast.Ident); (!ok || t == nil) && isIdent && id != nil {
			// A binder in a body not type-checked yet: its declared pattern or loop type.
			if t, ok = a.storeScanPatternBinderType(decl, id.Name); !ok || t == nil {
				t, ok = a.storeScanLoopBinderElemType(decl, id)
			}
		}
		if id, isIdent := storeScanUnwrap(expr).(*ast.Ident); (!ok || t == nil) && isIdent && id != nil && a.storeScanPatternBindersShareNot(decl, id.Name) {
			return storeFlowEdgeElements
		}
		if ok && t != nil && !a.containsTypeParamCached(t) {
			if a.storeScanSharesCached(t) {
				return storeFlowEdgeHeader
			}
			return storeFlowEdgeElements
		}
		if container != nil {
			if _, isIdent := storeScanUnwrap(container).(*ast.Ident); isIdent && !a.storeScanContainerElemsMayShare(decl, container) {
				return storeFlowEdgeElements
			}
		}
		return storeFlowEdgeHeader
	}
	// through: v passed through a callee edge of the given kind.
	through := func(v storeFlowKindValue, kind storeFlowEdgeKind) storeFlowKindValue {
		switch kind {
		case storeFlowEdgeNone:
			return empty()
		case storeFlowEdgeElements:
			return lower(v, storeFlowEdgeElements)
		}
		return v
	}
	calleeOf := func(call *ast.CallExpr) (*ast.FuncDecl, *storeFlowSummary) {
		callee, ok := a.resolveReadOnlyScanCallee(call)
		if !ok || callee == nil || !storeFlowCallFitsParams(callee, call) || funcHasArenaParam(callee) {
			return nil, nil
		}
		sum := a.storeFlowKindsFor(callee)
		if sum == nil || len(sum.retKind) != len(callee.Params) {
			return nil, nil
		}
		return callee, sum
	}
	// memo: per-round results (the state only grows within a round; a round that changes
	// nothing recomputes everything against the final state, so the fixpoint reads nothing stale).
	memo := map[ast.Expr]storeFlowKindValue{}
	var evalRaw func(expr ast.Expr) storeFlowKindValue
	eval := func(expr ast.Expr) storeFlowKindValue {
		if v, ok := memo[expr]; ok {
			return v
		}
		v := evalRaw(expr)
		memo[expr] = v
		return v
	}
	evalRaw = func(expr ast.Expr) storeFlowKindValue {
		out := empty()
		inner := storeScanUnwrap(expr)
		if inner == nil {
			return out
		}
		if t, ok := a.storeScanStaticType(decl, inner); ok && t != nil && !a.containsTypeParamCached(t) && a.storeScanHoldsNoRegionStorage(t) && !a.typeMayHoldFrameBorrow(t, map[Type]bool{}) {
			return out
		}
		switch e := inner.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit:
			return out
		case *ast.Ident:
			key := keyOf(e)
			if e == nil || isScalar(key) || !known[key] {
				return out
			}
			// A local whose class only ever received copied elements is those elements: storing
			// it copies them one way instead of joining every destination into one class.
			if _, isParam := paramIndex[key]; !isParam && key != retName && !joined[key] && find(key) == key {
				for i := range elem[key] {
					if out.el == nil {
						out.el = map[int]bool{}
					}
					out.el[i] = true
				}
				return out
			}
			return storeFlowKindValue{hdr: map[string]bool{key: true}}
		case *ast.IndexExpr:
			if e == nil || e.Index2 != nil {
				return mentioned(inner)
			}
			return lower(eval(e.Object), elemKind(e, e.Object))
		case *ast.FieldExpr:
			if e == nil {
				return out
			}
			// A field of a container is part of its storage unless its value shares none.
			return lower(eval(e.Object), elemKind(e, nil))
		case *ast.StructLitExpr:
			if e == nil || e.CopyBase != nil {
				return mentioned(inner)
			}
			for _, arg := range e.Args {
				merge(&out, eval(arg))
			}
			for _, spread := range e.Spreads {
				merge(&out, mentioned(spread))
			}
			return out
		case *ast.TernaryExpr:
			// `v if c else w`: either branch (the condition is only tested).
			if e == nil {
				return out
			}
			merge(&out, eval(e.Value))
			merge(&out, eval(e.Alt))
			return out
		case *ast.TupleExpr:
			for _, elem := range e.Elems {
				merge(&out, eval(elem))
			}
			return out
		case *ast.ListLitExpr:
			if e == nil || len(e.Keys) != 0 {
				return mentioned(inner)
			}
			for i, item := range e.Elems {
				if i < len(e.Spreads) && e.Spreads[i] {
					merge(&out, mentioned(item))
					continue
				}
				merge(&out, eval(item))
			}
			return out
		case *ast.ListComprehensionExpr:
			if e == nil {
				return out
			}
			// Each produced element is the value expression over a binder; only a value whose
			// type shares no storage is a copied element.
			return lower(mentioned(inner), elemKind(e.Value, nil))
		case *ast.CallExpr:
			if e == nil {
				return out
			}
			if field, isMethod := e.Func.(*ast.FieldExpr); isMethod && field != nil && len(returnBorrowCallArgs(e)) == len(e.Args) {
				if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && containerGrowthMethods[field.Field] {
					merge(&out, eval(field.Object))
					for _, arg := range e.Args {
						if field.Field == "extend" {
							merge(&out, lower(eval(arg), elemKind(&ast.IndexExpr{Object: arg}, arg)))
						} else {
							merge(&out, eval(arg))
						}
					}
					return out
				}
				return mentioned(inner)
			}
			if _, sum := calleeOf(e); sum != nil {
				for i, arg := range returnBorrowCallArgs(e) {
					merge(&out, through(eval(arg), sum.retKindOf(i)))
				}
				return out
			}
			return mentioned(inner)
		}
		return mentioned(inner)
	}
	// copied: the value of expr where it is stored. Reading a place (a name, field or index
	// chain, no call) whose type shares no storage copies a value out: the destination then
	// holds an element value of the place's class (one way), not a part of its storage. Any
	// other expression may produce a borrow (`s.view()`) and keeps its header.
	paramProjections := storeFlowKindParamProjections(idx.flat, paramIndex, sc)
	var copied func(expr ast.Expr) storeFlowKindValue
	copied = func(expr ast.Expr) storeFlowKindValue {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			if e != nil {
				return copied(e.Inner)
			}
		case *ast.TernaryExpr:
			// `v if c else w`: either branch (the condition is only tested).
			if e != nil && e.Alt != nil {
				out := copied(e.Value)
				merge(&out, copied(e.Alt))
				return out
			}
		case *ast.MatchExpr:
			// Each arm yields its tail value; an arm of any other shape keeps the whole match.
			if e == nil || len(e.Arms) == 0 {
				break
			}
			out := empty()
			for _, arm := range e.Arms {
				if len(arm.Body) == 0 {
					return eval(expr)
				}
				switch tail := arm.Body[len(arm.Body)-1].(type) {
				case *ast.ExprStmt:
					if tail == nil {
						return eval(expr)
					}
					merge(&out, copied(tail.Expr))
				case *ast.ReturnStmt:
					// Leaves the function; the return statement is applied on its own.
				default:
					return eval(expr)
				}
			}
			return out
		}
		if root, ok := storeFlowKindProjectionRoot(expr); ok {
			if _, isParam := paramIndex[root]; isParam || paramProjections[storeFlowKindProjectionKey(expr, root, sc)] {
				return lower(eval(expr), elemKind(expr, nil))
			}
		}
		if id, ok := expr.(*ast.Ident); ok && id != nil && paramProjections[keyOf(id)] {
			return lower(eval(expr), elemKind(expr, nil))
		}
		return eval(expr)
	}
	// callWrites applies what a call may store into its arguments.
	callWrites := func(call *ast.CallExpr) {
		args := returnBorrowCallArgs(call)
		if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(call.Args) {
			root := rootIdentExpr(stripAddrAndParens(field.Object))
			if storeScanReadOnlyMethods[field.Field] {
				return
			}
			if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && containerGrowthMethods[field.Field] {
				if root == nil {
					return
				}
				for _, arg := range call.Args {
					if field.Field == "extend" {
						put(keyOf(root), lower(eval(arg), elemKind(&ast.IndexExpr{Object: arg}, arg)))
					} else {
						put(keyOf(root), copied(arg))
					}
				}
				return
			}
		}
		if callee, sum := calleeOf(call); sum != nil {
			for m, target := range args {
				root := rootIdentExpr(stripAddrAndParens(target))
				if root == nil || !a.declParamIsWritableRef(callee, m) {
					continue
				}
				for k, arg := range args {
					if k == m {
						continue
					}
					if kind := sum.reachKindOf(k, m); kind != storeFlowEdgeNone {
						put(keyOf(root), through(eval(arg), kind))
					}
				}
			}
			return
		}
		// Unknown: any argument (or receiver) may receive everything the others hold.
		all := empty()
		var roots []string
		visit := func(e ast.Expr) {
			merge(&all, mentioned(e))
			if root := rootIdentExpr(stripAddrAndParens(e)); root != nil {
				roots = append(roots, keyOf(root))
			}
		}
		if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil {
			visit(field.Object)
		}
		for _, arg := range call.Args {
			visit(arg)
		}
		for _, r := range roots {
			put(r, all)
		}
	}
	for rounds := 0; rounds < 64; rounds++ {
		changed = false
		memo = map[ast.Expr]storeFlowKindValue{}
		for name, owners := range sc.opaque {
			for _, owner := range owners {
				put(name, mentioned(owner))
			}
		}
		for key, source := range sc.sources {
			if paramProjections[key] && elemKind(sc.binder(key), nil) == storeFlowEdgeElements {
				put(key, lower(mentioned(source), storeFlowEdgeElements))
				continue
			}
			put(key, mentioned(source))
		}
		for _, node := range idx.flat {
			switch nd := node.(type) {
			case *ast.VarDeclStmt:
				if nd != nil && nd.Value != nil {
					put(sc.bound(nd, nd.Name), copied(nd.Value))
				}
			case *ast.AssignStmt:
				if nd == nil {
					continue
				}
				if root := rootIdentExpr(stripAddrAndParens(nd.Target)); root != nil {
					put(keyOf(root), copied(nd.Value))
				}
			case *ast.AugAssignStmt:
				// `xs += ys` may store the value into the target.
				if nd == nil {
					continue
				}
				if root := rootIdentExpr(stripAddrAndParens(nd.Target)); root != nil {
					put(keyOf(root), eval(nd.Value))
				}
				if nd.CollectionAppend != nil {
					callWrites(nd.CollectionAppend)
				}
			case *ast.AsRefAssignStmt:
				if nd == nil {
					continue
				}
				if root := rootIdentExpr(stripAddrAndParens(nd.Target)); root != nil {
					put(keyOf(root), eval(nd.Value))
				}
			case *ast.ReturnStmt:
				if nd != nil && nd.Value != nil {
					put(retName, copied(nd.Value))
				}
			case *ast.CallExpr:
				if nd != nil {
					callWrites(nd)
				}
			case *ast.TupleBindStmt:
				// `a, b = f(..)`: each name may hold anything the value holds. An arg-manifest
				// `a, b <- f(a, b)` binds nothing; the call's writes are applied with the call.
				if nd == nil || nd.ArgManifest {
					continue
				}
				value := eval(nd.Value)
				for _, name := range nd.Names {
					put(name.Name, value)
				}
			case *ast.ForStmt:
				// an integer binder
			case *ast.IterForStmt:
				if nd == nil {
					continue
				}
				bound := lower(eval(nd.Source), elemKind(&ast.IndexExpr{Object: nd.Source}, nd.Source))
				for _, name := range idx.boundNames(nd) {
					put(sc.bound(nd, name), bound)
				}
			case ast.Stmt:
				if nd == nil {
					continue
				}
				names := idx.boundNames(nd)
				if len(names) == 0 {
					continue
				}
				// Any other binder (match / is patterns, catch, ...): the whole header, at Header
				// -- except a binder that only ever names a part of a parameter, at a type that
				// shares no storage: it holds a copied element value of the header.
				all := headerOf(storeScanHeaderTokensKeyed(nd, sc))
				for _, name := range names {
					for _, key := range sc.boundAll(nd, name) {
						if paramProjections[key] && elemKind(sc.binder(key), nil) == storeFlowEdgeElements {
							put(key, lower(all, storeFlowEdgeElements))
							continue
						}
						put(key, all)
					}
				}
			}
		}
		if !changed {
			outRet := make([]storeFlowEdgeKind, n)
			outReach := make([][]storeFlowEdgeKind, n)
			retRoot := find(retName)
			for i, pi := range decl.Params {
				ri := find(pi.Name)
				switch {
				case ri == retRoot:
					outRet[i] = storeFlowEdgeHeader
				case elem[retRoot][i]:
					outRet[i] = storeFlowEdgeElements
				}
				outReach[i] = make([]storeFlowEdgeKind, n)
				for j, pj := range decl.Params {
					rj := find(pj.Name)
					switch {
					case i == j || ri == rj:
						outReach[i][j] = storeFlowEdgeHeader
					case elem[rj][i]:
						outReach[i][j] = storeFlowEdgeElements
					}
				}
			}
			return outRet, outReach
		}
	}
	return top()
}
