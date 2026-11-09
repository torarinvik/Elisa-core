package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"os"
	"reflect"
)

func (a *Analyzer) typeHasWritableBorrow(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *RefType:
		return tt.Mutable || a.typeHasWritableBorrow(tt.Elem, seen)
	case *ViewType:
		return tt.Mutable
	case *OptionalType:
		return a.typeHasWritableBorrow(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.typeHasWritableBorrow(field.Type, seen) {
				return true
			}
		}
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeHasWritableBorrow(field.Type, seen) {
				return true
			}
		}
	case *ErrorUnionType:
		return a.typeHasWritableBorrow(tt.Value, seen)
	case *GenericInstanceType, *TypeParamType:
		return true
	}
	return false
}

// collectReturnBorrowNodeNames collects every identifier and every string field under a node:
// a statement kind the walker skips may name a local through either.
func collectReturnBorrowNodeNames(v reflect.Value, out map[string]bool) {
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.String:
			if s := v.String(); s != "" {
				out[s] = true
			}
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
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
	walk(v)
}

func (a *Analyzer) errorReportedAt(pos lexer.Pos) bool {
	for key := range a.reportedDiagnostics {
		if key.Pos == pos && key.Severity == DiagnosticSeverityError {
			return true
		}
	}
	return false
}

// queueCallArgumentStoreChecks: a call whose `mutable T&` parameter receives storage that outlives
// the function (a parameter's referent, a global) may store a borrow of another argument there.
// `fill(&frame_local, out)` with fill pushing `&h.buf[0]` into out leaves the caller's out holding a
// pointer into this frame. The callee's own checks accept the store (both are its parameters), so
// the caller checks what the call may store through each such argument.
func (a *Analyzer) queueCallArgumentStoreChecks(call *ast.CallExpr) {
	if a == nil || call == nil || a.suppressDiagnostics || a.staticContextDepth != 0 || a.currentFuncDecl == nil {
		return
	}
	fnType := a.returnBorrowCalleeSignature(call)
	if fnType == nil {
		return
	}
	args := returnBorrowCallArgs(call)
	for index, paramType := range fnType.Params {
		if index >= len(args) {
			break
		}
		if !a.returnBorrowWritableParam(paramType) {
			continue
		}
		// Writability describes what the argument permits, not what this callee actually does.
		// A settled body summary can rule out retention through a read-only parameter. Keep the
		// check conservative for unresolved/ambiguous calls, arena-polymorphic functions, and
		// unsettled summaries; those cases may still write through the argument.
		if callee, ok := a.resolveDirectCallFuncDecl(call); ok && callee != nil &&
			storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) {
			if summary := a.storeFlowSummaryFor(callee); summary != nil && !summary.writtenParam(index) {
				continue
			}
		}
		// A verified TaskGroup window registers this exact pool_submit1 handle
		// and joins it before the enclosing function can return. That discharges
		// the pool's retention of the submitted capture; it does not change the
		// flow-insensitive store summary or any other writable argument.
		if index == 0 {
			if _, joined := a.taskGroupSubmits[call]; joined {
				continue
			}
		}
		place := returnBorrowStripAddr(args[index])
		if place == nil || !a.lvalueStorageOutlivesFunction(place) {
			continue
		}
		message := fmt.Sprintf("call may store a reference to function-local storage into longer-lived storage through argument %d", index+1)
		check := pendingLocalBorrowStore{value: call, message: message, call: call, callArg: index}
		if a.deferLocalBorrowCheck(check) {
			continue
		}
		if a.returnBorrowCallArgStoreFlow(call, index).Local {
			if storeScanTrace {
				a.traceLocalBorrowStoreArgs(call, index)
			}
			a.errorf(call.Pos(), "%s; it dangles once the function returns. Store the value or owner by value, or clone it into a longer-lived region", message)
		}
	}
}

func (a *Analyzer) returnBorrowCallArgStoreFlow(call *ast.CallExpr, index int) returnBorrowFlow {
	fnType := a.returnBorrowCalleeSignature(call)
	if fnType == nil {
		return returnBorrowFlow{}
	}
	if index >= 0 && index < len(fnType.Params) {
		// Only `heap`/`static` references inside the pointee (`first: mutable heap R&?`): nothing
		// stored through the parameter can point into a frame.
		var pointee Type
		switch pt := fnType.Params[index].(type) {
		case *RefType:
			pointee = pt.Elem
		case *ViewType:
			pointee = pt.Elem
		}
		if pointee != nil && containsTypeParam(pointee) && index < len(returnBorrowCallArgs(call)) {
			// A generic pointee: the argument's own type is the instantiation.
			if at, ok := a.lookupExprType(returnBorrowStripAddr(returnBorrowCallArgs(call)[index])); ok && at != nil {
				if rt, isRef := a.exprType(returnBorrowCallArgs(call)[index]).(*RefType); isRef && rt.Elem != nil {
					pointee = rt.Elem
				} else if _, isRef := at.(*RefType); !isRef {
					pointee = at
				}
			}
		}
		if pointee != nil && !containsTypeParam(pointee) && !a.typeMayHoldFrameBorrow(pointee, map[Type]bool{}) {
			return returnBorrowFlow{}
		}
	}
	args := returnBorrowCallArgs(call)
	// A summarized callee states which arguments' contents may reach the written parameter; the
	// others cannot be stored through it.
	keep, contentsOnly := a.returnBorrowCallStorePredicates(call, index)
	flow := a.returnBorrowCallOtherArgsFlowWhereKinds(args, index, fnType.Params, keep, contentsOnly, map[string]returnBorrowFlow{}, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
	return flow
}

// returnBorrowCallStorePredicates resolves the callee once for a transfer.
// An in-progress flow summary may be replaced while the component settles;
// only the published summary can be reused across arguments.
func (a *Analyzer) returnBorrowCallStorePredicates(call *ast.CallExpr, index int) (keep, contentsOnly func(int) bool) {
	var decl *ast.FuncDecl
	resolved := false
	resolve := func() *ast.FuncDecl {
		if !resolved {
			resolved = true
			decl = a.storeFlowCalleeForArgCheck(call)
		}
		return decl
	}
	var flow *storeFlowSummary
	settled := false
	keep = func(other int) bool {
		if !settled {
			if target := resolve(); target != nil {
				flow = a.storeFlowSummaryFor(target)
				_, settled = a.storeFlowSummaries[a.readDecl(target)]
			}
		}
		return flow == nil || flow.reaches(other, index)
	}
	if a.storeFlowKindsOff {
		return keep, nil
	}
	var kinds *storeFlowSummary
	kindsResolved := false
	contentsOnly = func(other int) bool {
		if !kindsResolved {
			kindsResolved = true
			if target := resolve(); target != nil {
				kinds = a.storeFlowKindsFor(target)
			}
		}
		if kinds == nil {
			return false
		}
		kind := a.storeFlowReachKind(kinds, other, index)
		return kind == storeFlowEdgeElements || kind == storeFlowEdgeNone
	}
	return keep, contentsOnly
}

// traceLocalBorrowStoreArgs (trace only): which other argument's flow is frame-local.
func (a *Analyzer) traceLocalBorrowStoreArgs(call *ast.CallExpr, index int) {
	fnType := a.returnBorrowCalleeSignature(call)
	if fnType == nil {
		return
	}
	args := returnBorrowCallArgs(call)
	keep, contentsOnly := a.returnBorrowCallStorePredicates(call, index)
	for other := range args {
		if other == index || !keep(other) {
			continue
		}
		f := a.returnBorrowCallOtherArgsFlowWhereKinds(args, index, fnType.Params, func(k int) bool { return k == other }, contentsOnly, map[string]returnBorrowFlow{}, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
		fmt.Fprintf(os.Stderr, "LOCALSTORE at %s into=%d from=%d local=%v arg=%s type=%s\n", call.Pos(), index+1, other+1, f.Local, args[other].Pos(), typeString(a.exprType(args[other])))
		if f.Local {
			a.traceLocalFlowExplain(args[other], 3, "  ")
		}
	}
}

// traceLocalFlowExplain (trace only): why expr's return-borrow flow is frame-local -- a local's
// initializer, a call's arguments, recursively.
func (a *Analyzer) traceLocalFlowExplain(expr ast.Expr, depth int, indent string) {
	if expr == nil || depth < 0 {
		return
	}
	f := a.returnBorrowFlowForExpr(expr, nil, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
	fmt.Fprintf(os.Stderr, "%sEXPLAIN %T at %s local=%v params=%v type=%s\n", indent, expr, expr.Pos(), f.Local, f.Params, typeString(a.exprType(expr)))
	if !f.Local {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if a.currentFuncDecl != nil {
			idx := a.storeScanIndex(a.currentFuncDecl)
			for _, v := range idx.decls[e.Name] {
				if v.Value != nil {
					a.traceLocalFlowExplain(v.Value, depth-1, indent+"  ")
				}
			}
			if idx.otherBound[e.Name] {
				fmt.Fprintf(os.Stderr, "%s  (bound by a pattern/loop)\n", indent)
			}
		}
	case *ast.CallExpr:
		for _, arg := range e.Args {
			a.traceLocalFlowExplain(arg, depth-1, indent+"  ")
		}
	case *ast.FieldExpr:
		a.traceLocalFlowExplain(e.Object, depth-1, indent+"  ")
	case *ast.IndexExpr:
		a.traceLocalFlowExplain(e.Object, depth-1, indent+"  ")
	}
}
