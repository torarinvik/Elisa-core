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
	// modLoops maps each element-adding site (a pushed argument, a filling call) to its loops.
	modLoops map[ast.Node][]ast.Node
	// scalarUse are reads whose copy is only compared: they need no in-loop assumption.
	scalarUse map[*ast.IndexExpr]bool
	extendIn  map[ast.Node]map[string]bool
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
	// argByValue reports whether a call's argument binds a by-value (non-reference, non-view)
	// parameter of a directly resolved callee.
	argByValue func(call *ast.CallExpr, index int) bool
	// ufcsCall predicts the direct call a receiver-form call is rewritten to, or nil.
	ufcsCall func(call *ast.CallExpr) *ast.CallExpr
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
func (a *Analyzer) scanLocalContainerElementReads(body []ast.Stmt) (map[*ast.IndexExpr]bool, map[*ast.IterForStmt]bool, *elementLoopInfo) {
	scan := &localContainerElementScan{
		argReadOnly:       a.callArgBindsReadOnlyParam,
		argFillable:       a.callArgFillsWritableParam,
		argByValue:        a.callArgBindsByValueParam,
		ufcsCall:          a.predictedUFCSScanCall(),
		ineligible:        map[string]bool{},
		pushedIn:          map[ast.Node]map[string]bool{},
		declLoops:         map[string][]ast.Node{},
		multiDecl:         map[string]bool{},
		mentioned:         map[string]bool{},
		acceptedReceivers: map[*ast.FieldExpr]bool{},
		acceptedReads:     map[*ast.IndexExpr]bool{},
		visited:           map[uintptr]bool{},
		addrArgs:          map[*ast.AddrOfExpr]addrArgSite{},
		modLoops:          map[ast.Node][]ast.Node{},
		scalarUse:         map[*ast.IndexExpr]bool{},
		extendIn:          map[ast.Node]map[string]bool{},
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
	var loop *elementLoopInfo
	for _, read := range scan.reads {
		if accepted(read) {
			out[read.node] = true
			continue
		}
		// Rejected only because an enclosing loop also pushes into the container: the read may
		// use the elements pushed before it, as long as no later push in that loop adds
		// anything they lack (checked at each such push; region_local_container_loop_reads.go).
		if scan.ineligible[read.name] || scan.scalarUse[read.node] {
			continue
		}
		for _, l := range read.loops {
			if scan.pushedIn[l][read.name] {
				if scan.extendIn[l][read.name] {
					break
				}
				if loop == nil {
					loop = &elementLoopInfo{reads: map[*ast.IndexExpr]ast.Node{}, modLoops: scan.modLoops}
				}
				loop.reads[read.node] = l
				break
			}
		}
	}
	binders := map[*ast.IterForStmt]bool{}
	for _, read := range scan.binderReads {
		if accepted(read) {
			binders[read.loop] = true
		}
	}
	return out, binders, loop
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

// statementContainerCall matches the statement `xs.push(v)` / `xs.extend(src)` / `xs.reserve(n)` /
// `xs.truncate(n)` on a bare name (truncation only drops elements: the survivors keep their state).
func statementContainerCall(stmt *ast.ExprStmt) (*ast.FieldExpr, *ast.Ident, bool) {
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok || call.SafeReceiver != nil || call.Safe || call.HasArgForward || len(call.Args) != 1 || len(call.ArgNames) != 0 {
		return nil, nil, false
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok || callee.Safe || (callee.Field != "push" && callee.Field != "extend" && callee.Field != "reserve" && callee.Field != "truncate") {
		return nil, nil, false
	}
	receiver, ok := callee.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	return callee, receiver, true
}

func (s *localContainerElementScan) acceptRead(expr ast.Expr) {
	switch n := expr.(type) {
	case *ast.IndexExpr:
		s.acceptedReads[n] = true
	case *ast.ParenExpr:
		s.acceptRead(n.Inner)
	case *ast.TernaryExpr:
		// The value's provenance is the merge of its branches' (regionRefStateForExpr).
		s.acceptRead(n.Value)
		s.acceptRead(n.Alt)
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
				s.modLoops[n.Expr.(*ast.CallExpr).Args[0]] = append([]ast.Node(nil), s.loops...)
				if callee.Field == "extend" {
					// A bulk add usually stops tracking: an in-loop read keeps the conservative state.
					for _, loop := range s.loops {
						if s.extendIn[loop] == nil {
							s.extendIn[loop] = map[string]bool{}
						}
						s.extendIn[loop][receiver.Name] = true
					}
				}
			}
		}
	case *ast.CallExpr:
		if len(n.ArgNames) == 0 && !n.HasArgForward && s.argByValue != nil {
			// `f(xs[i])` into a by-value parameter hands the callee a copy of the element; the copy
			// carries the element's tracked provenance exactly as a struct-literal field does.
			for index, arg := range n.Args {
				if _, isIndex := arg.(*ast.IndexExpr); isIndex && s.argByValue(n, index) {
					s.acceptRead(arg)
				}
			}
		}
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
	case *ast.AssignStmt:
		// `y <- xs[i]` copies the element into y (or into a field or element of it); the store
		// checks read the same tracked provenance a declaration does.
		if !n.WriteThrough && n.AsOverlayCall == nil {
			s.acceptRead(n.Value)
		}
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
		if isComparisonOp(n.Op) {
			// A comparison yields a bool: the operand's provenance flows nowhere.
			for _, operand := range []ast.Expr{n.Left, n.Right} {
				if index, isIndex := stripParenExpr(operand).(*ast.IndexExpr); isIndex {
					s.scalarUse[index] = true
				}
			}
		}
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
			// A receiver-form call `recv.f(xs)` is rewritten to `f(recv, xs)` only during the
			// body's analysis; judge it as that call here. A prediction the rewrite does not
			// confirm is harmless: the flow judges the analyzed call and stops tracking any
			// container it cannot describe (recordCallFilledElementStates).
			judged, shift := p, 0
			if s.ufcsCall != nil {
				if rewritten := s.ufcsCall(p); rewritten != nil {
					judged, shift = rewritten, 1
				}
			}
			for index, arg := range p.Args {
				if arg == ast.Expr(ident) {
					if s.argReadOnly(judged, index+shift) {
						return
					}
					if s.argFillable != nil && s.argFillable(judged, index+shift) {
						// The call adds elements, like a push: a read in an enclosing loop
						// may see the previous iteration's fill.
						s.markPushed(name)
						s.modLoops[p] = append([]ast.Node(nil), s.loops...)
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
				s.modLoops[site.call] = append([]ast.Node(nil), s.loops...)
				return
			}
		}
	case *ast.AssignStmt:
		// `xs <- f(args)` replaces every element with the call result's, whose provenance the
		// callee's return-element summary describes; the flow records it (or stops tracking).
		if field == "Target" && !p.Optional && ast.Expr(ident) == p.Target {
			if _, isCall := stripParenExpr(p.Value).(*ast.CallExpr); isCall {
				s.markPushed(name)
				s.modLoops[p] = append([]ast.Node(nil), s.loops...)
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
	case *ast.ListComprehensionExpr:
		// `[f(x) for x in xs]` only reads xs: its binder is a by-value element copy, and the new
		// list's element provenance is computed from the binder, not from xs's tracked state.
		if field == "Source" && p.Owner == nil {
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
	if tern, ok := stripParenExpr(value).(*ast.TernaryExpr); ok {
		// `f(args) if c else []`: the elements are one branch's, so their provenance is the merge.
		state, known := a.ternaryInitElementState(tern)
		if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !known || !isDArray {
			delete(a.currentElementStates, sym)
			return
		}
		a.currentElementStates[sym] = state
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

// ternaryInitElementState describes a ternary initializer whose branches are each a call
// with a return-element summary, `[]`, or such a ternary.
func (a *Analyzer) ternaryInitElementState(tern *ast.TernaryExpr) (regionRefState, bool) {
	states := make([]regionRefState, 0, 2)
	for _, branch := range []ast.Expr{tern.Value, tern.Alt} {
		switch b := stripParenExpr(branch).(type) {
		case *ast.CallExpr:
			state, known := a.callReturnedElementState(b)
			if !known {
				return regionRefState{}, false
			}
			states = append(states, state)
		case *ast.ListLitExpr:
			if len(b.Elems) != 0 || b.Brace || b.Owner != nil || b.Keys != nil {
				return regionRefState{}, false
			}
		case *ast.TernaryExpr:
			state, known := a.ternaryInitElementState(b)
			if !known {
				return regionRefState{}, false
			}
			states = append(states, state)
		default:
			return regionRefState{}, false
		}
	}
	provenanced := states[:0]
	for _, state := range states {
		if hasRegionProvenance(state) {
			provenanced = append(provenanced, state)
		}
	}
	if len(provenanced) == 0 {
		// Every branch is `[]` or a call whose elements carry no region provenance.
		return regionRefState{}, true
	}
	return mergeRegionRefStates(provenanced...)
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

// recordLocalContainerElementAssign replaces a tracked local container's element state on a
// whole assignment `xs <- f(args)` with the callee's summarized return-element provenance;
// a call without a summary stops tracking the container.
func (a *Analyzer) recordLocalContainerElementAssign(n *ast.AssignStmt) {
	if a.currentElementStates == nil || a.currentScope == nil || n == nil || n.Optional {
		return
	}
	ident, ok := n.Target.(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	if _, tracked := a.currentElementStates[sym]; !tracked {
		return
	}
	call, isCall := stripParenExpr(n.Value).(*ast.CallExpr)
	state, known := regionRefState{}, false
	if isCall {
		state, known = a.callReturnedElementState(call)
	}
	if _, isDArray := stripRefForBounds(sym.Type).(*DArrayType); !known || !isDArray {
		a.checkElementLoopAssumptions(sym, n, regionRefState{}, false)
		delete(a.currentElementStates, sym)
		return
	}
	a.checkElementLoopAssumptions(sym, n, state, true)
	a.currentElementStates[sym] = cloneRegionRefState(state)
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
	bulkParamIndex := -1
	if bulk {
		// Only a parameter source is modelled: its elements are no shorter-lived than its region,
		// as for a by-value read `src[i]`. A local source's elements may be shorter-lived than the
		// container that holds them.
		if comp, isComp := stripParenExpr(arg).(*ast.ListComprehensionExpr); isComp {
			// `xs.extend([f(x) for x in src])`: the comprehension's elements are fresh by-value
			// copies whose provenance was recorded from its element expression.
			state, recorded := a.comprehensionElementStates[comp]
			if !recorded || comp.Owner != nil || comp.Parallel {
				a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
				a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
				delete(a.currentElementStates, sym)
				return
			}
			merged, _ := mergeRegionRefStates(existing, state)
			a.checkElementLoopAssumptions(sym, arg, merged, true)
			a.currentElementStates[sym] = merged
			return
		}
		source, isIdent := stripParenExpr(arg).(*ast.Ident)
		if !isIdent {
			a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			return
		}
		sourceSym, found := a.currentScope.Lookup(source.Name)
		if !found || sourceSym == nil || sourceSym.Kind != SymbolParam {
			a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			return
		}
		bulkParamIndex = sourceSym.ParamIndex
	}
	if bulk && bulkParamIndex >= 0 {
		if container, ok := stripRefForBounds(sym.Type).(*DArrayType); ok && container != nil && a.typeMayOwnArenaStorage(container.Elem, map[string]bool{}) {
			// `extend(param)` copies the parameter's elements, not the parameter's
			// backing buffer. Their unknown nested references are conservatively
			// bounded by the source parameter's lifetime. Recording a concrete
			// symbolic-region dependency here loses the return summary for region-
			// polymorphic copies (especially when the element type comes from an
			// imported module and its payload provenance is opaque).
			state := regionRefStateFromParamDependency(bulkParamIndex)
			merged, _ := mergeRegionRefStates(existing, state)
			a.checkElementLoopAssumptions(sym, arg, merged, true)
			a.currentElementStates[sym] = merged
			return
		}
	}
	state, ok, known := a.elementStorageState(arg)
	if !known {
		a.checkElementLoopAssumptions(sym, arg, regionRefState{}, false)
		delete(a.currentElementStates, sym)
		return
	}
	if !ok {
		return
	}
	merged, _ := mergeRegionRefStates(existing, state)
	a.checkElementLoopAssumptions(sym, arg, merged, true)
	a.currentElementStates[sym] = merged
}

// localContainerElementState returns the tracked element provenance for a by-value read
// `xs[i]` of an eligible local container.
func (a *Analyzer) localContainerElementState(n *ast.IndexExpr) (regionRefState, bool) {
	if a.currentScope == nil {
		return regionRefState{}, false
	}
	loopRead := false
	if !a.currentElementReads[n] {
		if a.currentElementLoop == nil || a.currentElementLoop.reads[n] == nil {
			return regionRefState{}, false
		}
		loopRead = true
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
	if ok && loopRead {
		a.currentElementLoop.assume(sym, state, n)
	}
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
	a.ensureProvisionalSummaries(decl)
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
			a.checkElementLoopAssumptions(sym, call, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			continue
		}
		merged := existing
		known := true
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
			a.checkElementLoopAssumptions(sym, call, regionRefState{}, false)
			delete(a.currentElementStates, sym)
			continue
		}
		a.checkElementLoopAssumptions(sym, call, merged, true)
		a.currentElementStates[sym] = merged
	}
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

// containerElementsHoldOnlyViews reports a darray (through a reference) whose elements borrow
// only through sviews: every reachable component is an sview, a scalar, or an inline aggregate of
// them (no reference, view, cstr, nested container or opaque type). Such an element can view
// bytes, but cannot point at another container's storage or share its buffer, so a
// `darray[sview]` or non-byte scalar container can hand it only its ELEMENTS.
func containerElementsHoldOnlyViews(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil || darray.Elem == nil {
		return false
	}
	return typeComponentsViewOnly(darray.Elem)
}

// typeComponentsViewOnly reports a type whose every component is a view or a plain scalar —
// nothing that owns region storage. Anything opaque is not view-only.
func typeComponentsViewOnly(t Type) bool {
	if t == nil {
		return false
	}
	components, opaque := []Type{}, false
	collectReturnBorrowComponents(StripAggregateStateType(t), false, &components, &opaque, map[Type]bool{})
	if opaque {
		return false
	}
	for _, component := range components {
		switch c := component.(type) {
		case *BuiltinType:
			switch c.Name {
			case "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64", "bool", "char":
			default:
				return false
			}
		case *SViewType, *BitIntType, *ConstEnumType, *BitGroupType, *StructType, *EnumType, *TupleType, *ArrayType, *OptionalType, *ErrorUnionType:
		default:
			return false
		}
	}
	return true
}

// isNonByteScalarContainerType reports a darray or fixed array (through a reference) whose
// element type holds no byte anywhere (no u8/i8 scalar, array or nested darray of them): the
// container's own storage holds no bytes a `darray[sview]` element could view, so what a callee
// can hand a filled `darray[sview]` from it is its ELEMENTS, never its storage. Anything opaque
// (a type parameter, an unresolved type) is not exempt.
func isNonByteScalarContainerType(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	var elem Type
	switch c := t.(type) {
	case *DArrayType:
		if c == nil {
			return false
		}
		elem = c.Elem
	case *ArrayType:
		if c == nil {
			return false
		}
		elem = c.Elem
	default:
		return false
	}
	if elem == nil {
		return false
	}
	components, opaque := []Type{}, false
	collectReturnBorrowComponents(StripAggregateStateType(elem), false, &components, &opaque, map[Type]bool{})
	if opaque {
		return false
	}
	for _, component := range components {
		switch c := component.(type) {
		case *BuiltinType:
			switch c.Name {
			case "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "bool":
			default:
				return false
			}
		case *CStrType:
			return false
		case *OptionalType, *ErrorUnionType:
		}
	}
	return true
}

// summariesOff reports whether the current body's fill summary must not be recorded: a quiet
// pass publishes summaries only when it is the on-demand pass for exactly this function.
func (a *Analyzer) summariesOff() bool {
	return a.suppressDiagnostics && a.provisionalSummaryFn != a.currentFuncDecl
}

// ensureProvisionalSummaries analyzes a callee that a caller reaches before the declaration-order
// pass does, quietly, so its fill and return-element summaries exist at the call site. Without
// it a later-declared callee is judged by the coarse fallbacks, which over-report. The real pass
// still runs later and republishes the same summaries with diagnostics on. A callee on a call
// cycle is solved by summaryConvergence instead (region_summary_convergence.go).
func (a *Analyzer) ensureProvisionalSummaries(decl *ast.FuncDecl) {
	if a == nil || decl == nil || a.ambientFillAnalyzed[decl] || (a.suppressDiagnostics && a.provisionalSummaryFn == nil) {
		return
	}
	if a.funcAnalysisActive[decl] {
		a.assumeSummaryForActive(decl)
		return
	}
	if !a.summaryAnalyzable(decl) {
		return
	}
	// Only a callee whose summary can feed a store or return check is worth a second analysis.
	if !a.summaryRelevant(decl) {
		return
	}
	if a.convergeDepth == 0 && a.funcIsCyclic(decl) {
		a.convergeSummaries(decl)
		return
	}
	a.analyzeQuietly(decl)
}

func (a *Analyzer) summaryAnalyzable(decl *ast.FuncDecl) bool {
	if len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 || decl.IsContract || decl.Body == nil {
		return false
	}
	if _, ok := a.symbolForFuncDecl(decl); !ok {
		return false
	}
	return a.funcDeclSymbols[decl] != nil
}

// analyzeQuietly runs decl's body analysis with diagnostics off, publishing its summaries.
func (a *Analyzer) analyzeQuietly(decl *ast.FuncDecl) {
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return
	}
	if a.convergeDepth > 0 {
		a.convMembers[decl] = true
	}
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	savedSuppress, savedOpt, savedProvisional := a.suppressDiagnostics, a.suppressOptimizationFacts, a.provisionalSummaryFn
	savedStatic := a.staticContextDepth
	if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
		if idx := strings.LastIndex(fnType.Name, "."); idx >= 0 {
			a.currentNamespace = fnType.Name[:idx]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.funcDeclUsings[decl]...)
	a.suppressDiagnostics, a.suppressOptimizationFacts, a.provisionalSummaryFn = true, true, decl
	a.staticContextDepth = 0
	proofMark := len(a.proofReport)
	a.analyzeFuncWithTypeArgs(decl, nil)
	if len(a.proofReport) > proofMark {
		a.proofReport = a.proofReport[:proofMark] // a quiet pass reports nothing
	}
	a.currentNamespace, a.currentUsings = savedNamespace, savedUsings
	a.suppressDiagnostics, a.suppressOptimizationFacts, a.provisionalSummaryFn = savedSuppress, savedOpt, savedProvisional
	a.staticContextDepth = savedStatic
	returnElementWalk(reflect.ValueOf(decl.Body), func(ret *ast.ReturnStmt) {
		delete(a.returnElementStmtStates, ret)
		delete(a.returnElementStmtUnknown, ret)
	}, map[uintptr]bool{})
}

// headerOnlyDarrayElemTypeName reports a `darray[E]` (through a reference) whose element E is
// `sview` or a non-byte scalar, returning E's name.
func headerOnlyDarrayElemTypeName(t Type) (string, bool) {
	elem, ok := darrayElemType(t)
	if !ok {
		return "", false
	}
	switch e := StripAggregateStateType(elem).(type) {
	case *SViewType:
		return "sview", true
	case *BuiltinType:
		switch e.Name {
		case "bool", "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "char":
			return e.Name, true
		}
	}
	return "", false
}

// darrayElemType returns the element type of a `darray[E]` (through a reference).
func darrayElemType(t Type) (Type, bool) {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil || darray.Elem == nil {
		return nil, false
	}
	return darray.Elem, true
}

// predictedUFCSScanCall returns the scan's predictor for receiver-form calls: `recv.f(args)`
// whose name has exactly one free UFCS function is judged as `f(recv, args)`. Predictions are
// memoized per call so the synthetic call is built once.
func (a *Analyzer) predictedUFCSScanCall() func(call *ast.CallExpr) *ast.CallExpr {
	memo := map[*ast.CallExpr]*ast.CallExpr{}
	return func(call *ast.CallExpr) *ast.CallExpr {
		if call == nil {
			return nil
		}
		if rewritten, seen := memo[call]; seen {
			return rewritten
		}
		var rewritten *ast.CallExpr
		if fieldExpr, ok := call.Func.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Object != nil && len(a.ufcsFunctionsByName[fieldExpr.Field]) == 1 {
			args := make([]ast.Expr, 0, len(call.Args)+1)
			args = append(args, fieldExpr.Object)
			args = append(args, call.Args...)
			rewritten = &ast.CallExpr{Position: call.Position, Func: &ast.Ident{Position: fieldExpr.Position, Name: fieldExpr.Field}, Args: args}
		}
		memo[call] = rewritten
		return rewritten
	}
}
