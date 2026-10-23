package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
)

// storeFlowKindScopes gives the kinds pass one key per lexical binding instead of one per name:
// a body that rebinds `name` in thirty sibling blocks has thirty classes, not one class every
// binding of `name` joins. A name is scoped only when the walk can resolve every use of it to
// exactly one binding: no binding shadows another, no binder of it lives in an expression
// (comprehension, match-expression arm), no statement binds it for its following siblings, and
// no binder is also mentioned by its own statement's header. Every other name, and every
// parameter, keeps its plain name, which is the old (name-unioned) behaviour.
type storeFlowKindScopes struct {
	use    map[*ast.Ident]string
	bind   map[any]map[string]string // binding node -> bound name -> key
	keys   []string
	scoped map[string]bool       // names with at least one key
	first  map[string]*ast.Ident // a use of each key (its type is the binding's)
	// armKeys: per match statement, each bound name's keys across its arms.
	armKeys map[ast.Stmt]map[string][]string
	// opaque: names an expression the walk does not model may bind (`r` in `r if a is r else
	// b`), each with the expressions that hold it. Such a binder may hold anything they mention.
	opaque map[string][]ast.Expr
	// sources: what an expression-level binder (a comprehension's, a match-expression arm's)
	// may hold, keyed by the binder's key. Statement binders are applied with their statement.
	sources map[string]ast.Expr
}

var storeFlowStmtListType = reflect.TypeOf([]ast.Stmt(nil))

// binder: an identifier standing for key's binding (a use of it, else a bare one of its name).
func (sc *storeFlowKindScopes) binder(key string) *ast.Ident {
	if id := sc.first[key]; id != nil {
		return id
	}
	return &ast.Ident{Name: storeFlowBaseName(key)}
}

// key: the key of an identifier use.
func (sc *storeFlowKindScopes) key(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if k, ok := sc.use[id]; ok {
		return k
	}
	return id.Name
}

// bound: the key a binding node gives name.
func (sc *storeFlowKindScopes) bound(node any, name string) string {
	if k, ok := sc.bind[node][name]; ok {
		return k
	}
	return name
}

// boundAll: every key stmt gives name (a match statement scopes each arm's binders apart).
func (sc *storeFlowKindScopes) boundAll(stmt ast.Stmt, name string) []string {
	if keys, ok := sc.armKeys[stmt][name]; ok {
		return keys
	}
	return []string{sc.bound(stmt, name)}
}

func (a *Analyzer) storeFlowKindScopesFor(decl *ast.FuncDecl, idx *storeScanDeclIndex) *storeFlowKindScopes {
	if idx.kindScopes != nil {
		return idx.kindScopes
	}
	excluded := map[string]bool{}
	for _, p := range decl.Params {
		excluded[p.Name] = true
	}
	var sc *storeFlowKindScopes
	for round := 0; round < 8; round++ {
		var bad map[string]bool
		sc, bad = storeFlowResolveKindScopes(decl.Body, excluded)
		grew := false
		for name := range bad {
			if !excluded[name] {
				excluded[name] = true
				grew = true
			}
		}
		if !grew {
			break
		}
		if round == 7 {
			sc = &storeFlowKindScopes{}
		}
	}
	// A parameter is never bound by an expression.
	for _, p := range decl.Params {
		delete(sc.opaque, p.Name)
	}
	idx.kindScopes = sc
	return sc
}

// storeFlowResolveKindScopes resolves body's names, skipping excluded ones; bad lists the names
// the walk found it cannot scope (the caller excludes them and resolves again).
func storeFlowResolveKindScopes(body []ast.Stmt, excluded map[string]bool) (*storeFlowKindScopes, map[string]bool) {
	sc := &storeFlowKindScopes{use: map[*ast.Ident]string{}, bind: map[any]map[string]string{}, sources: map[string]ast.Expr{}, scoped: map[string]bool{}, first: map[string]*ast.Ident{}, armKeys: map[ast.Stmt]map[string][]string{}, opaque: map[string][]ast.Expr{}}
	bad := map[string]bool{}
	bindings := map[string]bool{} // names some binding scopes (an unresolved use of one is bad)
	seen := map[uintptr]bool{}
	type env = map[string]string
	extend := func(e env, node any, names []string) env {
		var out env
		for _, name := range names {
			if name == "" || excluded[name] {
				continue
			}
			if _, shadows := e[name]; shadows {
				bad[name] = true
				continue
			}
			if out == nil {
				out = make(env, len(e)+len(names))
				for k, v := range e {
					out[k] = v
				}
			}
			key := fmt.Sprintf("%s#%x", name, reflect.ValueOf(node).Pointer())
			out[name] = key
			if sc.bind[node] == nil {
				sc.bind[node] = map[string]string{}
			}
			sc.bind[node][name] = key
			sc.keys = append(sc.keys, key)
			sc.scoped[name] = true
			bindings[name] = true
		}
		if out == nil {
			return e
		}
		return out
	}
	var unresolved []*ast.Ident
	var owner ast.Expr           // the innermost expression being walked
	var owners []ast.Expr        // every expression being walked, outermost first
	escapes := map[string]bool{} // unresolved names used outside the expressions that bind them
	unresolvedOwners := map[*ast.Ident][]ast.Expr{}
	var walk func(v reflect.Value, e env, inExpr bool, field string)
	var walkStmts func(list []ast.Stmt, e env)
	var walkStmt func(stmt ast.Stmt, e env) env
	walkStmts = func(list []ast.Stmt, e env) {
		for _, stmt := range list {
			e = walkStmt(stmt, e)
		}
	}
	walkFields := func(v reflect.Value, e env, inExpr bool) {
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return
		}
		rfs := reflectFields(v.Type())
		for i := 0; i < v.NumField(); i++ {
			walk(v.Field(i), e, inExpr, rfs[i].Name)
		}
	}
	walkStmt = func(stmt ast.Stmt, e env) env {
		v := reflect.ValueOf(stmt)
		if stmt == nil || (v.Kind() == reflect.Pointer && v.IsNil()) {
			return e
		}
		if v.Kind() == reflect.Pointer {
			if seen[v.Pointer()] {
				return e
			}
			seen[v.Pointer()] = true
		}
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			// The value is evaluated before the name is bound; the name scopes the rest of the block.
			walkFields(v, e, false)
			return extend(e, s, []string{s.Name})
		case *ast.MatchStmt:
			walk(reflect.ValueOf(&s.Value).Elem(), e, true, "")
			walk(reflect.ValueOf(&s.Store).Elem(), e, true, "")
			header := storeScanExprNames(s.Value)
			for name := range storeScanExprNames(s.Store) {
				header[name] = true
			}
			for i := range s.Arms {
				arm := &s.Arms[i]
				names := stmtBoundNames(&ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: arm.Pattern}}})
				for _, name := range names {
					if header[name] {
						bad[name] = true
					}
				}
				inner := extend(e, arm, names)
				for name, key := range sc.bind[arm] {
					if sc.armKeys[s] == nil {
						sc.armKeys[s] = map[string][]string{}
					}
					sc.armKeys[s][name] = append(sc.armKeys[s][name], key)
				}
				walk(reflect.ValueOf(&arm.Pattern).Elem(), inner, false, "")
				walk(reflect.ValueOf(&arm.Guard).Elem(), inner, true, "")
				walkStmts(arm.Body, inner)
			}
			return e
		case *ast.TupleBindStmt:
			for _, name := range s.Names {
				bad[name.Name] = true
			}
			walkFields(v, e, false)
			return e
		}
		names := stmtBoundNames(stmt)
		if len(names) == 0 {
			walkFields(v, e, false)
			return e
		}
		// A binder its own header mentions (`for name in name.items`) is one the walk cannot
		// place on the right side of the binding; a statement with no nested block binds for
		// its following siblings.
		header := storeScanHeaderNames(stmt)
		nested := false
		walkStmtLists(v, func([]ast.Stmt) { nested = true })
		for _, name := range names {
			if header[name] || !nested {
				bad[name] = true
			}
		}
		walkFields(v, extend(e, stmt, names), false)
		return e
	}
	walk = func(v reflect.Value, e env, inExpr bool, field string) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), e, inExpr, field)
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.Ident:
				if n == nil || seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
				if root := rootNameOfDotted(n.Name); root != n.Name {
					bad[root] = true
					return
				}
				if k, ok := e[n.Name]; ok {
					sc.use[n] = k
					if sc.first[k] == nil {
						sc.first[k] = n
					}
				} else {
					unresolved = append(unresolved, n)
					unresolvedOwners[n] = append([]ast.Expr(nil), owners...)
				}
				return
			case ast.Stmt:
				walkStmt(n, e)
				return
			case *ast.ListComprehensionExpr:
				if n.LoweredParallel != nil || seen[v.Pointer()] {
					break
				}
				seen[v.Pointer()] = true
				heads := []ast.Expr{n.Source, n.RangeEnd, n.RangeStep, n.Owner}
				binders := []string{n.Name, n.SecondName}
				for _, head := range heads {
					walk(reflect.ValueOf(&head).Elem(), e, true, "")
					for name := range storeScanExprNames(head) {
						for _, b := range binders {
							if b == name {
								bad[b] = true
							}
						}
					}
				}
				inner := extend(e, n, binders)
				for _, b := range binders {
					if key, ok := sc.bind[n][b]; ok {
						sc.sources[key] = n.Source
					}
				}
				for _, binding := range n.Bindings {
					inner = walkStmt(binding, inner)
				}
				for _, part := range []ast.Expr{n.Key, n.Value, n.Filter} {
					walk(reflect.ValueOf(&part).Elem(), inner, true, "")
				}
				return
			case *ast.MatchExpr:
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
				walk(reflect.ValueOf(&n.Value).Elem(), e, true, "")
				walk(reflect.ValueOf(&n.Store).Elem(), e, true, "")
				for i := range n.Arms {
					arm := &n.Arms[i]
					names := stmtBoundNames(&ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: arm.Pattern}}})
					header := storeScanExprNames(n.Value)
					for _, name := range names {
						if header[name] {
							bad[name] = true
						}
					}
					inner := extend(e, arm, names)
					for _, name := range names {
						if key, ok := sc.bind[arm][name]; ok {
							sc.sources[key] = n.Value
						}
					}
					walk(reflect.ValueOf(&arm.Pattern).Elem(), inner, false, "")
					walk(reflect.ValueOf(&arm.Guard).Elem(), inner, true, "")
					walkStmts(arm.Body, inner)
				}
				return
			}
			switch n := v.Interface().(type) {
			case ast.Expr:
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
				saved := owner
				owner = n
				owners = append(owners, n)
				walkFields(v, e, true)
				owners = owners[:len(owners)-1]
				owner = saved
				return
			}
			if seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem(), e, inExpr, field)
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			rfs := reflectFields(v.Type())
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), e, inExpr, rfs[i].Name)
			}
		case reflect.Slice, reflect.Array:
			if v.Type() == storeFlowStmtListType {
				walkStmts(v.Interface().([]ast.Stmt), e)
				return
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), e, inExpr, field)
			}
		case reflect.String:
			// A string inside an expression that is not a use or a call / literal argument label
			// may be a binder of that expression: its name is not scoped.
			if inExpr && !storeScanQualifierFields[field] && field != "ArgNames" && field != "Captures" {
				if s := v.String(); s != "" {
					bad[s] = true
					if owner != nil {
						sc.opaque[s] = append(sc.opaque[s], owner)
					}
				}
			}
		}
	}
	walkStmts(body, env{})
	// An opaque binder read only inside an expression that binds it flows out through that
	// expression's own value; one read elsewhere needs a class of its own.
	for _, id := range unresolved {
		held := sc.opaque[id.Name]
		if len(held) == 0 {
			continue
		}
		inside := false
		for _, o := range unresolvedOwners[id] {
			for _, h := range held {
				inside = inside || o == h
			}
		}
		if !inside {
			escapes[id.Name] = true
		}
	}
	for name := range sc.opaque {
		if !escapes[name] {
			delete(sc.opaque, name)
		}
	}
	for _, id := range unresolved {
		if bindings[id.Name] {
			bad[id.Name] = true
		}
	}
	return sc, bad
}

// walkStmtLists calls f on each statement list directly inside stmt (its own blocks, arm bodies).
func walkStmtLists(v reflect.Value, f func([]ast.Stmt)) {
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	var rec func(v reflect.Value, depth int)
	rec = func(v reflect.Value, depth int) {
		if !v.IsValid() || depth > 4 {
			return
		}
		switch v.Kind() {
		case reflect.Slice:
			if v.Type() == storeFlowStmtListType {
				f(v.Interface().([]ast.Stmt))
				return
			}
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), depth+1)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i), depth+1)
			}
		}
	}
	for i := 0; i < v.NumField(); i++ {
		rec(v.Field(i), 0)
	}
}
