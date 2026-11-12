package semantic

import (
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// storeFlowComponent returns the summarizable members of decl's strongly connected component
// (iterative Tarjan over callEdgesOf). Every component found on the way is cached, so each
// function's component is computed once.
func (a *Analyzer) storeFlowComponent(decl *ast.FuncDecl) []*ast.FuncDecl {
	decl = a.readDecl(decl)
	if comp, ok := a.storeFlowSCC[decl]; ok {
		if mapped, changed := a.parallelReadDeclSlice(comp); changed {
			// Warm SCCs contain canonical declarations. Translate them into this
			// worker's read identities and keep the shadow-bearing slice local.
			a.storeFlowSCC[decl] = mapped
			return mapped
		}
		return comp
	}
	if a.storeFlowSCC == nil {
		a.storeFlowSCC = map[*ast.FuncDecl][]*ast.FuncDecl{}
	}
	edges := func(fn *ast.FuncDecl) []*ast.FuncDecl {
		var out []*ast.FuncDecl
		for _, c := range a.callEdgesOf(fn) {
			if !storeFlowSummarizable(c) {
				continue
			}
			if _, done := a.storeFlowSCC[c]; !done {
				out = append(out, c)
			}
		}
		return out
	}
	initial := edges(decl)
	if len(initial) == 0 {
		// No unresolved outgoing edge can participate in a cycle. Avoid
		// allocating Tarjan's maps for this common leaf component.
		component := []*ast.FuncDecl{decl}
		a.storeFlowSCC[decl] = component
		return component
	}
	index := map[*ast.FuncDecl]int{}
	low := map[*ast.FuncDecl]int{}
	onStack := map[*ast.FuncDecl]bool{}
	var stack []*ast.FuncDecl
	type frame struct {
		fn   *ast.FuncDecl
		next int
	}
	counter := 0
	push := func(fn *ast.FuncDecl) frame {
		index[fn], low[fn] = counter, counter
		counter++
		stack = append(stack, fn)
		onStack[fn] = true
		return frame{fn: fn}
	}
	succ := map[*ast.FuncDecl][]*ast.FuncDecl{decl: initial}
	frames := []frame{push(decl)}
	for len(frames) > 0 {
		top := &frames[len(frames)-1]
		if top.next < len(succ[top.fn]) {
			c := succ[top.fn][top.next]
			top.next++
			if _, seen := index[c]; !seen {
				succ[c] = edges(c)
				frames = append(frames, push(c))
			} else if onStack[c] && index[c] < low[top.fn] {
				low[top.fn] = index[c]
			}
			continue
		}
		fn := top.fn
		frames = frames[:len(frames)-1]
		if len(frames) > 0 && low[fn] < low[frames[len(frames)-1].fn] {
			low[frames[len(frames)-1].fn] = low[fn]
		}
		if low[fn] == index[fn] {
			var comp []*ast.FuncDecl
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == fn {
					break
				}
			}
			for _, w := range comp {
				a.storeFlowSCC[w] = comp
			}
		}
	}
	return a.storeFlowSCC[decl]
}

// storeFlowBoolMatrix keeps matrix cells in one allocation. Each row has a
// bounded capacity so appending to it cannot overwrite the following row.
func storeFlowBoolMatrix(n int) [][]bool {
	rows := make([][]bool, n)
	cells := make([]bool, n*n)
	for i := range rows {
		start, end := i*n, (i+1)*n
		rows[i] = cells[start:end:end]
	}
	return rows
}

// newStoreFlowSummary is the least summary: each parameter reaches only itself.
func newStoreFlowSummary(n int) *storeFlowSummary {
	vectors := make([]bool, 2*n)
	s := &storeFlowSummary{conn: storeFlowBoolMatrix(n), reach: storeFlowBoolMatrix(n), retConn: vectors[:n:n], written: vectors[n : 2*n : 2*n]}
	for i := 0; i < n; i++ {
		s.conn[i][i] = true
		s.reach[i][i] = true
	}
	return s
}

// storeFlowSummaryWithin reports that every flow x states, y states too.
func storeFlowSummaryWithin(x, y *storeFlowSummary) bool {
	n := len(y.conn)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if x.conn[i][j] && !y.conn[i][j] || x.reach[i][j] && !y.reach[i][j] {
				return false
			}
		}
		if x.retConn[i] && !y.retConn[i] || x.written[i] && !y.written[i] {
			return false
		}
	}
	return true
}

// storeFlowSummaryJoin is the pointwise union of two summaries of the same function.
func storeFlowSummaryJoin(x, y *storeFlowSummary) *storeFlowSummary {
	n := len(y.conn)
	out := newStoreFlowSummary(n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			out.conn[i][j] = x.conn[i][j] || y.conn[i][j]
			out.reach[i][j] = x.reach[i][j] || y.reach[i][j]
		}
		out.retConn[i] = x.retConn[i] || y.retConn[i]
		out.written[i] = x.written[i] || y.written[i]
	}
	return out
}

func (a *Analyzer) computeStoreFlow(decl *ast.FuncDecl) *storeFlowSummary {
	decl = a.readDecl(decl)
	savedFlowDecl := a.storeFlowCurrentDecl
	a.storeFlowCurrentDecl = decl
	defer func() { a.storeFlowCurrentDecl = savedFlowDecl }()
	// Each summary sees a stable named-type set. Do not retain these results
	// across analyses, where the named-type table can be edited.
	savedStructLookup, savedStructLookupComplete := a.storeFlowStructLookup, a.storeFlowStructLookupComplete
	a.storeFlowStructLookup = make(map[string]Type)
	a.storeFlowStructLookupComplete = false
	defer func() {
		a.storeFlowStructLookup, a.storeFlowStructLookupComplete = savedStructLookup, savedStructLookupComplete
	}()
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		if index := lastDotIndex(sym.Name); index >= 0 {
			a.currentNamespace = sym.Name[:index]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.declarationUsings(decl)...)
	defer func() { a.currentNamespace, a.currentUsings = savedNamespace, savedUsings }()
	savedParams := a.storeFlowParams
	a.storeFlowParams = map[string]bool{}
	for _, param := range decl.Params {
		a.storeFlowParams[param.Name] = true
	}
	binders := a.storeFlowBinderIndexFor(decl)
	binders.classify()
	savedDarrays := a.storeFlowDarrays
	a.storeFlowDarrays = binders.darrayNames
	savedLocals := a.storeFlowLocals
	a.storeFlowLocals = binders.binderNames()
	defer func() { a.storeFlowLocals = savedLocals }()
	savedViews := a.storeFlowViews
	a.storeFlowViews = binders.viewNames
	defer func() { a.storeFlowViews = savedViews }()
	savedNamed := a.storeFlowNamedTypes
	a.storeFlowNamedTypes = binders.namedTypes
	defer func() { a.storeFlowNamedTypes = savedNamed }()
	savedEnv, savedStmtEnv := a.storeFlowEnv, a.storeFlowStmtEnv
	a.storeFlowEnv, a.storeFlowStmtEnv = nil, map[uintptr]map[string]string{}
	defer func() { a.storeFlowEnv, a.storeFlowStmtEnv = savedEnv, savedStmtEnv }()
	defer func() { a.storeFlowParams, a.storeFlowDarrays = savedParams, savedDarrays }()
	scalars := a.storeFlowScalarNames(decl)
	c := &storeFlowCtx{written: a.storeFlowWritten(decl, scalars), scalars: scalars}
	for _, param := range decl.Params {
		c.u.find(param.Name)
		c.pu.find(param.Name)
	}
	seen := acquireStoreFlowSeen()
	a.storeFlowNodes(reflect.ValueOf(decl.Body), c, seen)
	releaseStoreFlowSeen(seen)
	anchors := map[storeFlowClass][]storeFlowClass{}
	for _, e := range c.edges {
		root := c.storeFlowKey(e[0])
		anchors[root] = append(anchors[root], c.storeFlowKey(e[1]))
	}
	reachMemo := storeFlowReachMemo{ctx: c, anchors: anchors}
	n := len(decl.Params)
	conn := storeFlowBoolMatrix(n)
	reach := storeFlowBoolMatrix(n)
	retConn := make([]bool, n)
	// A function that yields a value on a path with no `return` (or threads lmut values back)
	// is treated as returning something derived from every parameter.
	returnsValue := decl.ReturnType != nil
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		if ft, ok := sym.Type.(*FuncType); ok && ft != nil && ft.Return != nil && isVoidType(ft.Return) {
			returnsValue = false
		}
	}
	implicit := len(decl.LmutThreadSlots) != 0 || (returnsValue && !storeFlowEndsInReturn(decl.Body))
	// A scalar result carries no reference: nothing a parameter holds reaches the caller through it.
	scalarResult := len(decl.LmutThreadSlots) == 0 && decl.ReturnType != nil && storeFlowScalarTypeExpr(decl.ReturnType)
	if len(decl.LmutThreadSlots) == 0 {
		if sym := a.funcDeclSymbols[decl]; sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok && ft != nil && ft.Return != nil {
				scalarResult = storeFlowPointerFreeReturn(ft.Return, map[Type]bool{})
			}
		}
	}
	// These classifications and union roots are fixed after the body scan.
	// Compute them once per parameter instead of once per matrix cell.
	type paramClass struct {
		pure bool
		root string
	}
	var classBuf [16]paramClass
	classes := classBuf[:]
	if n > len(classes) {
		classes = make([]paramClass, n)
	} else {
		classes = classes[:n]
	}
	for i, p := range decl.Params {
		classes[i].pure = c.isPure(p.Name)
		if classes[i].pure {
			classes[i].root = c.pu.find(p.Name)
		} else {
			classes[i].root = c.u.find(p.Name)
		}
	}
	for i, pi := range decl.Params {
		iPure := classes[i].pure
		for j, pj := range decl.Params {
			jPure := classes[j].pure
			switch {
			case i == j:
				reach[i][j] = true
			case !iPure && !jPure:
				reach[i][j] = classes[i].root == classes[j].root || reachMemo.reaches(pi.Name, pj.Name)
			case iPure && jPure:
				reach[i][j] = classes[i].root == classes[j].root
			case iPure:
				reach[i][j] = reachMemo.reaches(pi.Name, pj.Name)
			default:
				reach[i][j] = false // a parameter that is never written receives nothing
			}
		}
	}
	for i, pi := range decl.Params {
		for j := range decl.Params {
			conn[i][j] = reach[i][j] || reach[j][i]
		}
		iPure := classes[i].pure
		if iPure {
			retConn[i] = implicit || reachMemo.reaches(pi.Name, storeFlowRetName)
		} else {
			retConn[i] = implicit || classes[i].root == c.u.find(storeFlowRetName) || reachMemo.reaches(pi.Name, storeFlowRetName)
		}
		if scalarResult {
			retConn[i] = false
		}
	}
	written := make([]bool, n)
	for i := range decl.Params {
		written[i] = !classes[i].pure
	}
	return &storeFlowSummary{conn: conn, reach: reach, retConn: retConn, written: written}
}

const storeFlowRetName = "\x00ret"

func storeFlowEndsInReturn(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	switch tail := body[len(body)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.RegionStmt:
		// Function-body inference wraps allocation-bearing bodies in a
		// synthesized lazy region. The region is transparent to control flow;
		// inspect its tail so an explicit return is not mistaken for an
		// implicit result derived from every parameter.
		return tail != nil && storeFlowEndsInReturn(tail.Body)
	case *ast.CanStmt:
		// A plain permission block executes its body directly. Handler forms can
		// redirect control, so keep those conservative.
		return tail != nil && tail.As == "" && tail.HandlerName == "" && len(tail.HandlerArgs) == 0 && storeFlowEndsInReturn(tail.Body)
	case *ast.ScopeStmt:
		return tail != nil && storeFlowEndsInReturn(tail.Body)
	case *ast.InStoreStmt:
		return tail != nil && storeFlowEndsInReturn(tail.Body)
	case *ast.IfStmt:
		if tail == nil || len(tail.Else) == 0 || !storeFlowEndsInReturn(tail.Then) || !storeFlowEndsInReturn(tail.Else) {
			return false
		}
		for _, clause := range tail.Elifs {
			if !storeFlowEndsInReturn(clause.Body) {
				return false
			}
		}
		return true
	case *ast.MatchStmt:
		// Every arm returns, and an unguarded wildcard arm leaves no value unmatched.
		if tail == nil || len(tail.Arms) == 0 {
			return false
		}
		catchAll := false
		for _, arm := range tail.Arms {
			if !storeFlowEndsInReturn(arm.Body) {
				return false
			}
			if _, wild := arm.Pattern.(*ast.MatchWildcardPattern); wild && arm.Guard == nil {
				catchAll = true
			}
		}
		return catchAll
	default:
		return false
	}
}

var astStmtType = reflect.TypeOf((*ast.Stmt)(nil)).Elem()
var astTypeExprType = reflect.TypeOf((*ast.TypeExpr)(nil)).Elem()
var astMatchArmType = reflect.TypeOf(ast.MatchArm{})

// The identifier fast path is valid only for the schema whose sole string
// fields are Position.File and Name. Fall back if the AST schema changes.
var storeFlowIdentFast = func() bool {
	id := reflect.TypeOf(ast.Ident{})
	pos := reflect.TypeOf(lexer.Pos{})
	if id.NumField() != 2 || id.Field(0).Name != "Position" || id.Field(0).Type != pos || id.Field(1).Name != "Name" || id.Field(1).Type.Kind() != reflect.String || pos.NumField() != 7 {
		return false
	}
	fields := []string{"File", "Line", "Col", "Offset", "EndLine", "EndCol", "EndOffset"}
	for i, name := range fields {
		field := pos.Field(i)
		if field.Name != name || (i == 0 && field.Type.Kind() != reflect.String) || (i != 0 && field.Type.Kind() != reflect.Int) {
			return false
		}
	}
	return true
}()

var storeFlowPositionFast = storeFlowIdentFast

// Integer literal payload and suffix are excluded by storeFlowSkipFields.
// Keep the shortcut conditional on the exact syntax schema.
var storeFlowIntLiteralFast = func() bool {
	t := reflect.TypeOf(ast.IntLit{})
	return storeFlowIdentFast && t.NumField() == 4 &&
		t.Field(0).Name == "Position" && t.Field(0).Type == reflect.TypeOf(lexer.Pos{}) &&
		t.Field(1).Name == "Value" && t.Field(1).Type.Kind() == reflect.String &&
		t.Field(2).Name == "Suffix" && t.Field(2).Type.Kind() == reflect.String &&
		t.Field(3).Name == "IsHex" && t.Field(3).Type.Kind() == reflect.Bool
}()

// The direct binary walk must visit all query-bearing fields in schema order.
var storeFlowBinaryFast = func() bool {
	t := reflect.TypeOf(ast.BinaryExpr{})
	return t.NumField() == 5 && t.Field(0).Name == "Position" && t.Field(0).Type == reflect.TypeOf(lexer.Pos{}) &&
		t.Field(1).Name == "Op" && t.Field(1).Type.Kind() == reflect.Int16 &&
		t.Field(2).Name == "Left" && t.Field(2).Type == reflect.TypeOf((*ast.Expr)(nil)).Elem() &&
		t.Field(3).Name == "Right" && t.Field(3).Type == reflect.TypeOf((*ast.Expr)(nil)).Elem() &&
		t.Field(4).Name == "LoweredCall" && t.Field(4).Type == reflect.TypeOf((*ast.CallExpr)(nil))
}()

var storeFlowParenFast = func() bool {
	t := reflect.TypeOf(ast.ParenExpr{})
	return t.NumField() == 2 && t.Field(0).Name == "Position" && t.Field(0).Type == reflect.TypeOf(lexer.Pos{}) &&
		t.Field(1).Name == "Inner" && t.Field(1).Type == reflect.TypeOf((*ast.Expr)(nil)).Elem()
}()

var storeFlowIndexFast = func() bool {
	t := reflect.TypeOf(ast.IndexExpr{})
	if t.NumField() != 9 || t.Field(0).Name != "Position" || t.Field(0).Type != reflect.TypeOf(lexer.Pos{}) {
		return false
	}
	for i, name := range []string{"Object", "Index", "Index2", "Fallback"} {
		f := t.Field(i + 1)
		if f.Name != name || f.Type != reflect.TypeOf((*ast.Expr)(nil)).Elem() {
			return false
		}
	}
	return t.Field(5).Name == "LegacyElseFallback" && t.Field(5).Type.Kind() == reflect.Bool &&
		t.Field(6).Name == "AsSpecialize" && t.Field(6).Type == reflect.TypeOf((*ast.SpecializeExpr)(nil)) &&
		t.Field(7).Name == "AsOverlayCall" && t.Field(7).Type == reflect.TypeOf((*ast.CallExpr)(nil)) &&
		t.Field(8).Name == "AsBuiltinDictGet" && t.Field(8).Type == reflect.TypeOf((*ast.CallExpr)(nil))
}()

// storeFlowTypeInfo caches what storeFlowMentions asks of a reflect.Type on every node it
// visits: method-set tests (Implements) and the struct field filter were ~4 s of a stage1
// driver compile when recomputed per node.
type storeFlowTypeInfo struct {
	isStmt       bool  // pointer type implementing ast.Stmt
	isTypeExpr   bool  // pointer type implementing ast.TypeExpr
	binding      bool  // pointer to a storeFlowBindingExprs node
	binder       bool  // pointer to a syntax node that can introduce binders
	fields       []int // struct: exported fields to walk, in declaration order
	binderFields []int // fields the binder-index collector can examine
}

var storeFlowTypeInfoCache sync.Map // map[reflect.Type]*storeFlowTypeInfo

// The same immutable type metadata is read at every node. Keep a lock-free
// direct-mapped front cache; collisions fall back to the authoritative map.
type storeFlowTypeSlot struct {
	t    reflect.Type
	info *storeFlowTypeInfo
}

var storeFlowTypeFront [256]atomic.Pointer[storeFlowTypeSlot]

func storeFlowTypeInfoOf(t reflect.Type) *storeFlowTypeInfo {
	addr := (*[2]uintptr)(unsafe.Pointer(&t))[1]
	slot := &storeFlowTypeFront[(addr>>5^addr>>13)&255]
	if entry := slot.Load(); entry != nil && entry.t == t {
		return entry.info
	}
	info := storeFlowTypeInfoSlow(t)
	slot.Store(&storeFlowTypeSlot{t: t, info: info})
	return info
}

func storeFlowTypeInfoSlow(t reflect.Type) *storeFlowTypeInfo {
	if cached, ok := storeFlowTypeInfoCache.Load(t); ok {
		return cached.(*storeFlowTypeInfo)
	}
	info := &storeFlowTypeInfo{}
	switch t.Kind() {
	case reflect.Pointer:
		info.isStmt = t.Implements(astStmtType)
		info.isTypeExpr = t.Implements(astTypeExprType)
		info.binding = storeFlowBindingExprs[t.Elem().Name()]
		info.binder = storeFlowBinderNode(t.Elem().Name())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Name != "Captures" && field.Name != "HeaderCaptures" {
				switch field.Type.Kind() {
				case reflect.String:
					if !storeScanQualifierFields[field.Name] {
						info.binderFields = append(info.binderFields, i)
					}
				case reflect.Slice:
					elem := field.Type.Elem()
					if elem.Kind() == reflect.String || (elem.Kind() == reflect.Struct && elem != storeScanPatternArgType) {
						info.binderFields = append(info.binderFields, i)
					}
				}
			}
			if !field.IsExported() {
				continue
			}
			if storeFlowSkipFields[field.Name] && (field.Type.Kind() == reflect.String || field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String) {
				continue
			}
			// All consumers of these fields ignore scalar kinds other than strings,
			// and do not traverse maps. Avoid dispatching the walker for them.
			switch field.Type.Kind() {
			case reflect.String, reflect.Pointer, reflect.Interface, reflect.Struct, reflect.Slice, reflect.Array:
			default:
				continue
			}
			info.fields = append(info.fields, i)
		}
	}
	cached, _ := storeFlowTypeInfoCache.LoadOrStore(t, info)
	return cached.(*storeFlowTypeInfo)
}

// storeFlowStmt applies unit to statement stmt and then to the statements nested in it.
func (a *Analyzer) storeFlowStmt(stmt reflect.Value, seen map[uintptr]bool, unit func(stmt reflect.Value, nested []reflect.Value) []reflect.Value) {
	a.storeFlowEnv = a.storeFlowStmtEnv[stmt.Pointer()]
	holder := storeFlowNestedPool.Get().(*[]reflect.Value)
	nested := unit(stmt, (*holder)[:0])
	a.storeFlowEnv = nil
	for _, n := range nested {
		seen[n.Pointer()] = true
		a.storeFlowStmt(n, seen, unit)
	}
	if cap(nested) <= 4096 {
		clear(nested[:cap(nested)])
		*holder = nested[:0]
		storeFlowNestedPool.Put(holder)
	}
}

var storeFlowNestedPool = sync.Pool{New: func() any { values := make([]reflect.Value, 0, 8); return &values }}
