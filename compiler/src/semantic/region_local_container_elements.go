package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sync"
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
	// extendSources are local sources of statement `ys.extend(xs)` calls with their loops.
	extendSources []localContainerExtendSource
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

type localContainerExtendSource struct {
	call  *ast.CallExpr
	name  string
	loops []ast.Node
}

type localContainerElementRead struct {
	name  string
	node  *ast.IndexExpr
	loop  *ast.IterForStmt
	loops []ast.Node
}

var (
	astNodeType          = reflect.TypeOf((*ast.Node)(nil)).Elem()
	astPkgPath           = reflect.TypeOf(ast.Ident{}).PkgPath()
	elementScanSeenPool  = sync.Pool{New: func() any { return &fillWalkSeen{values: make(map[uintptr]bool)} }}
	elementScanNamesPool = sync.Pool{New: func() any {
		return &elementScanNames{ineligible: map[string]bool{}, multiDecl: map[string]bool{}, mentioned: map[string]bool{}}
	}}
)

type elementScanNames struct {
	ineligible, multiDecl, mentioned map[string]bool
	peak, sparse                     int
}

func (n *elementScanNames) reset() bool {
	used := max(len(n.ineligible), len(n.multiDecl), len(n.mentioned))
	if used > 1<<14 {
		return false
	}
	n.peak = max(n.peak, used)
	if used < n.peak/8 {
		n.sparse++
	} else {
		n.sparse = 0
	}
	if n.sparse >= 32 {
		n.ineligible, n.multiDecl, n.mentioned = make(map[string]bool, used), make(map[string]bool, used), make(map[string]bool, used)
		n.peak, n.sparse = used, 0
	} else {
		clear(n.ineligible)
		clear(n.multiDecl)
		clear(n.mentioned)
	}
	return true
}

// scanLocalContainerElementReads computes, for one function body, the index reads whose
// element provenance may be used (see the file comment).
func (a *Analyzer) scanLocalContainerElementReads(body []ast.Stmt) (map[*ast.IndexExpr]bool, map[*ast.IterForStmt]bool, *elementLoopInfo) {
	if invocation := a.incrementalBodies; invocation != nil && invocation.tracking && !a.suppressDiagnostics {
		invocation.session.stats.PrimaryElementScans++
	}
	seen := elementScanSeenPool.Get().(*fillWalkSeen)
	names := elementScanNamesPool.Get().(*elementScanNames)
	defer func() {
		if seen.reset() {
			elementScanSeenPool.Put(seen)
		}
		if names.reset() {
			elementScanNamesPool.Put(names)
		}
	}()
	scan := &localContainerElementScan{
		argReadOnly:       a.callArgBindsReadOnlyParam,
		argFillable:       a.callArgFillsWritableParam,
		argByValue:        a.callArgBindsByValueParam,
		ufcsCall:          a.predictedUFCSScanCall(),
		ineligible:        names.ineligible,
		pushedIn:          map[ast.Node]map[string]bool{},
		declLoops:         map[string][]ast.Node{},
		multiDecl:         names.multiDecl,
		mentioned:         names.mentioned,
		acceptedReceivers: map[*ast.FieldExpr]bool{},
		acceptedReads:     map[*ast.IndexExpr]bool{},
		visited:           seen.values,
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
	// An extend source is modelled directly when no loop enclosing the extend grows it again.
	// When one does, a later iteration copies elements the state at the call has not seen yet,
	// so its state at the call is only an ASSUMPTION that every later growth of the source in
	// that loop must preserve (checked like an in-loop element read; see
	// region_local_container_loop_reads.go).
	extendSources := map[ast.Expr]bool{}
	var loopExtendSources []localContainerExtendSource
	for _, src := range scan.extendSources {
		ok := !scan.ineligible[src.name]
		for _, loop := range src.loops {
			if scan.pushedIn[loop][src.name] {
				ok = false
				break
			}
		}
		if ok {
			extendSources[src.call.Args[0]] = true
		} else if !scan.ineligible[src.name] {
			loopExtendSources = append(loopExtendSources, src)
		}
	}
	a.currentElementExtendSources = extendSources
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
					loop = &elementLoopInfo{reads: map[ast.Node]ast.Node{}, modLoops: scan.modLoops}
				}
				loop.reads[read.node] = l
				break
			}
		}
	}
	for _, src := range loopExtendSources {
		for _, l := range src.loops {
			if scan.pushedIn[l][src.name] {
				if loop == nil {
					loop = &elementLoopInfo{reads: map[ast.Node]ast.Node{}, modLoops: scan.modLoops}
				}
				loop.reads[src.call.Args[0]] = l
				extendSources[src.call.Args[0]] = true
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
	for _, f := range localContainerScanFields(v.Type()) {
		s.walkValue(v.Field(f.index), owner, f.name)
	}
}

type localContainerScanField struct {
	index int
	name  string
}

var localContainerScanFieldCache sync.Map // map[reflect.Type][]localContainerScanField

// localContainerScanFields lists a struct type's exported fields that walkStructFields descends
// into (maps, funcs and channels are skipped), cached per type instead of re-derived per node.
func localContainerScanFields(t reflect.Type) []localContainerScanField {
	if cached, ok := localContainerScanFieldCache.Load(t); ok {
		return cached.([]localContainerScanField)
	}
	var fields []localContainerScanField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Map, reflect.Func, reflect.Chan:
			continue
		}
		fields = append(fields, localContainerScanField{index: i, name: f.Name})
	}
	cached, _ := localContainerScanFieldCache.LoadOrStore(t, fields)
	return cached.([]localContainerScanField)
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
