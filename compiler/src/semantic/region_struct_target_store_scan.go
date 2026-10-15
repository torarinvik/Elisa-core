package semantic

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// storeScanPosType is skipped by the name scans: a position's file name is not a binding.
var storeScanPosType = reflect.TypeOf(lexer.Pos{})

// storeMode classifies what a callee may store from one parameter into another's storage.
type storeMode int

const (
	storeNone storeMode = iota // nothing reachable from the source is stored
	// storeViews: only VIEW-ONLY values (built from scalars and plain views) read out of a source
	// that holds no bytes are stored -- they cannot point into the source's own storage, only
	// where its views already point.
	storeViews
	storeElements // only element/field VALUES read out of the source are stored
	storeHeader   // the source value itself (a container header, a view of it) may be stored
)

// narrowStoreMode: a callee storing (part of) its parameter stores at most what the argument
// passed for it carried from the caller's source -- the smaller of the two, unless the argument
// is the source itself (header), which the callee's verdict alone describes.
func narrowStoreMode(argMode, inner storeMode) storeMode {
	if inner == storeNone || argMode == storeHeader || inner < argMode {
		return inner
	}
	return argMode
}

func maxStoreMode(x, y storeMode) storeMode {
	if x > y {
		return x
	}
	return y
}

// calleeStoreModeInto is a DIRECTED, syntactic refinement of the store-flow summary for a
// struct-reference target: what may the callee store from parameter `other` into storage rooted
// at parameter `index`? The store-flow summary unions every pair of written parameters, so
// `parse_one(parser, items)` pushing FROM parser INTO items also reports items reaching parser.
//
// Two taint sets are tracked to a fixpoint: HEADER names may hold the source value itself (the
// parameter, and locals bound from an expression that uses it whole), CONTENT names may hold
// something read out of it (element copies, loop binders over it, anything bound by a
// construct this scan does not model whose header mentions a tainted name). A store into the
// target is any assignment whose target is rooted at a target alias, any method call on a
// target-rooted receiver, and any call passing both a target-rooted and a tainted argument --
// recursed into when the callee resolves, assumed a HEADER store otherwise.
func (a *Analyzer) calleeStoreModeInto(decl *ast.FuncDecl, other, index int) storeMode {
	key := storeScanKey{decl, other, index}
	if mode, ok := a.storeScanCache[key]; ok {
		return mode
	}
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	restore := a.storeScanBeginSearch()
	mode := a.calleeStoreModeIntoSeen(decl, other, index, map[storeScanKey]bool{})
	restore()
	a.storeScanCycleHit = saved
	// The top-level result of one DFS is the complete least fixpoint for its own key: every
	// body on any cycle through it was scanned within this search.
	a.cacheStoreScan(key, mode)
	return mode
}

type storeScanKey struct {
	decl         *ast.FuncDecl
	other, index int
}

func (a *Analyzer) cacheStoreScan(key storeScanKey, mode storeMode) {
	if a.storeScanCache == nil {
		a.storeScanCache = map[storeScanKey]storeMode{}
	}
	a.storeScanCache[key] = mode
}

// calleeStoreModeIntoSeen memoizes an inner frame only when its search consulted no frame
// above it (region_store_scan_frames.go): a provisional `none` for an open cycle member is not
// final for the frames inside it.
func (a *Analyzer) calleeStoreModeIntoSeen(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	key := storeScanKey{decl, other, index}
	if mode, ok := a.storeScanCache[key]; ok {
		return mode
	}
	if seen[key] {
		// A recursive cycle adds no store the rest of the body does not already show.
		a.storeScanOpenCycle(key)
		return storeNone
	}
	if mode, ok := a.storeScanReuseProvisional(key); ok {
		return mode
	}
	if a.storeScanSummaryNeverReaches(decl, other, index) {
		// The (completed, least-fixpoint) store-flow summary proves no flow from other into
		// index at all -- the same proof the call-site gate accepts. Final.
		a.cacheStoreScan(key, storeNone)
		return storeNone
	}
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	mark := a.storeScanPushFrame(key)
	mode := a.calleeStoreModeIntoBody(decl, other, index, seen)
	final := a.storeScanPopFrame(key, mark, mode)
	if final {
		a.cacheStoreScan(key, mode)
	}
	a.storeScanCycleHit = saved || (a.storeScanCycleHit && !final)
	return mode
}

func (a *Analyzer) calleeStoreModeIntoBody(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	if decl == nil || other < 0 || index < 0 || other >= len(decl.Params) || index >= len(decl.Params) || len(decl.Body) == 0 {
		return storeHeader
	}
	key := storeScanKey{decl, other, index}
	// seen is the DFS STACK, not a visited set: a key finished on another path (left uncached
	// because its own subtree met a cycle) is no cycle of a later query and must be rescanned,
	// never read as "stores nothing".
	seen[key] = true
	defer delete(seen, key)
	if storeScanTrace {
		savedOther := storeScanTraceOther
		storeScanTraceOther = decl.Params[other].Name
		defer func() { storeScanTraceOther = savedOther }()
	}
	params, header, content, targets, views := a.storeScanTaint(decl, other, decl.Params[index].Name)
	scanIdx := a.storeScanIndex(decl)
	holders := a.storeScanLastHolders
	// targetsOrHolders: anything that mentions the target or a holder of its parts.
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
	filled := a.lazyFilledLocals(decl, other, content, views, seen)
	var valueMode func(value ast.Expr) storeMode
	valueMode = func(value ast.Expr) storeMode {
		inner := storeScanUnwrap(value)
		// A value whose (already analyzed) type cannot hold region storage carries none.
		if t, known := a.exprTypes[inner]; known && t != nil && !containsTypeParam(t) && a.storeScanHoldsNoRegionStorage(t) {
			return storeNone
		}
		// A struct literal stores each field value: judge them one by one.
		if lit, isLit := inner.(*ast.StructLitExpr); isLit && lit != nil {
			mode := storeNone
			for _, arg := range lit.Args {
				mode = maxStoreMode(mode, valueMode(arg))
			}
			for _, spread := range lit.Spreads {
				mode = maxStoreMode(mode, valueMode(spread))
			}
			if lit.CopyBase != nil {
				mode = maxStoreMode(mode, valueMode(lit.CopyBase))
			}
			return mode
		}
		// `t.f.push(v)` on a builtin darray yields the receiver with v appended: it stores what
		// the receiver and the arguments store (rule 177's per-argument view of the same call).
		if call, isCall := inner.(*ast.CallExpr); isCall && call != nil && len(returnBorrowCallArgs(call)) == len(call.Args) {
			if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && containerGrowthMethods[field.Field] && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				mode := valueMode(field.Object)
				for _, arg := range call.Args {
					argMode := valueMode(arg)
					if field.Field == "extend" {
						argMode = a.storeScanExtendArgMode(arg, argMode)
					}
					mode = maxStoreMode(mode, argMode)
				}
				return mode
			}
		}
		if !scanIdx.mentions(value, content) {
			return storeNone
		}
		// A nested call's result carries, from each argument, only what its callee may RETURN
		// from that parameter (the rule-160 judgement, applied inside a stored value).
		if call, isCall := inner.(*ast.CallExpr); isCall && call != nil {
			if callee, ok := a.resolveReadOnlyScanCallee(call); ok && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) && !a.declReturnsVoid(callee) {
				mode := storeNone
				for i, arg := range returnBorrowCallArgs(call) {
					argMode := valueMode(arg)
					if argMode == storeNone || !a.declParamCarriesStorage(callee, i) {
						continue
					}
					mode = maxStoreMode(mode, narrowStoreMode(argMode, a.calleeReturnModeOf(callee, i, seen)))
				}
				return mode
			}
		}
		// A view-only value derived from a byte-free source cannot point into the source's storage.
		if views != nil && (a.storeScanValueIsView(decl, inner, views) || storeScanRootedAtFilled(inner, filled)) {
			return storeViews
		}
		if storeScanMentionsHeader(value, header) {
			return storeHeader
		}
		return storeElements
	}
	mode := storeNone
	a.storeScanEachNode(decl, func(node any) {
		if mode == storeHeader {
			return
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			root := rootIdentExpr(n.Target)
			// Through a holder, a place two projections deep is inside a target part
			// (`holder[0][0] <- v`); `holder[0] <- v` writes the holder's own buffer.
			if root == nil || !(targets[root.Name] || (holders[root.Name] && storeScanPlaceDepth(n.Target) >= 2)) {
				return
			}
			// A plain rebind of a non-param local alias is not a store into the target.
			if id, isIdent := n.Target.(*ast.Ident); isIdent && !params[id.Name] && !n.WriteThrough {
				return
			}
			// `table <- f(table, scope)`: the stored value is the call's RESULT. The call's own
			// stores are scanned when the walk reaches it; its result carries a tainted argument
			// only when the callee's summary connects that argument to its return.
			if call, isCall := storeScanUnwrap(n.Value).(*ast.CallExpr); isCall && call != nil {
				if callee, ok := a.resolveReadOnlyScanCallee(call); ok && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) {
					if a.declReturnsVoid(callee) {
						// The lmut manifest `t <- f(.., t, ..)` of a void callee: the result IS
						// the threaded argument; the call's own stores are scanned at the call.
						return
					}
					// A non-void call result carries what the callee may RETURN from each tainted
					// argument (a directed scan of its return statements).
					for i, arg := range returnBorrowCallArgs(call) {
						argMode := valueMode(arg)
						if argMode == storeNone || !a.declParamCarriesStorage(callee, i) {
							continue
						}
						inner := narrowStoreMode(argMode, a.calleeReturnModeOf(callee, i, seen))
						mode = storeScanRaise(decl, 160, n, mode, inner)
					}
					return
				}
			}
			mode = storeScanRaise(decl, 167, n, mode, valueMode(n.Value))
		case *ast.CallExpr:
			if n == nil {
				return
			}
			args := returnBorrowCallArgs(n)
			field, isMethod := n.Func.(*ast.FieldExpr)
			// A builtin darray's `extend(xs)` copies xs's ELEMENTS into the receiver; it never
			// retains xs's own header.
			copiesElements := isMethod && field != nil && field.Field == "extend" && a.storeScanReceiverIsBuiltinDArray(decl, field.Object)
			if isMethod && field != nil {
				if root := rootIdentExpr(field.Object); root != nil && (targets[root.Name] || (holders[root.Name] && storeScanPlaceDepth(field.Object) >= 1)) {
					for _, arg := range n.Args {
						argMode := valueMode(arg)
						if copiesElements {
							argMode = a.storeScanExtendArgMode(arg, argMode)
						}
						mode = storeScanRaise(decl, 177, n, mode, argMode)
					}
				}
			}
			type side struct {
				index int
				mode  storeMode
			}
			targetArgs, taintedArgs := []int{}, []side{}
			for i, arg := range args {
				if scanIdx.mentions(arg, targetsOrHolders) {
					targetArgs = append(targetArgs, i)
				}
				if m := valueMode(arg); m != storeNone {
					taintedArgs = append(taintedArgs, side{i, m})
				}
			}
			if isMethod && field != nil && len(args) == len(n.Args) {
				// An unrewritten method call: the receiver is an operand too, and the callee is
				// unresolved. A builtin growth method moves its arguments INTO the receiver only.
				recvTarget := scanIdx.mentions(field.Object, targets)
				if !recvTarget && len(holders) > 0 && storeScanPlaceDepth(field.Object) >= 1 {
					recvTarget = scanIdx.mentions(field.Object, holders)
				}
				recvMode := valueMode(field.Object)
				if containerGrowthMethods[field.Field] && !recvTarget {
					return
				}
				if recvTarget {
					for _, s := range taintedArgs {
						argMode := s.mode
						if copiesElements {
							argMode = a.storeScanExtendArgMode(n.Args[s.index], argMode)
						}
						mode = storeScanRaise(decl, 204, n, mode, argMode)
					}
				}
				if recvMode != storeNone && len(targetArgs) > 0 {
					mode = storeScanRaise(decl, 208, n, mode, recvMode)
				}
				return
			}
			if len(targetArgs) == 0 || len(taintedArgs) == 0 {
				return
			}
			callee, ok := a.resolveReadOnlyScanCallee(n)
			if !ok || callee == nil || !storeFlowCallFitsParams(callee, n) || funcHasArenaParam(callee) {
				for _, s := range taintedArgs {
					mode = storeScanRaise(decl, 218, n, mode, s.mode)
				}
				return
			}
			for _, ti := range targetArgs {
				if !a.declParamCarriesStorage(callee, ti) || !a.declParamIsWritableRef(callee, ti) {
					// The target-side parameter cannot hold region storage, or is passed by
					// value: the callee's writes land in its own copy, never the caller's place.
					continue
				}
				for _, s := range taintedArgs {
					if s.index != ti && !a.declParamCarriesStorage(callee, s.index) {
						continue // a scalar parameter (`first: uintptr`, a size) transmits none
					}
					if ti == s.index {
						// One argument derived from both sides (`f(table.types[id])`) is not a
						// store between them, unless it BUNDLES both into a fresh aggregate the
						// callee could then split and store.
						if scanIdx.bundles(args[ti]) && a.storeScanBundleHasWritableTarget(args[ti], targetsOrHolders) && !a.storeScanCallResultDropsTarget(args[ti], targetsOrHolders, seen) {
							mode = storeScanRaise(decl, 225, n, mode, s.mode)
						}
						continue
					}
					// The callee stores at most what this argument carried (an element value of an
					// element-mode argument is never this source's header).
					inner := narrowStoreMode(s.mode, a.calleeStoreModeIntoSeen(callee, s.index, ti, seen))
					mode = storeScanRaise(decl, 233, n, mode, inner)
				}
			}
		}
	})
	return mode
}

// storeScanMentionsHeader reports whether an expression may evaluate to (or view) a HEADER name
// as a whole. Element reads `xs[i]` and scalar size queries (`xs.count`) do not.
func storeScanMentionsHeader(node any, names map[string]bool) bool {
	if node == nil || len(names) == 0 {
		return false
	}
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch e := v.Interface().(type) {
			case *ast.Ident:
				if e != nil && (names[e.Name] || names[rootNameOfDotted(e.Name)]) {
					found = true
				}
				return
			case *ast.IndexExpr:
				if e != nil && e.Index2 == nil {
					if root := rootIdentExpr(e.Object); root != nil && names[root.Name] {
						rec(reflect.ValueOf(e.Index))
						return
					}
				}
			case *ast.FieldExpr:
				if e != nil {
					switch e.Field {
					case "count", "len", "capacity", "is_empty":
						return
					}
					rec(reflect.ValueOf(e.Object))
					return
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		case reflect.String:
			if names[v.String()] {
				found = true
			}
		}
	}
	rec(reflect.ValueOf(node))
	return found
}

// walkStoreScanNodes visits every AST node pointer reachable from stmts.
func walkStoreScanNodes(stmts []ast.Stmt, visit func(any)) {
	// Desugaring shares subtrees (a nested guard chain reuses its operands), so an unmemoized
	// reflective walk is exponential in nesting depth. Each node is visited once.
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			visit(v.Interface())
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
}

// exprMentionsAny reports whether any identifier in the node (or a string-valued binder field
// of it) names a member of names.
func storeScanMentions(node any, names map[string]bool) bool {
	if node == nil || len(names) == 0 {
		return false
	}
	found := false
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				if names[id.Name] || names[rootNameOfDotted(id.Name)] {
					found = true
				}
				return
			}
			if fe, ok := v.Interface().(*ast.FieldExpr); ok && fe != nil {
				// `.field` selectors are not names; only the object can mention one.
				rec(reflect.ValueOf(fe.Object))
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		case reflect.String:
			// Capture lists and binder spellings are plain strings.
			if names[v.String()] {
				found = true
			}
		}
	}
	rec(reflect.ValueOf(node))
	return found
}

func rootNameOfDotted(name string) string {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return name[:i]
		}
	}
	return name
}

// stmtBoundNames over-approximates the names a statement may bind: every string field in its
// non-body parts (patterns, binder names, rebind targets).
func stmtBoundNames(stmt ast.Stmt) []string {
	var out []string
	v := reflect.ValueOf(stmt)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	var rec func(v reflect.Value, depth int, field string)
	rec = func(v reflect.Value, depth int, field string) {
		if !v.IsValid() || depth > 64 {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if _, isStmt := v.Interface().(ast.Stmt); isStmt && depth > 0 {
				return // nested statements bind in their own visit
			}
			if _, isExpr := v.Interface().(ast.Expr); isExpr {
				return // expressions use names; binders live in patterns and binder fields
			}
			rec(v.Elem(), depth+1, field)
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			t := v.Type()
			for i := 0; i < v.NumField(); i++ {
				name := t.Field(i).Name
				if t == storeScanPatternArgType && name == "Name" {
					continue // `Key(key: k)`: the field label; the binder is the nested pattern
				}
				rec(v.Field(i), depth+1, name)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), depth+1, field)
			}
		case reflect.String:
			// A pattern's qualifying type / variant (`Expr.Index(a, b)` records "Expr" and
			// "Index") and a destructured field's name are not binders.
			if storeScanQualifierFields[field] {
				return
			}
			if s := v.String(); s != "" {
				out = append(out, s)
			}
		}
	}
	top := v.Type()
	for i := 0; i < v.NumField(); i++ {
		if top.Field(i).Name == "Captures" {
			continue // `while c |xs, t|:` names existing bindings (a mutation manifest); binds none
		}
		rec(v.Field(i), 1, top.Field(i).Name)
	}
	return out
}

// stmtHeaderMentionsAny reports whether the statement's non-nested-statement parts mention a
// name in names.
func stmtHeaderMentionsAny(stmt ast.Stmt, names map[string]bool) bool {
	v := reflect.ValueOf(stmt)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	// Nested statements (a match arm's body, an if's branches) bind and use names in their own
	// visit; only this statement's own expressions and patterns are its header. Arms are not a
	// []ast.Stmt, so the skip has to happen at every depth.
	found := false
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if _, isStmt := v.Interface().(ast.Stmt); isStmt {
				return
			}
			if expr, isExpr := v.Interface().(ast.Expr); isExpr {
				if storeScanMentions(expr, names) {
					found = true
				}
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		case reflect.String:
			if names[v.String()] {
				found = true
			}
		}
	}
	for i := 0; i < v.NumField() && !found; i++ {
		rec(v.Field(i))
	}
	return found
}

var storeScanTrace = os.Getenv("ELISA_STORESCAN_TRACE") != ""

// storeScanTraceOther names the source parameter of the body scan in progress (trace only).
var storeScanTraceOther string

func storeScanRaise(decl *ast.FuncDecl, rule int, node ast.Node, mode, next storeMode) storeMode {
	if next > mode && storeScanTrace && node != nil {
		fmt.Fprintf(os.Stderr, "STORESCAN %s rule=%d mode=%d src=%s at %s\n", decl.Name, rule, next, storeScanTraceOther, node.Pos())
	}
	return maxStoreMode(mode, next)
}

// storeScanBundles reports whether an argument builds a fresh aggregate (struct/tuple/list
// literal, lambda capture, or a call that may build one) that could carry both a target-side and
// a source-side component into the callee. Plain place reads (`table.types[id]`) do not.
func storeScanBundles(arg ast.Expr) bool {
	found := false
	walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: arg}}, func(node any) {
		switch node.(type) {
		case *ast.StructLitExpr, *ast.TupleExpr, *ast.ListLitExpr, *ast.LambdaExpr, *ast.CallExpr:
			found = true
		}
	})
	return found
}

// storeScanCallResultDropsTarget: the argument is a resolvable call whose result can carry, from
// every argument mentioning a target, at most read-only views (the callee's directed return scan
// for that parameter is none or views). Such a result bundles no writable target component, so
// a callee receiving it cannot write source storage into the target through it.
func (a *Analyzer) storeScanCallResultDropsTarget(arg ast.Expr, targets map[string]bool, seen map[storeScanKey]bool) bool {
	call, ok := storeScanUnwrap(arg).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	callee, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || callee == nil || !storeFlowCallFitsParams(callee, call) || funcHasArenaParam(callee) || a.declReturnsVoid(callee) {
		return false
	}
	for i, inner := range returnBorrowCallArgs(call) {
		if !storeScanMentions(inner, targets) || !a.declParamCarriesStorage(callee, i) {
			continue
		}
		// A provisional (open-cycle) answer proves nothing dropped: settled counts it as header.
		if a.storeScanSettled(func() storeMode { return a.calleeReturnModeOf(callee, i, seen) }) > storeViews && !a.calleeReturnsNothingWritableFrom(callee, i) {
			return false
		}
	}
	return true
}

// calleeReturnsNothingWritableFrom: every value decl returns is explicit (a `return`, no lmut
// thread slots) and carries from parameter i only read-only parts -- views and scalars, judged by
// storeScanBundleHasWritableTargetRO over the names derived from that parameter.
func (a *Analyzer) calleeReturnsNothingWritableFrom(decl *ast.FuncDecl, i int) bool {
	if decl == nil || i < 0 || i >= len(decl.Params) || len(decl.Body) == 0 || len(decl.LmutThreadSlots) > 0 {
		return false
	}
	key := storeScanKey{decl, i, -2}
	if cached, ok := a.storeScanWritableCache[key]; ok {
		return cached
	}
	_, _, content, _, _ := a.storeScanTaint(decl, i, "")
	readOnly := a.storeScanReadOnlyLocals(decl)
	ok, sawReturn := true, false
	a.storeScanEachNode(decl, func(node any) {
		ret, isRet := node.(*ast.ReturnStmt)
		if !isRet || ret == nil || !ok {
			return
		}
		sawReturn = true
		if ret.Value != nil && a.storeScanBundleHasWritableTargetRO(ret.Value, content, readOnly) {
			ok = false
		}
	})
	result := ok && (sawReturn || a.declReturnsVoid(decl))
	if a.storeScanWritableCache == nil {
		a.storeScanWritableCache = map[storeScanKey]bool{}
	}
	a.storeScanWritableCache[key] = result
	return result
}

// storeScanReadOnlyLocals: body locals every declaration of which is annotated as a view
// (`sview`/`cstr`) or a plain scalar, bound by no other construct. Memoized per function.
func (a *Analyzer) storeScanReadOnlyLocals(decl *ast.FuncDecl) map[string]bool {
	if cached, ok := a.storeScanReadOnlyCache[decl]; ok {
		return cached
	}
	readOnly, other := map[string]bool{}, map[string]bool{}
	a.storeScanEachNode(decl, func(node any) {
		if v, isVar := node.(*ast.VarDeclStmt); isVar {
			if v == nil {
				return
			}
			if !other[v.Name] && (storeScanTypeExprIsView(v.Type) || storeScanTypeExprIsPlainScalar(v.Type)) {
				readOnly[v.Name] = true
				return
			}
			other[v.Name] = true
			delete(readOnly, v.Name)
			return
		}
		if st, isStmt := node.(ast.Stmt); isStmt && st != nil {
			for _, name := range stmtBoundNames(st) {
				other[name] = true
				delete(readOnly, name)
			}
		}
	})
	for _, p := range decl.Params {
		delete(readOnly, p.Name)
	}
	if a.storeScanReadOnlyCache == nil {
		a.storeScanReadOnlyCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.storeScanReadOnlyCache[decl] = readOnly
	return readOnly
}

// storeScanBundleHasWritableTarget reports whether a bundled argument carries a target-side
// component the callee could WRITE source storage into: a target-aliasing name whose type shares
// indirect storage (a container, reference or aggregate holding one). A target-derived string or
// view (`sview`, `cstr`) is read-only, so bundling it next to a source component moves nothing
// into the target. An untyped mention is assumed writable.
func (a *Analyzer) storeScanBundleHasWritableTarget(arg ast.Expr, targets map[string]bool) bool {
	return a.storeScanBundleHasWritableTargetRO(arg, targets, nil)
}

// storeScanBundleHasWritableTargetRO: storeScanBundleHasWritableTarget, where a name in readOnly
// (a local only ever declared as a view or plain scalar) is a read-only mention.
func (a *Analyzer) storeScanBundleHasWritableTargetRO(arg ast.Expr, targets, readOnly map[string]bool) bool {
	// A typed sub-expression that cannot share storage (`h.name: sview`, `h.count`) is read-only
	// whatever it mentions, so the walk does not descend into it.
	found := false
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if expr, isExpr := v.Interface().(ast.Expr); isExpr && expr != nil {
				if t, typed := a.exprTypes[expr]; typed && t != nil && !a.storeScanSharesCached(t) {
					return
				}
				if id, isIdent := expr.(*ast.Ident); isIdent {
					if readOnly[id.Name] {
						return
					}
					if targets[id.Name] || targets[rootNameOfDotted(id.Name)] {
						found = true
					}
					return
				}
				if fe, isField := expr.(*ast.FieldExpr); isField {
					rec(reflect.ValueOf(fe.Object))
					return
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(arg))
	return found
}

// declParamCarriesStorage reports whether a declared function's parameter type can carry region
// storage (a container, view, reference or aggregate holding one). Unknown means it can.
func (a *Analyzer) declParamCarriesStorage(decl *ast.FuncDecl, index int) bool {
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return true
	}
	param := fnType.Params[index]
	if containsTypeParam(param) {
		return true
	}
	return !a.storeScanHoldsNoRegionStorage(param)
}

// storeScanTaint computes, to a fixpoint over the body, which names may hold the WHOLE source
// parameter (header) or something read out of it (content), and which names may alias the
// target place (targetName; "" for none). Locals become tainted by binding (decl/assign of the
// local itself) and by receiving a tainted store (`local.f <- v`, `local.xs.push(v)`).
func (a *Analyzer) storeScanTaint(decl *ast.FuncDecl, other int, targetName string) (params, header, content, targets, views map[string]bool) {
	// Memoized per (decl, source, target): every summary query of the same pair recomputed the
	// fixpoint. Results are read-only to callers; types learned later only make taint smaller,
	// so a result computed earlier is conservative (as the scalar / view caches already are).
	key := storeScanTaintKey{decl, other, targetName}
	if r, ok := a.storeScanTaintCache[key]; ok {
		a.storeScanLastHolders = r.holders
		return r.params, r.header, r.content, r.targets, r.views
	}
	defer func() {
		if a.storeScanTaintCache == nil {
			a.storeScanTaintCache = map[storeScanTaintKey]storeScanTaintResult{}
		}
		a.storeScanTaintCache[key] = storeScanTaintResult{params, header, content, targets, views, a.storeScanLastHolders}
	}()
	params = map[string]bool{}
	for _, p := range decl.Params {
		params[p.Name] = true
	}
	header = map[string]bool{decl.Params[other].Name: true}
	content = map[string]bool{decl.Params[other].Name: true}
	targets = map[string]bool{}
	if targetName != "" {
		targets[targetName] = true
	}
	scalars := a.storeScanScalarLocals(decl)
	add := func(set map[string]bool, name string) bool {
		if name == "" || params[name] || set[name] || scalars[name] {
			return false
		}
		set[name] = true
		return true
	}
	views = a.storeScanViewLocalsCached(decl, other)
	idx := a.storeScanIndex(decl)
	taint := func(name string, value ast.Expr) bool {
		changed := false
		if views[name] && idx.mentions(value, content) {
			// A view-typed local derived from a non-byte darray source holds (part of) an element
			// value, never the source's header (storeScanViewLocals).
			changed = add(content, name) || changed
		} else if storeScanMentionsHeader(value, header) {
			changed = add(header, name) || changed
			changed = add(content, name) || changed
		} else if idx.mentions(value, content) {
			changed = add(content, name) || changed
		}
		return changed
	}
	// holders: locals that CONTAIN parts of the target (`holder.push(t.lists)`): their own buffer
	// is fresh, but a part read out of them (`holder[0]`) is target storage.
	holders := map[string]bool{}
	bind := func(name string, value ast.Expr) bool {
		changed := taint(name, value)
		if storeScanMayAliasPlace(idx, value, targets) {
			changed = add(targets, name) || changed
		} else if storeScanMayAliasPlace(idx, value, holders) {
			if storeScanPlaceDepth(value) >= 1 {
				changed = add(targets, name) || changed // `x = holder[0]`: a target part
			} else {
				changed = add(holders, name) || changed // `x = holder`: another holder
			}
		}
		return changed
	}
	locals := idx.locals
	for changed := true; changed; {
		changed = false
		for _, node := range idx.flat {
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil && bind(n.Name, n.Value) {
					changed = true
				}
			case *ast.AssignStmt:
				if n == nil {
					continue
				}
				if id, ok := n.Target.(*ast.Ident); ok && id != nil {
					if bind(id.Name, n.Value) {
						changed = true
					}
					continue
				}
				// A store into part of a local taints the local.
				if root := rootIdentExpr(n.Target); root != nil && !params[root.Name] && taint(root.Name, n.Value) {
					changed = true
				}
			case *ast.CallExpr:
				// A method call on a local receiver may store its arguments into it. A receiver
				// that names no value (an enum constructor `Expr.Field(..)`) is not a receiver.
				if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
					if root := rootIdentExpr(field.Object); root != nil && !params[root.Name] && a.storeScanReceiverIsValue(root.Name, locals) {
						// A builtin darray `extend(xs)` copies xs's elements: the receiver then
						// holds values read out of xs (content), never xs's header.
						copiesElements := field.Field == "extend" && a.storeScanReceiverIsBuiltinDArray(decl, field.Object)
						for _, arg := range n.Args {
							// A receiver given (part of) the target place holds the target's storage
							// (`holder.push(table.deep.lists)`; an `extend` copies element headers that
							// still share it): stores through the receiver then reach the target.
							if !storeScanReadOnlyMethods[field.Field] && (storeScanMayAliasPlace(idx, arg, targets) || storeScanMayAliasPlace(idx, arg, holders)) && a.storeScanContainerElemsMayShare(decl, field.Object) && add(holders, root.Name) {
								changed = true
								if storeScanTrace {
									fmt.Fprintf(os.Stderr, "TGTRECV %s src=%s local=%s at %s\n", decl.Name, storeScanTraceOther, root.Name, n.Pos())
								}
							}
							if copiesElements {
								// Scalar elements copied out of xs carry nothing of it.
								if a.storeScanExtendArgMode(arg, storeElements) == storeNone {
									continue
								}
								if idx.mentions(arg, content) && add(content, root.Name) {
									changed = true
								}
								continue
							}
							if taint(root.Name, arg) {
								changed = true
							}
						}
					}
				}
				// A body local passed to a call may be WRITTEN by the callee with any other
				// argument (`push_list(merged, right.lists[row])`): taint it with each of them,
				// unless the callee is resolved and the local's parameter is read-only. A builtin
				// container method writes only its receiver (handled above).
				if a.storeScanTaintCallWrites(decl, n, params, locals, taint, idx, targets, holders, func(name string) bool { return add(holders, name) }, content, func(name string) bool { return add(content, name) }) {
					changed = true
				}
			case *ast.IterForStmt:
				// Loop binders over (a holder of) the target are target parts.
				if n != nil && (storeScanMayAliasPlace(idx, n.Source, targets) || storeScanMayAliasPlace(idx, n.Source, holders)) {
					for _, name := range idx.boundNames(n) {
						if add(targets, name) {
							changed = true
						}
					}
				}
				// Loop binders over a tainted source are element values.
				if n != nil && idx.mentions(n.Source, content) {
					for _, name := range idx.boundNames(n) {
						if add(content, name) {
							changed = true
						}
					}
				}
			case *ast.ForStmt:
				// A counted range loop binds an integer (analyzeForStmt rejects non-integral
				// bounds), which carries nothing, however its header mentions the source
				// (`for i in 0..<src.count |src|`).
			case ast.Stmt:
				// Any other binding construct: its binders may hold the whole source.
				if !idx.headerMentions(n, content) {
					continue
				}
				a.storeScanTaintBinders(idx, n, header, content, add, &changed)
			}
		}
	}
	a.storeScanLastHolders = holders
	return params, header, content, targets, views
}

// storeScanScalarLocals: body locals that only ever hold a plain numeric or bool value -- every
// declaration of the name is initialized with such a value (analyzed type) and no other construct
// binds it. Such a local can never carry a reference into a source, whatever it is computed from
// (`index: int = bound_last_index(bound, name)`). uintptr is excluded: it may encode an address.
func (a *Analyzer) storeScanScalarLocals(decl *ast.FuncDecl) map[string]bool {
	if cached, ok := a.storeScanScalarCache[decl]; ok {
		return cached
	}
	scalars, other := map[string]bool{}, map[string]bool{}
	defer func() {
		if a.storeScanScalarCache == nil {
			a.storeScanScalarCache = map[*ast.FuncDecl]map[string]bool{}
		}
		a.storeScanScalarCache[decl] = scalars
	}()
	a.storeScanEachNode(decl, func(node any) {
		if v, isVar := node.(*ast.VarDeclStmt); isVar {
			if v == nil {
				return
			}
			if storeScanTypeExprIsPlainScalar(v.Type) || a.storeScanTypeExprIsOpaque(v.Type) || (v.Type == nil && v.Value != nil && storeScanPlainScalarType(a.exprTypes[v.Value])) {
				if !other[v.Name] {
					scalars[v.Name] = true
				}
				return
			}
			other[v.Name] = true
			delete(scalars, v.Name)
			return
		}
		if st, isStmt := node.(ast.Stmt); isStmt && st != nil {
			for _, name := range stmtBoundNames(st) {
				other[name] = true
				delete(scalars, name)
			}
		}
	})
	return scalars
}

// storeScanTypeExprIsOpaque: a declared foreign handle (`extern LLVMValueRef`) or a named type
// that can hold no region reference, through
// `mutable`, never through a reference or optional. Its value is produced by foreign code and
// can hold no Elisa region reference (storeFlowOpaqueExternCall's rule).
func (a *Analyzer) storeScanTypeExprIsOpaque(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.NamedType:
			if tt == nil || tt.Region != "" {
				return false
			}
			resolved := a.storeFlowNamedType(tt.Name)
			if _, opaque := resolved.(*OpaqueType); opaque {
				return true
			}
			// A named value type that can hold no region reference (`ValueType`, a struct of
			// scalars) carries nothing either.
			return resolved != nil && !containsTypeParam(resolved) && a.storeScanHoldsNoRegionStorage(resolved)
		}
		return false
	}
}

// storeScanTypeExprIsPlainScalar: a declared `int`/`u32`/`bool`/... annotation (through `mutable`,
// never through a reference). Builtin scalar names are reserved, so the spelling is the type.
func storeScanTypeExprIsPlainScalar(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.BuiltinTypeExpr:
			return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && storeScanPlainScalarNames[tt.Name]
		case *ast.NamedType:
			return tt != nil && tt.Region == "" && storeScanPlainScalarNames[tt.Name]
		}
		return false
	}
}

var storeScanPlainScalarNames = map[string]bool{
	"bool": true, "char": true, "int": true, "i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true, "f32": true, "f64": true,
}

func storeScanPlainScalarType(t Type) bool {
	if t == nil || containsTypeParam(t) {
		return false
	}
	if b, ok := t.(*BuiltinType); ok && b != nil {
		switch b.Name {
		case "bool", "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "f32", "f64":
			return true
		}
		return false
	}
	_, _, ok := BitIntInfo(t)
	return ok
}

// storeScanTaintCallWrites taints each body local passed to call with the call's other
// arguments when the callee may write that local's parameter.
func (a *Analyzer) storeScanTaintCallWrites(decl *ast.FuncDecl, call *ast.CallExpr, params, locals map[string]bool, taint func(string, ast.Expr) bool, idx *storeScanDeclIndex, targets, holders map[string]bool, addHolder func(string) bool, content map[string]bool, addContent func(string) bool) bool {
	changed := false
	for _, w := range a.storeScanCallWritePlan(decl, call, params, locals) {
		// A holder: the callee may store (part of) the target place it is given into the local
		// (`stash(holder, table.deep.lists)`), whose stores then reach the target.
		if !targets[w.local] && !holders[w.local] && (storeScanMayAliasPlace(idx, w.value, targets) || storeScanMayAliasPlace(idx, w.value, holders)) && a.storeScanHolderMayAlias(decl, w) &&
			(w.callee == nil || a.storeScanParamReaches(storeScanShareKey(w.callee, w.into, w.from))) && addHolder(w.local) {
			changed = true
			if storeScanTrace {
				fmt.Fprintf(os.Stderr, "TGTHOLD %s src=%s local=%s at %s writer=%s\n", decl.Name, storeScanTraceOther, w.local, call.Pos(), storeScanParamWriterOf[storeScanShareKey(w.callee, w.into, w.from)])
			}
		}
		if w.callee != nil && !a.storeScanParamMayBeWritten(w.callee, w.into) {
			continue // the resolved callee never writes through that parameter
		}
		if w.callee != nil && !a.storeScanParamReaches(storeScanShareKey(w.callee, w.into, w.from)) {
			// The callee never stores a value sharing parameter from's storage into into's: the
			// local receives at most what the callee reads out of the source (content), never
			// its header. (Share facts are per (into, from) pair.)
			if idx.mentions(w.value, content) && addContent(w.local) {
				changed = true
			}
			continue
		}
		if taint(w.local, w.value) {
			changed = true
			if storeScanTrace {
				fmt.Fprintf(os.Stderr, "TAINTW %s src=%s local=%s at %s\n", decl.Name, storeScanTraceOther, w.local, call.Pos())
			}
		}
	}
	return changed
}

// storeScanCallWrite: the callee of a call may write value into the body local.
type storeScanCallWrite struct {
	local string
	value ast.Expr
	// arg is the argument place the callee writes through (`local`, `local.items`).
	arg ast.Expr
	// callee (nil when unresolved) writes its parameter `from` (value) into `into` (local).
	callee     *ast.FuncDecl
	from, into int
}

// storeScanCallWritePlan: the (local, value) writes a call may perform. It depends only on the
// declaration and the call (params/locals are the declaration's), so it is computed once.
func (a *Analyzer) storeScanCallWritePlan(decl *ast.FuncDecl, call *ast.CallExpr, params, locals map[string]bool) []storeScanCallWrite {
	if plan, ok := a.storeScanCallWriteCache[call]; ok {
		return plan
	}
	var plan []storeScanCallWrite
	defer func() {
		if a.storeScanCallWriteCache == nil {
			a.storeScanCallWriteCache = map[*ast.CallExpr][]storeScanCallWrite{}
		}
		a.storeScanCallWriteCache[call] = plan
	}()
	args := returnBorrowCallArgs(call)
	if len(args) < 2 {
		return plan
	}
	if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(call.Args) {
		if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) || storeScanReadOnlyMethods[field.Field] {
			return plan
		}
	}
	callee, resolved := a.resolveReadOnlyScanCallee(call)
	resolved = resolved && callee != nil && storeFlowCallFitsParams(callee, call)
	for i, arg := range args {
		root := rootIdentExpr(stripAddrAndParens(arg))
		if root == nil || params[root.Name] || !locals[root.Name] {
			continue
		}
		if resolved && (!a.declParamIsWritableRef(callee, i) || a.declParamIsViewOrScalar(callee, i)) {
			continue // the callee cannot write through this parameter
		}
		for j, other := range args {
			if j == i || (resolved && !a.declParamCarriesStorage(callee, j)) {
				continue
			}
			w := storeScanCallWrite{local: root.Name, value: other, arg: stripAddrAndParens(arg)}
			if resolved {
				w.callee, w.from, w.into = callee, j, i
			}
			plan = append(plan, w)
		}
	}
	return plan
}

// declParamIsViewOrScalar: decl's parameter index has a view-only or plain scalar type, which
// no callee can store into.
func (a *Analyzer) declParamIsViewOrScalar(decl *ast.FuncDecl, index int) bool {
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return false
	}
	t := fnType.Params[index]
	if t == nil || containsTypeParam(t) {
		return false
	}
	if _, isRef := t.(*RefType); isRef {
		return false
	}
	if _, isOpaque := t.(*OpaqueType); isOpaque {
		// A by-value foreign handle (`extern LLVMValueRef`) is a copy: the callee cannot change
		// what the caller's local holds, and what it writes behind the handle is foreign memory.
		return true
	}
	return a.storeScanViewOnlyCached(t) || storeScanPlainScalarType(t)
}

// storeScanTaintBinders: a binding construct whose header mentions a tainted name taints its
// binders. Pattern binders of a match / `is` test project fields or payloads (content); only
// other binders (a rebind of a call result, a destructure) may be the whole.
func (a *Analyzer) storeScanTaintBinders(idx *storeScanDeclIndex, n ast.Stmt, header, content map[string]bool, add func(map[string]bool, string) bool, changed *bool) {
	if n == nil {
		return
	}
	bound := idx.boundNames(n)
	readsContent := idx.headerMentions(n, content)
	readsHeader := idx.headerMentions(n, header)
	if m, isMatch := n.(*ast.MatchStmt); isMatch && m != nil {
		// A match's binders receive parts of its scrutinee only; its patterns NAME the binders
		// (a tainted `then_body` in `St.If(cond, then_body, ..)` is being bound, not read) and
		// its guards read without flowing into them.
		readsContent = idx.mentions(m.Value, content) || idx.mentions(m.Store, content)
		readsHeader = idx.mentions(m.Value, header) || idx.mentions(m.Store, header)
	}
	if !readsContent {
		return
	}
	whole := readsHeader && !storeScanProjectingStmt(n)
	for _, name := range bound {
		if add(content, name) {
			*changed = true
		}
		if whole && add(header, name) {
			*changed = true
		}
	}
}

// calleeReturnModeOf: what may the callee RETURN from parameter `other` -- nothing, values read
// out of it, or the value itself? Judged on its return statements (and an expression-bodied
// tail), with calls in a returned expression recursed into. Unknown means the value itself.
func (a *Analyzer) calleeReturnModeOf(decl *ast.FuncDecl, other int, seen map[storeScanKey]bool) storeMode {
	if decl == nil || other < 0 || other >= len(decl.Params) || len(decl.Body) == 0 {
		return storeHeader
	}
	key := storeScanKey{decl, other, -1}
	if mode, ok := a.storeScanCache[key]; ok {
		return mode
	}
	if seen[key] {
		a.storeScanOpenCycle(key)
		return storeNone
	}
	if mode, ok := a.storeScanReuseProvisional(key); ok {
		return mode
	}
	seen[key] = true
	defer delete(seen, key) // the DFS stack (see calleeStoreModeIntoBody)
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	mark := a.storeScanPushFrame(key)
	_, header, content, _, views := a.storeScanTaint(decl, other, "")
	filled := a.lazyFilledLocals(decl, other, content, views, seen)
	mode := storeNone
	sawReturn := false
	a.storeScanEachNode(decl, func(node any) {
		if _, isRet := node.(*ast.ReturnStmt); isRet {
			sawReturn = true
		}
	})
	if !sawReturn && !a.declReturnsVoid(decl) {
		// A value produced without a `return` (an expression tail) is not modelled.
		mode = storeHeader
	}
	a.storeScanEachNode(decl, func(node any) {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok || ret == nil || ret.Value == nil || mode == storeHeader {
			return
		}
		value := storeScanUnwrap(ret.Value)
		if call, isCall := value.(*ast.CallExpr); isCall && call != nil {
			if callee, ok := a.resolveReadOnlyScanCallee(call); ok && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) {
				for i, arg := range returnBorrowCallArgs(call) {
					argMode := storeNone
					if storeScanMentionsHeader(arg, header) {
						argMode = storeHeader
					} else if storeScanMentions(arg, content) {
						argMode = storeElements
					}
					if argMode == storeNone {
						continue
					}
					mode = maxStoreMode(mode, narrowStoreMode(argMode, a.calleeReturnModeOf(callee, i, seen)))
				}
				return
			}
		}
		if !storeScanMentions(value, content) {
			return
		}
		if views != nil && (a.storeScanValueIsView(decl, value, views) || a.storeScanDeclReturnsViewOnly(decl) || storeScanRootedAtFilled(value, filled)) {
			mode = maxStoreMode(mode, storeViews)
		} else if storeScanMentionsHeader(value, header) {
			mode = storeHeader
		} else {
			mode = maxStoreMode(mode, storeElements)
		}
	})
	final := a.storeScanPopFrame(key, mark, mode)
	if final {
		a.cacheStoreScan(key, mode)
	}
	a.storeScanCycleHit = saved || (a.storeScanCycleHit && !final)
	return mode
}

// storeScanUnwrap strips value-preserving wrappers (parens, an inline `can ...` grant, `move`)
// so a wrapped call is still judged as a call.
func storeScanUnwrap(expr ast.Expr) ast.Expr {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.Inner
		case *ast.CanExpr:
			if e == nil {
				return expr
			}
			expr = e.Expr
		case *ast.MoveExpr:
			if e == nil {
				return expr
			}
			expr = e.Operand
		default:
			return expr
		}
	}
}

// declReturnsVoid reports whether a declared function's resolved return type is void.
func (a *Analyzer) declReturnsVoid(decl *ast.FuncDecl) bool {
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	builtin, ok := fnType.Return.(*BuiltinType)
	return fnType.Return == nil || ok && builtin != nil && builtin.Name == "void"
}

// declParamIsWritableRef reports whether a declared parameter is a mutable reference (including
// `lmut`), the only way a callee can write into caller-visible storage. Unknown means it is.
func (a *Analyzer) declParamIsWritableRef(decl *ast.FuncDecl, index int) bool {
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return true
	}
	switch p := fnType.Params[index].(type) {
	case *RefType:
		return p == nil || p.Mutable
	case *ViewType:
		return p == nil || p.Mutable
	case *TypeParamType:
		return true
	}
	// A by-value parameter is a copy, but a copied darray/dict/set header (or a struct, tuple,
	// enum payload holding one, or a reference) SHARES its indirect storage with the caller:
	// `p[i] <- x` / `p.items[0] <- x` writes caller-visible memory. Only a by-value type that
	// reaches no shared indirect storage is exempt.
	return containsTypeParam(fnType.Params[index]) || a.storeScanSharesCached(fnType.Params[index])
}

// typeSharesIndirectStorage reports whether a by-value copy of t can still reach storage it
// shares with the original (container buffers, references, mutable views). Read-only views
// (sview, cstr) cannot be written through. Unknown types are assumed to share.
func typeSharesIndirectStorage(t Type, seen map[Type]bool) bool {
	if t == nil {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch tt := StripAggregateStateType(t).(type) {
	case *BuiltinType, *BitIntType, *SViewType, *CStrType, *ConstEnumType:
		return false
	case *DArrayType, *DictType, *SetType, *RefType, *ViewType:
		return true
	case *StructType:
		if tt == nil {
			return true
		}
		for _, field := range tt.Fields {
			if typeSharesIndirectStorage(field.Type, seen) {
				return true
			}
		}
		return false
	case *TupleType:
		for _, field := range tt.Fields {
			if typeSharesIndirectStorage(field.Type, seen) {
				return true
			}
		}
		return false
	case *ArrayType:
		return typeSharesIndirectStorage(tt.Elem, seen)
	case *OptionalType:
		return typeSharesIndirectStorage(tt.Value, seen)
	case *EnumType:
		if tt == nil {
			return true
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				continue
			}
			for _, payload := range variant.Payload {
				if typeSharesIndirectStorage(payload, seen) {
					return true
				}
			}
		}
		for _, field := range tt.Common {
			if typeSharesIndirectStorage(field.Type, seen) {
				return true
			}
		}
		return false
	}
	return true
}

// storeScanMayAliasPlace reports whether binding a local to value may make it a REFERENCE into
// target storage: a place rooted at a target (`t`, `t.f`, `t.xs[i]`, `&t.f`), a conditional
// choosing one, or a call that may return one. A fresh literal or comprehension that merely
// reads the target (`Scope{names: [n for n in t.names]}`) owns new storage and is no alias.
func storeScanMayAliasPlace(idx *storeScanDeclIndex, value ast.Expr, targets map[string]bool) bool {
	if value == nil || !idx.mentions(value, targets) {
		return false
	}
	switch stripParenExpr(value).(type) {
	case *ast.StructLitExpr, *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.TupleExpr, *ast.StringLit, *ast.BinaryExpr:
		return false
	}
	return true
}

// storeScanProjectingStmt reports statements whose binders are projections of their subject
// (match arms, `if x is P(...)`, `while ... is`): never the subject value itself.
func storeScanProjectingStmt(stmt ast.Stmt) bool {
	name := reflect.TypeOf(stmt).String()
	return strings.Contains(name, "Match") || strings.Contains(name, "If") || strings.Contains(name, "While") || strings.Contains(name, "Guard")
}

// storeScanQualifierFields are pattern string fields that name a type, variant or struct field,
// never a bound value.
var storeScanQualifierFields = map[string]bool{"EnumName": true, "Variant": true, "TypeName": true, "Field": true, "Value": true}

var storeScanPatternArgType = reflect.TypeOf(ast.MatchPatternArg{})

// storeScanReceiverIsValue reports whether a method-call receiver root names a value that could
// hold what the call stores: a local or binder of the function, or a global variable. A type or
// enum name qualifying a constructor (`Expr.Field(..)`, `Pattern.Struct(..)`) names none.
func (a *Analyzer) storeScanReceiverIsValue(name string, locals map[string]bool) bool {
	if locals[name] {
		return true
	}
	if a.globalScope == nil {
		return true
	}
	sym, ok := a.globalScope.Lookup(name)
	if !ok || sym == nil {
		_, isType := a.namedTypes[name]
		return !isType && !a.storeScanLooksLikeTypeName(name)
	}
	switch sym.Kind {
	case SymbolStruct, SymbolExternType, SymbolFunc, SymbolExternFunc:
		return false
	}
	return true
}

// storeScanLooksLikeTypeName: an unresolved capitalized root that is no local, parameter or
// global symbol is a (module-qualified) type name at a constructor call; values are lowercase or
// resolve through the global scope.
func (a *Analyzer) storeScanLooksLikeTypeName(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// storeScanReceiverIsBuiltinDArray reports whether a method receiver is a builtin darray (refs
// peeled). The analyzed type answers when known; a callee scanned before its body was analyzed
// falls back to declared syntax: a bare name every one of whose declarations in decl (params and
// locals) is spelled `darray[..]` / `dstr`. Anything else answers false (the caller then keeps its
// conservative mode).
func (a *Analyzer) storeScanReceiverIsBuiltinDArray(decl *ast.FuncDecl, recv ast.Expr) bool {
	inner := storeScanUnwrap(recv)
	if t, known := a.exprTypes[inner]; known && t != nil {
		_, ok := stripRefForBounds(t).(*DArrayType)
		return ok
	}
	if _, isField := inner.(*ast.FieldExpr); isField && decl != nil {
		if t, ok := a.storeScanParamFieldChainType(decl, inner); ok {
			_, isDArray := stripRefForBounds(t).(*DArrayType)
			return isDArray
		}
		return false
	}
	id, ok := inner.(*ast.Ident)
	if !ok || id == nil || decl == nil {
		return false
	}
	found, all := false, true
	for _, p := range decl.Params {
		if p.Name == id.Name {
			found = true
			all = all && storeScanTypeExprIsDArray(p.Type)
		}
	}
	idx := a.storeScanIndex(decl)
	for _, v := range idx.decls[id.Name] {
		found = true
		all = all && storeScanTypeExprIsDArray(v.Type)
	}
	if idx.otherBound[id.Name] {
		all = false // a pattern / loop binder of unknown type
	}
	return found && all
}

// storeScanParamFieldChainType types `p.f.g` where p is a parameter of decl that no body
// construct rebinds or shadows, from the callee's resolved signature and the struct field
// types -- usable before the callee's body has been analyzed.
func (a *Analyzer) storeScanParamFieldChainType(decl *ast.FuncDecl, expr ast.Expr) (Type, bool) {
	fields := []string{}
	cur := expr
	for {
		f, ok := cur.(*ast.FieldExpr)
		if !ok || f == nil {
			break
		}
		fields = append([]string{f.Field}, fields...)
		cur = storeScanUnwrap(f.Object)
	}
	root, ok := cur.(*ast.Ident)
	if !ok || root == nil || len(fields) == 0 {
		return nil, false
	}
	if t, ok := a.storeScanLocalLiteralType(decl, root.Name); ok {
		return storeScanWalkFields(t, fields)
	}
	index := -1
	for i, p := range decl.Params {
		if p.Name == root.Name {
			index = i
		}
	}
	if index < 0 {
		return nil, false
	}
	if a.storeScanBoundNames(decl)[root.Name] {
		return nil, false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return nil, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Variadic || index >= len(fnType.Params) {
		return nil, false
	}
	return storeScanWalkFields(fnType.Params[index], fields)
}

func storeScanWalkFields(t Type, fields []string) (Type, bool) {
	for _, name := range fields {
		st, ok := peelToStructType(t)
		if !ok || st == nil || st.Fields == nil {
			return nil, false
		}
		field, ok := st.Fields[name]
		if !ok || field.Type == nil || containsTypeParam(field.Type) {
			return nil, false
		}
		t = field.Type
	}
	return t, true
}

// storeScanBoundNames: every name a body construct of decl binds, memoized per function.
func (a *Analyzer) storeScanBoundNames(decl *ast.FuncDecl) map[string]bool {
	if cached, ok := a.storeScanBoundCache[decl]; ok {
		return cached
	}
	names := map[string]bool{}
	a.storeScanEachNode(decl, func(node any) {
		if v, isVar := node.(*ast.VarDeclStmt); isVar && v != nil {
			names[v.Name] = true
		}
		if st, isStmt := node.(ast.Stmt); isStmt && st != nil {
			for _, name := range stmtBoundNames(st) {
				names[name] = true
			}
		}
	})
	if a.storeScanBoundCache == nil {
		a.storeScanBoundCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.storeScanBoundCache[decl] = names
	return names
}

// storeScanLocalLiteralType types a body local every declaration of which is initialized by a
// literal of one non-generic struct `T{..}` (bound by no other construct), when T names exactly
// one struct type in the program.
func (a *Analyzer) storeScanLocalLiteralType(decl *ast.FuncDecl, name string) (Type, bool) {
	for _, p := range decl.Params {
		if p.Name == name {
			return nil, false
		}
	}
	idx := a.storeScanIndex(decl)
	litName, ok, other := "", true, idx.otherBound[name]
	for _, v := range idx.decls[name] {
		lit, isLit := storeScanUnwrap(v.Value).(*ast.StructLitExpr)
		if !isLit || lit == nil || len(lit.TypeArgs) > 0 || (litName != "" && lit.Name != litName) {
			ok = false
			break
		}
		litName = lit.Name
	}
	if !ok || other || litName == "" {
		return nil, false
	}
	st := a.storeScanUniqueStruct(litName)
	return st, st != nil
}

// storeScanUniqueStruct: the one struct type whose name is name (unqualified, or its last
// `.`-segment), nil when none or several exist. The index is built once.
func (a *Analyzer) storeScanUniqueStruct(name string) *StructType {
	if a.storeScanStructIndex == nil {
		a.storeScanStructIndex = map[string]*StructType{}
		ambiguous := map[string]bool{}
		seen := map[*StructType]bool{}
		for _, t := range a.namedTypes {
			st, isStruct := t.(*StructType)
			if !isStruct || st == nil || seen[st] || len(st.TypeParams) > 0 {
				continue
			}
			seen[st] = true
			bare := st.Name
			if i := strings.LastIndex(bare, "."); i >= 0 {
				bare = bare[i+1:]
			}
			if prev, dup := a.storeScanStructIndex[bare]; dup && prev != st {
				ambiguous[bare] = true
			}
			a.storeScanStructIndex[bare] = st
		}
		for bare := range ambiguous {
			a.storeScanStructIndex[bare] = nil
		}
	}
	bare := name
	if i := strings.LastIndex(bare, "."); i >= 0 {
		bare = bare[i+1:]
	}
	return a.storeScanStructIndex[bare]
}

// storeScanTypeExprIsDArray: `darray[..]` / `dstr`, through ref and mutable wrappers.
func storeScanTypeExprIsDArray(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.RefType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.BuiltinTypeExpr:
			return tt != nil && (tt.Name == "darray" || tt.Name == "dstr")
		case *ast.NamedType:
			return tt != nil && tt.Name == "dstr"
		}
		return false
	}
}

// storeScanViewLocals: when the scan's source parameter is a NON-BYTE darray (`darray[sview]`,
// `darray[ValueType]`, ...), no `sview`/`cstr` value can be or view the source's own backing --
// only a byte darray yields a view into its storage (`as_sview`) -- so a view derived from the
// source is (part of) an element value: content, never the header. Returns the locals whose every
// declaration is spelled `sview`/`cstr` and that no other construct binds, or nil when the source
// is not a non-byte darray (the caller then keeps its conservative header taint).
func (a *Analyzer) storeScanViewLocals(decl *ast.FuncDecl, other int) map[string]bool {
	if !a.storeScanSourceHasNoBytes(decl, other) {
		return nil
	}
	views, poisoned := map[string]bool{}, map[string]bool{}
	for _, p := range decl.Params {
		poisoned[p.Name] = true
	}
	a.storeScanEachNode(decl, func(node any) {
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				return
			}
			// Declared `sview`/`cstr`, or initialized from a value whose analyzed type is built only
			// from scalars and plain views (no container, view[T], reference or pointer).
			if t, known := a.exprTypes[n.Value]; storeScanTypeExprIsView(n.Type) || (n.Value != nil && known && t != nil && typeComponentsViewOnly(t)) {
				views[n.Name] = true
			} else {
				poisoned[n.Name] = true
			}
		case ast.Stmt:
			for _, name := range stmtBoundNames(n) {
				poisoned[name] = true
			}
		}
	})
	for name := range poisoned {
		delete(views, name)
	}
	for name := range a.storeScanViewFilledLocals(decl) {
		views[name] = true
	}
	return views
}

// storeScanViewFilledLocals: locals (of any type) that start empty -- every declaration is an
// argument-less struct literal or an empty list -- and are otherwise mentioned only as the root of
// a builtin darray `push`/`extend` receiver whose appended values are view-only or scalar, or in a
// `return`. From a byte-free source such a local can only ever hold views (its own storage is
// fresh), so it is a view value for the store scan.
func (a *Analyzer) storeScanViewFilledLocals(decl *ast.FuncDecl) map[string]bool {
	fresh, poisoned := map[string]bool{}, map[string]bool{}
	for _, p := range decl.Params {
		poisoned[p.Name] = true
	}
	allowed := map[string]int{}
	a.storeScanEachNode(decl, func(node any) {
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				return
			}
			if storeScanEmptyLiteral(n.Value) {
				fresh[n.Name] = true
			} else {
				poisoned[n.Name] = true
			}
		case *ast.ReturnStmt:
			if n != nil && n.Value != nil {
				for name := range fresh {
					allowed[name] += storeScanIdentCount(n.Value, name)
				}
			}
		case *ast.CallExpr:
			field, isMethod := n.Func.(*ast.FieldExpr)
			if n == nil || !isMethod || field == nil || (field.Field != "push" && field.Field != "extend") {
				return
			}
			root := rootIdentExpr(field.Object)
			if root == nil || !a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				return
			}
			for _, arg := range n.Args {
				t, known := a.exprTypes[storeScanUnwrap(arg)]
				if !known || t == nil {
					t, known = a.storeScanDeclTypeOf(decl, storeScanUnwrap(arg))
				}
				if !known || t == nil {
					return
				}
				if field.Field == "extend" {
					switch c := stripRefForBounds(t).(type) {
					case *DArrayType:
						t = c.Elem
					case *ArrayType:
						t = c.Elem
					default:
						return
					}
				}
				if t == nil || !(a.storeScanViewOnlyCached(t) || storeScanPlainScalarType(t)) {
					return
				}
			}
			allowed[root.Name] += storeScanIdentCount(field.Object, root.Name)
		case ast.Stmt:
			for _, name := range stmtBoundNames(n) {
				poisoned[name] = true
			}
		}
	})
	filled := map[string]bool{}
	for name := range fresh {
		if !poisoned[name] && storeScanIdentCount(decl.Body, name) == allowed[name] {
			filled[name] = true
		}
	}
	return filled
}

// storeScanEmptyLiteral: `T{}` with no fields, spreads or copy base, or `[]`.
func storeScanEmptyLiteral(value ast.Expr) bool {
	switch v := storeScanUnwrap(value).(type) {
	case *ast.StructLitExpr:
		return v != nil && len(v.Args) == 0 && len(v.Spreads) == 0 && v.CopyBase == nil
	case *ast.ListLitExpr:
		return v != nil && len(v.Elems) == 0
	}
	return false
}

// storeScanIdentCount counts the identifier occurrences of name in node (field selectors are not
// names).
func storeScanIdentCount(node any, name string) int {
	count := 0
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				if id.Name == name || rootNameOfDotted(id.Name) == name {
					count++
				}
				return
			}
			if fe, ok := v.Interface().(*ast.FieldExpr); ok && fe != nil {
				rec(reflect.ValueOf(fe.Object))
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				rec(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(node))
	return count
}

// storeScanViewLocalsCached memoizes the per-function view-local set (it depends on the source
// parameter only through the non-byte-darray gate).
func (a *Analyzer) storeScanViewLocalsCached(decl *ast.FuncDecl, other int) map[string]bool {
	if !a.storeScanSourceHasNoBytes(decl, other) {
		return nil
	}
	if views, ok := a.storeScanViewCache[decl]; ok {
		return views
	}
	views := a.storeScanViewLocals(decl, other)
	if a.storeScanViewCache == nil {
		a.storeScanViewCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.storeScanViewCache[decl] = views
	return views
}

// storeScanValueIsView reports whether a value is a plain `sview`/`cstr` (by analyzed type, or as a
// view local from storeScanViewLocals). References and `view[T]` slices are not.
func (a *Analyzer) storeScanValueIsView(decl *ast.FuncDecl, value ast.Expr, views map[string]bool) bool {
	inner := storeScanUnwrap(value)
	if id, ok := inner.(*ast.Ident); ok && id != nil && views[id.Name] {
		return true
	}
	t, known := a.exprTypes[inner]
	if !known || t == nil {
		// Before the body is analyzed: `p.f[i]` / `p.f` typed from the signature.
		t, known = a.storeScanDeclTypeOf(decl, inner)
	}
	if known && t != nil {
		return a.storeScanViewOnlyCached(t)
	}
	if id, ok := inner.(*ast.Ident); ok && id != nil {
		return views[id.Name]
	}
	return false
}

func storeScanTypeIsViewOnly(t Type) bool {
	switch StripAggregateStateType(t).(type) {
	case *SViewType, *CStrType:
		return true
	}
	return typeComponentsViewOnly(t)
}

// storeScanDeclReturnsViewOnly: decl's resolved return type is view-only (and nothing else is
// returned through lmut thread slots), so whatever it returns is at most a view.
func (a *Analyzer) storeScanDeclReturnsViewOnly(decl *ast.FuncDecl) bool {
	if decl == nil || len(decl.LmutThreadSlots) > 0 {
		return false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft == nil || ft.Return == nil || containsTypeParam(ft.Return) {
		return false
	}
	return a.storeScanViewOnlyCached(ft.Return)
}

// storeScanDeclTypeOf types a param field chain `p.f.g`, or an index into one `p.f[i]`, from
// decl's resolved signature (storeScanParamFieldChainType).
func (a *Analyzer) storeScanDeclTypeOf(decl *ast.FuncDecl, expr ast.Expr) (Type, bool) {
	if decl == nil || expr == nil {
		return nil, false
	}
	switch e := expr.(type) {
	case *ast.FieldExpr:
		return a.storeScanParamFieldChainType(decl, e)
	case *ast.IndexExpr:
		if e == nil || e.Index2 != nil {
			return nil, false
		}
		object, ok := storeScanUnwrap(e.Object).(*ast.FieldExpr)
		if !ok {
			return nil, false
		}
		t, ok := a.storeScanParamFieldChainType(decl, object)
		if !ok {
			return nil, false
		}
		switch c := stripRefForBounds(t).(type) {
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

// storeScanTypeExprIsView: a declared `sview` / `cstr` (no reference wrapper).
func storeScanTypeExprIsView(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && (tt.Name == "sview" || tt.Name == "cstr")
	case *ast.BuiltinTypeExpr:
		return tt != nil && (tt.Name == "sview" || tt.Name == "cstr")
	}
	return false
}

// storeScanTypeExprIsNonByteDArray: `darray[E]` (through ref/mutable wrappers) whose element is
// spelled as a non-byte scalar or a plain view. Any other element (a named struct or enum, which may
// hold a dstr) is left to the resolved-type check (typeHoldsNoBytes).
func storeScanTypeExprIsNonByteDArray(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.RefType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.BuiltinTypeExpr:
			if tt == nil || tt.Name != "darray" || len(tt.TypeArgs) != 1 {
				return false
			}
			var elemName string
			switch e := tt.TypeArgs[0].(type) {
			case *ast.NamedType:
				if e != nil {
					elemName = e.Name
				}
			case *ast.BuiltinTypeExpr:
				if e != nil && len(e.TypeArgs) == 0 {
					elemName = e.Name
				}
			}
			switch elemName {
			case "sview", "cstr", "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "bool":
				return true
			}
			return false
		}
		return false
	}
}

// storeScanSourceHasNoBytes: the scan's source parameter holds no byte storage anywhere (no
// darray[u8]/dstr, no fixed byte array, through fields, payloads and elements), so no view-only
// value derived from it can view its backing. A syntactic non-byte darray qualifies directly;
// otherwise the callee's resolved parameter type decides. Type parameters, packed enums and
// anything unrecognized are treated as possibly holding bytes.
func (a *Analyzer) storeScanSourceHasNoBytes(decl *ast.FuncDecl, other int) bool {
	if decl == nil || other < 0 || other >= len(decl.Params) {
		return false
	}
	if storeScanTypeExprIsNonByteDArray(decl.Params[other].Type) {
		return true
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || other >= len(fnType.Params) {
		return false
	}
	return typeHoldsNoBytes(fnType.Params[other], map[Type]bool{})
}

func typeHoldsNoBytes(t Type, seen map[Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true // a cycle adds no new component
	}
	seen[t] = true
	switch tt := StripAggregateStateType(t).(type) {
	case *BuiltinType:
		switch tt.Name {
		case "u8", "i8":
			return true // a scalar byte by value is not storage a view can point into
		case "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "bool", "char":
			return true
		}
		return false
	case *SViewType, *CStrType, *BitIntType, *ConstEnumType, *BitGroupType:
		return true
	case *RefType:
		return tt != nil && typeHoldsNoBytes(tt.Elem, seen)
	case *DArrayType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		if b, isBuiltin := tt.Elem.(*BuiltinType); isBuiltin && (b.Name == "u8" || b.Name == "i8") {
			return false
		}
		return typeHoldsNoBytes(tt.Elem, seen)
	case *ArrayType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		if b, isBuiltin := tt.Elem.(*BuiltinType); isBuiltin && (b.Name == "u8" || b.Name == "i8") {
			return false
		}
		return typeHoldsNoBytes(tt.Elem, seen)
	case *TupleType:
		for _, f := range tt.Fields {
			if !typeHoldsNoBytes(f.Type, seen) {
				return false
			}
		}
		return true
	case *OptionalType:
		return typeHoldsNoBytes(tt.Value, seen)
	case *ErrorUnionType:
		return typeHoldsNoBytes(tt.Value, seen)
	case *StructType:
		if tt == nil || tt.Store {
			return false
		}
		for _, f := range tt.Fields {
			if !typeHoldsNoBytes(f.Type, seen) {
				return false
			}
		}
		return true
	case *EnumType:
		if tt == nil || tt.Packed || tt.Root().Packed {
			return false
		}
		for _, v := range tt.Variants {
			for _, payload := range v.Payload {
				if !typeHoldsNoBytes(payload, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// storeScanHolderMayAlias: the callee can make local w.local alias storage it is given. A
// container local whose elements share no storage holds only its own buffer unless its header
// is rebound -- which a by-value parameter cannot do to the caller's local.
func (a *Analyzer) storeScanHolderMayAlias(decl *ast.FuncDecl, w storeScanCallWrite) bool {
	if w.callee == nil || a.storeScanContainerElemsMayShare(decl, &ast.Ident{Name: w.local}) {
		return true
	}
	sym := a.funcDeclSymbols[w.callee]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || w.into < 0 || w.into >= len(fnType.Params) {
		return true
	}
	_, isRef := fnType.Params[w.into].(*RefType)
	return isRef
}

// storeScanPlaceDepth: the number of field / index projections between a place and its root
// identifier (`h` 0, `h[0]` 1, `h[0][0]` and `h.lists[0]` 2).
func storeScanPlaceDepth(expr ast.Expr) int {
	depth := 0
	for {
		switch e := stripAddrAndParens(expr).(type) {
		case *ast.FieldExpr:
			if e == nil {
				return depth
			}
			depth++
			expr = e.Object
		case *ast.IndexExpr:
			if e == nil {
				return depth
			}
			depth++
			expr = e.Object
		default:
			return depth
		}
	}
}

// storeScanHoldsNoRegionStorage: t carries no region storage and can contain no region reference
// (memoized per type: the scan asks it for every node of every summary query).
// storeScanExtendArgMode: a builtin darray's `extend(xs)` copies xs's ELEMENTS into the
// receiver and never retains xs's own header; elements whose type holds no region storage
// (`darray[u32]`) carry nothing from xs at all.
func (a *Analyzer) storeScanExtendArgMode(arg ast.Expr, mode storeMode) storeMode {
	if mode == storeNone {
		return mode
	}
	if t, known := a.exprTypes[storeScanUnwrap(arg)]; known && t != nil && !containsTypeParam(t) {
		if da, ok := stripRefForBounds(t).(*DArrayType); ok && da.Elem != nil && !containsTypeParam(da.Elem) && a.storeScanHoldsNoRegionStorage(da.Elem) {
			return storeNone
		}
	}
	if mode == storeHeader {
		return storeElements
	}
	return mode
}

func (a *Analyzer) storeScanHoldsNoRegionStorage(t Type) bool {
	cacheable := t != nil && reflect.ValueOf(t).Kind() == reflect.Pointer
	if cacheable {
		if cached, ok := a.storeScanNoStorageCache[t]; ok {
			return cached
		}
	}
	result := !typeCarriesRegionStorage(t) && !a.typeCanContainRegionRefs(stripRefForBounds(t), map[string]bool{})
	if cacheable {
		if a.storeScanNoStorageCache == nil {
			a.storeScanNoStorageCache = map[Type]bool{}
		}
		a.storeScanNoStorageCache[t] = result
	}
	return result
}

// storeScanViewOnlyCached: storeScanTypeIsViewOnly, memoized per type.
func (a *Analyzer) storeScanViewOnlyCached(t Type) bool {
	cacheable := t != nil && reflect.ValueOf(t).Kind() == reflect.Pointer
	if cacheable {
		if cached, ok := a.storeScanViewOnlyCache[t]; ok {
			return cached
		}
	}
	result := storeScanTypeIsViewOnly(t)
	if cacheable {
		if a.storeScanViewOnlyCache == nil {
			a.storeScanViewOnlyCache = map[Type]bool{}
		}
		a.storeScanViewOnlyCache[t] = result
	}
	return result
}

// storeScanSharesCached: typeSharesIndirectStorage, memoized per type.
func (a *Analyzer) storeScanSharesCached(t Type) bool {
	cacheable := t != nil && reflect.ValueOf(t).Kind() == reflect.Pointer
	if cacheable {
		if cached, ok := a.storeScanSharesCache[t]; ok {
			return cached
		}
	}
	result := typeSharesIndirectStorage(t, map[Type]bool{})
	if cacheable {
		if a.storeScanSharesCache == nil {
			a.storeScanSharesCache = map[Type]bool{}
		}
		a.storeScanSharesCache[t] = result
	}
	return result
}

type storeScanTaintKey struct {
	decl   *ast.FuncDecl
	other  int
	target string
}

type storeScanTaintResult struct {
	params, header, content, targets, views, holders map[string]bool
}
