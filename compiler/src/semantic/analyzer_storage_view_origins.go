package semantic

import (
	"sort"

	"elisacore/src/ast"
)

// storageViewReturnOrigin names the storage a function's returned view points into: parameter
// Param (0 = receiver for a method call) followed by the field path Suffix (".buf").
type storageViewReturnOrigin struct {
	Param  int
	Suffix string
	// Into: the result points INTO the path's own storage (`bytes_view(p.buf)`, `&p.xs[i]`).
	// Otherwise the result is a copy of a view the path holds (`p.name`, `p.views[i]`) and
	// carries only the argument's existing view dependencies.
	Into bool
}

// storageViewReturnOriginSummary is a syntactic summary of every place a function's return values
// borrow from. Known=false means some return could not be traced; callers then record nothing
// (a decline) rather than guess a coarse dependency on every argument.
type storageViewReturnOriginSummary struct {
	Origins []storageViewReturnOrigin
	Known   bool
}

const storageViewOriginMaxDepth = 4

func (a *Analyzer) storageViewReturnOriginsFor(decl *ast.FuncDecl, depth int) *storageViewReturnOriginSummary {
	if decl == nil || depth > storageViewOriginMaxDepth {
		return &storageViewReturnOriginSummary{}
	}
	if a.storageViewReturnOrigins == nil {
		a.storageViewReturnOrigins = map[*ast.FuncDecl]*storageViewReturnOriginSummary{}
	}
	if cached, ok := a.storageViewReturnOrigins[decl]; ok {
		return cached
	}
	// In-progress marker: recursion through this function is untraceable.
	a.storageViewReturnOrigins[decl] = &storageViewReturnOriginSummary{}
	params := map[string]int{}
	for i, p := range decl.Params {
		params[p.Name] = i
	}
	locals := map[string]ast.Expr{}
	summary := &storageViewReturnOriginSummary{Known: true}
	seen := map[storageViewReturnOrigin]bool{}
	addAll := func(origins []storageViewReturnOrigin, known bool) {
		if !known {
			summary.Known = false
			return
		}
		for _, o := range origins {
			if !seen[o] {
				seen[o] = true
				summary.Origins = append(summary.Origins, o)
			}
		}
	}
	var walk func(body []ast.Stmt, tail bool)
	walk = func(body []ast.Stmt, tail bool) {
		for i, stmt := range body {
			last := tail && i == len(body)-1
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if _, dup := locals[s.Name]; dup || s.Value == nil {
					locals[s.Name] = nil // shadowed / uninitialised: untraceable
				} else {
					locals[s.Name] = s.Value
				}
			case *ast.ReturnStmt:
				if s.Value != nil {
					addAll(a.storageViewOriginsOfExpr(s.Value, params, locals, depth, 0, false))
				}
			case *ast.ExprStmt:
				if last && s.Expr != nil {
					addAll(a.storageViewOriginsOfExpr(s.Expr, params, locals, depth, 0, false))
				}
			case *ast.IfStmt:
				walk(s.Then, last)
				for _, e := range s.Elifs {
					walk(e.Body, last)
				}
				walk(s.Else, last)
			case *ast.WhileStmt:
				walk(s.Body, false)
			case *ast.ForStmt:
				walk(s.Body, false)
			case *ast.MatchStmt:
				// Pattern binders are untraceable aliases of the scrutinee.
				for _, arm := range s.Arms {
					walk(arm.Body, last)
				}
			default:
				for _, child := range returnBorrowChildBlocks(stmt) {
					walk(child, false)
				}
			}
		}
	}
	walk(decl.Body, true)
	a.storageViewReturnOrigins[decl] = summary
	return summary
}

// storageViewOriginsOfExpr traces a returned expression back to parameter field paths.
func (a *Analyzer) storageViewOriginsOfExpr(expr ast.Expr, params map[string]int, locals map[string]ast.Expr, depth, hops int, into bool) ([]storageViewReturnOrigin, bool) {
	if expr == nil || hops > 16 {
		return nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.storageViewOriginsOfExpr(n.Inner, params, locals, depth, hops+1, into)
	case *ast.AddrOfExpr:
		return a.storageViewOriginsOfExpr(n.Operand, params, locals, depth, hops+1, true)
	case *ast.MoveExpr:
		return a.storageViewOriginsOfExpr(n.Operand, params, locals, depth, hops+1, into)
	case *ast.CastExpr:
		return a.storageViewOriginsOfExpr(n.Operand, params, locals, depth, hops+1, into)
	case *ast.StringLit:
		return nil, true
	case *ast.Ident:
		if value, isLocal := locals[n.Name]; isLocal {
			if value == nil {
				return nil, false
			}
			return a.storageViewOriginsOfExpr(value, params, locals, depth, hops+1, into)
		}
		if index, isParam := params[n.Name]; isParam {
			return []storageViewReturnOrigin{{Param: index, Into: into}}, true
		}
		return nil, false
	case *ast.FieldExpr:
		base, ok := a.storageViewOriginsOfExpr(n.Object, params, locals, depth, hops+1, into)
		if !ok {
			return nil, false
		}
		out := make([]storageViewReturnOrigin, 0, len(base))
		for _, o := range base {
			out = append(out, storageViewReturnOrigin{Param: o.Param, Suffix: o.Suffix + "." + n.Field, Into: o.Into})
		}
		return out, true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, false
		}
		return a.storageViewOriginsOfExpr(n.Object, params, locals, depth, hops+1, into)
	case *ast.SliceExpr:
		return a.storageViewOriginsOfExpr(n.Object, params, locals, depth, hops+1, true)
	case *ast.CallExpr:
		switch callBaseName(n) {
		case "bytes_view", "bytes_view_range", "bytes_view_range_ref", "sview", "string_view_slice", "string_view_prefix", "string_view_suffix", "darray_view", "arena_da_view", "readonly", "arena_da_view_slice", "arena_da_view_prefix", "arena_da_view_suffix":
			if len(n.Args) == 0 {
				return nil, false
			}
			return a.storageViewOriginsOfExpr(n.Args[0], params, locals, depth, hops+1, true)
		}
		if field, isField := n.Func.(*ast.FieldExpr); isField && field != nil && field.Object != nil {
			switch field.Field {
			case "as_sview", "as_cstr", "view":
				return a.storageViewOriginsOfExpr(field.Object, params, locals, depth, hops+1, true)
			}
		}
		decls, args, ok := a.storageViewOriginCallee(n)
		if !ok {
			return nil, false
		}
		inner := a.storageViewReturnOriginsForAll(decls, depth+1)
		if !inner.Known {
			return nil, false
		}
		var out []storageViewReturnOrigin
		for _, o := range inner.Origins {
			if o.Param < 0 || o.Param >= len(args) {
				return nil, false
			}
			mapped, ok := a.storageViewOriginsOfExpr(args[o.Param], params, locals, depth, hops+1, o.Into)
			if !ok {
				return nil, false
			}
			for _, m := range mapped {
				out = append(out, storageViewReturnOrigin{Param: m.Param, Suffix: m.Suffix + o.Suffix, Into: m.Into})
			}
		}
		return out, true
	}
	return nil, false
}

// storageViewOriginCallee resolves a user call to its FuncDecl plus the argument list aligned
// with decl.Params (receiver first for a UFCS method call). Ambiguous overloads decline.
func (a *Analyzer) storageViewOriginCallee(call *ast.CallExpr) ([]*ast.FuncDecl, []ast.Expr, bool) {
	if call == nil {
		return nil, nil, false
	}
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	if field, isField := call.Func.(*ast.FieldExpr); isField && field != nil && field.Object != nil {
		full := append([]ast.Expr{field.Object}, args...)
		var decls []*ast.FuncDecl
		candidates := a.ufcsFunctionsByName[field.Field]
		switch {
		case len(candidates) > 1:
			return nil, nil, false
		case len(candidates) == 1:
			if candidates[0] == nil {
				return nil, nil, false
			}
			decl, ok := candidates[0].Node.(*ast.FuncDecl)
			if !ok || decl == nil {
				return nil, nil, false
			}
			decls = append(decls, decl)
		default:
			// A protocol/impl method: any impl defining it may be the target (static or
			// generic dispatch), so the summary is the union over all of them.
			keys := make([]string, 0, len(a.staticImpls))
			for key := range a.staticImpls {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			// The static form `Holder.name(h)` names the impl's receiver type and passes
			// the receiver as an ordinary argument.
			typeName := ""
			if ident, isIdent := field.Object.(*ast.Ident); isIdent && ident != nil && a.exprTypes[field.Object] == nil {
				typeName = ident.Name
			}
			collect := func(onlyType string) []*ast.FuncDecl {
				var out []*ast.FuncDecl
				for _, key := range keys {
					impl := a.staticImpls[key]
					if impl == nil || impl.Decl == nil {
						continue
					}
					if onlyType != "" && (impl.Receiver == nil || impl.Receiver.String() != onlyType) {
						continue
					}
					if decl := implMethodBodies(impl.Decl)[field.Field]; decl != nil {
						out = append(out, decl)
					}
				}
				return out
			}
			if typeName != "" {
				decls = collect(typeName)
			}
			if len(decls) == 0 {
				// A generic `T.name(x)` (T: Named) or a receiver value: any impl may run.
				decls = collect("")
			}
			if typeName != "" {
				full = args
			}
		}
		if len(decls) == 0 {
			return nil, nil, false
		}
		for _, decl := range decls {
			if len(full) != len(decl.Params) {
				return nil, nil, false
			}
		}
		return decls, full, true
	}
	if ident, isIdent := call.Func.(*ast.Ident); isIdent && ident != nil && len(a.ufcsFunctionsByName[ident.Name]) > 1 {
		return nil, nil, false
	}
	decl, ok := a.resolveCalleeFuncDecl(call)
	if !ok || decl == nil {
		if call.Func != nil {
			if spec, isSpec := call.Func.(*ast.SpecializeExpr); isSpec && spec != nil {
				if ident, isIdent := spec.Operand.(*ast.Ident); isIdent && ident != nil && a.globalScope != nil {
					if sym, found := a.globalScope.Lookup(ident.Name); found && sym != nil {
						decl, ok = sym.Node.(*ast.FuncDecl)
					}
				}
			}
		}
		if !ok || decl == nil {
			return nil, nil, false
		}
	}
	if len(args) != len(decl.Params) {
		return nil, nil, false
	}
	return []*ast.FuncDecl{decl}, args, true
}

// storageViewReturnOriginsForAll unions the summaries of every possible callee.
func (a *Analyzer) storageViewReturnOriginsForAll(decls []*ast.FuncDecl, depth int) *storageViewReturnOriginSummary {
	if len(decls) == 1 {
		return a.storageViewReturnOriginsFor(decls[0], depth)
	}
	out := &storageViewReturnOriginSummary{Known: true}
	seen := map[storageViewReturnOrigin]bool{}
	for _, decl := range decls {
		inner := a.storageViewReturnOriginsFor(decl, depth)
		if !inner.Known {
			return &storageViewReturnOriginSummary{}
		}
		for _, o := range inner.Origins {
			if !seen[o] {
				seen[o] = true
				out.Origins = append(out.Origins, o)
			}
		}
	}
	return out
}
