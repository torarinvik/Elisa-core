package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) returnBorrowFlowForCall(call *ast.CallExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	// These builtin views borrow the receiver's backing storage. Their implicit
	// receiver is absent from call.Args, so ordinary argument tracing misses it.
	if builtin, ok := a.exprType(call.Func).(*FuncType); ok && builtin != nil &&
		(builtin.Name == "darray.sview" || builtin.Name == "darray.cstr") {
		if field, ok := call.Func.(*ast.FieldExpr); ok {
			return a.returnBorrowAddressFlow(field.Object, aliases, active, localBindings)
		}
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		if ident, isIdent := call.Func.(*ast.Ident); isIdent && ident != nil {
			shadowed := false
			if a.currentScope != nil {
				_, shadowed = a.currentScope.Lookup(ident.Name)
			}
			if !shadowed {
				if sym, _, visible := a.lookupVisibleGlobal(ident.Name); visible && sym != nil {
					decl, ok = sym.Node.(*ast.FuncDecl)
				}
			}
		}
	}
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	if ok && decl != nil {
		var calleeType *FuncType
		if sym := a.funcDeclSymbols[decl]; sym != nil {
			calleeType, _ = sym.Type.(*FuncType)
		}
		// A declared program-lived view cannot retain the caller's stack.
		// The callee's body still validates its return contract independently.
		if calleeType != nil && a.isProgramRegion(calleeType.ReturnRegion) &&
			!returnRegionListContains(calleeType.RegionParams, calleeType.ReturnRegion) {
			if view, ok := calleeType.Return.(*SViewType); ok && a.isProgramRegion(view.Region) {
				if a.retained != nil {
					a.retained.noteReturnBorrowDep(decl)
				}
				return returnBorrowFlow{}
			}
		}
		summary := a.returnBorrowFlowForFunc(decl, active)
		flow := returnBorrowFlow{Local: summary.Local}
		for index := range summary.Params {
			if index >= len(args) {
				continue
			}
			// A by-value argument whose binding is tracked hands the callee its VALUE: the result
			// can hold only what that value holds, never the local's address. (`close(token, items)`
			// with `items: darray[T]` returning `Expr.Array(items)`.) A reference parameter still
			// borrows the local itself.
			if calleeType != nil && index < len(calleeType.Params) && calleeType.Params[index] != nil {
				if _, isRef := calleeType.Params[index].(*RefType); !isRef && a.returnBorrowTrackedLocalArg(args[index], aliases) {
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(args[index], aliases, active, localBindings))
					continue
				}
				// A result that cannot point at the referent (`snap(n: darray[sview]&) -> darray[sview]`
				// called on `&local`) carries only what the referent holds, never its address.
				if ref, isRef := calleeType.Params[index].(*RefType); isRef && ref != nil && ref.Elem != nil && calleeType.Return != nil && !a.returnBorrowResultMayTarget(calleeType.Return, ref.Elem) {
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowReferentContentsFlow(args[index], aliases, active, localBindings))
					continue
				}
			}
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForArgument(args[index], aliases, active, localBindings))
		}
		for index := range summary.Contents {
			if index >= len(args) {
				continue
			}
			if calleeType != nil && index < len(calleeType.Params) && calleeType.Params[index] != nil {
				if _, isRef := calleeType.Params[index].(*RefType); !isRef {
					// A by-value parameter's contents are the passed value's own flow.
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(args[index], aliases, active, localBindings))
					continue
				}
			}
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForArgumentContents(args[index], aliases, active, localBindings))
		}
		return flow
	}
	// Indirect and external calls without a source summary are unknown. Preserve
	// every argument's local-borrow taint rather than silently treating the result
	// as fresh; this can conservatively reject a safe call but cannot bless a leak.
	flow := returnBorrowFlow{}
	payload := a.returnBorrowVariantPayloadTypes(call, args)
	for index, arg := range args {
		if payload != nil {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForCopiedValue(arg, payload[index], aliases, active, localBindings))
			continue
		}
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
	}
	if a.returnBorrowRecordingActive() || a.returnBorrowEnvActive() {
		// A builtin method's result (`xs.pop()`, `m.get(k)`) can be anything its receiver holds.
		if field, isField := call.Func.(*ast.FieldExpr); isField && field != nil && a.returnBorrowBuiltinMethodCall(call) {
			if resultType := a.exprType(call); resultType == nil || a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
				flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(field.Object, aliases, active, localBindings))
			}
		}
	}
	return flow
}

// returnBorrowVariantPayloadTypes is the payload types, one per argument, of a positional enum
// variant construction (`E.V(a, b)`); nil for any other call.
func (a *Analyzer) returnBorrowVariantPayloadTypes(call *ast.CallExpr, args []ast.Expr) []Type {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || len(call.ArgNames) != 0 || !a.returnBorrowTypeNameObject(field.Object) {
		return nil
	}
	enumType, ok := a.exprType(call).(*EnumType)
	if !ok || enumType == nil {
		return nil
	}
	variant := enumType.VariantMap[field.Field]
	if variant == nil || len(variant.Payload) != len(args) {
		return nil
	}
	return variant.Payload
}

// returnBorrowTrackedLocalArg reports an argument that is a plain local identifier whose flow the
// summary tracks (declared in this body, so every store into it merged into its alias).
func (a *Analyzer) returnBorrowTrackedLocalArg(arg ast.Expr, aliases map[string]returnBorrowFlow) bool {
	for {
		switch n := arg.(type) {
		case *ast.ParenExpr:
			arg = n.Inner
			continue
		case *ast.MoveExpr:
			arg = n.Operand
			continue
		}
		break
	}
	ident, ok := arg.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return false
	}
	if a.returnBorrowEnvActive() {
		if root, _, trusted := a.returnBorrowEnvTrustedRoot(ident); trusted && root == ident {
			return true
		}
	}
	if _, tracked := aliases[ident.Name]; !tracked {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	return ok && sym != nil && sym.Kind == SymbolLocal
}

func (a *Analyzer) returnBorrowFlowForArgument(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	argFlow := a.returnBorrowFlowForExpr(arg, aliases, active, localBindings)
	if argFlow.empty() {
		argFlow = a.returnBorrowFlowForBorrowedArgument(arg, aliases, active, localBindings)
	}
	return argFlow
}

// returnBorrowFlowForArgumentContents maps a callee's Contents[i] to the call site: the borrows
// stored in the argument's referent. For `&x` (explicit, or the implicit borrow of a `T&`
// parameter) that is x's own flow -- what was stored in x -- rather than x's address. Any other
// argument falls back to the argument's own flow, which over-approximates its contents.
func (a *Analyzer) returnBorrowFlowForArgumentContents(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	inner := arg
	for {
		paren, ok := inner.(*ast.ParenExpr)
		if !ok || paren == nil {
			break
		}
		inner = paren.Inner
	}
	operand := inner
	if addr, ok := inner.(*ast.AddrOfExpr); ok && addr != nil {
		operand = addr.Operand
	}
	if a.returnBorrowEnvActive() {
		if ident, flow, trusted := a.returnBorrowEnvTrustedRoot(operand); trusted && ident == returnBorrowStripParens(operand) {
			return flow
		}
	}
	// `&local` lends a tracked local: its referent holds exactly the local's tracked value (every
	// store through a writable argument merges into it), never the local's own address.
	if a.returnBorrowTrackedLocalArg(operand, aliases) {
		return a.returnBorrowFlowForExpr(operand, aliases, active, localBindings)
	}
	// A reference parameter passed on (`f(names)` with `names: T&`) lends its referent, whose
	// contents are the caller's Contents[i], not the parameter's address.
	if flow := a.returnBorrowReferentContentsFlow(operand, aliases, active, localBindings); !flow.empty() {
		return flow
	}
	return a.returnBorrowFlowForArgument(operand, aliases, active, localBindings)
}

// returnBorrowContentReadParam reports a read, inside a return-borrow summary, of a reference or
// view VALUE held in a reference parameter's pointee (`a.owner`, `a.inner.r`, `items[i].name`
// with `a: A&`, `items: darray[A]&`). Only a read whose own type is reference-like qualifies: an
// aggregate element (`xs[0]` returned as `A&`) may be an address into the pointee itself. The
// parameter's alias must still be its entry flow, so a rebound `a <- &local` is not mistaken for
// caller contents.
func (a *Analyzer) returnBorrowContentReadParam(read ast.Expr, readType Type, aliases map[string]returnBorrowFlow) (int, bool) {
	if readType == nil || !isBorrowLikeType(readType) {
		return 0, false
	}
	return a.returnBorrowPointeeReadParam(read, aliases)
}

// returnBorrowPointeeReadParam reports the reference parameter whose pointee read reads from.
func (a *Analyzer) returnBorrowPointeeReadParam(read ast.Expr, aliases map[string]returnBorrowFlow) (int, bool) {
	if !a.returnBorrowContentReads || aliases == nil || a.currentScope == nil {
		return 0, false
	}
	root := read
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
			continue
		case *ast.FieldExpr:
			root = n.Object
			continue
		case *ast.IndexExpr:
			root = n.Object
			continue
		}
		break
	}
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || ident == read {
		return 0, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return 0, false
	}
	if _, isRef := sym.Type.(*RefType); !isRef {
		return 0, false
	}
	flow, ok := aliases[ident.Name]
	// Stored CONTENTS (`p <- p.step()` writing a copy of the pointee back through `p`) keep the
	// alias an address of the caller's referent; a stored local or other address does not.
	if !ok || flow.Local || len(flow.Params) != 1 || !flow.Params[sym.ParamIndex] {
		return 0, false
	}
	return sym.ParamIndex, true
}

// returnBorrowPointeeReadFlow is the flow of a read from reference parameter index's pointee:
// the caller's contents plus whatever the body stored through the parameter since.
func returnBorrowPointeeReadFlow(read ast.Expr, index int, aliases map[string]returnBorrowFlow) returnBorrowFlow {
	flow := returnBorrowFlow{Contents: map[int]bool{index: true}}
	if root := returnBorrowPlaceRootName(read); root != "" {
		for stored := range aliases[root].Contents {
			flow.Contents[stored] = true
		}
	}
	return flow
}

// returnBorrowFlowForCopiedValue is the flow of an operand copied by value into a place whose
// type is not a reference (a returned value, an enum payload, a tuple or list element). A copy of
// a field or element of a reference parameter's pointee holds what the pointee holds -- the
// caller's contents -- never the pointee's address, whatever the copied type is.
func (a *Analyzer) returnBorrowFlowForCopiedValue(expr ast.Expr, placeType Type, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	if placeType != nil && !isBorrowLikeType(placeType) {
		inner := returnBorrowStripParens(expr)
		switch inner.(type) {
		case *ast.FieldExpr, *ast.IndexExpr:
			if readType := a.exprType(inner); readType != nil && !isBorrowLikeType(readType) {
				if index, ok := a.returnBorrowPointeeReadParam(inner, aliases); ok {
					return returnBorrowPointeeReadFlow(inner, index, aliases)
				}
			}
		}
	}
	return a.returnBorrowFlowForExpr(expr, aliases, active, localBindings)
}

func (a *Analyzer) returnBorrowFlowForBorrowedArgument(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	root := arg
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
		case *ast.CastExpr:
			root = n.Operand
		case *ast.MoveExpr:
			root = n.Operand
		case *ast.FieldExpr:
			root = n.Object
		case *ast.IndexExpr:
			root = n.Object
		default:
			goto rooted
		}
	}
rooted:
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return returnBorrowFlow{}
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return returnBorrowFlow{}
	}
	switch sym.Type.(type) {
	case *DArrayType:
		if sym.Kind == SymbolParam {
			return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
		}
		if sym.Kind == SymbolLocal {
			return returnBorrowFlow{Local: true}
		}
	}
	if sym.Kind == SymbolParam && a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{}) {
		return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
	}
	if sym.Kind == SymbolLocal {
		if flow, exists := aliases[ident.Name]; exists {
			return flow
		}
		if source, iterBinding := a.iterBindingSource(sym); iterBinding && !localBindings[sym] {
			localBindings[sym] = true
			flow := a.returnBorrowFlowForArgument(source, aliases, active, localBindings)
			delete(localBindings, sym)
			return flow
		}
		if _, knownBinding := a.currentValueBindings[sym]; !knownBinding && a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{}) {
			return returnBorrowFlow{Local: true}
		}
	}
	return returnBorrowFlow{}
}

func (a *Analyzer) borrowedContainerParamRoot(expr ast.Expr) *Symbol {
	root := expr
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
		case *ast.CastExpr:
			root = n.Operand
		case *ast.FieldExpr:
			root = n.Object
		case *ast.IndexExpr:
			root = n.Object
		default:
			goto rooted
		}
	}
rooted:
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return nil
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return nil
	}
	switch sym.Type.(type) {
	case *DArrayType:
	case *RefType, *ViewType:
		// A place reached through a reference or view parameter (`&a[0]` with `a: u8[4]&`) lives
		// in the caller's referent: its address flows from that parameter.
	default:
		return nil
	}
	return sym
}

func (a *Analyzer) beginReturnBorrowSummary(fn *ast.FuncDecl) {
	if fn == nil {
		return
	}
	delete(a.returnBorrowFuncAnalyzed, fn)
	delete(a.returnBorrowFuncSummaries, fn)
	a.pushReturnBorrowDeferFrame(fn)
	if a.currentScope != nil && a.currentScope != a.globalScope {
		a.currentScope.returnBorrowFrame = a.returnBorrowDeferFrames[len(a.returnBorrowDeferFrames)-1]
	}
}

func (a *Analyzer) finishReturnBorrowSummary(fn *ast.FuncDecl) {
	if fn == nil {
		return
	}
	if a.returnBorrowFuncAnalyzed == nil {
		a.returnBorrowFuncAnalyzed = map[*ast.FuncDecl]bool{}
	}
	a.returnBorrowFuncAnalyzed[fn] = true
	a.popReturnBorrowDeferFrame(fn)
}

// returnBorrowQueryEntry is a summary memoized within one top-level query. A summary cut short by
// recursion depends on which callers were active: it is reused only while every function it was
// cut on is still active (the recomputation would cut at the same places). A summary cut by the
// depth limit is never reused -- a shallower call would have computed it exactly.
type returnBorrowQueryEntry struct {
	flow     returnBorrowFlow
	cutOn    []*ast.FuncDecl
	depthCut bool
}
