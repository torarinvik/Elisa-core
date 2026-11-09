package semantic

import (
	"elisacore/src/ast"
	"strings"
)

// resolveReadOnlyScanCallee resolves a direct call before the body is analyzed: a local
// function, or a module-visible one whose name has no overload set a later receiver
// rewrite could pick a different callee from.
func (a *Analyzer) resolveReadOnlyScanCallee(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	// `f[r1, r2](args)` binds only region parameters: the body, and so its store summary, is
	// the same for every region instantiation. Resolve through the plain name and keep the
	// result only for a callee with no type parameters.
	if spec, ok := call.Func.(*ast.SpecializeExpr); ok && spec != nil {
		plain := *call
		plain.Func = spec.Operand
		decl, ok := a.resolveReadOnlyScanCallee(&plain)
		if !ok || decl == nil || len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 ||
			len(spec.TypeArgs) != len(decl.RegionParams) {
			return nil, false
		}
		return decl, true
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil || len(a.ufcsFunctionsByName[ident.Name]) > 1 {
		return nil, false
	}
	if _, local := a.currentScope.Lookup(ident.Name); local {
		if decl, ok := a.resolveDirectCallFuncDecl(call); ok {
			return decl, true
		}
		// A module-qualified identifier can be present in the active scope as a
		// namespace/member symbol rather than a FuncDecl. Give the exact qualified
		// declaration index a chance before treating it as an indirect call.
		return a.resolveQualifiedStoreFlowCallee(call, ident.Name)
	}
	if strings.Contains(ident.Name, ".") {
		if decl, ok := a.resolveQualifiedStoreFlowCallee(call, ident.Name); ok {
			return decl, true
		}
	}
	if a.globalScope == nil {
		return nil, false
	}
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolFunc {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	return decl, ok && decl != nil
}

// resolveQualifiedStoreFlowCallee resolves only an exact module-qualified spelling. The
// StoreFlow index can find candidates by their final segment, so validate the full symbol name
// here before using its body summary for a borrow-safety decision.
func (a *Analyzer) resolveQualifiedStoreFlowCallee(call *ast.CallExpr, name string) (*ast.FuncDecl, bool) {
	if !strings.Contains(name, ".") {
		return nil, false
	}
	decl := a.storeFlowCallee(call)
	if decl == nil {
		return nil, false
	}
	symbol := a.funcDeclSymbols[decl]
	return decl, symbol != nil && symbol.Name == name
}

// callArgBindsReadOnlyParam reports whether argument index of a direct call to a declared
// function binds a plain by-value parameter (not `mutable`, lmut, a reference or a view), through
// which the callee cannot change the argument's elements.
func (a *Analyzer) callArgBindsReadOnlyParam(call *ast.CallExpr, index int) bool {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || index >= len(decl.Params) {
		return false
	}
	param := decl.Params[index]
	if param.Mutable {
		return false
	}
	switch param.Type.(type) {
	case *ast.NamedType, *ast.GenericType, *ast.BuiltinTypeExpr, *ast.RefType:
	default:
		return false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Variadic || index >= len(fnType.Params) {
		return false
	}
	switch pt := fnType.Params[index].(type) {
	case *RefType:
		// A non-mutable reference can neither grow nor rewrite the container.
		return !pt.Mutable && !pt.Linear
	case *ViewType:
		return !pt.Mutable
	}
	return true
}

// callArgBindsByValueParam reports whether argument index of a direct call binds a by-value
// parameter: not `mutable`, not a reference, not a view of the argument's storage.
func (a *Analyzer) callArgBindsByValueParam(call *ast.CallExpr, index int) bool {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || index >= len(decl.Params) || decl.Params[index].Mutable {
		return false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Variadic || index >= len(fnType.Params) {
		return false
	}
	switch fnType.Params[index].(type) {
	case *RefType, *ViewType:
		return false
	}
	return true
}

// callArgFillsWritableParam reports whether argument index of a direct call binds a writable
// parameter of a callee that cannot allocate into the caller's arenas: not region-polymorphic,
// not the void grower whose ambient region is this parameter's container, no explicit region
// parameter and no Arena parameter. Such a callee's own region checks forbid storing its fresh
// data into a parameter container, so everything it can store there is reachable from its
// other arguments (or is static).
func (a *Analyzer) callArgFillsWritableParam(call *ast.CallExpr, index int) bool {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || index >= len(decl.Params) || funcHasArenaParam(decl) {
		return false
	}
	// Trusted only once the callee's body was checked and nothing it can put in the container was
	// allocated in a caller's arena (a void grower adopts its container's; a forwarded parameter
	// may reach one that does).
	// A callee not analyzed yet (declared later, or recursive and in progress) is judged by
	// whether it can reach a void grower at all.
	if len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 {
		return false
	}
	// Most arguments cannot be filled at all. Check the signature before requesting
	// a provisional body analysis, which can recursively analyze many callees.
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Variadic || fnType.RegionPolymorphic || index >= len(fnType.Params) || !a.returnBorrowWritableParam(fnType.Params[index]) {
		return false
	}
	if !functionHasOnlyInferredRegionParams(decl) {
		return false
	}
	a.ensureProvisionalSummaries(decl)
	if a.ambientFillAnalyzed[decl] {
		if a.ambientFillFresh[decl] || a.ambientFillFreshParams[decl][index] {
			return false
		}
	} else if a.fillMayAdopt(decl) {
		return false
	}
	return true
}

func functionHasOnlyInferredRegionParams(decl *ast.FuncDecl) bool {
	if decl == nil {
		return false
	}
	if len(decl.RegionParams) == 0 {
		return true
	}
	inferred := make(map[string]bool, len(decl.InferredRegionParams))
	for _, name := range decl.InferredRegionParams {
		inferred[name] = true
	}
	for _, name := range decl.RegionParams {
		if !inferred[name] {
			return false
		}
	}
	return true
}

// recordCallFilledElementStates merges, for each tracked local container a call may fill, the
// provenance of every other argument into the container's element state; a container the
// callee is not known to fill only from its arguments stops being tracked.
func (a *Analyzer) recordCallFilledElementStates(call *ast.CallExpr) {
	if a.currentElementStates == nil || a.currentScope == nil || call == nil || len(call.ArgNames) != 0 || call.HasArgForward {
		return
	}
	if a.isBuiltinDarrayCopyInCall(call) && a.currentElementExtendSources[call.Args[0]] {
		// `ys.extend(xs)` / `ys.push(xs)` only READ xs (its elements are copied by value into
		// ys, whose state recordLocalContainerElementPush already updated): xs keeps its state.
		return
	}
	for index, arg := range call.Args {
		ident, ok := stripAddrAndParens(arg).(*ast.Ident)
		if !ok {
			continue
		}
		sym, ok := a.currentScope.Lookup(ident.Name)
		if !ok {
			continue
		}
		existing, tracked := a.currentElementStates[sym]
		if !tracked || a.callArgBindsReadOnlyParam(call, index) {
			continue
		}
		if !a.callArgFillsWritableParam(call, index) {
			a.checkElementLoopAssumptions(sym, call, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			continue
		}
		merged := existing
		known := true
		// Gather read-only argument states before merging. Folding each source into
		// an ever-growing union repeatedly copies the same dependency/field maps.
		var inlineStates [4]regionRefState
		states := inlineStates[:1]
		states[0] = existing
		for other, otherArg := range call.Args {
			if other == index {
				continue
			}
			// A `darray[sview]`, or a container of non-byte scalars (`darray[u32]`), holds no bytes an
			// sview could view: what it can hand
			// the filled container is its ELEMENTS (merged below), not its own storage.
			ownStorageIsOnlyElements := false
			if id, isIdent := stripAddrAndParens(otherArg).(*ast.Ident); isIdent && containerElementsHoldOnlyViews(sym.Type) {
				if otherSym, found := a.currentScope.Lookup(id.Name); found && otherSym != nil && (isSviewDarrayType(otherSym.Type) || isNonByteScalarContainerType(otherSym.Type)) {
					ownStorageIsOnlyElements = true
				}
			} else if isIdent {
				// A filled container whose elements are not view-only (`darray[Ast::Expr]`) still
				// cannot receive a `darray[sview]`/`darray[u32]` argument's STORAGE when no part of
				// its element type can hold that container's header, a reference or a generic view:
				// that storage has no bytes an sview could view either.
				if otherSym, found := a.currentScope.Lookup(id.Name); found && otherSym != nil {
					if elemName, ok := headerOnlyDarrayElemTypeName(otherSym.Type); ok {
						if filledElem, ok := darrayElemType(sym.Type); ok && !typeMayReachDarrayStorage(filledElem, elemName) {
							ownStorageIsOnlyElements = true
						}
					}
				}
			}
			if state, ok := a.regionRefStateForExpr(otherArg); ok && !ownStorageIsOnlyElements && hasRegionProvenance(state) {
				states = append(states, state)
			}
			if otherIdent, ok := stripAddrAndParens(otherArg).(*ast.Ident); ok {
				if otherSym, ok := a.currentScope.Lookup(otherIdent.Name); ok {
					if elems, ok := a.currentElementStates[otherSym]; ok && hasRegionProvenance(elems) {
						states = append(states, elems)
					}
				}
			}
		}
		if len(states) > 1 {
			merged, known = mergeCallElementStates(states)
		}
		if !known {
			a.checkElementLoopAssumptions(sym, call, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			continue
		}
		a.checkElementLoopAssumptions(sym, call, merged, true)
		a.currentElementStates[sym] = merged
		a.traceElemState(sym, 1069)
	}
}

// Flat direct provenance survives every intermediate union. Nested field merges
// have order-sensitive normalization and sticky unknown verdicts, so retain their fold.
func mergeCallElementStates(states []regionRefState) (regionRefState, bool) {
	if len(states) == 0 {
		return regionRefState{}, false
	}
	direct := len(states) > 1
	for _, state := range states {
		direct = direct && state.Fields == nil && (hasRegionDependencies(state) || hasRegionParamDependencies(state))
	}
	if direct {
		return mergeRegionRefStates(states...)
	}
	merged, known := states[0], true
	for _, state := range states[1:] {
		if next, ok := mergeRegionRefStates(merged, state); ok {
			merged = next
		} else {
			known = false
		}
	}
	return merged, known
}

// isBuiltinDarrayCopyInCall reports whether call is the builtin `recv.extend(src)` or
// `recv.push(src)` on a darray receiver: a call whose single argument is only read.
// Callers additionally require the source to be a modelled extend source.
func (a *Analyzer) isBuiltinDarrayCopyInCall(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || len(call.Args) != 1 || (field.Field != "extend" && field.Field != "push") {
		return false
	}
	if _, isDArray := stripRefForBounds(a.exprType(field.Object)).(*DArrayType); !isDArray {
		return false
	}
	return a.returnBorrowBuiltinMethodCall(call)
}

// noteAmbientFillFresh records that the current void grower used its adoption sanction: data
// allocated in the adopted arena may reach its grown container.
func (a *Analyzer) noteAmbientFillFresh() {
	if a.currentFuncDecl == nil || a.summariesOff() {
		return
	}
	if a.ambientFillFresh == nil {
		a.ambientFillFresh = map[*ast.FuncDecl]bool{}
	}
	a.ambientFillFresh[a.currentFuncDecl] = true
}

// noteAmbientFillFreshParam records that one parameter container of the current function may
// receive data allocated in a caller's arena (see ambientFillFreshParams).
func (a *Analyzer) noteAmbientFillFreshParam(index int) {
	if a.currentFuncDecl == nil || a.summariesOff() {
		return
	}
	if a.ambientFillFreshParams == nil {
		a.ambientFillFreshParams = map[*ast.FuncDecl]map[int]bool{}
	}
	if a.ambientFillFreshParams[a.currentFuncDecl] == nil {
		a.ambientFillFreshParams[a.currentFuncDecl] = map[int]bool{}
	}
	a.ambientFillFreshParams[a.currentFuncDecl][index] = true
}

// noteAmbientCallFill marks the current function fresh at a call that may allocate in a
// caller's arena: in a void grower, a region-polymorphic callee (its threaded region is the
// adopted one); in any function, a parameter (or a reference that may alias one) passed to a
// writable parameter of a callee not known to fill it only from its other arguments.
func (a *Analyzer) noteAmbientCallFill(call *ast.CallExpr) {
	fn := a.currentFuncDecl
	if fn == nil || call == nil || a.summariesOff() {
		return
	}
	// A direct self-call can only put in a container what this body puts there elsewhere: it is
	// fresh exactly when this function is, so the least fixpoint leaves it out.
	if decl, ok := a.resolveReadOnlyScanCallee(call); ok && decl == fn {
		return
	}
	fnType := a.returnBorrowCalleeSignature(call)
	if fn.AmbientGrownContainerRegion != "" && fnType != nil && fnType.RegionPolymorphic {
		a.noteAmbientFillFresh()
		return
	}
	for index, arg := range returnBorrowCallArgs(call) {
		ident, ok := returnBorrowStripParens(arg).(*ast.Ident)
		if !ok || a.currentScope == nil {
			continue
		}
		// A parameter, or a reference local that may alias one.
		sym, found := a.currentScope.Lookup(ident.Name)
		if !found || sym == nil {
			continue
		}
		if _, isRef := sym.Type.(*RefType); sym.Kind != SymbolParam && !isRef {
			continue
		}
		if fnType == nil && a.returnBorrowBuiltinMethodCall(call) {
			// A builtin method only reads its arguments (`local.extend(param)`); what it writes is its
			// receiver, which is not among them.
			continue
		}
		if fnType == nil || index >= len(fnType.Params) {
			a.noteAmbientFillFresh()
			return
		}
		if !a.returnBorrowWritableParam(fnType.Params[index]) {
			continue
		}
		if len(call.ArgNames) != 0 || call.HasArgForward || index >= len(call.Args) || call.Args[index] != arg {
			a.noteAmbientFillFresh()
			return
		}
		if !a.callArgFillsWritableParam(call, index) {
			// The callee may put caller-arena data into THIS writable argument (and its other
			// writable arguments). When it is one of our own parameters, only that parameter
			// container becomes fresh; a reference local that may alias one keeps the
			// whole-function verdict.
			if sym.Kind == SymbolParam && sym.ParamIndex >= 0 {
				a.noteAmbientFillFreshParam(sym.ParamIndex)
				continue
			}
			a.noteAmbientFillFresh()
			return
		}
	}
}
