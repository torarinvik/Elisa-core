package semantic

import (
	"fmt"
	"reflect"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	fn      *ast.FuncDecl
	pending []pendingLocalBorrowStore
	// binders are the non-declaration local bindings the body's analysis defined, by name.
	binders map[string][]*Symbol
}

func (a *Analyzer) pushReturnBorrowDeferFrame(fn *ast.FuncDecl) {
	a.returnBorrowDeferFrames = append(a.returnBorrowDeferFrames, &returnBorrowDeferFrame{fn: fn})
}

// noteReturnBorrowBinder records a local binding defined while the current function's body is
// analyzed that is not a plain declaration.
func (a *Analyzer) noteReturnBorrowBinder(sym *Symbol) {
	if sym == nil || sym.Kind != SymbolLocal {
		return
	}
	if _, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl {
		return
	}
	count := len(a.returnBorrowDeferFrames)
	if count == 0 {
		return
	}
	frame := a.returnBorrowDeferFrames[count-1]
	if frame.fn != a.currentFuncDecl {
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
	frames := a.returnBorrowLateFrames
	a.returnBorrowLateFrames = nil
	a.returnBorrowLateEnabled = false
	if len(frames) == 0 {
		return
	}
	a.returnBorrowFuncSummaries = nil
	a.returnBorrowRootMemos = nil
	a.returnBorrowRootMemosFor = nil
	// A function analyzed more than once keeps only its last analysis's checks.
	last := make(map[*ast.FuncDecl]int, len(frames))
	for index, late := range frames {
		last[late.frame.fn] = index
	}
	savedFuncDecl, savedFuncType := a.currentFuncDecl, a.currentFuncType
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	savedScope, savedBindings := a.currentScope, a.currentValueBindings
	for index, late := range frames {
		if last[late.frame.fn] != index {
			continue
		}
		a.currentFuncDecl, a.currentFuncType = late.frame.fn, late.fnType
		a.currentNamespace, a.currentUsings = late.namespace, late.usings
		a.evaluateReturnBorrowDeferFrame(late.frame.fn, late.frame)
	}
	a.currentFuncDecl, a.currentFuncType = savedFuncDecl, savedFuncType
	a.currentNamespace, a.currentUsings = savedNamespace, savedUsings
	a.currentScope, a.currentValueBindings = savedScope, savedBindings
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
	a.returnBorrowDeclEnv = a.computeReturnBorrowDeclEnv(fn, frame)
	for index, check := range frame.pending {
		if check.regionDependent && a.errorReportedAt(check.value.Pos()) {
			continue
		}
		if check.summaryGated && !summaryLocal[index] {
			continue
		}
		a.currentScope, a.currentValueBindings = check.scope, check.valueBindings
		var flow returnBorrowFlow
		if check.call != nil {
			flow = a.returnBorrowCallArgStoreFlow(check.call, check.callArg)
		} else {
			flow = a.returnBorrowFlowForExpr(check.value, nil, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
		}
		if flow.Local {
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
	scope, ok := a.snapshotReturnBorrowScope()
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

// snapshotReturnBorrowScope flattens the function-local scope levels into one scope over the global
// scope, so a name declared later in the same block cannot capture a deferred check's lookups.
func (a *Analyzer) snapshotReturnBorrowScope() (*Scope, bool) {
	var levels []*Scope
	cur := a.currentScope
	for cur != nil && cur != a.globalScope {
		levels = append(levels, cur)
		cur = cur.Parent
	}
	if cur != a.globalScope {
		return nil, false
	}
	flat := NewScope(a.globalScope)
	for _, level := range levels { // innermost first: the first definition wins
		for name, sym := range level.Symbols {
			if _, exists := flat.Symbols[name]; !exists {
				flat.Symbols[name] = sym
			}
		}
	}
	return flat, true
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

// setReturnBorrowAlias writes a walker alias and, while recording, merges the flow into the store
// union of the binding the name currently resolves to.
func (a *Analyzer) setReturnBorrowAlias(aliases map[string]returnBorrowFlow, name string, flow returnBorrowFlow, merge bool) {
	stored := flow
	if merge {
		flow = mergeReturnBorrowFlow(aliases[name], flow)
	}
	aliases[name] = flow
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

// returnBorrowRefDeclTarget is the local a reference declaration `r: mutable T& = &x.f` borrows
// from, or "" when the declaration is not a reference bound to the address of a local place.
func (a *Analyzer) returnBorrowRefDeclTarget(n *ast.VarDeclStmt) (*ast.AddrOfExpr, string) {
	addr, ok := returnBorrowStripParens(n.Value).(*ast.AddrOfExpr)
	if !ok || addr == nil {
		return nil, ""
	}
	return addr, returnBorrowPlaceRootName(addr.Operand)
}

// returnBorrowRefDeclLinks maps each reference local declared from `&place` to the locals its
// place is rooted at. Name-based, like the untrusted census: an extra link only forwards more.
func (a *Analyzer) returnBorrowRefDeclLinks(fn *ast.FuncDecl) map[string][]string {
	links := map[string][]string{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if decl, ok := v.Interface().(*ast.VarDeclStmt); ok && decl != nil && decl.Name != "" && v.Kind() == reflect.Pointer {
				if _, root := a.returnBorrowRefDeclTarget(decl); root != "" && root != decl.Name {
					links[decl.Name] = append(links[decl.Name], root)
				}
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
	for _, stmt := range fn.Body {
		walk(reflect.ValueOf(stmt))
	}
	return links
}

// defineReturnBorrowBinding defines a walker local and seeds its alias and store union.
func (a *Analyzer) defineReturnBorrowBinding(aliases map[string]returnBorrowFlow, name string, node ast.Node, typ Type, flow returnBorrowFlow) {
	if name == "" || name == "_" {
		return
	}
	if a.currentScope != nil {
		a.currentScope.Define(&Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: node})
	}
	a.setReturnBorrowAlias(aliases, name, flow, false)
}

// returnBorrowConservativeFlow is the flow of a value the walk cannot trace: a frame borrow
// whenever its type can hold one.
func (a *Analyzer) returnBorrowConservativeFlow(expr ast.Expr, fallback Type) returnBorrowFlow {
	t := a.exprTypes[expr]
	if t == nil {
		t = fallback
	}
	if t == nil || a.typeMayHoldFrameBorrow(t, map[Type]bool{}) {
		return returnBorrowFlow{Local: true}
	}
	return returnBorrowFlow{}
}

// returnBorrowRecordingIdentFlow reads a name while recording: its walker alias joined with the
// store union of the binding it resolves to.
func (a *Analyzer) returnBorrowRecordingIdentFlow(ident *ast.Ident, aliases map[string]returnBorrowFlow) returnBorrowFlow {
	if a.returnBorrowDeclConservative[ident.Name] {
		return a.returnBorrowConservativeFlow(ident, nil)
	}
	flow, hasAlias := aliases[ident.Name]
	var sym *Symbol
	if a.currentScope != nil {
		sym, _ = a.currentScope.Lookup(ident.Name)
	}
	if sym == nil {
		if hasAlias {
			return mergeReturnBorrowFlow(flow, a.returnBorrowConservativeFlow(ident, nil))
		}
		return a.returnBorrowConservativeFlow(ident, nil)
	}
	if sym.Kind != SymbolLocal {
		return flow
	}
	if sym.Node == nil {
		return mergeReturnBorrowFlow(flow, a.returnBorrowConservativeFlow(ident, sym.Type))
	}
	return mergeReturnBorrowFlow(flow, a.returnBorrowDeclRecording[returnBorrowBinderKey{node: sym.Node, name: ident.Name}])
}

// returnBorrowEnvSymbolFlow is the recorded store union of a trusted local binding. Reference
// declarations are excluded: their value is an address, not the union of what was stored.
func (a *Analyzer) returnBorrowEnvSymbolFlow(sym *Symbol) (returnBorrowFlow, bool) {
	if !a.returnBorrowEnvActive() || sym == nil || sym.Kind != SymbolLocal || sym.Node == nil {
		return returnBorrowFlow{}, false
	}
	if _, isRef := sym.Type.(*RefType); isRef {
		// A pattern binder is never rebound, so its union is the bound value itself.
		if _, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl {
			return returnBorrowFlow{}, false
		}
	}
	flow, ok := a.returnBorrowDeclEnv[returnBorrowBinderKey{node: sym.Node, name: sym.Name}]
	return flow, ok
}

// returnBorrowEnvTrustedRoot resolves the local a place is rooted at when it is env-trusted.
func (a *Analyzer) returnBorrowEnvTrustedRoot(expr ast.Expr) (*ast.Ident, returnBorrowFlow, bool) {
	if !a.returnBorrowEnvActive() || a.currentScope == nil {
		return nil, returnBorrowFlow{}, false
	}
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
			continue
		case *ast.MoveExpr:
			expr = n.Operand
			continue
		case *ast.FieldExpr:
			expr = n.Object
			continue
		case *ast.IndexExpr:
			expr = n.Object
			continue
		case *ast.SliceExpr:
			expr = n.Object
			continue
		}
		break
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return nil, returnBorrowFlow{}, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return nil, returnBorrowFlow{}, false
	}
	flow, ok := a.returnBorrowEnvSymbolFlow(sym)
	return ident, flow, ok
}

// returnBorrowModeledBinderNodes lists the binding nodes the walker defines besides declarations:
// iterator-loop patterns and declaring tuple bindings on statement paths it walks.
// returnBorrowMarkConditionBinders marks the condition unwraps defineReturnBorrowConditionBindings
// binds; the two must agree on which binders the walk models.
func returnBorrowMarkConditionBinders(cond ast.Expr, modeled map[ast.Node]bool) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		returnBorrowMarkConditionBinders(n.Inner, modeled)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			returnBorrowMarkConditionBinders(n.Left, modeled)
			returnBorrowMarkConditionBinders(n.Right, modeled)
		case lexer.TOKEN_OR:
			// The analysis defines the alternatives' shared binders with the or-expression as
			// their node; the walk defines them under that same node.
			modeled[n] = true
		case lexer.TOKEN_IS:
			if _, _, pattern, ok := unwrapDirectConditionPattern(n); ok && pattern != nil {
				for _, binder := range returnBorrowMatchPatternBinders(pattern, nil) {
					modeled[binder.node] = true
				}
			}
		}
	case *ast.OptionalBindExpr:
		if n.Name != "" && n.Name != "_" {
			modeled[n] = true
		}
	}
}

// returnBorrowMatchBinder is a match-arm binder the return-borrow walk models: its value is a copy
// of the scrutinee's payload, except a `...rest` binder, which is a view into the scrutinee.
type returnBorrowMatchBinder struct {
	name string
	node ast.Node
	view bool
}

// returnBorrowMatchPatternBinders lists the binders of a match pattern the walk models. An
// or-pattern's binders are left out (they stay conservative).
func returnBorrowMatchPatternBinders(pattern ast.MatchPattern, out []returnBorrowMatchBinder) []returnBorrowMatchBinder {
	keep := func(name string) bool { return name != "" && name != "_" }
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Binder != "" {
			if keep(p.Binder) {
				out = append(out, returnBorrowMatchBinder{name: p.Binder, node: p})
			}
		} else if keep(p.Name) {
			out = append(out, returnBorrowMatchBinder{name: p.Name, node: p})
		}
	case *ast.MatchVariantPattern:
		if keep(p.As) {
			out = append(out, returnBorrowMatchBinder{name: p.As, node: p})
		}
		for _, arg := range p.Args {
			out = returnBorrowMatchPatternBinders(arg.Pattern, out)
		}
	case *ast.MatchStructPattern:
		for _, arg := range p.Args {
			out = returnBorrowMatchPatternBinders(arg.Pattern, out)
		}
	case *ast.MatchTuplePattern:
		for _, elem := range p.Elems {
			out = returnBorrowMatchPatternBinders(elem, out)
		}
	case *ast.MatchListPattern:
		for _, elem := range p.Elems {
			out = returnBorrowMatchPatternBinders(elem, out)
		}
	case *ast.MatchRestPattern:
		if keep(p.Name) {
			out = append(out, returnBorrowMatchBinder{name: p.Name, node: p, view: true})
		}
	}
	return out
}

func returnBorrowModeledBinderNodes(fn *ast.FuncDecl) map[ast.Node]bool {
	modeled := map[ast.Node]bool{}
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch n := stmt.(type) {
			case *ast.IterForStmt:
				if n.Pattern != nil {
					modeled[n.Pattern] = true
				}
			case *ast.TupleBindStmt:
				if n.Declare {
					modeled[n] = true
				}
			case *ast.IfStmt:
				returnBorrowMarkConditionBinders(n.Cond, modeled)
				walk(n.Then)
				for _, clause := range n.Elifs {
					returnBorrowMarkConditionBinders(clause.Cond, modeled)
					walk(clause.Body)
				}
				walk(n.Else)
				continue
			case *ast.WhileStmt:
				returnBorrowMarkConditionBinders(n.Cond, modeled)
				walk(n.Body)
				continue
			case *ast.ForStmt:
				walk(n.Body)
				continue
			case *ast.MatchStmt:
				for _, arm := range n.Arms {
					for _, binder := range returnBorrowMatchPatternBinders(arm.Pattern, nil) {
						modeled[binder.node] = true
					}
					walk(arm.Body)
				}
				continue
			case *ast.ExprStmt:
				// A value-form loop's statements are walked like a nested body.
				for block, isBlock := n.Expr.(*ast.ExprBlock); isBlock && block != nil; block, isBlock = block.Value.(*ast.ExprBlock) {
					walk(block.Stmts)
				}
				continue
			case *ast.VarDeclStmt:
				// ... also as a declaration's value (`n: u32 = for c in xs |acc| -> acc:`).
				for block, isBlock := n.Value.(*ast.ExprBlock); isBlock && block != nil; block, isBlock = block.Value.(*ast.ExprBlock) {
					walk(block.Stmts)
				}
			}
			for _, body := range returnBorrowChildBlocks(stmt) {
				walk(body)
			}
		}
	}
	walk(fn.Body)
	return modeled
}

// returnBorrowWalkedStmt lists the statement kinds returnBorrowFlowForStatements models (or that
// cannot store anything: a MachineCoverageStmt only re-reads the input its machine's input var
// declaration already evaluated).
func returnBorrowWalkedStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.ReturnStmt, *ast.ExprStmt, *ast.IfStmt, *ast.WhileStmt,
		*ast.ForStmt, *ast.IterForStmt, *ast.TupleBindStmt, *ast.AugAssignStmt, *ast.MatchStmt,
		*ast.CanStmt, *ast.ScopeStmt, *ast.RegionStmt, *ast.InStoreStmt, *ast.PoolStmt, *ast.LockStmt,
		*ast.CheckpointStmt, *ast.GroupedCheckpointStmt, *ast.StaticBlockStmt, *ast.ParallelForStmt,
		*ast.StaticIfStmt, *ast.BreakStmt, *ast.ContinueStmt, *ast.PassStmt, *ast.MachineCoverageStmt:
		return true
	}
	return false
}

// returnBorrowDiscardedCall returns the call of `_ = f(x)` (also `parser, _ <- parser.advance()`,
// whose lmut thread is claimed): its result is dropped, exactly like a call statement.
func (a *Analyzer) returnBorrowDiscardedCall(stmt ast.Stmt) *ast.CallExpr {
	discard, ok := stmt.(*ast.DiscardStmt)
	if !ok || discard == nil {
		return nil
	}
	return a.returnBorrowAsCall(discard.Value)
}

// returnBorrowAsCall returns the call an expression is: a CallExpr, or a `Name(args)` the parser
// read as a struct literal that analysis lowered to a call (a PascalCase function such as an
// extern `LLVMBuildCall2(...)`, a generic function, a cast or init hook). The lowered call shares
// the literal's argument nodes, so modeling it models the literal's stores.
func (a *Analyzer) returnBorrowAsCall(expr ast.Expr) *ast.CallExpr {
	switch n := returnBorrowStripParens(expr).(type) {
	case *ast.CallExpr:
		return n
	case *ast.StructLitExpr:
		if n != nil && a.loweredInitCalls != nil {
			return a.loweredInitCalls[n]
		}
	}
	return nil
}

// returnBorrowUntrustedNames collects the local names with a write the walker does not model.
// Name-based, so a shadowed binding is untrusted whenever any same-named binding is.
func (a *Analyzer) returnBorrowUntrustedNames(fn *ast.FuncDecl, processed map[*ast.CallExpr]bool) map[string]bool {
	untrusted := map[string]bool{}
	markRoot := func(expr ast.Expr) {
		if name := returnBorrowPlaceRootName(returnBorrowStripAddr(expr)); name != "" {
			untrusted[name] = true
		}
	}
	markAll := func(expr ast.Expr) {
		collectReturnBorrowNodeNames(reflect.ValueOf(expr), untrusted)
	}
	var boundMachineInputs map[string]bool
	machineInputBound := func(name string) bool {
		if boundMachineInputs == nil {
			boundMachineInputs = returnBorrowBoundMachineInputs(fn)
		}
		return boundMachineInputs[name]
	}
	exemptAddr := map[*ast.AddrOfExpr]bool{}
	refLinks := map[string][]string{}
	refDecls := map[string]bool{}
	// A call whose result is discarded (`out.push(x)` as a statement) hands back no alias.
	discarded := map[*ast.CallExpr]bool{}
	var walk func(v reflect.Value, inExpr bool)
	// A value-form loop lowers to nested ExprBlocks (`do: do: ... acc`): every level's statements
	// are modeled like a nested body; only the innermost value is an expression.
	var walkValueBlock func(block *ast.ExprBlock)
	walkValueBlock = func(block *ast.ExprBlock) {
		walk(reflect.ValueOf(block.Stmts), false)
		if inner, isBlock := block.Value.(*ast.ExprBlock); isBlock && inner != nil {
			walkValueBlock(inner)
			return
		}
		walk(reflect.ValueOf(block.Value), true)
	}
	walk = func(v reflect.Value, inExpr bool) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			node := v.Interface()
			if stmt, ok := node.(ast.Stmt); ok {
				if inExpr || (!returnBorrowWalkedStmt(stmt) && a.returnBorrowDiscardedCall(stmt) == nil) {
					collectReturnBorrowNodeNames(v, untrusted)
					return
				}
				switch n := stmt.(type) {
				case *ast.VarDeclStmt:
					// A value-form loop as the declaration's value is modeled like a nested body.
					if block, isBlock := n.Value.(*ast.ExprBlock); isBlock && block != nil {
						walkValueBlock(block)
						return
					}
					// `r: mutable T& = x` aliases x: stores through r reach x.
					isRef := false
					if n.Type != nil {
						_, isRef = n.Type.(*ast.RefType)
					}
					if _, ok := a.exprTypes[n.Value].(*RefType); ok {
						isRef = true
					}
					// A machine's input var is only compared against its arms, unless an arm binds it.
					if isRef && (!strings.HasPrefix(n.Name, "__machine_input_") || machineInputBound(n.Name)) {
						if addr, root := a.returnBorrowRefDeclTarget(n); root != "" && root != n.Name {
							// Stores through r are forwarded into x's union; x stays trusted unless r
							// itself reaches a write the walker does not model.
							exemptAddr[addr] = true
							refLinks[n.Name] = append(refLinks[n.Name], root)
						} else {
							markRoot(n.Value)
						}
						refDecls[n.Name] = true
					}
				case *ast.AssignStmt:
					if _, isRef := a.exprTypes[n.Target].(*RefType); isRef {
						markRoot(n.Value)
					}
					// `xs <- xs.truncate(0)` and `xs <- xs.push(v)` copy the returned receiver back
					// into itself: no new name aliases it (the push's store into xs is modeled).
					if call, isCall := returnBorrowStripParens(n.Value).(*ast.CallExpr); isCall && call != nil && (a.returnBorrowCountOnlyDArrayMethod(call) || a.returnBorrowDArrayValueArgMethod(call) || a.returnBorrowBorrowFreeContainerUpdate(call)) {
						if field, isField := call.Func.(*ast.FieldExpr); isField && returnBorrowSamePlace(n.Target, field.Object) {
							discarded[call] = true
						}
					}
					if returnBorrowPlaceRootName(n.Target) == "" {
						markAll(n.Target)
					}
				case *ast.AugAssignStmt:
					if returnBorrowPlaceRootName(n.Target) == "" {
						markAll(n.Target)
					}
				case *ast.IterForStmt:
					// A reference loop binding writes into the iterated container.
					if n.Mode != ast.IterBindValue {
						markRoot(n.Source)
					}
				case *ast.ParallelForStmt:
					markRoot(n.Source)
				case *ast.MatchStmt:
					if n.Store != nil {
						markAll(n.Store)
					}
				case *ast.DiscardStmt:
					discarded[a.returnBorrowDiscardedCall(n)] = true
				case *ast.ExprStmt:
					if call := a.returnBorrowAsCall(n.Expr); call != nil {
						discarded[call] = true
					}
					// A value-form loop (`for x in xs |acc| -> acc:`) desugars to an ExprBlock
					// statement; the walker models its statements like a nested body.
					if block, isBlock := n.Expr.(*ast.ExprBlock); isBlock && block != nil {
						walkValueBlock(block)
						return
					}
				}
			}
			if expr, ok := node.(ast.Expr); ok {
				if lit, isLit := expr.(*ast.StructLitExpr); isLit {
					if call := a.returnBorrowAsCall(lit); call != nil {
						expr = call
					}
				}
				switch n := expr.(type) {
				case *ast.LambdaExpr:
					collectReturnBorrowNodeNames(v, untrusted)
					return
				case *ast.AddrOfExpr:
					if !exemptAddr[n] {
						markRoot(n.Operand)
					}
				case *ast.CallExpr:
					if processed[n] {
						// An address passed straight to a processed call is modeled by its stores.
						for _, arg := range returnBorrowCallArgs(n) {
							if addr, isAddr := returnBorrowStripParens(arg).(*ast.AddrOfExpr); isAddr && addr != nil {
								exemptAddr[addr] = true
							}
						}
						if !discarded[n] {
							a.markReturnBorrowCallResultAliases(n, markRoot)
						}
					} else if a.returnBorrowCountOnlyDArrayMethod(n) && discarded[n] {
						// Resizes its receiver by integer counts: stores no borrow anywhere.
					} else {
						byValue := a.returnBorrowDArrayValueArgMethod(n)
						for _, arg := range returnBorrowCallArgs(n) {
							if byValue && a.returnBorrowCopiedArg(arg) {
								continue
							}
							markRoot(arg)
						}
						if field, isField := n.Func.(*ast.FieldExpr); isField && field != nil {
							markRoot(field.Object)
						}
					}
				}
				walk(v.Elem(), true)
				return
			}
			walk(v.Elem(), inExpr)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walk(v.Field(i), inExpr)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), inExpr)
			}
		}
	}
	for _, stmt := range fn.Body {
		walk(reflect.ValueOf(stmt), false)
	}
	// A reference whose own uses escape the model can write its referent anywhere.
	for changed := true; changed; {
		changed = false
		for name, roots := range refLinks {
			if !untrusted[name] {
				continue
			}
			for _, root := range roots {
				if !untrusted[root] {
					untrusted[root] = true
					changed = true
				}
			}
		}
	}
	for name := range refDecls {
		untrusted[name] = true
	}
	return untrusted
}

// returnBorrowDArrayValueArgMethod reports a builtin darray `push`/`insert`, which takes its
// arguments by value: the call cannot write into a local passed to it.
func (a *Analyzer) returnBorrowDArrayValueArgMethod(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || (field.Field != "push" && field.Field != "insert") || !a.returnBorrowBuiltinMethodCall(call) {
		return false
	}
	objType := a.exprTypes[field.Object]
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	_, isDArray := objType.(*DArrayType)
	return isDArray
}

// returnBorrowCopiedArg reports an argument passed as a plain value copy: neither an address nor a
// reference, so the callee receives no way to write the argument's place.
// returnBorrowBoundMachineInputs collects the machine input vars an arm binds by name
// (`State, c if ...:` lowers to `c = __machine_input_N`).
func returnBorrowBoundMachineInputs(fn *ast.FuncDecl) map[string]bool {
	bound := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if decl, ok := v.Interface().(*ast.VarDeclStmt); ok && decl != nil {
				if ident, isIdent := decl.Value.(*ast.Ident); isIdent && ident != nil && strings.HasPrefix(ident.Name, "__machine_input_") {
					bound[ident.Name] = true
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(fn.Body))
	return bound
}

// returnBorrowCountOnlyDArrayMethod reports a darray `reserve`/`clear`/`truncate` whose arguments
// are all integers: it changes the receiver's length or capacity and stores no borrow.
func (a *Analyzer) returnBorrowCountOnlyDArrayMethod(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || !returnBorrowCountOnlyMethods[field.Field] || !a.returnBorrowBuiltinMethodCall(call) {
		return false
	}
	objType := a.exprTypes[field.Object]
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	if _, isDArray := objType.(*DArrayType); !isDArray {
		return false
	}
	for _, arg := range returnBorrowCallArgs(call) {
		switch t := a.exprTypes[returnBorrowStripParens(arg)].(type) {
		case *BuiltinType:
			if !isIntegerBuiltinName(t.Name) {
				return false
			}
		case *BitIntType:
		default:
			return false
		}
	}
	return true
}

var returnBorrowCountOnlyMethods = map[string]bool{"reserve": true, "clear": true, "truncate": true}

// returnBorrowBorrowFreeContainerUpdate reports a builtin dict/set update (`d.put(k, v)`,
// `s.add(x)`, `d.remove(k)`) whose arguments can carry no frame borrow: it hands back its receiver
// holding nothing new.
func (a *Analyzer) returnBorrowBorrowFreeContainerUpdate(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || !returnBorrowContainerUpdateMethods[field.Field] || !a.returnBorrowBuiltinMethodCall(call) {
		return false
	}
	objType := a.exprTypes[field.Object]
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	switch objType.(type) {
	case *DictType, *SetType:
	default:
		return false
	}
	for _, arg := range returnBorrowCallArgs(call) {
		argType := a.exprTypes[returnBorrowStripParens(arg)]
		if argType == nil || a.typeMayHoldFrameBorrow(argType, map[Type]bool{}) {
			return false
		}
	}
	return true
}

var returnBorrowContainerUpdateMethods = map[string]bool{"put": true, "add": true, "remove": true}

// returnBorrowSamePlace reports two spellings of one field-chain place (`p.items`, `p.items`).
func returnBorrowSamePlace(left, right ast.Expr) bool {
	leftRoot, leftFields, leftOK := exprPlacePath(returnBorrowStripParens(left))
	rightRoot, rightFields, rightOK := exprPlacePath(returnBorrowStripParens(right))
	if !leftOK || !rightOK || leftRoot != rightRoot || len(leftFields) != len(rightFields) {
		return false
	}
	for i := range leftFields {
		if leftFields[i] != rightFields[i] {
			return false
		}
	}
	return true
}

func (a *Analyzer) returnBorrowCopiedArg(arg ast.Expr) bool {
	inner := returnBorrowStripParens(arg)
	if _, isAddr := inner.(*ast.AddrOfExpr); isAddr {
		return false
	}
	argType := a.exprTypes[inner]
	if argType == nil {
		return false
	}
	_, isRef := argType.(*RefType)
	return !isRef
}

func returnBorrowCallArgs(call *ast.CallExpr) []ast.Expr {
	if call.ResolvedArgsValid {
		return call.ResolvedArgs
	}
	return call.Args
}

func returnBorrowStripParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}

func returnBorrowStripAddr(expr ast.Expr) ast.Expr {
	expr = returnBorrowStripParens(expr)
	if addr, ok := expr.(*ast.AddrOfExpr); ok && addr != nil {
		return addr.Operand
	}
	return expr
}

// markReturnBorrowCallResultAliases marks the arguments a call may hand back as a WRITABLE
// reference: writes through the result then reach the argument without the walker seeing them.
func (a *Analyzer) markReturnBorrowCallResultAliases(call *ast.CallExpr, markRoot func(ast.Expr)) {
	resultType := a.exprTypes[call]
	if resultType == nil || !a.typeHasWritableBorrow(resultType, map[Type]bool{}) {
		return
	}
	// A darray `push`/`insert` hands back its receiver; its by-value arguments are not aliased.
	byValue := a.returnBorrowDArrayValueArgMethod(call)
	for _, arg := range returnBorrowCallArgs(call) {
		if byValue && a.returnBorrowCopiedArg(arg) {
			continue
		}
		markRoot(arg)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil {
		markRoot(field.Object)
	}
}

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
			if at, ok := a.exprTypes[returnBorrowStripAddr(returnBorrowCallArgs(call)[index])]; ok && at != nil {
				if rt, isRef := a.exprTypes[returnBorrowCallArgs(call)[index]].(*RefType); isRef && rt.Elem != nil {
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
	keep := func(other int) bool { return !a.callArgNeverReaches(call, other, index) }
	flow := a.returnBorrowCallOtherArgsFlowWhere(args, index, fnType.Params, keep, map[string]returnBorrowFlow{}, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
	return flow
}
