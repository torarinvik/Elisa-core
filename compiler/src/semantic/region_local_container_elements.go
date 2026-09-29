package semantic

import (
	"reflect"
	"strings"

	"elisacore/src/ast"
)

// Element provenance of function-local containers.
//
// A by-value element copy `xs[i]` normally inherits the region-ref state of the container
// itself, which for a local `xs: darray[sview] = []` is the container's own (function-local)
// region. That is sound but blind: `names.push(src); out.push(names[0])` copies a view of the
// CALLER's bytes, yet is rejected as storing a function-local reference into `out`.
//
// For a local container whose every use is one of a small set of shapes that cannot mutate
// an element in place (`xs.push(v)` statements, `xs.count`, a by-value `for x in xs` or
// `any x in xs where ...` query, and an
// rvalue `xs[i]` whose copy flows into a value position), the region-ref state of an element
// copy is exactly the merge of the states of the values ever pushed into it. The analysis is
// flow-sensitive (a read sees the pushes analyzed before it); a read inside a loop that also
// pushes into the container would miss the pushes of later iterations, so such reads keep the
// container's conservative state. Any other use of the name anywhere in the function, including
// inside a lambda, nested function or `defer` body, makes the container ineligible.

type addrArgSite struct {
	call  *ast.CallExpr
	index int
}

type localContainerElementScan struct {
	ineligible map[string]bool
	// addrArgs maps a call argument `&name` to its call and position, so the name is classified
	// like a bare argument.
	addrArgs map[*ast.AddrOfExpr]addrArgSite
	// reads are the rvalue index reads `xs[i]`, with the loops that enclose each; binderReads
	// are the by-value `for x in xs` loops over a bare name (the loop itself encloses its
	// source, so a push into xs in its body poisons the binder).
	reads       []localContainerElementRead
	binderReads []localContainerElementRead
	// pushedIn records, per enclosing loop, the container names pushed into inside it.
	pushedIn map[ast.Node]map[string]bool
	loops    []ast.Node
	// declLoops are the loops enclosing a name's only declaration; a name declared more than
	// once, or mentioned before its declaration, is in multiDecl.
	declLoops map[string][]ast.Node
	multiDecl map[string]bool
	mentioned map[string]bool
	// opaque counts enclosing lambdas / nested functions / defer bodies.
	opaque int
	// acceptedReceivers are `xs.push` / `xs.reserve` callees of statement-level calls;
	// acceptedReads are index expressions in a by-value position.
	acceptedReceivers map[*ast.FieldExpr]bool
	acceptedReads     map[*ast.IndexExpr]bool
	visited           map[uintptr]bool
	// argReadOnly reports whether a call's argument binds a parameter the callee cannot
	// mutate through.
	argReadOnly func(call *ast.CallExpr, index int) bool
	// argFillable reports whether a call's argument binds a writable parameter of a callee
	// that can only fill it with data reachable from its other arguments (see
	// callArgFillsWritableParam); the analysis merges those arguments' provenance at the call.
	argFillable func(call *ast.CallExpr, index int) bool
}

type localContainerElementRead struct {
	name  string
	node  *ast.IndexExpr
	loop  *ast.IterForStmt
	loops []ast.Node
}

var (
	astNodeType = reflect.TypeOf((*ast.Node)(nil)).Elem()
	astPkgPath  = reflect.TypeOf(ast.Ident{}).PkgPath()
)

// scanLocalContainerElementReads computes, for one function body, the index reads whose
// element provenance may be used (see the file comment).
func (a *Analyzer) scanLocalContainerElementReads(body []ast.Stmt) (map[*ast.IndexExpr]bool, map[*ast.IterForStmt]bool) {
	scan := &localContainerElementScan{
		argReadOnly:       a.callArgBindsReadOnlyParam,
		argFillable:       a.callArgFillsWritableParam,
		ineligible:        map[string]bool{},
		pushedIn:          map[ast.Node]map[string]bool{},
		declLoops:         map[string][]ast.Node{},
		multiDecl:         map[string]bool{},
		mentioned:         map[string]bool{},
		acceptedReceivers: map[*ast.FieldExpr]bool{},
		acceptedReads:     map[*ast.IndexExpr]bool{},
		visited:           map[uintptr]bool{},
		addrArgs:          map[*ast.AddrOfExpr]addrArgSite{},
	}
	for _, stmt := range body {
		scan.walkNode(stmt, nil, "")
	}
	accepted := func(read localContainerElementRead) bool {
		if scan.ineligible[read.name] {
			return false
		}
		for _, loop := range read.loops {
			if scan.pushedIn[loop][read.name] {
				return false
			}
		}
		return true
	}
	out := map[*ast.IndexExpr]bool{}
	for _, read := range scan.reads {
		if accepted(read) {
			out[read.node] = true
		}
	}
	binders := map[*ast.IterForStmt]bool{}
	for _, read := range scan.binderReads {
		if accepted(read) {
			binders[read.loop] = true
		}
	}
	return out, binders
}

func isElementScanLoop(node ast.Node) bool {
	switch node.(type) {
	case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt, *ast.MachineFromExpr:
		return true
	}
	return false
}

func isElementScanOpaque(node ast.Node) bool {
	switch node.(type) {
	case *ast.LambdaExpr, *ast.FuncDecl, *ast.DeferStmt:
		return true
	}
	return false
}

// statementContainerCall matches the statement `xs.push(v)` / `xs.extend(src)` / `xs.reserve(n)`
// on a bare name.
func statementContainerCall(stmt *ast.ExprStmt) (*ast.FieldExpr, *ast.Ident, bool) {
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok || call.SafeReceiver != nil || call.Safe || call.HasArgForward || len(call.Args) != 1 || len(call.ArgNames) != 0 {
		return nil, nil, false
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok || callee.Safe || (callee.Field != "push" && callee.Field != "extend" && callee.Field != "reserve") {
		return nil, nil, false
	}
	receiver, ok := callee.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	return callee, receiver, true
}

func (s *localContainerElementScan) acceptRead(expr ast.Expr) {
	if index, ok := expr.(*ast.IndexExpr); ok {
		s.acceptedReads[index] = true
	}
}

// markChildren accepts the children of node that sit in a shape the element analysis models.
func (s *localContainerElementScan) markChildren(node ast.Node) {
	switch n := node.(type) {
	case *ast.ExprStmt:
		if callee, receiver, ok := statementContainerCall(n); ok {
			s.acceptedReceivers[callee] = true
			if callee.Field == "push" || callee.Field == "extend" {
				s.markPushed(receiver.Name)
			}
		}
	case *ast.CallExpr:
		if len(n.Args) == 1 && len(n.ArgNames) == 0 {
			if callee, ok := n.Func.(*ast.FieldExpr); ok && callee.Field == "push" {
				// The pushed value is copied into the target container (statement or
				// value-form `ys <- ys.push(xs[i])`).
				s.acceptRead(n.Args[0])
			}
		}
	case *ast.VarDeclStmt:
		if _, isRef := n.Type.(*ast.RefType); !isRef && n.Owner == nil {
			s.acceptRead(n.Value)
		}
		if _, seen := s.declLoops[n.Name]; seen || s.mentioned[n.Name] {
			s.multiDecl[n.Name] = true
		}
		s.declLoops[n.Name] = append([]ast.Node(nil), s.loops...)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			s.acceptRead(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			s.acceptRead(elem)
		}
	case *ast.BinaryExpr:
		s.acceptRead(n.Left)
		s.acceptRead(n.Right)
	case *ast.ReturnStmt:
		s.acceptRead(n.Value)
	}
}

// markPushed records that name gains elements inside every enclosing loop, except the loops that
// also enclose its declaration: each of their iterations declares a fresh container, so no read
// there sees an earlier iteration's elements. Only a name declared once, and not mentioned before
// that declaration (which would be another binding of the name), gets the exception; element
// state is only ever kept for declared locals.
func (s *localContainerElementScan) markPushed(name string) {
	skip := 0
	if decl, declared := s.declLoops[name]; declared && !s.multiDecl[name] {
		for skip < len(decl) && skip < len(s.loops) && decl[skip] == s.loops[skip] {
			skip++
		}
	}
	for _, loop := range s.loops[skip:] {
		if s.pushedIn[loop] == nil {
			s.pushedIn[loop] = map[string]bool{}
		}
		s.pushedIn[loop][name] = true
	}
}

// walkNode visits node (reached from parent through the parent's field `field`), then its children.
func (s *localContainerElementScan) walkNode(node ast.Node, parent ast.Node, field string) {
	if node == nil {
		return
	}
	v := reflect.ValueOf(node)
	if v.Kind() != reflect.Ptr || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return
	}
	if s.visited[v.Pointer()] {
		return
	}
	s.visited[v.Pointer()] = true
	if call, ok := node.(*ast.CallExpr); ok {
		for index, arg := range call.Args {
			if addr, isAddr := arg.(*ast.AddrOfExpr); isAddr && addr != nil {
				s.addrArgs[addr] = addrArgSite{call: call, index: index}
			}
		}
	}
	if ident, ok := node.(*ast.Ident); ok {
		s.classifyIdent(ident, parent, field)
	}
	s.markChildren(node)
	isLoop := isElementScanLoop(node)
	if isLoop {
		s.loops = append(s.loops, node)
	}
	opaque := isElementScanOpaque(node)
	if opaque {
		s.opaque++
	}
	s.walkStructFields(v.Elem(), node)
	if opaque {
		s.opaque--
	}
	if isLoop {
		s.loops = s.loops[:len(s.loops)-1]
	}
}

func (s *localContainerElementScan) walkValue(v reflect.Value, owner ast.Node, field string) {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		s.walkValue(v.Elem(), owner, field)
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		if node, ok := v.Interface().(ast.Node); ok {
			s.walkNode(node, owner, field)
			return
		}
		if v.Elem().Kind() != reflect.Struct || v.Elem().Type().PkgPath() != astPkgPath || s.visited[v.Pointer()] {
			return
		}
		s.visited[v.Pointer()] = true
		s.walkStructFields(v.Elem(), owner)
	case reflect.Struct:
		if v.Type().PkgPath() != astPkgPath {
			return
		}
		s.walkStructFields(v, owner)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.walkValue(v.Index(i), owner, field)
		}
	case reflect.String:
		// A name mentioned as a bare string (a statement naming its target) rather than an
		// Ident node disqualifies that name.
		if stringFieldMayNameLocal(owner, field) {
			s.ineligible[v.String()] = true
		}
	}
}

func (s *localContainerElementScan) walkStructFields(v reflect.Value, owner ast.Node) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Map || fv.Kind() == reflect.Func || fv.Kind() == reflect.Chan {
			continue
		}
		s.walkValue(fv, owner, f.Name)
	}
}

// stringFieldMayNameLocal reports whether a string field could name a local variable. Labels
// that never do (member names, argument labels, literal text) are excluded; anything else is
// conservatively treated as a possible reference.
func stringFieldMayNameLocal(owner ast.Node, field string) bool {
	switch owner.(type) {
	case *ast.Ident, *ast.FieldExpr, *ast.CallExpr, *ast.StructLitExpr, *ast.EnumColumnExpr:
		return false
	case *ast.VarDeclStmt:
		// The declaration itself; a redeclaration is a different symbol with no element state.
		return field != "Name"
	case *ast.ExprBlock, *ast.WhileStmt:
		// A `|capture|` header is a mutation manifest: the body still names the outer binding
		// itself (no shadow symbol), so its pushes are analyzed as that binding's pushes.
		return field != "Captures"
	}
	switch field {
	case "Filename", "File", "Keyword", "Op", "Doc", "Comment", "Text", "Raw", "Value":
		return false
	}
	return true
}

func (s *localContainerElementScan) classifyIdent(ident *ast.Ident, parent ast.Node, field string) {
	name := ident.Name
	s.mentioned[name] = true
	if s.opaque > 0 {
		s.ineligible[name] = true
		return
	}
	switch p := parent.(type) {
	case *ast.FieldExpr:
		if field != "Object" {
			break
		}
		switch p.Field {
		case "count", "len", "is_empty":
			return
		}
		if s.acceptedReceivers[p] {
			return
		}
	case *ast.IndexExpr:
		if field != "Object" || p.Index2 != nil {
			break
		}
		if s.acceptedReads[p] {
			s.reads = append(s.reads, localContainerElementRead{name: name, node: p, loops: append([]ast.Node(nil), s.loops...)})
			return
		}
	case *ast.IterForStmt:
		if field == "Source" && p.Mode == ast.IterBindValue {
			if ast.Expr(ident) == p.Source {
				s.binderReads = append(s.binderReads, localContainerElementRead{name: name, loop: p, loops: append([]ast.Node(nil), s.loops...)})
			}
			return
		}
	case *ast.CallExpr:
		if field == "Args" && len(p.ArgNames) == 0 && !p.HasArgForward && s.argReadOnly != nil {
			for index, arg := range p.Args {
				if arg == ast.Expr(ident) {
					if s.argReadOnly(p, index) {
						return
					}
					if s.argFillable != nil && s.argFillable(p, index) {
						// The call adds elements, like a push: a read in an enclosing loop
						// may see the previous iteration's fill.
						s.markPushed(name)
						return
					}
					break
				}
			}
		}
	case *ast.AddrOfExpr:
		// `f(&xs)` is the bare argument `xs`.
		if site, ok := s.addrArgs[p]; ok && field == "Operand" && len(site.call.ArgNames) == 0 && !site.call.HasArgForward && s.argReadOnly != nil {
			if s.argReadOnly(site.call, site.index) {
				return
			}
			if s.argFillable != nil && s.argFillable(site.call, site.index) {
				s.markPushed(name)
				return
			}
		}
	case *ast.ReturnStmt:
		// Returning the container hands its elements to the caller; the return-element summary
		// reads its tracked state.
		if field == "Value" {
			return
		}
	case *ast.QueryExpr:
		// `any x in xs where ...`: the binder is a by-value element copy.
		if field == "Source" {
			return
		}
	}
	s.ineligible[name] = true
}

// recordLocalContainerElementInit starts element tracking for a local container declared
// with a list literal: its elements' provenance is the merge of the literal elements'.
func (a *Analyzer) recordLocalContainerElementInit(sym *Symbol, value ast.Expr) {
	if a.currentElementStates == nil || sym == nil || sym.Kind != SymbolLocal {
		return
	}
	if comp, ok := value.(*ast.ListComprehensionExpr); ok {
		state, recorded := a.comprehensionElementStates[comp]
		if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !recorded || !isDArray || comp.Owner != nil || comp.Parallel {
			delete(a.currentElementStates, sym)
			return
		}
		a.currentElementStates[sym] = cloneRegionRefState(state)
		return
	}
	if call, ok := stripParenExpr(value).(*ast.CallExpr); ok {
		// A call result: its elements are what the callee's return-element summary says.
		state, known := a.callReturnedElementState(call)
		if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !known || !isDArray {
			delete(a.currentElementStates, sym)
			return
		}
		a.currentElementStates[sym] = state
		return
	}
	lit, ok := value.(*ast.ListLitExpr)
	if !ok || lit.Brace || lit.Owner != nil || lit.Keys != nil {
		delete(a.currentElementStates, sym)
		return
	}
	if _, ok := stripRefForBounds(sym.Type).(*DArrayType); !ok {
		return
	}
	for _, spread := range lit.Spreads {
		if spread {
			return
		}
	}
	states := make([]regionRefState, 0, len(lit.Elems))
	for _, elem := range lit.Elems {
		state, ok, known := a.elementStorageState(elem)
		if !known {
			delete(a.currentElementStates, sym)
			return
		}
		if ok {
			states = append(states, state)
		}
	}
	merged, _ := mergeRegionRefStates(states...)
	a.currentElementStates[sym] = merged
}

// recordComprehensionElementState records the provenance of a list comprehension's element
// expression while its binders are in scope; one without provenance records the empty state.
func (a *Analyzer) recordComprehensionElementState(expr *ast.ListComprehensionExpr) {
	if a.currentElementStates == nil {
		return
	}
	state, _, known := a.elementStorageState(expr.Value)
	if a.comprehensionElementStates == nil {
		a.comprehensionElementStates = map[*ast.ListComprehensionExpr]regionRefState{}
	}
	if !known {
		delete(a.comprehensionElementStates, expr)
		return
	}
	a.comprehensionElementStates[expr] = state
}

// recordLocalContainerElementPush merges a pushed value's provenance into the receiver's
// element state; a bulk push (or extend) from anything but a parameter stops tracking the receiver.
func (a *Analyzer) recordLocalContainerElementPush(receiver ast.Expr, arg ast.Expr, bulk bool) {
	if a.currentElementStates == nil || a.currentScope == nil {
		return
	}
	ident, ok := receiver.(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	existing, tracked := a.currentElementStates[sym]
	if !tracked {
		return
	}
	if bulk {
		// Only a parameter source is modelled: its elements are no shorter-lived than its region,
		// as for a by-value read `src[i]`. A local source's elements may be shorter-lived than the
		// container that holds them.
		source, isIdent := stripParenExpr(arg).(*ast.Ident)
		if !isIdent {
			delete(a.currentElementStates, sym)
			return
		}
		sourceSym, found := a.currentScope.Lookup(source.Name)
		if !found || sourceSym == nil || sourceSym.Kind != SymbolParam {
			delete(a.currentElementStates, sym)
			return
		}
	}
	state, ok, known := a.elementStorageState(arg)
	if !known {
		delete(a.currentElementStates, sym)
		return
	}
	if !ok {
		return
	}
	merged, _ := mergeRegionRefStates(existing, state)
	a.currentElementStates[sym] = merged
}

// localContainerElementState returns the tracked element provenance for a by-value read
// `xs[i]` of an eligible local container.
func (a *Analyzer) localContainerElementState(n *ast.IndexExpr) (regionRefState, bool) {
	if !a.currentElementReads[n] || a.currentScope == nil {
		return regionRefState{}, false
	}
	ident, ok := n.Object.(*ast.Ident)
	if !ok {
		return regionRefState{}, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return regionRefState{}, false
	}
	state, ok := a.currentElementStates[sym]
	return state, ok
}

// resolveReadOnlyScanCallee resolves a direct call before the body is analyzed: a local
// function, or a module-visible one whose name has no overload set a later receiver
// rewrite could pick a different callee from.
func (a *Analyzer) resolveReadOnlyScanCallee(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil || len(a.ufcsFunctionsByName[ident.Name]) > 1 {
		return nil, false
	}
	if _, local := a.currentScope.Lookup(ident.Name); local {
		return a.resolveDirectCallFuncDecl(call)
	}
	if a.globalScope == nil {
		return nil, false
	}
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
	if !ok || sym == nil {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	return decl, ok && decl != nil
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
	for _, rp := range decl.RegionParams {
		if !strings.HasPrefix(rp, "__rg_") {
			return false
		}
	}
	// Trusted only once the callee's body was checked and nothing it can put in the container was
	// allocated in a caller's arena (a void grower adopts its container's; a forwarded parameter
	// may reach one that does).
	// A callee not analyzed yet (declared later, or recursive and in progress) is judged by
	// whether it can reach a void grower at all.
	if len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 {
		return false
	}
	if a.ambientFillAnalyzed[decl] {
		if a.ambientFillFresh[decl] {
			return false
		}
	} else if a.fillMayAdopt(decl) {
		return false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Variadic || fnType.RegionPolymorphic || index >= len(fnType.Params) {
		return false
	}
	return a.returnBorrowWritableParam(fnType.Params[index])
}

// recordCallFilledElementStates merges, for each tracked local container a call may fill, the
// provenance of every other argument into the container's element state; a container the
// callee is not known to fill only from its arguments stops being tracked.
func (a *Analyzer) recordCallFilledElementStates(call *ast.CallExpr) {
	if a.currentElementStates == nil || a.currentScope == nil || call == nil || len(call.ArgNames) != 0 || call.HasArgForward {
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
			delete(a.currentElementStates, sym)
			continue
		}
		merged := existing
		known := true
		for other, otherArg := range call.Args {
			if other == index {
				continue
			}
			// A `darray[sview]` holds views, never bytes a `darray[sview]` could view: what it can hand
			// the filled container is its ELEMENTS (merged below), not its own storage.
			ownStorageIsOnlyElements := false
			if id, isIdent := stripAddrAndParens(otherArg).(*ast.Ident); isIdent && isSviewDarrayType(sym.Type) {
				if otherSym, found := a.currentScope.Lookup(id.Name); found && otherSym != nil && isSviewDarrayType(otherSym.Type) {
					ownStorageIsOnlyElements = true
				}
			}
			if state, ok := a.regionRefStateForExpr(otherArg); ok && !ownStorageIsOnlyElements && hasRegionProvenance(state) {
				if m, ok := mergeRegionRefStates(merged, state); ok {
					merged = m
				} else {
					known = false
				}
			}
			if otherIdent, ok := stripAddrAndParens(otherArg).(*ast.Ident); ok {
				if otherSym, ok := a.currentScope.Lookup(otherIdent.Name); ok {
					if elems, ok := a.currentElementStates[otherSym]; ok && hasRegionProvenance(elems) {
						if m, ok := mergeRegionRefStates(merged, elems); ok {
							merged = m
						} else {
							known = false
						}
					}
				}
			}
		}
		if !known {
			delete(a.currentElementStates, sym)
			continue
		}
		a.currentElementStates[sym] = merged
	}
}

// noteAmbientFillFresh records that the current void grower used its adoption sanction: data
// allocated in the adopted arena may reach its grown container.
func (a *Analyzer) noteAmbientFillFresh() {
	if a.currentFuncDecl == nil || a.suppressDiagnostics {
		return
	}
	if a.ambientFillFresh == nil {
		a.ambientFillFresh = map[*ast.FuncDecl]bool{}
	}
	a.ambientFillFresh[a.currentFuncDecl] = true
}

// noteAmbientCallFill marks the current function fresh at a call that may allocate in a
// caller's arena: in a void grower, a region-polymorphic callee (its threaded region is the
// adopted one); in any function, a parameter (or a reference that may alias one) passed to a
// writable parameter of a callee not known to fill it only from its other arguments.
func (a *Analyzer) noteAmbientCallFill(call *ast.CallExpr) {
	fn := a.currentFuncDecl
	if fn == nil || call == nil || a.suppressDiagnostics {
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
		if len(call.ArgNames) != 0 || call.HasArgForward || index >= len(call.Args) || call.Args[index] != arg || !a.callArgFillsWritableParam(call, index) {
			a.noteAmbientFillFresh()
			return
		}
	}
}

// isSviewDarrayType reports `darray[sview]`, through a reference.
func isSviewDarrayType(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil {
		return false
	}
	_, isView := darray.Elem.(*SViewType)
	return isView
}
