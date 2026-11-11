package semantic

import (
	"elisacore/src/ast"
)

// Store environment for the local-borrow escape checks.
//
// A push/store check (`out.push(v)` with `out` outliving the frame) asks whether v may hold a
// borrow of this frame. v is usually read out of a LOCAL: a loop binding over a local container, a
// field of a local copy, an element of a local darray. What that local holds is the union of
// everything ever stored into it, which an expression-only walk cannot see: it either assumed the
// worst (every local darray "holds local borrows", rejecting the whole self-host) or read only the
// declaration's initial value (accepting `h.r <- &x; out.push(h)`).
//
// The checks are therefore deferred to the end of the function. The environment is computed once
// per function by the statement walker that builds return-borrow summaries, in RECORDING mode: for
// every local binding (declaration, loop binding, tuple binding) it is the may-union of the bound
// value and of every later store into it -- assignments to it or a place rooted at it, retaining
// container methods, `+=`, and calls that may write their other arguments through a `mutable T&`
// parameter. Reads of a binding inside the walk see the union so far, and the walk repeats until
// nothing grows, so a store later in a loop body reaches a read earlier in it.
//
// The union is only as complete as the walker. A binding is TRUSTED only when its name has no write
// the walker does not model anywhere in the function: a statement kind the walker skips, a
// statement nested inside an expression (lambdas, value blocks), an address-of outside a call
// argument, a reference binding, a call the walker did not process, or a call that may return a
// mutable reference into it. Untrusted bindings keep the conservative expression walk, so the
// environment can only replace a worst-case assumption with the exact union, never drop a store.
// Binders the walker does not model at all (match arms, `is` bindings) read conservatively while
// recording, and a mutable one of those makes the whole function's environment empty.

type returnBorrowBinderKey struct {
	node ast.Node
	name string
}

type pendingLocalBorrowStore struct {
	value         ast.Expr
	message       string
	scope         *Scope
	valueBindings map[*Symbol]ast.Expr
	// regionDependent: the region escape machinery also checked the value; report only when it
	// did not report at the same position.
	regionDependent bool
	// summaryGated: a return check. It reports only when the whole-function summary also carries
	// a local borrow: the environment narrows the summary to this return path, never widens it.
	summaryGated bool
	// call is a call whose writable argument callArg outlives the function: the check is what the
	// call may store through it, not the value's own flow.
	call    *ast.CallExpr
	callArg int
}

type returnBorrowDeferFrame struct {
	collecting bool // active only while this function body defines binders
	fn         *ast.FuncDecl
	pending    []pendingLocalBorrowStore
	// binders are the non-declaration local bindings the body's analysis defined, by name.
	binders          map[string][]*Symbol
	snapshotScopes   []*Scope
	snapshotVersions []uint64
	snapshotSymbols  map[string]*Symbol
	snapshotGlobal   *Scope
}

func (a *Analyzer) pushReturnBorrowDeferFrame(fn *ast.FuncDecl) {
	a.returnBorrowDeferFrames = append(a.returnBorrowDeferFrames, &returnBorrowDeferFrame{fn: fn, collecting: true})
}

// noteReturnBorrowBinder records a local binding defined while the current function's body is
// analyzed that is not a plain declaration.
func (frame *returnBorrowDeferFrame) noteBinder(sym *Symbol) {
	if !frame.collecting {
		return
	}
	if sym == nil || sym.Kind != SymbolLocal {
		return
	}
	if _, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl {
		return
	}
	if frame.binders == nil {
		frame.binders = map[string][]*Symbol{}
	}
	frame.binders[sym.Name] = append(frame.binders[sym.Name], sym)
}

// popReturnBorrowDeferFrame evaluates the checks deferred while fn's body was analyzed.
func (a *Analyzer) popReturnBorrowDeferFrame(fn *ast.FuncDecl) {
	count := len(a.returnBorrowDeferFrames)
	if count == 0 {
		return
	}
	frame := a.returnBorrowDeferFrames[count-1]
	frame.collecting = false
	frame.snapshotScopes = nil
	frame.snapshotVersions = nil
	frame.snapshotSymbols = nil
	frame.snapshotGlobal = nil
	a.returnBorrowDeferFrames = a.returnBorrowDeferFrames[:count-1]
	if frame.fn != fn || len(frame.pending) == 0 {
		return
	}
	if a.returnBorrowLateEnabled && a.returnBorrowFrameCanWait() {
		a.returnBorrowLateFrames = append(a.returnBorrowLateFrames, returnBorrowLateFrame{
			frame:     frame,
			fnType:    a.currentFuncType,
			namespace: a.currentNamespace,
			usings:    append([]string(nil), a.currentUsings...),
		})
		return
	}
	a.evaluateReturnBorrowDeferFrame(fn, frame)
}

// returnBorrowLateFrame is a function's deferred store checks, held until every function body has
// been analyzed.
type returnBorrowLateFrame struct {
	frame     *returnBorrowDeferFrame
	fnType    *FuncType
	namespace string
	usings    []string
}

// returnBorrowFrameCanWait reports whether the current function's checks can be evaluated after
// the whole program is analyzed. A callee summarized before its own body was analyzed has no
// expression types and falls back to its most conservative flow (a copied view field of a
// reference parameter reads as the parameter's address), so a check evaluated at the end of its
// own function wrongly rejected values that came from callees declared later. A body analyzed
// under generic, const or interface-associated bindings keeps the immediate evaluation: those
// bindings are gone once its analysis returns.
func (a *Analyzer) returnBorrowFrameCanWait() bool {
	return len(a.typeParamScopes) == 0 && len(a.constParamScopes) == 0 &&
		len(a.interfaceAssocTypeScopes) == 0 && len(a.typeParamInterfaceScopes) == 0
}

// evaluateLateReturnBorrowFrames evaluates the checks held by returnBorrowFrameCanWait. Summaries
// cached while a transitive callee was still unanalyzed are dropped so they are recomputed from
// the complete bodies.
func (a *Analyzer) evaluateLateReturnBorrowFrames() {
	frames := a.prepareLateReturnBorrowFrames()
	last := lastReturnBorrowFrames(frames)
	for index, late := range frames {
		if last[late.frame.fn] == index {
			a.evaluateLateReturnBorrowFrame(late)
		}
	}
	if len(frames) > 0 && a.retained != nil {
		a.retained.keepReturnBorrowSummaries(a.returnBorrowFuncSummaries)
	}
}

// Initialize final summaries once, after every body has been analyzed. A bounded
// initial build keeps these same summaries while saving between frame checks.
func (a *Analyzer) prepareLateReturnBorrowFrames() []returnBorrowLateFrame {
	frames := a.returnBorrowLateFrames
	a.returnBorrowLateFrames = nil
	a.returnBorrowLateEnabled = false
	if len(frames) == 0 {
		return frames
	}
	a.returnBorrowFuncSummaries = nil
	a.returnBorrowRootMemos = nil
	a.returnBorrowRootMemosFor = nil
	if r := a.retained; r != nil {
		a.returnBorrowFuncSummaries = r.reusableReturnBorrowSummaries()
		if r.rbUpdating {
			if a.returnBorrowFuncSummaries == nil {
				a.returnBorrowFuncSummaries = map[*ast.FuncDecl]returnBorrowFlow{}
			}
			r.narrowReturnBorrowInvalidation(a)
		}
	}
	return frames
}

func lastReturnBorrowFrames(frames []returnBorrowLateFrame) map[*ast.FuncDecl]int {
	// A function analyzed more than once keeps only its final analysis's checks.
	last := make(map[*ast.FuncDecl]int, len(frames))
	for index, late := range frames {
		last[late.frame.fn] = index
	}
	return last
}

func (a *Analyzer) evaluateLateReturnBorrowFrame(late returnBorrowLateFrame) {
	r := a.retained
	if r != nil && r.replayLateFrame(a, late.frame.fn) {
		return
	}
	savedFuncDecl, savedFuncType := a.currentFuncDecl, a.currentFuncType
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	savedScope, savedBindings := a.currentScope, a.currentValueBindings
	savedEnv := a.returnBorrowDeclEnv
	defer func() {
		a.currentFuncDecl, a.currentFuncType = savedFuncDecl, savedFuncType
		a.currentNamespace, a.currentUsings = savedNamespace, savedUsings
		a.currentScope, a.currentValueBindings = savedScope, savedBindings
		a.returnBorrowDeclEnv = savedEnv
	}()
	a.currentFuncDecl, a.currentFuncType = late.frame.fn, late.fnType
	a.currentNamespace, a.currentUsings = late.namespace, late.usings
	if r == nil {
		a.evaluateReturnBorrowDeferFrame(late.frame.fn, late.frame)
		return
	}
	before := len(a.diagnostics)
	r.pushReturnBorrowOwner(late.frame.fn)
	a.evaluateReturnBorrowDeferFrame(late.frame.fn, late.frame)
	r.popReturnBorrowOwner()
	r.recordLateFrame(late.frame.fn, a.diagnostics[before:])
}

// evaluateReturnBorrowDeferFrame reports the pending checks of fn's frame whose value holds a
// borrow of fn's local storage.
func (a *Analyzer) evaluateReturnBorrowDeferFrame(fn *ast.FuncDecl, frame *returnBorrowDeferFrame) {
	savedScope, savedBindings := a.currentScope, a.currentValueBindings
	// The summary gate is judged as the immediate check judged it: in the return's own scope,
	// before the environment is active.
	summaryLocal := make([]bool, len(frame.pending))
	for index, check := range frame.pending {
		if check.summaryGated {
			a.currentScope, a.currentValueBindings = check.scope, check.valueBindings
			summaryLocal[index] = a.returnBorrowFlowForFunc(fn, map[*ast.FuncDecl]bool{}).Local
		}
	}
	a.currentScope, a.currentValueBindings = savedScope, savedBindings
	savedEnv := a.returnBorrowDeclEnv
	envReady := false
	for index, check := range frame.pending {
		if check.regionDependent && a.errorReportedAt(check.value.Pos()) {
			continue
		}
		if check.summaryGated && !summaryLocal[index] {
			continue
		}
		// No declaration flow is needed if every check was already rejected
		// by the summary gate or covered by an error at the same site.
		if !envReady {
			a.returnBorrowDeclEnv = a.computeReturnBorrowDeclEnv(fn, frame)
			envReady = true
		}
		a.currentScope, a.currentValueBindings = check.scope, check.valueBindings
		var flow returnBorrowFlow
		if check.call != nil {
			flow = a.returnBorrowCallArgStoreFlow(check.call, check.callArg)
		} else {
			flow = a.returnBorrowFlowForExpr(check.value, nil, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
		}
		if flow.Local {
			if storeScanTrace && check.call != nil {
				a.traceLocalBorrowStoreArgs(check.call, check.callArg)
			}
			a.errorf(check.value.Pos(), "%s; it dangles once the function returns. Store the value or owner by value, or clone it into a longer-lived region", check.message)
		}
	}
	a.currentScope, a.currentValueBindings = savedScope, savedBindings
	a.returnBorrowDeclEnv = savedEnv
}

// deferLocalBorrowStoreCheck queues a store check for the end of the current function. It reports
// false when no frame for the current function is open; the caller then checks immediately.
func (a *Analyzer) deferLocalBorrowStoreCheck(value ast.Expr, message string, regionDependent bool) bool {
	return a.deferLocalBorrowCheck(pendingLocalBorrowStore{value: value, message: message, regionDependent: regionDependent})
}

func (a *Analyzer) deferLocalBorrowCheck(check pendingLocalBorrowStore) bool {
	if a.suppressDiagnostics {
		// A speculative analysis reports nothing; the real analysis queues its own check.
		return true
	}
	count := len(a.returnBorrowDeferFrames)
	if count == 0 || a.currentFuncDecl == nil || a.currentScope == nil {
		return false
	}
	frame := a.returnBorrowDeferFrames[count-1]
	if frame.fn != a.currentFuncDecl {
		return false
	}
	scope, ok := a.snapshotReturnBorrowScope(frame)
	if !ok {
		return false
	}
	bindings := make(map[*Symbol]ast.Expr, len(a.currentValueBindings))
	for sym, value := range a.currentValueBindings {
		bindings[sym] = value
	}
	check.scope, check.valueBindings = scope, bindings
	frame.pending = append(frame.pending, check)
	return true
}

// returnBorrowRecordingActive reports that the walker is walking the function whose environment is
// being recorded (not a callee summarized along the way).
func (a *Analyzer) returnBorrowRecordingActive() bool {
	return a.returnBorrowDeclRecording != nil && a.returnBorrowWalkFn != nil && a.returnBorrowWalkFn == a.returnBorrowDeclRecordingFn
}

// returnBorrowEnvActive reports a deferred check being evaluated against a function's environment
// (outside any summary walk).
func (a *Analyzer) returnBorrowEnvActive() bool {
	return a.returnBorrowDeclEnv != nil && a.returnBorrowWalkFn == nil
}

const returnBorrowDeclEnvPassLimit = 8

// computeReturnBorrowDeclEnv walks fn's body in recording mode until the per-binding store unions
// stop growing, and keeps the bindings whose stores the walk fully models. The result is non-nil;
// an empty map trusts nothing.
func (a *Analyzer) computeReturnBorrowDeclEnv(fn *ast.FuncDecl, frame *returnBorrowDeferFrame) map[returnBorrowBinderKey]returnBorrowFlow {
	env := map[returnBorrowBinderKey]returnBorrowFlow{}
	if fn == nil {
		return env
	}
	modeled := returnBorrowModeledBinderNodes(fn)
	// A ternary's condition unwraps and a match expression's arm binders are defined by the walk.
	a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		switch n := expr.(type) {
		case *ast.TernaryExpr:
			returnBorrowMarkConditionBinders(n.Cond, modeled)
		case *ast.MatchExpr:
			for _, arm := range n.Arms {
				for _, binder := range returnBorrowMatchPatternBinders(arm.Pattern, nil) {
					modeled[binder.node] = true
				}
			}
		}
		return false
	})
	conservative := map[string]bool{}
	for name, syms := range frame.binders {
		for _, sym := range syms {
			if modeled[sym.Node] {
				continue
			}
			// A comprehension or query binder is defined whenever the walk evaluates its
			// expression, and is only readable inside it.
			_, isComprehension := sym.Node.(*ast.ListComprehensionExpr)
			if (isComprehension || a.returnBorrowSynthesizedBinders[sym.Node]) && !sym.Mutable {
				if ref, isRef := sym.Type.(*RefType); !isRef || ref == nil || !ref.Mutable {
					continue
				}
			}
			conservative[name] = true
			// A mutable binder (or a writable reference) the walker does not model can store into
			// whatever it was bound from.
			if sym.Mutable {
				return env
			}
			if ref, isRef := sym.Type.(*RefType); isRef && ref != nil && ref.Mutable {
				return env
			}
		}
	}

	savedRecording, savedFn, savedConservative, savedChanged, savedProcessed :=
		a.returnBorrowDeclRecording, a.returnBorrowDeclRecordingFn, a.returnBorrowDeclConservative, a.returnBorrowDeclChanged, a.returnBorrowDeclProcessedCalls
	savedEnv := a.returnBorrowDeclEnv
	savedMemo, savedLog := a.returnBorrowQueryMemo, a.returnBorrowCutLog
	defer func() {
		a.returnBorrowDeclRecording, a.returnBorrowDeclRecordingFn, a.returnBorrowDeclConservative, a.returnBorrowDeclChanged, a.returnBorrowDeclProcessedCalls =
			savedRecording, savedFn, savedConservative, savedChanged, savedProcessed
		a.returnBorrowDeclEnv = savedEnv
		a.returnBorrowQueryMemo, a.returnBorrowCutLog = savedMemo, savedLog
	}()
	a.returnBorrowDeclEnv = nil
	a.returnBorrowDeclRecording = map[returnBorrowBinderKey]returnBorrowFlow{}
	a.returnBorrowDeclRecordingFn = fn
	a.returnBorrowDeclConservative = conservative
	a.returnBorrowDeclProcessedCalls = map[*ast.CallExpr]bool{}
	savedLinks := a.returnBorrowDeclRefLinks
	defer func() { a.returnBorrowDeclRefLinks = savedLinks }()
	a.returnBorrowDeclRefLinks = a.returnBorrowRefDeclLinks(fn)
	converged := false
	for pass := 0; pass < returnBorrowDeclEnvPassLimit; pass++ {
		a.returnBorrowDeclChanged = false
		a.returnBorrowQueryMemo = a.returnBorrowRootMemo(fn)
		a.returnBorrowCutLog = nil
		a.computeReturnBorrowFlowForFunc(fn, map[*ast.FuncDecl]bool{})
		if !a.returnBorrowDeclChanged {
			converged = true
			break
		}
	}
	if !converged {
		return env
	}
	untrusted := a.returnBorrowUntrustedNames(fn, a.returnBorrowDeclProcessedCalls)
	for key, flow := range a.returnBorrowDeclRecording {
		if untrusted[key.name] || conservative[key.name] {
			continue
		}
		env[key] = flow
	}
	return env
}

func returnBorrowFlowGrew(before, after returnBorrowFlow) bool {
	return (after.Local && !before.Local) || len(after.Params) > len(before.Params) || len(after.Contents) > len(before.Contents)
}

// returnBorrowDeclaredValueFlow removes expression provenance that cannot be stored in a
// binding's declared type. The expression walk can run before exprTypes is complete (notably while
// solving a callee summary), so a reference-free by-value result such as ValueType must not inherit
// every argument's borrow merely because its call is still opaque. Calls' independent store effects
// are recorded before this filter is applied.
func (a *Analyzer) returnBorrowDeclaredValueFlow(flow returnBorrowFlow, declared Type) returnBorrowFlow {
	if declared == nil || IsInvalidType(declared) || !a.typeIsBorrowFree(declared, map[Type]bool{}) {
		return flow
	}
	return returnBorrowFlow{}
}

// setReturnBorrowAlias writes a walker alias and, while recording, merges the flow into the store
// union of the binding the name currently resolves to.
func (a *Analyzer) setReturnBorrowAlias(aliases map[string]returnBorrowFlow, name string, flow returnBorrowFlow, merge bool) {
	stored := flow
	if merge {
		flow = mergeReturnBorrowFlow(aliases[name], flow)
	}
	a.writeReturnBorrowAlias(aliases, name, flow)
	if !a.returnBorrowRecordingActive() || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Kind != SymbolLocal || sym.Node == nil {
		return
	}
	key := returnBorrowBinderKey{node: sym.Node, name: name}
	before := a.returnBorrowDeclRecording[key]
	after := mergeReturnBorrowFlow(before, flow)
	if returnBorrowFlowGrew(before, after) {
		a.returnBorrowDeclChanged = true
	}
	a.returnBorrowDeclRecording[key] = after
	// A declaration's initializer is the reference itself, not a store into its referent.
	if merge {
		a.forwardReturnBorrowRefStore(name, stored, map[string]bool{name: true})
	}
}

// forwardReturnBorrowRefStore records a store through a reference local (`al.f <- v` after
// `al: mutable T& = &owner`) into the binding the reference was declared from as well: that is
// the storage the store lands in.
func (a *Analyzer) forwardReturnBorrowRefStore(name string, flow returnBorrowFlow, seen map[string]bool) {
	for _, target := range a.returnBorrowDeclRefLinks[name] {
		if seen[target] {
			continue
		}
		seen[target] = true
		sym, ok := a.currentScope.Lookup(target)
		if !ok || sym == nil || sym.Kind != SymbolLocal || sym.Node == nil {
			continue
		}
		key := returnBorrowBinderKey{node: sym.Node, name: target}
		before := a.returnBorrowDeclRecording[key]
		after := mergeReturnBorrowFlow(before, flow)
		if returnBorrowFlowGrew(before, after) {
			a.returnBorrowDeclChanged = true
		}
		a.returnBorrowDeclRecording[key] = after
		a.forwardReturnBorrowRefStore(target, flow, seen)
	}
}
