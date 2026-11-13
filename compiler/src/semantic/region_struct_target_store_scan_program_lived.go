package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) calleeStoresProgramLivedElementsActive(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool, root *storeLivedProof) (result bool) {
	decl = a.readDecl(decl)
	if decl == nil || other < 0 || index < 0 || other >= len(decl.Params) || index >= len(decl.Params) {
		a.traceStorePayloadFailure(decl, other, index, "invalid-input", nil)
		return false
	}
	key := storeScanKey{decl, other, index}
	if result, known := a.storeLivedFinal(key); known {
		return result
	}
	if done, known := root.completed[key]; known {
		if done.tentative {
			root.consult(-1)
		}
		return done.result
	}
	if depth, active := root.active[key]; active {
		// This invocation is already checking every store reachable from the
		// same source/target pair. Re-entering it adds no new obligation; the
		// active invocation still rejects any unsafe concrete store or unknown
		// callee in the cycle before its result can reach the root caller.
		root.consult(depth)
		return true
	}
	frame := root.enter(a, key)
	defer func() {
		// Memoization within the root proof is unchanged: if a recursive
		// assumption later fails, that failure propagates up its initiating
		// call chain and rejects the root. Only a result that consulted no
		// assumption still open above it is final (storeLivedProof.leave).
		if root.leave(a, frame, key, result) {
			a.storeLivedRecord(key, result)
		}
	}()
	params, header, content, targets, views := a.storeScanTaint(decl, other, decl.Params[index].Name, seen)
	idx := a.storeScanIndex(decl)
	holders := a.storeScanLastHolders
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
	_ = header // The mode proof already rejects a stored header; this proof handles content leaves.
	sawStoredPayload := false
	valueIsProgramLived := func(value ast.Expr) bool {
		inner := storeScanUnwrap(value)
		if inner == nil || !idx.mentions(value, content) {
			return true // unrelated fields in the aggregate do not contribute to this store.
		}
		if _, ok := inner.(*ast.StructLitExpr); ok {
			// Omitted/default fields can carry region storage too. Accept this literal only
			// when its complete declared type is program-lived.
			t, known := a.lookupExprType(inner)
			if !known || t == nil {
				t = a.storeScanDeclaredStructLiteralType(decl, inner.(*ast.StructLitExpr))
				known = t != nil
			}
			if known && t != nil && len(a.programRegions) != 0 && !a.storeScanTypeMayEncodeAddress(t, map[Type]bool{}) && a.typeIsProgramLivedCached(t) {
				return true
			}
			st, ok := t.(*StructType)
			return ok && a.storeScanLiteralFieldsProgramLived(decl, inner.(*ast.StructLitExpr), st, views)
		}
		return a.storeScanStoredElementExprProgramLived(decl, value, views)
	}
	proof := true
	fail := func(reason string, expr ast.Expr) {
		if proof {
			a.traceStorePayloadFailure(decl, other, index, reason, expr)
		}
		proof = false
	}
	idx.eachPayloadNode(func(node any) {
		if !proof {
			return
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			// A manifest invokes the call without storing a result. The
			// call node is still visited for writes through its arguments.
			if n == nil || n.ArgManifest {
				return
			}
			root := rootIdentExpr(n.Target)
			if root == nil || !(targets[root.Name] || (holders[root.Name] && storeScanPlaceDepth(n.Target) >= 2)) {
				return
			}
			if id, ok := n.Target.(*ast.Ident); ok && !params[id.Name] && !n.WriteThrough {
				return
			}
			if !idx.mentions(n.Value, content) {
				return
			}
			// A void call assigned through an lmut/thread slot stores through its call arguments;
			// the call result itself is not the value written to the target. Its effects are
			// checked at the CallExpr node below, matching calleeStoreModeIntoBody.
			if call, ok := storeScanUnwrap(n.Value).(*ast.CallExpr); ok && call != nil {
				// Builtin push returns this same destination header. The call
				// node below checks every appended payload independently.
				if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Field == "push" && len(returnBorrowCallArgs(call)) == len(call.Args) && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && returnBorrowSamePlace(n.Target, field.Object) {
					return
				}
				if callee, resolved := a.resolveReadOnlyScanCallee(call); resolved && callee != nil && a.declReturnsVoid(callee) {
					return
				}
			}
			sawStoredPayload = true
			if !valueIsProgramLived(n.Value) {
				fail("assignment-payload", n.Value)
			}
		case *ast.CallExpr:
			if n == nil {
				return
			}
			field, isMethod := n.Func.(*ast.FieldExpr)
			args := returnBorrowCallArgs(n)
			if isMethod && field != nil {
				root := rootIdentExpr(field.Object)
				receiverIsTarget := root != nil && (targets[root.Name] || (holders[root.Name] && storeScanPlaceDepth(field.Object) >= 1))
				if receiverIsTarget {
					for _, arg := range n.Args {
						if idx.mentions(arg, content) {
							sawStoredPayload = true
							if !valueIsProgramLived(arg) {
								fail("receiver-payload", arg)
								return
							}
						}
					}
				}
			}
			// A builtin push writes only through its receiver, checked above.
			// Payload subexpressions are separate nodes, so their own writes
			// remain checked without treating this builtin as an unknown call.
			if isMethod && field != nil && field.Field == "push" && len(args) == len(n.Args) && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				return
			}
			if len(args) != len(n.Args) {
				return
			}
			targetArgs, sourceArgs := []int{}, []int{}
			for i, arg := range args {
				mentionsTarget, mentionsSource := idx.mentionsTwo(arg, targetsOrHolders, content)
				if mentionsTarget {
					targetArgs = append(targetArgs, i)
				}
				if mentionsSource {
					sourceArgs = append(sourceArgs, i)
				}
			}
			if len(targetArgs) == 0 || len(sourceArgs) == 0 {
				return
			}
			callee, ok := a.resolveReadOnlyScanCallee(n)
			if !ok || callee == nil || !storeFlowCallFitsParams(callee, n) || funcHasArenaParam(callee) {
				fail("unresolved-or-arena-callee", n)
				return
			}
			for _, ti := range targetArgs {
				if !a.declParamCarriesStorage(callee, ti) || !a.declParamIsWritableRef(callee, ti) {
					continue
				}
				for _, si := range sourceArgs {
					if si == ti {
						continue
					}
					mode := a.calleeStoreModeIntoSeen(callee, si, ti, seen)
					if mode == storeNone {
						continue
					}
					if !a.declParamCarriesStorage(callee, si) {
						// Scalar arguments usually cannot retain region dependencies, but pointer-width
						// integers can encode an address. Do not drop those tainted call paths.
						if !a.storeScanStoredElementExprProgramLived(decl, args[si], views) {
							fail("scalar-payload", args[si])
							return
						}
						continue
					}
					if mode != storeViews && mode != storeElements {
						fail("callee-header", args[si])
						return
					}
					if !a.calleeStoresProgramLivedElementsActive(callee, si, ti, seen, root) {
						fail("callee-payload", args[si])
						return
					}
					sawStoredPayload = true
				}
			}
		}
	})
	if proof && !sawStoredPayload {
		a.traceStorePayloadFailure(decl, other, index, "no-stored-payload", nil)
	}
	return proof && sawStoredPayload
}

// calleeStoresProgramLivedViews is the narrower storeViews form used by its focused controls.
func (a *Analyzer) calleeStoresProgramLivedViews(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) bool {
	decl = a.readDecl(decl)
	if a.calleeStoreModeInto(decl, other, index) != storeViews {
		return false
	}
	return a.calleeStoresProgramLivedElements(decl, other, index, seen)
}

func (a *Analyzer) storeScanStoredElementExprProgramLived(decl *ast.FuncDecl, expr ast.Expr, views map[string]bool) bool {
	decl = a.readDecl(decl)
	return a.storeScanStoredElementExprProgramLivedSeen(decl, expr, views, map[ast.Expr]bool{}, map[string]bool{})
}

func (a *Analyzer) storeScanStoredElementExprProgramLivedSeen(decl *ast.FuncDecl, expr ast.Expr, views map[string]bool, seenExpr map[ast.Expr]bool, seenNames map[string]bool) bool {
	decl = a.readDecl(decl)
	if decl == nil || expr == nil {
		return false
	}
	inner := storeScanUnwrap(expr)
	if inner == nil {
		return false
	}
	if seenExpr[inner] {
		return false
	}
	seenExpr[inner] = true
	defer delete(seenExpr, inner)
	if id, ok := inner.(*ast.Ident); ok && id != nil {
		idx := a.storeScanIndex(decl)
		if decls := idx.decls[id.Name]; len(decls) > 0 {
			if seenNames[id.Name] {
				return false
			}
			if len(decls) != 1 || idx.otherBound[id.Name] || decls[0] == nil || decls[0].Value == nil || decls[0].Mutable {
				return false
			}
			if _, mutableType := decls[0].Type.(*ast.MutableType); mutableType {
				return false
			}
			for _, node := range idx.payloadNodeList() {
				assign, ok := node.(*ast.AssignStmt)
				if !ok || assign == nil {
					continue
				}
				if root := rootIdentExpr(assign.Target); root != nil && root.Name == id.Name {
					return false
				}
			}
			// The explicit local contract was checked at initialization. Do not
			// discard it by re-inferring a conditional initializer in a cold body.
			if named, ok := decls[0].Type.(*ast.NamedType); ok && named != nil && named.Name == "sview" && a.storeScanLocalIsProgramView(decl, id.Name) {
				return true
			}
			if views != nil && views[id.Name] {
				seenNames[id.Name] = true
				defer delete(seenNames, id.Name)
				return a.storeScanStoredElementExprProgramLivedSeen(decl, decls[0].Value, views, seenExpr, seenNames)
			}
		}
	}
	t, known := a.lookupExprType(inner)
	if !known || t == nil {
		if call, ok := inner.(*ast.CallExpr); ok && call != nil {
			t = a.storeScanCallReturnType(call)
			known = t != nil
		}
	}
	if !known || t == nil {
		t, known = a.storeScanStaticType(decl, inner)
	}
	if !known || t == nil {
		t, known = a.storeScanParamProjectionType(decl, inner)
	}
	if !known || t == nil {
		if id, ok := inner.(*ast.Ident); ok && id != nil {
			t, known = a.storeScanLoopBinderElemType(decl, id)
		}
	}
	if !known || t == nil {
		return false
	}
	if a.containsTypeParamCached(t) {
		return false
	}
	if a.storeScanTypeMayEncodeAddress(t, map[Type]bool{}) {
		return false
	}
	if a.storeScanHoldsNoRegionStorage(t) {
		return true
	}
	if len(a.programRegions) == 0 || !a.typeIsProgramLivedCached(t) {
		return false
	}
	if id, ok := inner.(*ast.Ident); ok && views != nil && views[id.Name] {
		return a.storeScanLocalIsProgramView(decl, id.Name) || a.typeIsProgramLivedCached(t)
	}
	return true
}

// Pointer-width integer values can carry an address without being represented as a region
// reference in the type system. Never treat them as harmless scalar payloads in this proof.
func (a *Analyzer) storeScanTypeMayEncodeAddress(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	// Builtin scalars have no recursive components. This common case does not
	// need a memo entry or a walk through the shared cycle-detection map.
	if builtin, ok := t.(*BuiltinType); ok && builtin != nil {
		return builtin.Name == "uintptr"
	}
	if len(seen) == 0 {
		return a.memoTypePredicate(&a.storeScanAddressMemo, t, func() bool {
			if a.storeScanAddressSeenBusy {
				return a.storeScanTypeMayEncodeAddressWalk(t, map[Type]bool{})
			}
			if a.storeScanAddressSeen == nil {
				a.storeScanAddressSeen = map[Type]bool{}
			}
			a.storeScanAddressSeenBusy = true
			clear(a.storeScanAddressSeen)
			defer func() {
				clear(a.storeScanAddressSeen)
				a.storeScanAddressSeenBusy = false
			}()
			return a.storeScanTypeMayEncodeAddressWalk(t, a.storeScanAddressSeen)
		})
	}
	return a.storeScanTypeMayEncodeAddressWalk(t, seen)
}

func (a *Analyzer) storeScanTypeMayEncodeAddressWalk(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *BuiltinType:
		return tt.Name == "uintptr"
	case *OptionalType:
		return a.storeScanTypeMayEncodeAddress(tt.Value, seen)
	case *ErrorUnionType:
		if tt == nil || tt.Errors == nil {
			return true
		}
		if a.storeScanTypeMayEncodeAddress(tt.Value, seen) {
			return true
		}
		for _, payloads := range tt.Errors.Payloads {
			for _, payload := range payloads {
				if a.storeScanTypeMayEncodeAddress(payload, seen) {
					return true
				}
			}
		}
		return false
	case *RefType:
		return a.storeScanTypeMayEncodeAddress(tt.Elem, seen)
	case *ArrayType:
		return a.storeScanTypeMayEncodeAddress(tt.Elem, seen)
	case *DArrayType:
		return a.storeScanTypeMayEncodeAddress(tt.Elem, seen)
	case *ViewType:
		return a.storeScanTypeMayEncodeAddress(tt.Elem, seen)
	case *DictType:
		return a.storeScanTypeMayEncodeAddress(tt.Key, seen) || a.storeScanTypeMayEncodeAddress(tt.Value, seen)
	case *SetType:
		return a.storeScanTypeMayEncodeAddress(tt.Elem, seen)
	case *DictEntryType:
		return tt.Dict == nil || a.storeScanTypeMayEncodeAddress(tt.Dict, seen)
	case *GenericInstanceType:
		if a.storeScanTypeMayEncodeAddress(tt.Base, seen) {
			return true
		}
		for _, arg := range tt.Args {
			if a.storeScanTypeMayEncodeAddress(arg, seen) {
				return true
			}
		}
	case *AggregateStateType:
		return a.storeScanTypeMayEncodeAddress(tt.Base, seen)
	case *IDType:
		return a.storeScanTypeMayEncodeAddress(tt.Storage, seen)
	case *AddressSpaceType:
		return a.storeScanTypeMayEncodeAddress(tt.Storage, seen) || a.storeScanTypeMayEncodeAddress(tt.Elem, seen)
	case *StoreRowsViewType:
		return tt.Store == nil || a.storeScanTypeMayEncodeAddress(tt.Store, seen)
	case *StoreRowViewType:
		return tt.Store == nil || a.storeScanTypeMayEncodeAddress(tt.Store, seen)
	case *PackedEnumStoreType:
		return a.storeScanTypeMayEncodeAddress(tt.Enum, seen) || a.storeScanTypeMayEncodeAddress(tt.State, seen)
	case *PackedVariantViewType:
		if tt == nil || tt.Variant == nil || tt.Enum == nil {
			return true
		}
		root := tt.Enum.Root()
		for _, field := range root.Common {
			if a.storeScanTypeMayEncodeAddress(field.Type, seen) {
				return true
			}
		}
		for _, payload := range tt.Variant.Payload {
			if a.storeScanTypeMayEncodeAddress(payload, seen) {
				return true
			}
		}
	case *TupleType:
		for _, field := range tt.Fields {
			if a.storeScanTypeMayEncodeAddress(field.Type, seen) {
				return true
			}
		}
	case *StructType:
		for _, field := range tt.Fields {
			if a.storeScanTypeMayEncodeAddress(field.Type, seen) {
				return true
			}
		}
	case *EnumType:
		if tt == nil {
			return true
		}
		root := tt.Root()
		for _, field := range root.Common {
			if a.storeScanTypeMayEncodeAddress(field.Type, seen) {
				return true
			}
		}
		for _, named := range a.namedTypes {
			enum, ok := named.(*EnumType)
			if !ok || enum.Root() != root {
				continue
			}
			for _, variant := range enum.Variants {
				for _, payload := range variant.Payload {
					if a.storeScanTypeMayEncodeAddress(payload, seen) {
						return true
					}
				}
			}
		}
	}
	return false
}

// storeScanParamProjectionType resolves an arbitrary field/index projection rooted in a
// parameter or explicitly typed local. Unlike storeScanParamFieldChainType, this handles
// interleaved paths such as `p.actions[i].names[j]`; any unsupported step remains unknown.
func (a *Analyzer) storeScanParamProjectionType(decl *ast.FuncDecl, expr ast.Expr) (Type, bool) {
	decl = a.readDecl(decl)
	if decl == nil || expr == nil {
		return nil, false
	}
	switch e := storeScanUnwrap(expr).(type) {
	case *ast.Ident:
		if t, ok := a.storeScanLoopBinderElemType(decl, e); ok {
			return t, true
		}
		return a.storeScanSubjectDeclType(decl, e)
	case *ast.FieldExpr:
		if e == nil {
			return nil, false
		}
		base, ok := a.storeScanParamProjectionType(decl, e.Object)
		if !ok {
			return nil, false
		}
		return storeScanWalkFields(base, []string{e.Field})
	case *ast.IndexExpr:
		if e == nil || e.Index2 != nil {
			return nil, false
		}
		if id, ok := storeScanUnwrap(e.Object).(*ast.Ident); ok {
			if elem, known := a.storeScanLocalDarrayStructElement(decl, id.Name); known {
				return elem, true
			}
		}
		base, ok := a.storeScanParamProjectionType(decl, e.Object)
		if !ok {
			return nil, false
		}
		switch c := stripRefForBounds(base).(type) {
		case *DArrayType:
			if c != nil && c.Elem != nil {
				return c.Elem, true
			}
		case *ArrayType:
			if c != nil && c.Elem != nil {
				return c.Elem, true
			}
		}
	}
	return nil, false
}
