package semantic

import (
	"reflect"

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
	return a.calleeStoreModeIntoSeen(decl, other, index, map[storeScanKey]bool{})
}

type storeScanKey struct {
	decl         *ast.FuncDecl
	other, index int
}

func (a *Analyzer) calleeStoreModeIntoSeen(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	if decl == nil || other < 0 || index < 0 || other >= len(decl.Params) || index >= len(decl.Params) || len(decl.Body) == 0 {
		return storeHeader
	}
	key := storeScanKey{decl, other, index}
	if seen[key] {
		// A recursive cycle adds no store the rest of the body does not already show.
		return storeNone
	}
	seen[key] = true
	params := map[string]bool{}
	for _, p := range decl.Params {
		params[p.Name] = true
	}
	header := map[string]bool{decl.Params[other].Name: true}
	content := map[string]bool{decl.Params[other].Name: true}
	targets := map[string]bool{decl.Params[index].Name: true}
	add := func(set map[string]bool, name string) bool {
		if name == "" || params[name] || set[name] {
			return false
		}
		set[name] = true
		return true
	}
	bind := func(name string, value ast.Expr) bool {
		changed := false
		if storeScanMentionsHeader(value, header) {
			changed = add(header, name) || changed
			changed = add(content, name) || changed
		} else if storeScanMentions(value, content) {
			changed = add(content, name) || changed
		}
		if storeScanMentions(value, targets) {
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
				if id, ok := n.Target.(*ast.Ident); ok && id != nil && bind(id.Name, n.Value) {
					changed = true
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
				whole := stmtHeaderMentionsAny(n, header)
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
			mode = maxStoreMode(mode, valueMode(n.Value))
		case *ast.CallExpr:
			if n == nil {
				return
			}
			args := returnBorrowCallArgs(n)
			field, isMethod := n.Func.(*ast.FieldExpr)
			if isMethod && field != nil {
				if root := rootIdentExpr(field.Object); root != nil && targets[root.Name] {
					for _, arg := range n.Args {
						mode = maxStoreMode(mode, valueMode(arg))
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
						mode = maxStoreMode(mode, s.mode)
					}
				}
				if recvMode != storeNone && len(targetArgs) > 0 {
					mode = maxStoreMode(mode, recvMode)
				}
				return
			}
			if len(targetArgs) == 0 || len(taintedArgs) == 0 {
				return
			}
			callee, ok := a.resolveReadOnlyScanCallee(n)
			if !ok || callee == nil || !storeFlowCallFitsParams(callee, n) || funcHasArenaParam(callee) {
				for _, s := range taintedArgs {
					mode = maxStoreMode(mode, s.mode)
				}
				return
			}
			for _, ti := range targetArgs {
				for _, s := range taintedArgs {
					if ti == s.index {
						mode = maxStoreMode(mode, s.mode)
						continue
					}
					inner := a.calleeStoreModeIntoSeen(callee, s.index, ti, seen)
					if inner != storeNone && s.mode == storeElements {
						// The callee stores (part of) an element value, never this source's header.
						inner = storeElements
					}
					mode = maxStoreMode(mode, inner)
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
