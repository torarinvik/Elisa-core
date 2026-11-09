package semantic

import (
	"reflect"
	"strings"
	"sync"

	"elisacore/src/ast"
)

// applyCallStoreFlow links the argument groups of a call the callee's summary connects and
// reports whether the call was fully handled (a resolvable, summarized, non-variadic callee).
func (a *Analyzer) applyCallStoreFlow(call *ast.CallExpr, c *storeFlowCtx, handled map[*ast.CallExpr][]string) ([]string, bool) {
	if _, joined := a.taskGroupSubmits[call]; joined {
		// The verified TaskGroup window discards this Task and joins it before the
		// enclosing function can return. Treating pool_submit1's temporary capture
		// as persistent store flow would falsely make worker payload borrows escape
		// through the pool. Argument thread-transfer and worker-capture checks run
		// during ordinary call analysis; this only removes the post-join store edge.
		return nil, true
	}
	if a.storeFlowEnumVariantConstructor(call) {
		// `Enum.Variant(payload...)` creates a fresh value containing exactly its
		// payloads. The payloads reach the expression result, but the constructor
		// does not store them into unrelated state or mutate the enum type.
		var retNames []string
		for _, arg := range returnBorrowCallArgs(call) {
			for _, name := range a.storeFlowArgNames(arg, handled) {
				if !c.scalars[storeFlowBaseName(name)] {
					retNames = append(retNames, name)
				}
			}
		}
		return retNames, true
	}
	if fn, ok := call.Func.(*ast.FieldExpr); ok && fn != nil && call.SafeReceiver == nil && call.NamedArgCount() == 0 && (a.isCompilerBuiltinHelperCall(call, "darray.push") || a.storeFlowBuiltinDarrayGrowth(fn)) {
		// Builtin DArray growth copies element values into the receiver. Preserve only
		// element-to-container flow for borrow-bearing element types; pushing or extending
		// pointer-free values cannot retain the source container or view. The returned
		// DArray still depends on its prior receiver contents for fluent forms.
		argumentIndex := -1
		switch fn.Field {
		case "push", "extend":
			if len(call.Args) == 1 {
				argumentIndex = 0
			}
		}
		if argumentIndex < 0 {
			return nil, false
		}
		receiver := a.storeFlowArgNames(fn.Object, handled)
		c.link(receiver)
		retNames := append([]string(nil), receiver...)
		// main: a pointer-free element type cannot retain the source, so no edge at all.
		if !a.storeFlowDarrayElementPointerFree(fn.Object) {
			group := a.storeFlowArgNames(call.Args[argumentIndex], handled)
			c.link(group)
			if a.storeFlowPushedElemMayShare(fn.Object, call.Args[argumentIndex]) {
				// An element that SHARES indirect storage (a container header, a reference) makes
				// the receiver an alias of the source's storage: writes through the receiver's
				// elements (`holder[0][0] <- v` after `holder.push(t.lists)`) land in the source.
				c.linkPair(group, receiver)
			} else {
				c.flow(group, receiver)
			}
			retNames = append(retNames, group...)
		}
		return retNames, true
	}
	if a.storeFlowBuiltinDarrayTruncate(call) {
		// truncate changes only the receiver's visible length. Its integer count is
		// not stored in the array, and the returned array is the same receiver.
		// Keep the receiver -> result dependency while avoiding a false count ->
		// receiver borrow edge.
		return a.storeFlowArgNames(call.Func.(*ast.FieldExpr).Object, handled), true
	}
	if a.storeFlowBuiltinDarrayReserve(call) {
		// Inner calls are processed first. If one is unresolved, leave this
		// outer call conservative so handling reserve cannot hide its effects.
		var capacityCalls []*ast.CallExpr
		a.storeFlowCollectMentions(reflect.ValueOf(call.Args[0]), 0, nil, nil, &capacityCalls, false, false)
		for _, inner := range capacityCalls {
			if _, ok := handled[inner]; !ok {
				return nil, false
			}
		}
		// Capacity controls allocation, but is never stored as an element.
		// reserve returns its mutable receiver, including its existing contents.
		return a.storeFlowArgNames(call.Func.(*ast.FieldExpr).Object, handled), true
	}
	if a.storeFlowRuntimeDictQuery(call) {
		// A lowered `d.get(k)` / `d.contains(k)`: the result reads the dict's storage; the key
		// is only compared, and nothing is stored into either argument.
		receiver := a.storeFlowArgNames(call.Args[0], handled)
		c.link(receiver)
		c.link(a.storeFlowArgNames(call.Args[1], handled))
		if ident, _ := call.Func.(*ast.Ident); ident != nil && ident.Name == "arena_dict_contains" {
			return nil, true
		}
		return receiver, true
	}
	callee := a.storeFlowCallee(call)
	if callee == nil {
		if !a.storeFlowOpaqueExternCall(call) {
			return nil, false
		}
		// A native extern over scalars and opaque handles cannot hold an Elisa reference in
		// any parameter: it connects no two arguments, and its result is (conservatively)
		// derived from all of them.
		var retNames []string
		for _, arg := range returnBorrowCallArgs(call) {
			group := a.storeFlowArgNames(arg, handled)
			c.link(group)
			retNames = append(retNames, group...)
		}
		return retNames, true
	}
	args := returnBorrowCallArgs(call)
	summary := a.storeFlowSummaryFor(callee)
	if summary == nil {
		return nil, false
	}
	var localGroups [8][]string
	groupNames := localGroups[:]
	if len(args) > len(localGroups) {
		groupNames = make([][]string, len(args))
	} else {
		groupNames = groupNames[:len(args)]
	}
	for j, arg := range args {
		groupNames[j] = a.storeFlowArgNames(arg, handled)
		c.link(groupNames[j])
	}
	for i := 0; i < len(args); i++ {
		if len(groupNames[i]) == 0 {
			continue
		}
		for j := i + 1; j < len(args); j++ {
			if len(groupNames[j]) == 0 || !summary.connected(i, j) {
				continue
			}
			// A pair flows only into a parameter the callee writes; one that flows both ways
			// exchanges contents. Neither written: the callee stores neither parameter into the
			// other, and whatever both feed (another parameter, the result) is linked through
			// that pair instead.
			ij := summary.writtenParam(j) && summary.reaches(i, j)
			ji := summary.writtenParam(i) && summary.reaches(j, i)
			switch {
			case ij && ji:
				c.linkPair(groupNames[i], groupNames[j])
			case ij:
				c.flow(groupNames[i], groupNames[j])
			case ji:
				c.flow(groupNames[j], groupNames[i])
			}
		}
	}
	var retNames []string
	for j, connected := range summary.retConn {
		if connected && j < len(groupNames) {
			retNames = append(retNames, groupNames[j]...)
		}
	}
	return retNames, true
}

// storeFlowChainRoots returns the variables an alias-shaped expression (a name, field or index
// path, a reborrow or an aliasing getter) denotes storage of, and whether it has that shape.
func storeFlowChainRoots(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil && storeFlowVariableName(n.Name) {
			return []string{n.Name}, true
		}
		return nil, true
	case *ast.ParenExpr:
		return storeFlowChainRoots(n.Inner)
	case *ast.FieldExpr:
		return storeFlowChainRoots(n.Object)
	case *ast.IndexExpr:
		return storeFlowChainRoots(n.Object)
	case *ast.UnaryExpr:
		return storeFlowChainRoots(n.Operand)
	case *ast.MoveExpr:
		return storeFlowChainRoots(n.Operand)
	case *ast.AddrOfExpr:
		return storeFlowChainRoots(n.Operand)
	case *ast.CallExpr:
		if fe, ok := n.Func.(*ast.FieldExpr); ok && fe != nil && storeFlowReadOnlyMethods[fe.Field] {
			return storeFlowChainRoots(fe.Object)
		}
	}
	return nil, false
}

// storeFlowWritten computes the names of decl that may be written: assignment targets, mutated
// receivers, arguments to callees whose summaries show a write (or unresolved callees), and
// everything that aliases one of those. Parameter mutability is only a capability; a settled
// body summary determines whether the capability is exercised. Every other name is a pure source.
func (a *Analyzer) storeFlowWritten(decl *ast.FuncDecl, scalars map[string]bool) map[string]bool {
	decl = a.readDecl(decl)
	written := map[string]bool{}
	var ft *FuncType
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		ft, _ = sym.Type.(*FuncType)
	}
	// Hidden trailing parameters (an implicit packed-enum store) follow the declared ones.
	allWritable := len(decl.LmutThreadSlots) != 0 || ft == nil || funcTypeExplicitParamCount(ft) != len(decl.Params)
	for _, param := range decl.Params {
		if allWritable {
			written[param.Name] = true
		}
	}
	markExpr := func(expr ast.Expr) {
		if roots, ok := storeFlowChainRoots(expr); ok {
			for _, r := range roots {
				written[a.storeFlowRenamed(r)] = true
			}
			return
		}
		for _, n := range a.storeFlowExprNames(expr) {
			written[n] = true
		}
	}
	// Rebinding a by-value named local replaces its value; it stores into nothing the local
	// aliases, so the rebind alone does not make it written.
	binders := a.storeFlowBinderIndexFor(decl)
	binders.classify()
	valueLocals := binders.valueNamedNames
	var groups, elemGroups [][]string
	grown := map[string]bool{}
	a.storeFlowNodes2(decl, func(stmt reflect.Value, nested []reflect.Value) []reflect.Value {
		var calls []*ast.CallExpr
		// These statements only need names for the binding-expression fallback
		// below. Keep collecting calls and nested statement scopes as usual.
		collectNames := true
		switch stmt.Interface().(type) {
		case *ast.AssignStmt, *ast.AugAssignStmt, *ast.AsRefAssignStmt, *ast.VarDeclStmt,
			*ast.ExprStmt, *ast.ReturnStmt, *ast.IfStmt, *ast.WhileStmt, *ast.DiscardStmt:
			collectNames = false
		}
		queryScan := storeFlowQueryScan{}
		names := a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, false, collectNames, &queryScan)
		binds := a.storeFlowBinds
		for _, call := range calls {
			fn, isMethod := call.Func.(*ast.FieldExpr)
			grows := isMethod && fn != nil && (a.storeFlowBuiltinDarrayGrowth(fn) || a.storeFlowBuiltinDarrayTruncate(call) || a.storeFlowBuiltinDarrayReserve(call))
			roots, isChain := []string(nil), false
			if grows {
				roots, isChain = storeFlowChainRoots(fn.Object)
			}
			if grows && isChain {
				// Growth (and reserve / truncate) writes the receiver's header, not its
				// elements: it reaches the receiver's aliases but not the storage of the
				// elements pushed into it.
				for _, r := range roots {
					grown[a.storeFlowRenamed(r)] = true
				}
			} else {
				a.storeFlowMarkCall(call, written, markExpr)
			}
			// A pushed element that shares indirect storage aliases the receiver to it: a write
			// through the receiver's elements is a write of the element (applyCallStoreFlow
			// links them the same way).
			if isMethod && fn != nil && fn.Field == "push" && len(call.Args) == 1 {
				if a.storeFlowPushedElemMayShare(fn.Object, call.Args[0]) {
					elemGroups = append(elemGroups, append(a.storeFlowExprNames(fn.Object), a.storeFlowExprNames(call.Args[0])...))
				}
			}
		}
		switch st := stmt.Interface().(type) {
		case *ast.AssignStmt:
			id, isIdent := returnBorrowStripParens(st.Target).(*ast.Ident)
			switch {
			case isIdent && id != nil && !st.WriteThrough && st.AsOverlayCall == nil && valueLocals[id.Name] && !a.storeFlowParams[id.Name]:
			case isIdent && id != nil && !st.WriteThrough && st.AsOverlayCall == nil && storeFlowVariableName(id.Name):
				// Rebinding a name overwrites its own slot: a header write, like growth. It
				// reaches the name's aliases but not storage the name's elements point into.
				grown[a.storeFlowRenamed(id.Name)] = true
			default:
				markExpr(st.Target)
			}
			if id, ok := returnBorrowStripParens(st.Target).(*ast.Ident); ok && id != nil {
				// A pointer-free value (`n <- xs.count`) copies bits and aliases nothing.
				if roots, isChain := storeFlowChainRoots(st.Value); isChain && !a.storeFlowPointerFreeValue(st.Value) {
					groups = append(groups, a.storeFlowRenamedAll(append([]string{id.Name}, roots...)))
				}
			}
		case *ast.AugAssignStmt:
			markExpr(st.Target)
		case *ast.AsRefAssignStmt:
			markExpr(st.Target)
		case *ast.VarDeclStmt:
			if st.Owner != nil {
				markExpr(st.Owner)
			}
			if roots, isChain := storeFlowChainRoots(st.Value); isChain && st.Value != nil && !a.storeFlowPointerFreeValue(st.Value) {
				groups = append(groups, a.storeFlowRenamedAll(append([]string{st.Name}, roots...)))
			}
		case *ast.ExprStmt, *ast.ReturnStmt, *ast.IfStmt, *ast.WhileStmt, *ast.DiscardStmt:
		case *ast.ForStmt, *ast.IterForStmt, *ast.MatchStmt, *ast.LetDestructureStmt, *ast.TupleBindStmt, *ast.MoveBindStmt:
			groups = append(groups, names)
		default:
			for _, n := range names {
				written[n] = true
			}
		}
		groups = append(groups, queryScan.result(a, stmt)...)
		if binds && !a.storeFlowScalarDecl(stmt) {
			if target, _ := a.storeFlowElementCopy(stmt); target == "" {
				if !collectNames {
					names = a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false)
				}
				groups = append(groups, names)
			}
		}
		return nested
	})
	storeFlowFilterScalarGroups(groups, scalars)
	storeFlowFilterScalarGroups(elemGroups, scalars)
	spread := func(set map[string]bool, gs [][]string) bool {
		// An empty seed cannot reach any group. Avoid walking the entire body
		// for the common case with no writes or no header growth.
		if len(set) == 0 || len(gs) == 0 {
			return false
		}
		changed := false
		for _, g := range gs {
			any := false
			for _, n := range g {
				if set[n] {
					any = true
					break
				}
			}
			if !any {
				continue
			}
			for _, n := range g {
				if !set[n] {
					set[n] = true
					changed = true
				}
			}
		}
		return changed
	}
	// Element writes reach both alias groups and pushed elements; a header write (growth)
	// reaches only alias groups. A grown parameter is the exception: the caller may write
	// through the elements pushed into it, so its pushed elements count as written.
	var params map[string]bool
	if len(elemGroups) != 0 {
		params = make(map[string]bool, len(decl.Params))
		for _, param := range decl.Params {
			params[param.Name] = true
		}
	}
	iterations := 0
	for changed := true; changed; {
		iterations++
		changed = spread(written, groups)
		if spread(grown, groups) {
			changed = true
		}
		if len(elemGroups) != 0 {
			for n := range grown {
				if params[n] && !written[n] && storeFlowInGroups(n, elemGroups) {
					written[n] = true
					changed = true
				}
			}
		}
		if spread(written, elemGroups) {
			changed = true
		}
		// Most summaries settle in one or two passes. For a longer chain,
		// index memberships so each remaining group is traversed once.
		if changed && iterations == 2 && len(groups)+len(elemGroups) >= 32 {
			storeFlowPropagateWritesIndexed(written, grown, params, groups, elemGroups)
			return written
		}
	}
	for n := range grown {
		written[n] = true
	}
	return written
}

// storeFlowPropagateWritesIndexed computes the same closures as the iterative
// spread above: growth follows alias groups; writes follow alias and element
// groups; a grown parameter in an element group seeds a write.
func storeFlowPropagateWritesIndexed(written, grown, params map[string]bool, groups, elemGroups [][]string) {
	index := func(gs [][]string) map[string][]int {
		out := make(map[string][]int)
		for i, group := range gs {
			for _, name := range group {
				out[name] = append(out[name], i)
			}
		}
		return out
	}
	aliases, elements := index(groups), index(elemGroups)
	spread := func(set map[string]bool, includeElements bool) {
		queue := make([]string, 0, len(set))
		for name, yes := range set {
			if yes {
				queue = append(queue, name)
			}
		}
		visitedAliases := make([]bool, len(groups))
		var visitedElements []bool
		if includeElements {
			visitedElements = make([]bool, len(elemGroups))
		}
		for i := 0; i < len(queue); i++ {
			visit := func(ids []int, gs [][]string, visited []bool) {
				for _, id := range ids {
					if visited[id] {
						continue
					}
					visited[id] = true
					for _, name := range gs[id] {
						if !set[name] {
							set[name] = true
							queue = append(queue, name)
						}
					}
				}
			}
			visit(aliases[queue[i]], groups, visitedAliases)
			if includeElements {
				visit(elements[queue[i]], elemGroups, visitedElements)
			}
		}
	}
	spread(grown, false)
	for name := range grown {
		if params[name] && !written[name] && len(elements[name]) != 0 {
			written[name] = true
		}
	}
	spread(written, true)
	for name := range grown {
		written[name] = true
	}
}

// Most groups contain no scalar names. Retain those slices rather than
// allocating a copy; groups can share storage, so filtered groups are copied.
func storeFlowFilterScalarGroups(groups [][]string, scalars map[string]bool) {
	if len(scalars) == 0 {
		return
	}
	for gi, group := range groups {
		for i, name := range group {
			if !scalars[storeFlowBaseName(name)] {
				continue
			}
			var kept []string
			kept = append(kept, group[:i]...)
			for _, tail := range group[i+1:] {
				if !scalars[storeFlowBaseName(tail)] {
					kept = append(kept, tail)
				}
			}
			groups[gi] = kept
			break
		}
	}
}

// storeFlowInGroups reports whether name is a member of any of groups.
func storeFlowInGroups(name string, groups [][]string) bool {
	for _, g := range groups {
		for _, n := range g {
			if n == name {
				return true
			}
		}
	}
	return false
}

// storeFlowBaseName strips a scoped binder's "#scope" suffix.
func storeFlowBaseName(name string) string {
	if i := strings.IndexByte(name, '#'); i >= 0 {
		return name[:i]
	}
	return name
}

// storeFlowNodes2 runs unit over every statement of decl.
func (a *Analyzer) storeFlowNodes2(decl *ast.FuncDecl, unit func(stmt reflect.Value, nested []reflect.Value) []reflect.Value) {
	decl = a.readDecl(decl)
	seen := acquireStoreFlowSeen()
	defer releaseStoreFlowSeen(seen)
	a.storeFlowEach(reflect.ValueOf(decl.Body), seen, unit)
}

var storeFlowSeenPool sync.Pool

func acquireStoreFlowSeen() map[uintptr]bool {
	if seen := storeFlowSeenPool.Get(); seen != nil {
		return seen.(map[uintptr]bool)
	}
	return make(map[uintptr]bool)
}

func releaseStoreFlowSeen(seen map[uintptr]bool) {
	// Avoid retaining exceptionally large function graphs between analyses.
	if len(seen) <= 8192 {
		clear(seen)
		storeFlowSeenPool.Put(seen)
	}
}
