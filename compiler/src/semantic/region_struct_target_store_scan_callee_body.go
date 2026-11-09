package semantic

import (
	"fmt"
	"os"
	"strings"

	"elisacore/src/ast"
)

func (a *Analyzer) calleeStoreModeIntoBody(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	decl = a.readDecl(decl)
	if decl == nil || other < 0 || index < 0 || other >= len(decl.Params) || index >= len(decl.Params) || len(decl.Body) == 0 {
		return storeHeader
	}
	// docs/128: everything reachable from a program-lived parameter (an AST node, a
	// `darray[sview @source] @source` row) is program-lived: storing it anywhere keeps no
	// shorter-lived storage alive.
	if a.declParamIsProgramLived(decl, other) {
		return storeNone
	}
	key := storeScanKey{decl, other, index}
	// seen is the DFS STACK, not a visited set: a key finished on another path (left uncached
	// because its own subtree met a cycle) is no cycle of a later query and must be rescanned,
	// never read as "stores nothing".
	seen[key] = true
	defer delete(seen, key)
	if storeScanTrace {
		savedOther, savedTarget := storeScanTraceOther, storeScanTraceTarget
		storeScanTraceOther, storeScanTraceTarget = decl.Params[other].Name, decl.Params[index].Name
		defer func() { storeScanTraceOther, storeScanTraceTarget = savedOther, savedTarget }()
	}
	scanIdx := a.storeScanIndex(decl)
	// The directed result is raised only by assignments or calls. For bodies
	// with neither, there is no reason to compute the taint fixpoint at all.
	if len(scanIdx.payloadNodeList()) == 0 {
		return storeNone
	}
	params, header, content, targets, views := a.storeScanTaint(decl, other, decl.Params[index].Name, seen)
	holders := a.storeScanLastHolders
	// targetsOrHolders: anything that mentions the target or a holder of its parts.
	targetsOrHolders := targets
	if len(holders) > 0 {
		targetsOrHolders = map[string]bool{}
		for name := range targets {
			targetsOrHolders[name] = true
		}
		for name := range holders {
			targetsOrHolders[name] = true
		}
	}
	filled := a.lazyFilledLocals(decl, other, content, views, seen)
	// A body often calls the same directly bound helper many times. Keep a
	// small per-scan cache for those bindings; other resolution forms retain
	// their ordinary per-call handling. Scope is part of the key because nested
	// scans can temporarily change the active scope.
	type directCallee struct {
		name  string
		scope *Scope
		decl  *ast.FuncDecl
	}
	var directCallees [4]directCallee
	directCount := 0
	resolvePayloadCallee := func(call *ast.CallExpr) (*ast.FuncDecl, bool) {
		if id, ok := call.Func.(*ast.Ident); ok && id != nil && a.currentScope != nil {
			for i := 0; i < directCount; i++ {
				cached := directCallees[i]
				if cached.name == id.Name && cached.scope == a.currentScope {
					a.addProofReportOrigin(cached.decl)
					return cached.decl, true
				}
			}
			callee, resolved := a.resolveReadOnlyScanCallee(call)
			if resolved && callee != nil && directCount < len(directCallees) {
				if sym, found := a.currentScope.Lookup(id.Name); found && sym != nil && sym.Kind == SymbolFunc && sym.Node == callee {
					directCallees[directCount] = directCallee{id.Name, a.currentScope, callee}
					directCount++
				}
			}
			return callee, resolved
		}
		return a.resolveReadOnlyScanCallee(call)
	}
	// Parameter type properties are stable once type shapes freeze. Repeated
	// calls to the same helper otherwise redo the same nested type predicates.
	type paramProperty struct {
		decl                                           *ast.FuncDecl
		index                                          int
		carries, carriesKnown, writable, writableKnown bool
	}
	var paramProperties [8]paramProperty
	paramCount := 0
	property := func(callee *ast.FuncDecl, index int) *paramProperty {
		if !a.typeShapesFrozen {
			return nil
		}
		callee = a.readDecl(callee)
		for i := 0; i < paramCount; i++ {
			if paramProperties[i].decl == callee && paramProperties[i].index == index {
				return &paramProperties[i]
			}
		}
		if paramCount == len(paramProperties) {
			return nil
		}
		p := &paramProperties[paramCount]
		paramCount++
		p.decl, p.index = callee, index
		return p
	}
	carriesStorage := func(callee *ast.FuncDecl, index int) bool {
		p := property(callee, index)
		if p != nil && p.carriesKnown {
			return p.carries
		}
		hits := a.semanticLimitHits
		value := a.declParamCarriesStorage(callee, index)
		if p != nil && hits == a.semanticLimitHits {
			p.carries, p.carriesKnown = value, true
		}
		return value
	}
	writableParam := func(callee *ast.FuncDecl, index int) bool {
		p := property(callee, index)
		if p != nil && p.writableKnown {
			return p.writable
		}
		hits := a.semanticLimitHits
		value := a.declParamIsWritableRef(callee, index)
		if p != nil && hits == a.semanticLimitHits {
			p.writable, p.writableKnown = value, true
		}
		return value
	}
	var valueMode func(value ast.Expr) storeMode
	valueMode = func(value ast.Expr) storeMode {
		inner := storeScanUnwrap(value)
		// A conditional carries the selected branch value, never the condition's storage.
		// Calls in the condition are visited separately and retain their own store effects.
		if choice, ok := inner.(*ast.TernaryExpr); ok && choice != nil {
			first := valueMode(choice.Value)
			if first == storeHeader {
				return first
			}
			return maxStoreMode(first, valueMode(choice.Alt))
		}
		// Simple payloads must mention a tainted name to retain its storage.
		// Compound literals and calls still check their component expressions,
		// including call arguments that may have been resolved since indexing.
		mentionsChecked := false
		switch inner.(type) {
		case *ast.StructLitExpr, *ast.CallExpr:
		default:
			if !scanIdx.mentions(value, content) {
				return storeNone
			}
			mentionsChecked = true
		}
		// A value whose (already analyzed) type cannot hold region storage carries none.
		// Probe the cached storage property before walking generic enum payloads.
		knownType, typeKnown := a.lookupExprType(inner)
		if typeKnown && a.storeScanTypeIsPlainValue(knownType) {
			return storeNone
		}
		// The callee may not be analyzed yet (no exprTypes): a parameter field chain still has a
		// declared type (`scope.deferred.count` is a usize).
		if !typeKnown {
			t, ok := a.storeScanDeclTypeOf(decl, inner)
			if !ok || t == nil {
				t, ok = a.storeScanParamProjectionType(decl, inner)
			}
			if ok && t != nil && !a.containsTypeParamCached(t) {
				if a.storeScanHoldsNoRegionStorage(t) && !a.storeScanTypeMayEncodeAddress(t, map[Type]bool{}) {
					return storeNone
				}
				// docs/128: a declared program-lived type (an element of a
				// `darray[sview @source]` parameter row) reaches only program-region storage.
				if a.storeScanTypeIsProgramLivedForStoreProof(t) {
					return storeNone
				}
			}
			// A by-value loop binder copies one element, not the container's backing storage.
			if id, isIdent := inner.(*ast.Ident); isIdent {
				if t, ok := a.storeScanLoopBinderElemType(decl, id); ok && t != nil && !a.containsTypeParamCached(t) && !a.storeScanTypeMayEncodeAddress(t, map[Type]bool{}) && (a.storeScanHoldsNoRegionStorage(t) || (len(a.programRegions) != 0 && a.typeIsProgramLivedCached(t))) {
					return storeNone
				}
			}
			// `p.items.count` / `.len` / `.capacity` of a builtin container or view is a scalar.
			if field, isField := inner.(*ast.FieldExpr); isField && field != nil && (field.Field == "count" || field.Field == "len" || field.Field == "capacity") {
				if t, ok := a.storeScanDeclTypeOf(decl, field.Object); ok && t != nil {
					switch stripRefForBounds(t).(type) {
					case *DArrayType, *SViewType, *CStrType:
						return storeNone
					}
				}
			}
		}
		// docs/128: a value whose declared type is program-lived (a `darray[T] @source` field)
		// reaches only program-region storage, whatever holds it.
		// Unknown-type resolution above can populate expression types, so only
		// recheck in that case. Already-known types need no repeated map lookup.
		if !typeKnown {
			knownType, typeKnown = a.lookupExprType(inner)
		}
		if typeKnown && a.storeScanTypeIsProgramLivedForStoreProof(knownType) {
			return storeNone
		}
		// A local declared `sview @source` (every declaration) views only program-region bytes.
		// Spelled-type check: the callee body may not be analyzed yet (exprTypes empty).
		if id, isIdent := inner.(*ast.Ident); isIdent && id != nil && a.storeScanLocalIsProgramView(decl, id.Name) {
			return storeNone
		}
		// A struct literal stores each field value: judge them one by one.
		if lit, isLit := inner.(*ast.StructLitExpr); isLit && lit != nil {
			mode := storeNone
			for _, arg := range lit.Args {
				mode = maxStoreMode(mode, valueMode(arg))
				if mode == storeHeader {
					return mode
				}
			}
			for _, spread := range lit.Spreads {
				mode = maxStoreMode(mode, valueMode(spread))
				if mode == storeHeader {
					return mode
				}
			}
			if lit.CopyBase != nil {
				mode = maxStoreMode(mode, valueMode(lit.CopyBase))
			}
			return mode
		}
		// `t.f.push(v)` on a builtin darray yields the receiver with v appended: it stores what
		// the receiver and the arguments store (rule 177's per-argument view of the same call).
		if call, isCall := inner.(*ast.CallExpr); isCall && call != nil && len(returnBorrowCallArgs(call)) == len(call.Args) {
			if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && containerGrowthMethods[field.Field] && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				mode := valueMode(field.Object)
				if mode == storeHeader {
					return mode
				}
				for _, arg := range call.Args {
					argMode := valueMode(arg)
					if field.Field == "extend" {
						argMode = a.storeScanExtendArgMode(decl, arg, argMode)
					}
					mode = maxStoreMode(mode, argMode)
					if mode == storeHeader {
						return mode
					}
				}
				return mode
			}
		}
		if !mentionsChecked && !scanIdx.mentions(value, content) {
			return storeNone
		}
		// A nested call's result carries, from each argument, only what its callee may RETURN
		// from that parameter (the rule-160 judgement, applied inside a stored value).
		if call, isCall := inner.(*ast.CallExpr); isCall && call != nil {
			if callee, ok := resolvePayloadCallee(call); ok && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) && !a.declReturnsVoid(callee) {
				mode := storeNone
				for i, arg := range returnBorrowCallArgs(call) {
					argMode := valueMode(arg)
					if argMode == storeNone || (!carriesStorage(callee, i) && !a.declParamMayEncodeAddress(callee, i)) {
						continue
					}
					mode = maxStoreMode(mode, narrowStoreMode(argMode, a.calleeReturnModeOf(callee, i, seen)))
					if mode == storeHeader {
						return mode
					}
				}
				return mode
			}
		}
		// A view-only value derived from a byte-free source cannot point into the source's storage.
		if views != nil {
			isView := false
			if typeKnown && knownType != nil {
				// The value's analyzed type was already looked up above. Keep the
				// declaration-read observation made by storeScanValueIsView, but
				// avoid looking up the same expression type a second time.
				a.readDecl(decl)
				if id, ok := inner.(*ast.Ident); ok && id != nil && views[id.Name] {
					isView = true
				} else {
					isView = a.storeScanViewOnlyCached(knownType)
				}
			} else {
				isView = a.storeScanValueIsView(decl, inner, views)
			}
			// Filled-local candidates explicitly exclude parameter names. Avoid
			// computing the body-wide filled-local fixpoint for a plain parameter.
			id, isIdent := inner.(*ast.Ident)
			isParam := isIdent && id != nil && params[id.Name]
			if isView || (!isParam && storeScanRootedAtFilled(inner, filled)) {
				return storeViews
			}
		}
		if storeScanMentionsHeader(value, header) {
			return storeHeader
		}
		return storeElements
	}
	mode := storeNone
	// Only assignments and calls can contribute to this scan. The cached subset
	// preserves their flat-walk order, including calls nested inside assignments.
	visitPayload := func(node any) {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			root := rootIdentExpr(n.Target)
			// Through a holder, a place two projections deep is inside a target part
			// (`holder[0][0] <- v`); `holder[0] <- v` writes the holder's own buffer.
			if root == nil || !(targets[root.Name] || (holders[root.Name] && storeScanPlaceDepth(n.Target) >= 2)) {
				return
			}
			// A plain rebind of a non-param local alias is not a store into the target.
			if id, isIdent := n.Target.(*ast.Ident); isIdent && !params[id.Name] && !n.WriteThrough {
				return
			}
			// `table <- f(table, scope)`: the stored value is the call's RESULT. The call's own
			// stores are scanned when the walk reaches it; its result carries a tainted argument
			// only when the callee's summary connects that argument to its return.
			if call, isCall := storeScanUnwrap(n.Value).(*ast.CallExpr); isCall && call != nil {
				if callee, ok := resolvePayloadCallee(call); ok && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) {
					if a.declReturnsVoid(callee) {
						// The lmut manifest `t <- f(.., t, ..)` of a void callee: the result IS
						// the threaded argument; the call's own stores are scanned at the call.
						return
					}
					// A non-void call result carries what the callee may RETURN from each tainted
					// argument (a directed scan of its return statements).
					for i, arg := range returnBorrowCallArgs(call) {
						argMode := valueMode(arg)
						if argMode == storeNone || (!carriesStorage(callee, i) && !a.declParamMayEncodeAddress(callee, i)) {
							continue
						}
						inner := narrowStoreMode(argMode, a.calleeReturnModeOf(callee, i, seen))
						mode = storeScanRaise(decl, 160, n, mode, inner)
						if mode == storeHeader {
							return
						}
					}
					return
				}
			}
			mode = storeScanRaise(decl, 167, n, mode, valueMode(n.Value))
		case *ast.CallExpr:
			if n == nil {
				return
			}
			args := returnBorrowCallArgs(n)
			field, isMethod := n.Func.(*ast.FieldExpr)
			// A builtin darray's `extend(xs)` copies xs's ELEMENTS into the receiver; it never
			// retains xs's own header.
			copiesElements, copiesElementsKnown := false, false
			copiesReceiverElements := func() bool {
				if !copiesElementsKnown {
					copiesElements = isMethod && field != nil && field.Field == "extend" && a.storeScanReceiverIsBuiltinDArray(decl, field.Object)
					copiesElementsKnown = true
				}
				return copiesElements
			}
			if isMethod && field != nil {
				if root := rootIdentExpr(field.Object); root != nil && (targets[root.Name] || (holders[root.Name] && storeScanPlaceDepth(field.Object) >= 1)) {
					for _, arg := range n.Args {
						argMode := valueMode(arg)
						if copiesReceiverElements() {
							argMode = a.storeScanExtendArgMode(decl, arg, argMode)
						}
						mode = storeScanRaise(decl, 177, n, mode, argMode)
						if mode == storeHeader {
							return
						}
					}
				}
			}
			type side struct {
				index int
				mode  storeMode
			}
			// Most calls have only a few operands. Keep the per-call lists on the
			// stack while allowing append to grow for larger signatures.
			var targetStorage [8]int
			var taintedStorage [8]side
			targetArgs, taintedArgs := targetStorage[:0], taintedStorage[:0]
			for i, arg := range args {
				if scanIdx.mentions(arg, targetsOrHolders) {
					targetArgs = append(targetArgs, i)
				}
			}
			unrewrittenMethod := isMethod && field != nil && len(args) == len(n.Args)
			recvTarget := false
			if unrewrittenMethod {
				recvTarget = scanIdx.mentions(field.Object, targets)
				if !recvTarget && len(holders) > 0 && storeScanPlaceDepth(field.Object) >= 1 {
					recvTarget = scanIdx.mentions(field.Object, holders)
				}
			}
			// No source payload can be retained through this call without a target
			// operand. Avoid the recursive value/type proofs for its source arguments.
			// An unrewritten method's receiver is an operand even though absent from args.
			if len(targetArgs) == 0 && !recvTarget {
				return
			}
			// A growth method only moves values into its receiver. When that
			// receiver is not the target, argument provenance cannot affect this
			// directed scan and need not be computed.
			if unrewrittenMethod && containerGrowthMethods[field.Field] && !recvTarget {
				return
			}
			for i, arg := range args {
				if m := valueMode(arg); m != storeNone {
					taintedArgs = append(taintedArgs, side{i, m})
				}
			}
			if unrewrittenMethod {
				// An unrewritten method call: the receiver is an operand too, and the callee is
				// unresolved. A builtin growth method moves its arguments INTO the receiver only.
				recvMode := valueMode(field.Object)
				if recvTarget {
					for _, s := range taintedArgs {
						argMode := s.mode
						if copiesReceiverElements() {
							argMode = a.storeScanExtendArgMode(decl, n.Args[s.index], argMode)
						}
						mode = storeScanRaise(decl, 204, n, mode, argMode)
						if mode == storeHeader {
							return
						}
					}
				}
				if recvMode != storeNone && len(targetArgs) > 0 {
					mode = storeScanRaise(decl, 208, n, mode, recvMode)
				}
				return
			}
			if len(targetArgs) == 0 || len(taintedArgs) == 0 {
				return
			}
			callee, ok := resolvePayloadCallee(n)
			if !ok || callee == nil || !storeFlowCallFitsParams(callee, n) || funcHasArenaParam(callee) {
				for _, s := range taintedArgs {
					mode = storeScanRaise(decl, 218, n, mode, s.mode)
					if mode == storeHeader {
						return
					}
				}
				return
			}
			for _, ti := range targetArgs {
				if !carriesStorage(callee, ti) || !writableParam(callee, ti) {
					// The target-side parameter cannot hold region storage, or is passed by
					// value: the callee's writes land in its own copy, never the caller's place.
					continue
				}
				for _, s := range taintedArgs {
					if s.index != ti && !carriesStorage(callee, s.index) && !a.declParamMayEncodeAddress(callee, s.index) {
						continue // only plain non-address scalars (such as a size) are skipped
					}
					if ti == s.index {
						// One argument derived from both sides (`f(table.types[id])`) is not a
						// store between them, unless it BUNDLES both into a fresh aggregate the
						// callee could then split and store.
						if scanIdx.bundles(args[ti]) && a.storeScanBundleHasWritableTarget(args[ti], targetsOrHolders) && !a.storeScanCallResultDropsTarget(args[ti], targetsOrHolders, seen) {
							mode = storeScanRaise(decl, 225, n, mode, s.mode)
							if mode == storeHeader {
								return
							}
						}
						continue
					}
					// The callee stores at most what this argument carried (an element value of an
					// element-mode argument is never this source's header).
					inner := narrowStoreMode(s.mode, a.calleeStoreModeIntoSeen(callee, s.index, ti, seen))
					if storeChainTrace && inner != storeNone && s.index < len(callee.Params) && ti < len(callee.Params) {
						fmt.Fprintf(os.Stderr, "CHAIN %s(%s->%s) -> %s(%s->%s) at %s\n", decl.Name, decl.Params[other].Name, decl.Params[index].Name, callee.Name, callee.Params[s.index].Name, callee.Params[ti].Name, n.Pos())
					}
					mode = storeScanRaise(decl, 233, n, mode, inner)
					if mode == storeHeader {
						return
					}
				}
			}
		}
	}
	for _, node := range scanIdx.payloadNodeList() {
		if mode == storeHeader {
			break
		}
		visitPayload(node)
	}
	return mode
}

// ELISA_STORE_PAYLOAD_TRACE=1 traces failed payload proofs; any other nonempty
// value filters by declaration-name prefix. Logging reads existing facts only.
var storePayloadTracePrefix = os.Getenv("ELISA_STORE_PAYLOAD_TRACE")

func (a *Analyzer) traceStorePayloadFailure(decl *ast.FuncDecl, other, index int, reason string, expr ast.Expr) {
	if storePayloadTracePrefix == "" {
		return
	}
	name, source, target, position := "<unknown>", "?", "?", "<unknown>"
	if decl != nil {
		name = decl.Name
		position = fmt.Sprint(decl.Pos())
		if other >= 0 && other < len(decl.Params) {
			source = decl.Params[other].Name
		}
		if index >= 0 && index < len(decl.Params) {
			target = decl.Params[index].Name
		}
	}
	if storePayloadTracePrefix != "1" && !strings.HasPrefix(name, storePayloadTracePrefix) {
		return
	}
	existingType := "<missing>"
	inner := storeScanUnwrap(expr)
	if expr != nil {
		position = fmt.Sprint(expr.Pos())
	}
	if t := a.exprType(inner); t != nil {
		existingType = typeString(t)
	}
	fmt.Fprintf(os.Stderr, "STOREPAYLOAD decl=%s source=%s[%d] target=%s[%d] reason=%s at=%s kind=%T existingType=%s\n", name, source, other+1, target, index+1, reason, position, inner, existingType)
}

// calleeStoresProgramLivedElements proves that every value from source parameter `other` which
// reaches target parameter `index` is region-free or program-lived. storeMode alone loses the
// field path: a large state object can contain both source-lived names and unrelated fields with
// shorter or unknown lifetimes. This proof follows only source-tainted values at actual stores
// and fails closed for unknown expressions. Recursive edges share the active
// proof: every concrete store in the reachable cycle is still checked.
func (a *Analyzer) calleeStoresProgramLivedElements(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) bool {
	return a.calleeStoresProgramLivedElementsActive(decl, other, index, seen, newStoreLivedProof())
}
