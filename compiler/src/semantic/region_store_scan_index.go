package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// mentions: storeScanMentions(expr, names), with expr's mention tokens memoized in the index
// (fixpoints ask the same expressions again on every iteration).
func (idx *storeScanDeclIndex) mentions(expr ast.Expr, names map[string]bool) bool {
	if expr == nil || len(names) == 0 {
		return false
	}
	tokens, ok := idx.exprTokens[expr]
	if !ok {
		tokens = storeScanMentionTokens(expr)
		idx.exprTokens[expr] = tokens
	}
	return storeScanTokensHit(tokens, names)
}

// headerMentions: stmtHeaderMentionsAny(stmt, names), memoized the same way.
func (idx *storeScanDeclIndex) headerMentions(stmt ast.Stmt, names map[string]bool) bool {
	if stmt == nil || len(names) == 0 {
		return false
	}
	tokens, ok := idx.headerTokens[stmt]
	if !ok {
		tokens = storeScanHeaderTokens(stmt)
		idx.headerTokens[stmt] = tokens
	}
	return storeScanTokensHit(tokens, names)
}

func storeScanTokensHit(tokens []string, names map[string]bool) bool {
	for _, t := range tokens {
		if names[t] {
			return true
		}
	}
	return false
}

// storeScanMentionTokens: every string storeScanMentions would test against its name set.
func storeScanMentionTokens(node any) []string {
	var tokens []string
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
				tokens = append(tokens, id.Name, rootNameOfDotted(id.Name))
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
		case reflect.String:
			tokens = append(tokens, v.String())
		}
	}
	rec(reflect.ValueOf(node))
	return tokens
}

// storeScanDeclIndex: per-function facts the store scan used to recompute with a whole-body walk
// per query (one walk per call site made the scan quadratic in function size).
type storeScanDeclIndex struct {
	decls      map[string][]*ast.VarDeclStmt // every declaration of a name
	otherBound map[string]bool               // names bound by a non-VarDecl construct
	// nodes lists, per name, the VarDecl / Assign / Call / binding-statement nodes whose own
	// expressions (a statement's header only, not nested statements) mention the name.
	nodes map[string][]any
	// flat: every node walkStoreScanNodes visits, in walk order (fixpoints iterate this instead
	// of re-walking the AST reflectively per iteration).
	flat []any
	// locals: names declared or bound anywhere in the body.
	locals map[string]bool
	// memoized mention tokens (see mentions / headerMentions)
	exprTokens   map[ast.Expr][]string
	headerTokens map[ast.Stmt][]string
	bound        map[ast.Stmt][]string
	bundleCache  map[ast.Expr]bool
}

func (a *Analyzer) storeScanIndex(decl *ast.FuncDecl) *storeScanDeclIndex {
	decl = a.readDecl(decl)
	if idx, ok := a.storeScanIndexCache[decl]; ok {
		return idx
	}
	idx := &storeScanDeclIndex{decls: map[string][]*ast.VarDeclStmt{}, otherBound: map[string]bool{}, nodes: map[string][]any{}, locals: map[string]bool{}, exprTokens: map[ast.Expr][]string{}, headerTokens: map[ast.Stmt][]string{}, bound: map[ast.Stmt][]string{}}
	// Each name's list is in walk order (one append per node), whatever the map order of names.
	addNode := func(node any, names map[string]bool) {
		for name := range names {
			idx.nodes[name] = append(idx.nodes[name], node)
		}
	}
	walkStoreScanNodes(decl.Body, func(node any) {
		idx.flat = append(idx.flat, node)
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				return
			}
			idx.locals[n.Name] = true
			idx.decls[n.Name] = append(idx.decls[n.Name], n)
			names := storeScanExprNames(n.Value)
			names[n.Name] = true
			addNode(n, names)
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			names := storeScanExprNames(n.Value)
			for name := range storeScanExprNames(n.Target) {
				names[name] = true
			}
			addNode(n, names)
		case *ast.CallExpr:
			if n == nil {
				return
			}
			names := map[string]bool{}
			if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil {
				for name := range storeScanExprNames(field.Object) {
					names[name] = true
				}
			}
			for _, arg := range n.Args {
				for name := range storeScanExprNames(arg) {
					names[name] = true
				}
			}
			addNode(n, names)
		case ast.Stmt:
			if n == nil {
				return
			}
			bound := stmtBoundNames(n)
			for _, name := range bound {
				idx.otherBound[name] = true
				idx.locals[name] = true
			}
			if len(bound) > 0 {
				addNode(n, storeScanHeaderNames(n))
			}
		}
	})
	if a.storeScanIndexCache == nil {
		a.storeScanIndexCache = map[*ast.FuncDecl]*storeScanDeclIndex{}
	}
	a.storeScanIndexCache[decl] = idx
	return idx
}

// storeScanExprNames: the identifier names mentioned in an expression (field selectors are not
// names; a dotted identifier also mentions its root).
func storeScanExprNames(expr ast.Expr) map[string]bool {
	names := map[string]bool{}
	if expr == nil {
		return names
	}
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
				names[id.Name] = true
				names[rootNameOfDotted(id.Name)] = true
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
	rec(reflect.ValueOf(expr))
	return names
}

// storeScanHeaderNames: names mentioned by a statement's own expressions, not its nested
// statements (the header stmtHeaderMentionsAny inspects).
func storeScanHeaderNames(stmt ast.Stmt) map[string]bool {
	names := map[string]bool{}
	v := reflect.ValueOf(stmt)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return names
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return names
	}
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
			if _, isStmt := v.Interface().(ast.Stmt); isStmt {
				return
			}
			if expr, isExpr := v.Interface().(ast.Expr); isExpr {
				for name := range storeScanExprNames(expr) {
					names[name] = true
				}
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
			if v.Type().Elem().Kind() == reflect.Interface || v.Type().Elem().Kind() == reflect.Pointer || v.Type().Elem().Kind() == reflect.Struct {
				for i := 0; i < v.Len(); i++ {
					rec(v.Index(i))
				}
			}
		}
	}
	for i := 0; i < v.NumField(); i++ {
		rec(v.Field(i))
	}
	return names
}

// storeScanHeaderTokens: every string stmtHeaderMentionsAny would test against its name set.
func storeScanHeaderTokens(stmt ast.Stmt) []string {
	var tokens []string
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
			if _, isStmt := v.Interface().(ast.Stmt); isStmt {
				return
			}
			if expr, isExpr := v.Interface().(ast.Expr); isExpr {
				tokens = append(tokens, storeScanMentionTokens(expr)...)
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
		case reflect.String:
			tokens = append(tokens, v.String())
		}
	}
	for i := 0; i < v.NumField(); i++ {
		rec(v.Field(i))
	}
	return tokens
}

// storeScanEachNode visits decl's body nodes in walkStoreScanNodes order from the cached flat
// list (one reflective walk per function instead of one per query).
func (a *Analyzer) storeScanEachNode(decl *ast.FuncDecl, visit func(node any)) {
	decl = a.readDecl(decl)
	for _, node := range a.storeScanIndex(decl).flat {
		visit(node)
	}
}

// boundNames: stmtBoundNames(stmt), memoized (fixpoints ask it on every iteration).
func (idx *storeScanDeclIndex) boundNames(stmt ast.Stmt) []string {
	if names, ok := idx.bound[stmt]; ok {
		return names
	}
	names := stmtBoundNames(stmt)
	idx.bound[stmt] = names
	return names
}

// bundles: storeScanBundles(expr), memoized per expression.
func (idx *storeScanDeclIndex) bundles(expr ast.Expr) bool {
	if idx.bundleCache == nil {
		idx.bundleCache = map[ast.Expr]bool{}
	}
	if found, ok := idx.bundleCache[expr]; ok {
		return found
	}
	found := storeScanBundles(expr)
	idx.bundleCache[expr] = found
	return found
}
