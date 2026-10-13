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
	storeNone     storeMode = iota // nothing reachable from the source is stored
	storeElements                  // only element/field VALUES read out of the source are stored
	storeHeader                    // the source value itself (a container header, a view of it) may be stored
)

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
	mode := a.calleeStoreModeIntoSeen(decl, other, index, map[storeScanKey]bool{})
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

// calleeStoreModeIntoSeen memoizes an inner frame only when its search hit no in-progress
// frame: a provisional `none` for an open cycle member is not final for the frames inside it.
func (a *Analyzer) calleeStoreModeIntoSeen(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	key := storeScanKey{decl, other, index}
	if mode, ok := a.storeScanCache[key]; ok {
		return mode
	}
	if seen[key] {
		// A recursive cycle adds no store the rest of the body does not already show.
		a.storeScanCycleHit = true
		return storeNone
	}
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	mode := a.calleeStoreModeIntoBody(decl, other, index, seen)
	hit := a.storeScanCycleHit
	if !hit {
		a.cacheStoreScan(key, mode)
	}
	a.storeScanCycleHit = saved || hit
	return mode
}

func (a *Analyzer) calleeStoreModeIntoBody(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	if decl == nil || other < 0 || index < 0 || other >= len(decl.Params) || index >= len(decl.Params) || len(decl.Body) == 0 {
		return storeHeader
	}
	key := storeScanKey{decl, other, index}
	seen[key] = true
	params, header, content, targets := storeScanTaint(decl, other, decl.Params[index].Name)
	valueMode := func(value ast.Expr) storeMode {
		if storeScanMentionsHeader(value, header) {
			return storeHeader
		}
		if storeScanMentions(value, content) {
			return storeElements
		}
		return storeNone
	}
	mode := storeNone
	walkStoreScanNodes(decl.Body, func(node any) {
		if mode == storeHeader {
			return
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			root := rootIdentExpr(n.Target)
			if root == nil || !targets[root.Name] {
				return
			}
			// A plain rebind of a non-param local alias is not a store into the target.
			if id, isIdent := n.Target.(*ast.Ident); isIdent && !params[id.Name] && !n.WriteThrough {
				return
			}
			// `table <- f(table, scope)`: the stored value is the call's RESULT. The call's own
			// stores are scanned when the walk reaches it; its result carries a tainted argument
			// only when the callee's summary connects that argument to its return.
			if call, isCall := stripParenExpr(n.Value).(*ast.CallExpr); isCall && call != nil {
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
						inner := a.calleeReturnModeOf(callee, i, seen)
						if inner != storeNone && argMode == storeElements {
							inner = storeElements
						}
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
			if isMethod && field != nil {
				if root := rootIdentExpr(field.Object); root != nil && targets[root.Name] {
					for _, arg := range n.Args {
						mode = storeScanRaise(decl, 177, n, mode, valueMode(arg))
					}
				}
			}
			type side struct {
				index int
				mode  storeMode
			}
			targetArgs, taintedArgs := []int{}, []side{}
			for i, arg := range args {
				if storeScanMentions(arg, targets) {
					targetArgs = append(targetArgs, i)
				}
				if m := valueMode(arg); m != storeNone {
					taintedArgs = append(taintedArgs, side{i, m})
				}
			}
			if isMethod && field != nil && len(args) == len(n.Args) {
				// An unrewritten method call: the receiver is an operand too, and the callee is
				// unresolved. A builtin growth method moves its arguments INTO the receiver only.
				recvTarget := storeScanMentions(field.Object, targets)
				recvMode := valueMode(field.Object)
				if containerGrowthMethods[field.Field] && !recvTarget {
					return
				}
				if recvTarget {
					for _, s := range taintedArgs {
						mode = storeScanRaise(decl, 204, n, mode, s.mode)
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
						if storeScanBundles(args[ti]) {
							mode = storeScanRaise(decl, 225, n, mode, s.mode)
						}
						continue
					}
					inner := a.calleeStoreModeIntoSeen(callee, s.index, ti, seen)
					if inner != storeNone && s.mode == storeElements {
						// The callee stores (part of) an element value, never this source's header.
						inner = storeElements
					}
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
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
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
	var rec func(v reflect.Value, depth int)
	rec = func(v reflect.Value, depth int) {
		if !v.IsValid() || depth > 8 {
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
			rec(v.Elem(), depth+1)
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i), depth+1)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), depth+1)
			}
		case reflect.String:
			if s := v.String(); s != "" {
				out = append(out, s)
			}
		}
	}
	for i := 0; i < v.NumField(); i++ {
		rec(v.Field(i), 1)
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
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			continue
		}
		if f.Kind() == reflect.Slice {
			if _, isBody := f.Interface().([]ast.Stmt); isBody {
				continue
			}
		}
		if storeScanMentions(f.Interface(), names) {
			return true
		}
	}
	return false
}

var storeScanTrace = os.Getenv("ELISA_STORESCAN_TRACE") != ""

func storeScanRaise(decl *ast.FuncDecl, rule int, node ast.Node, mode, next storeMode) storeMode {
	if next > mode && storeScanTrace && node != nil {
		fmt.Fprintf(os.Stderr, "STORESCAN %s rule=%d mode=%d at %s\n", decl.Name, rule, next, node.Pos())
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
	return typeCarriesRegionStorage(param) || a.typeCanContainRegionRefs(stripRefForBounds(param), map[string]bool{})
}

// storeScanTaint computes, to a fixpoint over the body, which names may hold the WHOLE source
// parameter (header) or something read out of it (content), and which names may alias the
// target place (targetName; "" for none). Locals become tainted by binding (decl/assign of the
// local itself) and by receiving a tainted store (`local.f <- v`, `local.xs.push(v)`).
func storeScanTaint(decl *ast.FuncDecl, other int, targetName string) (params, header, content, targets map[string]bool) {
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
	add := func(set map[string]bool, name string) bool {
		if name == "" || params[name] || set[name] {
			return false
		}
		set[name] = true
		return true
	}
	taint := func(name string, value ast.Expr) bool {
		changed := false
		if storeScanMentionsHeader(value, header) {
			changed = add(header, name) || changed
			changed = add(content, name) || changed
		} else if storeScanMentions(value, content) {
			changed = add(content, name) || changed
		}
		return changed
	}
	bind := func(name string, value ast.Expr) bool {
		changed := taint(name, value)
		if storeScanMayAliasPlace(value, targets) {
			changed = add(targets, name) || changed
		}
		return changed
	}
	for changed := true; changed; {
		changed = false
		walkStoreScanNodes(decl.Body, func(node any) {
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil && bind(n.Name, n.Value) {
					changed = true
				}
			case *ast.AssignStmt:
				if n == nil {
					return
				}
				if id, ok := n.Target.(*ast.Ident); ok && id != nil {
					if bind(id.Name, n.Value) {
						changed = true
					}
					return
				}
				// A store into part of a local taints the local.
				if root := rootIdentExpr(n.Target); root != nil && !params[root.Name] && taint(root.Name, n.Value) {
					changed = true
				}
			case *ast.CallExpr:
				// A method call on a local receiver may store its arguments into it.
				if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
					if root := rootIdentExpr(field.Object); root != nil && !params[root.Name] {
						for _, arg := range n.Args {
							if taint(root.Name, arg) {
								changed = true
							}
						}
					}
				}
			case *ast.IterForStmt:
				// Loop binders over a tainted source are element values.
				if n != nil && storeScanMentions(n.Source, content) {
					for _, name := range stmtBoundNames(n) {
						if add(content, name) {
							changed = true
						}
					}
				}
			case ast.Stmt:
				// Any other binding construct: its binders may hold the whole source.
				if !stmtHeaderMentionsAny(n, content) {
					return
				}
				// Pattern binders of a match / `is` test project fields or payloads (content);
				// only other binders (a rebind of a call result, a destructure) may be the whole.
				whole := stmtHeaderMentionsAny(n, header) && !storeScanProjectingStmt(n)
				for _, name := range stmtBoundNames(n) {
					if add(content, name) {
						changed = true
					}
					if whole && add(header, name) {
						changed = true
					}
				}
			}
		})
	}
	return params, header, content, targets
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
		a.storeScanCycleHit = true
		return storeNone
	}
	seen[key] = true
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	_, header, content, _ := storeScanTaint(decl, other, "")
	mode := storeNone
	sawReturn := false
	walkStoreScanNodes(decl.Body, func(node any) {
		if _, isRet := node.(*ast.ReturnStmt); isRet {
			sawReturn = true
		}
	})
	if !sawReturn && !a.declReturnsVoid(decl) {
		// A value produced without a `return` (an expression tail) is not modelled.
		mode = storeHeader
	}
	walkStoreScanNodes(decl.Body, func(node any) {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok || ret == nil || ret.Value == nil || mode == storeHeader {
			return
		}
		value := stripParenExpr(ret.Value)
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
					inner := a.calleeReturnModeOf(callee, i, seen)
					if inner != storeNone && argMode == storeElements {
						inner = storeElements
					}
					mode = maxStoreMode(mode, inner)
				}
				return
			}
		}
		if storeScanMentionsHeader(value, header) {
			mode = storeHeader
		} else if storeScanMentions(value, content) {
			mode = maxStoreMode(mode, storeElements)
		}
	})
	hit := a.storeScanCycleHit
	if !hit {
		a.cacheStoreScan(key, mode)
	}
	a.storeScanCycleHit = saved || hit
	return mode
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
	return containsTypeParam(fnType.Params[index]) || typeSharesIndirectStorage(fnType.Params[index], map[Type]bool{})
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
func storeScanMayAliasPlace(value ast.Expr, targets map[string]bool) bool {
	if value == nil || !storeScanMentions(value, targets) {
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
