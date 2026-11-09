package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
)

// Parameter write reachability. The callee-write taint (storeScanCallWritePlan) asks whether a
// resolved callee may write caller-visible storage through parameter i before tainting a body
// local passed there. The answer is reachability over a graph whose nodes are
// (function, parameter, mode) and whose node facts are purely local, so it never consults store
// summaries or taint: deterministic, cycle-safe, cacheable.
//
// Mode write, (f, i): f may write storage reachable from its parameter i. Local writers: a
// `Target` / `Store` place, a non-read-only method receiver, or an argument of an unresolved
// call, mentioning the parameter or a local alias of it (bound from a value that may share its
// storage, by a declaration, a store, a binding construct, or a builtin push into the local).
// Edges: (g, k, write) for an alias passed at a writable, storage-sharing parameter k of a
// resolved g; and (g, m, share) when such a call also receives a local holder L of f at m that
// f writes or passes on -- g may store the parameter's storage into L, and f then writes L.
//
// Mode share, (g, m): g may store a value sharing indirect storage into the storage of its
// parameter m (whatever that value's origin). Local writers: a place or non-read-only method on
// an alias of the parameter that receives a value that may share storage (unknown types do), any
// other `Target` / `Store` place, an unresolved call given an alias. Edges: (h, k, share) for an
// alias passed at a writable parameter k of a resolved h.

const (
	storeScanModeWrite = 0
	// Mode deep, (g, i): g may write storage reachable from the PARTS of its parameter i (what
	// its elements / fields point to), not i's own buffer. A holder argument (`entries`, holding
	// entries that contain a part) reaches the caller's storage only this way: the callee
	// pushing onto its own `entries` buffer writes nothing the holder's parts point to.
	storeScanModeDeep = 2
	// Mode share-from, (g, m, k): mode storeScanModeShareFrom+k. g may store a value sharing the
	// storage of its parameter k into the storage of its parameter m. Per (into, from) pair: a
	// callee pushing an AST handle it was given into `entries` does not thereby store some other
	// argument's darray header there.
	storeScanModeShareFrom = 16
)

// storeScanShareKey: the share-from node for (callee, into, from).
func storeScanShareKey(callee *ast.FuncDecl, into, from int) storeScanParamKey {
	return storeScanParamKey{callee, into, storeScanModeShareFrom + from}
}

type storeScanParamKey struct {
	decl  *ast.FuncDecl
	index int
	mode  int
}

type storeScanParamFacts struct {
	why    string
	at     any
	writes bool
	reach  []storeScanParamKey
}

// storeScanParamMayBeWritten: (decl, index, write) reaches a writing node.
func (a *Analyzer) storeScanParamMayBeWritten(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	return a.storeScanParamReaches(storeScanParamKey{decl, index, storeScanModeWrite})
}

func (a *Analyzer) storeScanParamReaches(start storeScanParamKey) bool {
	if cached, ok := a.lookupStoreScanParamWriteCache(start); ok {
		return cached
	}
	// A local writer or a node with no outgoing edges is already a
	// complete answer. Avoid constructing a graph walk for this common case.
	// Keep the ordinary path when tracing so it still records its explanation.
	var startFacts storeScanParamFacts
	if !storeScanTrace {
		startFacts = a.storeScanParamLocalFacts(start)
		if facts := startFacts; facts.writes || len(facts.reach) == 0 {
			a.setStoreScanParamWriteCache(start, facts.writes)
			return facts.writes
		}
	}
	var visited map[storeScanParamKey]bool
	type reachEntry struct {
		key    storeScanParamKey
		parent int
	}
	// The queue already owns every visited key. Parent indices avoid a second
	// map for the path while keeping the breadth-first traversal unchanged.
	var queueStorage [8]reachEntry
	queueStorage[0] = reachEntry{key: start, parent: -1}
	queue := queueStorage[:1]
	result := false
	resultIndex := -1
	for head := 0; head < len(queue) && !result; head++ {
		key := queue[head].key
		if cached, ok := a.lookupStoreScanParamWriteCache(key); ok {
			if cached && storeScanTrace {
				storeScanParamWriterOf[start] = storeScanParamWriterOf[key]
			}
			if cached {
				resultIndex = head
			}
			result = result || cached
			continue // a cached false already covers everything it reaches
		}
		facts := startFacts
		if head != 0 || storeScanTrace {
			facts = a.storeScanParamLocalFacts(key)
		}
		if facts.writes {
			resultIndex = head
			if storeScanTrace {
				pos := ""
				if n, ok := facts.at.(ast.Node); ok && n != nil {
					pos = n.Pos().String()
				}
				storeScanParamWriterOf[start] = fmt.Sprintf("%s/%d/%d %s %s", key.decl.Name, key.index, key.mode, facts.why, pos)
				path := ""
				for i := head; queue[i].parent >= 0; i = queue[i].parent {
					k := queue[i].key
					path = fmt.Sprintf(" <- %s/%d/%d", k.decl.Name, k.index, k.mode) + path
				}
				fmt.Fprintf(os.Stderr, "PARAMW start=%s/%d/%d writer=%s/%d/%d why=%s at %s path=%s\n", start.decl.Name, start.index, start.mode, key.decl.Name, key.index, key.mode, facts.why, pos, path)
			}
			result = true
			break
		}
		for _, next := range facts.reach {
			if visited == nil && len(queue) < len(queueStorage) {
				found := false
				for _, entry := range queue {
					if entry.key == next {
						found = true
						break
					}
				}
				if !found {
					queue = append(queue, reachEntry{key: next, parent: head})
				}
				continue
			}
			if visited == nil {
				visited = make(map[storeScanParamKey]bool, len(queue)+1)
				for _, entry := range queue {
					visited[entry.key] = true
				}
			}
			if !visited[next] {
				visited[next] = true
				queue = append(queue, reachEntry{key: next, parent: head})
			}
		}
	}
	if result {
		// Every node on the discovered path reaches the same writer, not only
		// the original query. Remember that path so callers that start from an
		// intermediate callee parameter do not repeat the graph walk.
		for i := resultIndex; i >= 0; i = queue[i].parent {
			key := queue[i].key
			a.setStoreScanParamWriteCache(key, true)
			if storeScanTrace {
				storeScanParamWriterOf[key] = storeScanParamWriterOf[start]
			}
		}
	} else {
		// Nothing reachable writes: the same holds for every node visited.
		for _, entry := range queue {
			a.setStoreScanParamWriteCache(entry.key, false)
		}
	}
	return result
}

func (a *Analyzer) storeScanParamLocalFacts(key storeScanParamKey) storeScanParamFacts {
	if facts, ok := a.lookupStoreScanParamFacts(key); ok {
		return facts
	}
	facts := a.computeStoreScanParamFacts(key)
	a.setStoreScanParamFacts(key, facts)
	return facts
}

// storeScanMayShare: value's type may share indirect storage (unknown types may).
func (a *Analyzer) storeScanMayShare(decl *ast.FuncDecl, value ast.Expr) bool {
	decl = a.readDecl(decl)
	if value == nil {
		return false
	}
	inner := storeScanUnwrap(value)
	// Literal values cannot carry shared storage, regardless of whether a
	// semantic type has already been recorded. Avoid resolving and inspecting
	// their types on every pass of the alias fixpoint.
	switch inner.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit:
		return false
	}
	if t, ok := a.storeScanStaticType(decl, inner); ok && t != nil && !a.containsTypeParamCached(t) && !a.storeScanSharesCached(t) {
		return false
	}
	switch e := inner.(type) {
	case *ast.Ident:
		// A local or parameter declared with a scalar / opaque type (`line: u32`).
		if e == nil {
			return false
		}
		for _, p := range decl.Params {
			if p.Name == e.Name {
				return !(storeScanTypeExprIsPlainScalar(p.Type) || a.storeScanTypeExprIsOpaque(p.Type))
			}
		}
		if a.storeScanScalarLocals(decl)[e.Name] {
			return false
		}
	case *ast.CallExpr:
		// An un-typed call result: the callee's declared return type.
		if t := a.storeScanCallReturnType(e); t != nil && !a.containsTypeParamCached(t) && !a.storeScanSharesCached(t) {
			return false
		}
	case *ast.TernaryExpr:
		// Only the selected branch is returned. Effects of evaluating the
		// condition are scanned separately, not retained in the result.
		return a.storeScanMayShare(decl, e.Value) || a.storeScanMayShare(decl, e.Alt)
	}
	return true
}

// storeScanCallReturnType: the declared return type of a call's resolved callee or global
// function / extern symbol, or nil.
func (a *Analyzer) storeScanCallReturnType(call *ast.CallExpr) Type {
	if callee, ok := a.resolveReadOnlyScanCallee(call); ok && callee != nil {
		if sym := a.funcDeclSymbols[callee]; sym != nil {
			if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil {
				return ft.Return
			}
		}
		return nil
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.globalScope == nil {
		return nil
	}
	sym, ok := a.globalScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil
	}
	if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil {
		return ft.Return
	}
	return nil
}

type storeScanAliasKey struct {
	index int
	deep  bool
}

type storeScanAliasResult struct {
	alias map[string]bool
	holds storeScanHolds
}

// storeScanParamAliases: the names that ARE the parameter's storage (alias) and the locals that
// CONTAIN parts of it (holds).
func (a *Analyzer) storeScanParamAliases(decl *ast.FuncDecl, idx *storeScanDeclIndex, index, mode int) (map[string]bool, storeScanHolds) {
	decl = a.readDecl(decl)
	key := storeScanAliasKey{index: index, deep: mode == storeScanModeDeep}
	if cached, ok := idx.paramAliases[key]; ok {
		return cached.alias, cached.holds
	}
	var alias map[string]bool
	var holds storeScanHolds
	if mode == storeScanModeDeep {
		// The parameter itself is only a holder: its parts are the storage.
		alias, holds = a.storeScanAliasClosureFrom(decl, idx, map[string]bool{}, map[string]bool{decl.Params[index].Name: true})
	} else {
		alias, holds = a.storeScanAliasClosure(decl, idx, map[string]bool{decl.Params[index].Name: true})
	}
	if idx.paramAliases == nil {
		idx.paramAliases = make(map[storeScanAliasKey]storeScanAliasResult)
	}
	idx.paramAliases[key] = storeScanAliasResult{alias: alias, holds: holds}
	return alias, holds
}

// storeScanAliasClosure grows the starting names into:
//   - alias: names bound to the storage itself or a part of it -- a place rooted at an alias
//     (`x = p.items`, `x = holder[0]`), a call that may return one, a binder over an alias or a
//     holder -- when the value's type can share storage at all;
//   - holds: locals whose own buffer is fresh but CONTAINS parts (`h = [p]`, `h.push(p.items)`):
//     writing the holder itself (`h.push(v)`, `h[0] <- v`) writes nothing of the storage; writing
//     through a part (`h[0][0] <- v`, `h[0].push(v)`) does.
func (a *Analyzer) storeScanAliasClosure(decl *ast.FuncDecl, idx *storeScanDeclIndex, alias map[string]bool) (map[string]bool, storeScanHolds) {
	decl = a.readDecl(decl)
	return a.storeScanAliasClosureFrom(decl, idx, alias, nil)
}

// storeScanAliasClosureFrom: storeScanAliasClosure starting from alias names and holder names
// (holding parts under any projection).
func (a *Analyzer) storeScanAliasClosureFrom(decl *ast.FuncDecl, idx *storeScanDeclIndex, alias, startHolds map[string]bool) (map[string]bool, storeScanHolds) {
	decl = a.readDecl(decl)
	var holds map[string]bool
	// fields: per holder, the first projections that lead to a part (`entry = E{target: p}`
	// holds a part under `.target` only; "*" = any projection).
	var fields map[string]map[string]bool
	for name := range startHolds {
		if holds == nil {
			holds = map[string]bool{}
			fields = map[string]map[string]bool{}
		}
		holds[name] = true
		fields[name] = map[string]bool{"*": true}
	}
	// Locals declared with a scalar / opaque type, and integer range binders, hold no storage
	// whatever their value mentions (`slot: usize = p.bits.usize()`).
	scalars := a.storeScanScalarLocals(decl)
	carries := func(value ast.Expr) bool {
		return a.storeScanFlowsFrom(decl, idx, value, alias) || (len(holds) > 0 && a.storeScanFlowsFrom(decl, idx, value, holds))
	}
	for changed := true; changed; {
		changed = false
		add := func(set map[string]bool, name string) {
			if name != "" && !alias[name] && !set[name] && !scalars[name] {
				set[name] = true
				changed = true
			}
		}
		h := storeScanHolds{names: holds, fields: fields}
		// hold: name contains a part under the first projection proj ("*" = any).
		hold := func(name, proj string) {
			if name == "" || alias[name] || scalars[name] {
				return
			}
			if holds == nil {
				holds = map[string]bool{}
				fields = map[string]map[string]bool{}
				h.names, h.fields = holds, fields
			}
			add(holds, name)
			if fields[name] == nil {
				fields[name] = map[string]bool{}
			}
			if !fields[name][proj] {
				fields[name][proj] = true
				changed = true
			}
		}
		bindValue := func(name string, value ast.Expr) {
			// An existing alias cannot become a holder, and scalars and empty
			// names cannot enter either set. Avoid re-proving their values at
			// every fixpoint iteration.
			if name == "" || alias[name] || scalars[name] || value == nil || !a.storeScanMayShare(decl, value) {
				return
			}
			switch {
			case storeScanMayAliasPlace(idx, value, alias):
				add(alias, name)
			case len(holds) > 0 && storeScanMayAliasPlace(idx, value, holds):
				if storeScanPlaceDepth(value) >= 1 {
					if h.through(value, 1) {
						add(alias, name) // `x = holder[0]`: a part
					} else if carries(value) {
						hold(name, "*") // `x = entry.other`: may still contain parts
					}
				} else {
					hold(name, "*") // `x = holder`: another holder
				}
			case carries(value):
				if lit, isLit := stripParenExpr(value).(*ast.StructLitExpr); isLit && lit != nil && len(lit.Spreads) == 0 && lit.CopyBase == nil && len(lit.ArgNames) == len(lit.Args) {
					for k, arg := range lit.Args {
						if carries(arg) {
							hold(name, lit.ArgNames[k]) // `E{target: p}`: a part under .target
						}
					}
				} else {
					hold(name, "*") // a fresh literal / result containing parts
				}
			}
		}
		for _, node := range idx.bindingEffectNodes() {
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil {
					bindValue(n.Name, n.Value)
				}
			case *ast.AssignStmt:
				if n == nil {
					continue
				}
				if id, bare := stripParenExpr(n.Target).(*ast.Ident); bare && id != nil {
					bindValue(id.Name, n.Value)
				} else {
					// `local.f <- p` / `local[i] <- p`: the local now contains a part.
					// A target that cannot enter the holder set needs no source proof.
					if root := rootIdentExpr(stripAddrAndParens(n.Target)); root != nil && root.Name != "" && !alias[root.Name] && !scalars[root.Name] && carries(n.Value) {
						hold(root.Name, storeScanFirstProjection(n.Target))
					}
				}
			case *ast.CallExpr:
				// `xs.push(p)` / `xs.extend(..p..)`: the receiver keeps a part.
				if n == nil {
					continue
				}
				if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil {
					root := rootIdentExpr(stripAddrAndParens(field.Object))
					// hold cannot add an alias, scalar, or unnamed receiver.
					// Check this before proving its arguments carry storage.
					if root == nil || root.Name == "" || alias[root.Name] || scalars[root.Name] {
						continue
					}
					if containerGrowthMethods[field.Field] && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && !a.storeScanContainerElemsMayShare(decl, field.Object) {
						// `path.names.extend(xs)` into a `darray[sview]`: element copies that share
						// no storage -- the receiver holds nothing of xs.
						continue
					}
					for _, arg := range n.Args {
						if carries(arg) {
							hold(root.Name, storeScanFirstProjection(field.Object))
							break
						}
					}
				}
			case *ast.ForStmt:
				// `for i in a..<b`: an integer binder.
			case ast.Stmt:
				if n != nil && (idx.headerMentions(n, alias) || (len(holds) > 0 && idx.headerMentions(n, holds))) {
					for _, name := range idx.boundNames(n) {
						add(alias, name)
					}
				}
			}
		}
	}
	return alias, storeScanHolds{names: holds, fields: fields}
}
