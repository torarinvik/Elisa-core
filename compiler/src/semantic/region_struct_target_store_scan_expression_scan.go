package semantic

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/astscan"
)

// storeScanMentionsHeader reports whether an expression may evaluate to (or view) a HEADER name
// as a whole. Element reads `xs[i]` and scalar size queries (`xs.count`) do not.
func storeScanMentionsHeader(node any, names map[string]bool) bool {
	if node == nil || len(names) == 0 {
		return false
	}
	// The common paths are an identifier or a projection of it. Defer
	// reflection and the visited set until we need to inspect a larger value.
	for depth := 0; depth < 8; depth++ {
		switch expr := node.(type) {
		case *ast.IntLit:
			if storeFlowIntLiteralFast {
				return expr != nil && (names[expr.Value] || names[expr.Suffix])
			}
			return storeScanMentionsHeaderPooled(node, names)
		case *ast.FloatLit:
			return expr != nil && (names[expr.Value] || names[expr.Suffix])
		case *ast.StringLit:
			return expr != nil && names[expr.Value]
		case *ast.CharLit:
			return expr != nil && names[expr.Value]
		case *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
			return false
		case *ast.ParenExpr:
			if expr == nil {
				return false
			}
			node = expr.Inner
		case *ast.MoveExpr:
			if expr == nil {
				return false
			}
			node = expr.Operand
		case *ast.AddrOfExpr:
			if expr == nil {
				return false
			}
			node = expr.Operand
		case *ast.UnaryExpr:
			if expr == nil {
				return false
			}
			if expr.LoweredCall != nil {
				return storeScanMentionsHeaderPooled(node, names)
			}
			node = expr.Operand
		case *ast.Ident:
			return expr != nil && (names[expr.Name] || names[rootNameOfDotted(expr.Name)])
		case *ast.FieldExpr:
			if expr == nil || storeScanScalarHeaderField(expr.Field) {
				return false
			}
			node = expr.Object
		case *ast.IndexExpr:
			if expr != nil && expr.Index2 == nil {
				if root := rootIdentExpr(expr.Object); root != nil && names[root.Name] {
					// A read from the tracked header does not itself retain that
					// header; only its index expression can mention one.
					node = expr.Index
					continue
				}
			}
			return storeScanMentionsHeaderPooled(node, names)
		default:
			return storeScanMentionsHeaderPooled(node, names)
		}
	}
	return storeScanMentionsHeaderPooled(node, names)
}

// Header expressions are typically much smaller than whole function bodies.
// Keep their visited maps separate so a large body walk cannot make every
// subsequent small header scan clear a large map.
var storeScanHeaderSeenPool = sync.Pool{New: func() any { return make(map[uintptr]bool, 32) }}

func storeScanMentionsHeaderPooled(node any, names map[string]bool) bool {
	seen := storeScanHeaderSeenPool.Get().(map[uintptr]bool)
	defer func() {
		if len(seen) <= 512 {
			clear(seen)
			storeScanHeaderSeenPool.Put(seen)
		}
	}()
	return storeScanMentionsHeaderWalk(reflect.ValueOf(node), names, seen)
}

func storeScanScalarHeaderField(name string) bool {
	switch name {
	case "count", "len", "capacity", "is_empty":
		return true
	default:
		return false
	}
}

func storeScanMentionsHeaderWalk(v reflect.Value, names map[string]bool, seen map[uintptr]bool) bool {
	if !v.IsValid() || !v.CanInterface() {
		return false
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return false
		}
		// Desugared calls and conditions can share their operands. For a
		// fixed name set, each pointer has the same answer on every path.
		seen[v.Pointer()] = true
		switch expr := v.Interface().(type) {
		case *ast.Ident:
			return names[expr.Name] || names[rootNameOfDotted(expr.Name)]
		case *ast.IndexExpr:
			if expr.Index2 == nil {
				if root := rootIdentExpr(expr.Object); root != nil && names[root.Name] {
					return storeScanMentionsHeaderWalk(reflect.ValueOf(expr.Index), names, seen)
				}
			}
		case *ast.FieldExpr:
			return !storeScanScalarHeaderField(expr.Field) && storeScanMentionsHeaderWalk(reflect.ValueOf(expr.Object), names, seen)
		}
		return storeScanMentionsHeaderWalk(v.Elem(), names, seen)
	case reflect.Interface:
		return !v.IsNil() && storeScanMentionsHeaderWalk(v.Elem(), names, seen)
	case reflect.Struct:
		if v.Type() == storeScanPosType {
			return false
		}
		for i := 0; i < v.NumField(); i++ {
			if storeScanMentionsHeaderWalk(v.Field(i), names, seen) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if storeScanMentionsHeaderWalk(v.Index(i), names, seen) {
				return true
			}
		}
	case reflect.String:
		return names[v.String()]
	}
	return false
}

// walkStoreScanNodes visits every AST node pointer reachable from stmts.
func walkStoreScanNodes(stmts []ast.Stmt, visit func(any)) {
	// Desugaring shares subtrees (a nested guard chain reuses its operands), so an unmemoized
	// walk is exponential in nesting depth. Each node is visited once. The generated typed walker
	// visits exactly what the reflective fallback does, without reflection.
	seen := borrowStoreScanSeen()
	defer releaseStoreScanSeen(seen)
	astscan.WalkStatements(stmts, visit, seen)
}

// The pre-body passes walk every function body dozens of times; a fresh visited set
// per walk regrew a map to the body's node count each time (map growth was a
// quarter of a retained update). Walks nest through visit callbacks, so the sets
// come from a pool rather than one shared map. A set grown by a very large body is
// dropped: clearing it costs its capacity, not its use, on every later small walk.
var storeScanSeenPool = sync.Pool{New: func() any { return make(map[uintptr]bool, 256) }}

const storeScanSeenKeepLimit = 1 << 14

func borrowStoreScanSeen() map[uintptr]bool {
	return storeScanSeenPool.Get().(map[uintptr]bool)
}

func releaseStoreScanSeen(seen map[uintptr]bool) {
	if len(seen) > storeScanSeenKeepLimit {
		return
	}
	clear(seen)
	storeScanSeenPool.Put(seen)
}

// storeScanWalkReflect is the reflective store-scan walk, the specification the generated walker
// follows: every non-nil pointer reachable through interfaceable struct fields, slices, arrays and
// interfaces (not map values), each address once.
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
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

// stmtBoundNames over-approximates the names a statement may bind: every string field in its
// non-body parts (patterns, binder names, rebind targets).
// These common statement schemas contain only a source position and an
// expression, which the reference walk skips. Check the schema at startup so
// added fields automatically fall back to reflection.
var storeScanExprStmtNoBinders = storeScanSimpleExprStmtSchema(reflect.TypeOf(ast.ExprStmt{}), "Expr")
var storeScanDiscardStmtNoBinders = storeScanSimpleExprStmtSchema(reflect.TypeOf(ast.DiscardStmt{}), "Value")
var storeScanReturnStmtNoBinders = storeScanSimpleExprStmtSchema(reflect.TypeOf(ast.ReturnStmt{}), "Value")

func storeScanSimpleExprStmtSchema(t reflect.Type, exprField string) bool {
	return t.NumField() == 2 && t.Field(0).Name == "Position" && t.Field(0).Type == storeScanPosType &&
		t.Field(1).Name == exprField && t.Field(1).Type == reflect.TypeOf((*ast.Expr)(nil)).Elem()
}

func stmtBoundNames(stmt ast.Stmt) []string {
	switch stmt.(type) {
	case *ast.ExprStmt:
		if storeScanExprStmtNoBinders {
			return nil
		}
	case *ast.DiscardStmt:
		if storeScanDiscardStmtNoBinders {
			return nil
		}
	case *ast.ReturnStmt:
		if storeScanReturnStmtNoBinders {
			return nil
		}
	}
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
			rfs := reflectFields(t)
			for i := 0; i < v.NumField(); i++ {
				name := rfs[i].Name
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
	rfs := reflectFields(top)
	for i := 0; i < v.NumField(); i++ {
		if rfs[i].Name == "Captures" {
			continue // `while c |xs, t|:` names existing bindings (a mutation manifest); binds none
		}
		rec(v.Field(i), 1, rfs[i].Name)
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

var storeScanTrace = os.Getenv("ELISA_STORESCAN_TRACE") != "" || storeChainTrace

// storeChainTrace (ELISA_STORE_TRACE=1) is a debug aid for "call may store" diagnostics: every
// raising store in a callee body prints a RAISE line, and every call that carries a callee's
// store back into the caller prints a CHAIN line, so a call-site error can be followed to the
// statements that cause it:
//
//	RAISE <fn> rule=<n> mode=<m> src=<source param>-><target param> at <pos>
//	CHAIN <fn>(<src>-><tgt>) -> <callee>(<src>-><tgt>) at <pos>
var storeChainTrace = os.Getenv("ELISA_STORE_TRACE") != ""

// storeScanTraceOther / storeScanTraceTarget name the source and target parameters of the body
// scan in progress (trace only).
var storeScanTraceOther, storeScanTraceTarget string

func storeScanRaise(decl *ast.FuncDecl, rule int, node ast.Node, mode, next storeMode) storeMode {
	if next > mode && storeScanTrace && node != nil {
		if storeChainTrace {
			fmt.Fprintf(os.Stderr, "RAISE %s rule=%d mode=%d src=%s->%s at %s\n", decl.Name, rule, next, storeScanTraceOther, storeScanTraceTarget, node.Pos())
		} else {
			fmt.Fprintf(os.Stderr, "STORESCAN %s rule=%d mode=%d src=%s at %s\n", decl.Name, rule, next, storeScanTraceOther, node.Pos())
		}
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
		if !storeScanMentions(inner, targets) || (!a.declParamCarriesStorage(callee, i) && !a.declParamMayEncodeAddress(callee, i)) {
			continue
		}
		// A provisional (open-cycle) answer proves nothing dropped: settled counts it as header.
		if a.storeScanSettled(func() storeMode { return a.calleeReturnModeOf(callee, i, seen) }) > storeViews && !a.calleeReturnsNothingWritableFrom(callee, i, seen) {
			return false
		}
	}
	return true
}
