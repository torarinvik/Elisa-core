package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Sentinel-index lint: a function that returns a signed integer and can return a
// negative "not found" sentinel (e.g. `-1`) is a footgun when its result is used as a
// container index without a guard — the negative becomes a huge value when cast to an
// unsigned index, reading far out of bounds (this is exactly the ge_find_native_region
// -> ge_native_regions[index.usize()] class of bug).
//
// The check is intentionally conservative to keep false positives low:
//   1. the index value must derive from a call to a *sentinel-returning* function
//      (signed return type + a negative integer literal somewhere in its body), and
//   2. that local must NEVER be compared anywhere in the function (if you compared it
//      at all — `idx >= 0`, `idx < cap`, `idx == -1`, ... — you almost certainly guarded
//      it, so we stay silent).
// A `get coll[i] else ...` (Fallback != nil) is already a checked index and is skipped.
//
// Emitted as a warning, not a hard error: it flags an unguarded-sentinel index for audit.

var sentinelSignedReturnTypeNames = map[string]bool{
	"int": true, "isize": true,
	"i8": true, "i16": true, "i32": true, "i64": true,
	// Project-defined signed aliases (e.g. core/common types) are matched by name so
	// the lint works without resolving the alias to its underlying builtin.
	"ssize": true, "s8": true, "s16": true, "s32": true, "s64": true,
}

var sentinelUnsignedCastTargetNames = map[string]bool{
	"usize": true, "uintptr": true, "u8": true, "u16": true, "u32": true, "u64": true,
}

// sentinelFuncNames returns (building once) the set of function names that return a
// signed integer and contain a negative integer literal — a proxy for "can return a
// negative not-found sentinel".
func (a *Analyzer) sentinelFuncNames() map[string]bool {
	if a == nil {
		return nil
	}
	if a.sentinelFuncNameCache != nil {
		return a.sentinelFuncNameCache
	}
	names := map[string]bool{}
	if a.globalScope != nil {
		for _, sym := range a.globalScope.Symbols {
			if sym == nil {
				continue
			}
			fd, ok := sym.Node.(*ast.FuncDecl)
			if !ok || fd == nil {
				continue
			}
			if !sentinelSignedReturnType(fd.ReturnType) {
				continue
			}
			if a.bodyHasNegativeIntLiteral(fd.Body) {
				names[fd.Name] = true
			}
		}
	}
	a.sentinelFuncNameCache = names
	return names
}

func sentinelSignedReturnType(te ast.TypeExpr) bool {
	named, ok := te.(*ast.NamedType)
	return ok && named != nil && sentinelSignedReturnTypeNames[named.Name]
}

func (a *Analyzer) bodyHasNegativeIntLiteral(body []ast.Stmt) bool {
	found := false
	a.walkStaticStmts(body, func(e ast.Expr) bool {
		if u, ok := e.(*ast.UnaryExpr); ok && u != nil && u.Op == lexer.TOKEN_MINUS {
			if _, isInt := unwrapParen(u.Operand).(*ast.IntLit); isInt {
				found = true
				return true // stop walking
			}
		}
		return false
	})
	return found
}

// checkSentinelIndex runs the lint over one function body. Hooked from analyzeFunc.
func (a *Analyzer) checkSentinelIndex(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	sentinels := a.sentinelFuncNames()
	if len(sentinels) == 0 {
		return
	}

	tainted := map[string]lexer.Pos{} // local name -> binding site, value came from a sentinel call
	a.collectSentinelTaints(fn.Body, sentinels, tainted)
	if len(tainted) == 0 {
		return
	}

	// A local that is compared anywhere in the function is treated as guarded.
	compared := map[string]bool{}
	a.walkStaticStmts(fn.Body, func(e ast.Expr) bool {
		if b, ok := e.(*ast.BinaryExpr); ok && b != nil && isComparisonOp(b.Op) {
			if name, ok := identUnderValue(b.Left); ok {
				compared[name] = true
			}
			if name, ok := identUnderValue(b.Right); ok {
				compared[name] = true
			}
		}
		return false
	})

	// Flag unguarded sentinel-derived index uses.
	a.walkStaticStmts(fn.Body, func(e ast.Expr) bool {
		idx, ok := e.(*ast.IndexExpr)
		if !ok || idx == nil || idx.Fallback != nil {
			return false
		}
		name, ok := identUnderValue(idx.Index)
		if !ok {
			return false
		}
		bindPos, isTainted := tainted[name]
		if !isTainted || compared[name] {
			return false
		}
		a.warnf(idx.Pos(), "index %q derives from a function that can return a negative not-found sentinel (declared at %s) and is never bounds-checked here; casting a negative sentinel to an unsigned index reads far out of bounds. Guard it (e.g. `if %s >= 0 ...`) or use `get coll[%s] else ...`", name, bindPos.String(), name, name)
		return false
	})
}

// collectSentinelTaints records locals bound from a direct call to a sentinel function.
// Statement-level walk (the binding name lives on the statement, not an expression).
func (a *Analyzer) collectSentinelTaints(stmts []ast.Stmt, sentinels map[string]bool, tainted map[string]lexer.Pos) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if n != nil && n.Name != "" && callTargetsSentinel(n.Value, sentinels) {
				tainted[n.Name] = n.Pos()
			}
		case *ast.AssignStmt:
			if n != nil && callTargetsSentinel(n.Value, sentinels) {
				if name, ok := identUnderValue(n.Target); ok {
					tainted[name] = n.Pos()
				}
			}
		case *ast.IfStmt:
			if n != nil {
				a.collectSentinelTaints(n.Then, sentinels, tainted)
				for _, elif := range n.Elifs {
					a.collectSentinelTaints(elif.Body, sentinels, tainted)
				}
				a.collectSentinelTaints(n.Else, sentinels, tainted)
			}
		case *ast.WhileStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		case *ast.ForStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		case *ast.IterForStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		case *ast.MatchStmt:
			if n != nil {
				for _, arm := range n.Arms {
					a.collectSentinelTaints(arm.Body, sentinels, tainted)
				}
			}
		case *ast.CanStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		case *ast.WithStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		case *ast.InStoreStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		case *ast.DeferStmt:
			if n != nil {
				a.collectSentinelTaints(n.Body, sentinels, tainted)
			}
		}
	}
}

// callTargetsSentinel reports whether expr is a direct call `f(...)` whose callee f is
// a sentinel-returning function (possibly wrapped in parens).
func callTargetsSentinel(expr ast.Expr, sentinels map[string]bool) bool {
	call, ok := unwrapParen(expr).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	ident, ok := unwrapParen(call.Func).(*ast.Ident)
	return ok && ident != nil && sentinels[ident.Name]
}

// identUnderValue returns the variable name read by expr when it is an identifier,
// optionally wrapped in parens or a single postfix cast to an unsigned type
// (`v`, `(v)`, `v.usize()`, `v.u32()`). Used for both index operands and comparison
// operands so `idx.u32() < cap` counts as comparing `idx`.
func identUnderValue(expr ast.Expr) (string, bool) {
	e := unwrapParen(expr)
	if cast, ok := e.(*ast.CastExpr); ok && cast != nil {
		if named, ok := cast.Target.(*ast.NamedType); ok && named != nil && sentinelUnsignedCastTargetNames[named.Name] {
			e = unwrapParen(cast.Operand)
		}
	}
	if ident, ok := e.(*ast.Ident); ok && ident != nil {
		return ident.Name, true
	}
	return "", false
}

func unwrapParen(expr ast.Expr) ast.Expr {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok || p == nil {
			return expr
		}
		expr = p.Inner
	}
}

func isComparisonOp(op lexer.TokenKind) bool {
	switch op {
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		return true
	}
	return false
}
