package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// storeScanExprNames: the identifier names mentioned in an expression (field selectors are not
// names; a dotted identifier also mentions its root).
func storeScanExprNames(expr ast.Expr) map[string]bool {
	names := map[string]bool{}
	storeScanAddExprNames(expr, names)
	return names
}

func storeScanAddExprNames(expr ast.Expr, names map[string]bool) {
	if expr == nil {
		return
	}
	if name, root, known := storeScanSimpleMentionNames(expr); known {
		names[name] = true
		names[root] = true
		return
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
}

// storeScanHeaderNames: names mentioned by a statement's own expressions, not its nested
// statements (the header stmtHeaderMentionsAny inspects).
func storeScanHeaderNames(stmt ast.Stmt) map[string]bool {
	names := map[string]bool{}
	storeScanAddHeaderNames(stmt, names)
	return names
}

func storeScanAddHeaderNames(stmt ast.Stmt, names map[string]bool) {
	v := reflect.ValueOf(stmt)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
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
				storeScanAddExprNames(expr, names)
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
}

// storeScanHeaderTokens: every string stmtHeaderMentionsAny would test against its name set.
func storeScanHeaderTokens(stmt ast.Stmt) []string {
	return storeScanHeaderTokensKeyed(stmt, nil)
}

// storeScanHeaderTokensKeyed: storeScanHeaderTokens, spelled as storeScanMentionTokensKeyed.
func storeScanHeaderTokensKeyed(stmt ast.Stmt, sc *storeFlowKindScopes) []string {
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
				tokens = append(tokens, storeScanMentionTokensKeyed(expr, sc)...)
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
			if sc == nil || !sc.scoped[v.String()] {
				tokens = append(tokens, v.String())
			}
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

// Parameter names do not depend on a source/target pair. Keep one read-only
// set in the declaration index; pair-specific taint sets remain independent.
func (idx *storeScanDeclIndex) parameters(decl *ast.FuncDecl) map[string]bool {
	if idx.parameterNames == nil {
		idx.parameterNames = make(map[string]bool, len(decl.Params))
		for _, param := range decl.Params {
			idx.parameterNames[param.Name] = true
		}
	}
	return idx.parameterNames
}

func (idx *storeScanDeclIndex) payloadNodeList() []any {
	if !idx.payloadNodesReady {
		for _, node := range idx.flat {
			switch node.(type) {
			case *ast.AssignStmt, *ast.CallExpr:
				idx.payloadNodes = append(idx.payloadNodes, node)
			}
		}
		idx.payloadNodesReady = true
	}
	return idx.payloadNodes
}

func (idx *storeScanDeclIndex) eachPayloadNode(visit func(any)) {
	for _, node := range idx.payloadNodeList() {
		visit(node)
	}
}

// boundNames: stmtBoundNames(stmt), memoized (fixpoints ask it on every iteration).
func (idx *storeScanDeclIndex) boundNames(stmt ast.Stmt) []string {
	if names, ok := idx.bound[stmt]; ok {
		return names
	}
	names := stmtBoundNames(stmt)
	if idx.bound == nil {
		idx.bound = map[ast.Stmt][]string{}
	}
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

// Identifier counts are queried for many names and fixpoint iterations over the
// same syntax. Build all names in one walk, only when a count is first needed.
func (idx *storeScanDeclIndex) identCount(expr ast.Expr, name string) int {
	if expr == nil {
		return 0
	}
	counts, ok := idx.identCounts[expr]
	if !ok {
		counts = storeScanIdentCounts(expr)
		if idx.identCounts == nil {
			idx.identCounts = map[ast.Expr]map[string]int{}
		}
		idx.identCounts[expr] = counts
	}
	return counts[name]
}

func (idx *storeScanDeclIndex) bodyIdentCount(body []ast.Stmt, name string) int {
	if idx.bodyIdentCounts == nil {
		idx.bodyIdentCounts = storeScanIdentCounts(body)
	}
	return idx.bodyIdentCounts[name]
}
