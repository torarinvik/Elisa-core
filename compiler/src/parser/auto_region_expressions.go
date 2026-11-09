package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

// exprBlockTailNeedsAutoRegion classifies a direct return value or an ExprBlock
// tail that needs an ambient owner. A comprehension or `each` query needs an
// active scope when it has no explicit owner. A plain top-level list literal
// remains region-polymorphic, but a list literal nested in a tuple or branch
// may need a region for its contextual container type. Struct literals with
// allocating container fields need an ambient owner as well.
func exprBlockTailNeedsAutoRegion(value ast.Expr) bool {
	var scan autoRegionScan
	return scan.exprBlockTailNeedsAutoRegion(value)
}

func (scan *autoRegionScan) exprBlockTailNeedsAutoRegion(value ast.Expr) bool {
	return scan.exprTailNeedsAutoRegion(value, false)
}

func exprTailNeedsAutoRegion(value ast.Expr, nested bool) bool {
	var scan autoRegionScan
	return scan.exprTailNeedsAutoRegion(value, nested)
}

func (scan *autoRegionScan) exprTailNeedsAutoRegion(value ast.Expr, nested bool) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch tail := value.(type) {
	case *ast.ListLitExpr:
		return nested && tail != nil && tail.Owner == nil
	case *ast.ListComprehensionExpr:
		return tail != nil && tail.Owner == nil
	case *ast.QueryExpr:
		return tail != nil && tail.Kind == ast.QueryExprEach && tail.Owner == nil
	case *ast.StructLitExpr:
		return scan.structLiteralAllocatesContainerArg(tail)
	case *ast.AllocExpr:
		return tail != nil && (tail.AutoRegion || tail.Owner == nil)
	case *ast.TupleExpr:
		if tail != nil {
			for _, elem := range tail.Elems {
				if scan.exprTailNeedsAutoRegion(elem, true) {
					return true
				}
			}
		}
	case *ast.TernaryExpr:
		return tail != nil && (scan.exprTailNeedsAutoRegion(tail.Value, true) || scan.exprTailNeedsAutoRegion(tail.Alt, true))
	case *ast.MatchExpr:
		if tail != nil {
			for _, arm := range tail.Arms {
				if scan.exprTailNeedsAutoRegion(matchArmValueTail(arm.Body), true) {
					return true
				}
			}
		}
	case *ast.ExprBlock:
		if tail != nil {
			return scan.exprBlockBodyNeedsAutoRegion(tail) || scan.exprTailNeedsAutoRegion(tail.Value, nested)
		}
	}
	return false
}

func matchArmValueTail(body []ast.Stmt) ast.Expr {
	if len(body) == 0 {
		return nil
	}
	stmt, ok := body[len(body)-1].(*ast.ExprStmt)
	if !ok || stmt == nil {
		return nil
	}
	return stmt.Expr
}

// structLiteralAllocatesContainerArg reports whether value is a struct construction
// (`Bag([], [])`) with an allocating container literal in one of its immediate arguments.
// The body-level reflection scan visits nested struct literals independently, avoiding a
// recursive rescan of the same constructor subtree for every enclosing literal.
func structLiteralAllocatesContainerArg(value ast.Expr) bool {
	var scan autoRegionScan
	return scan.structLiteralAllocatesContainerArg(value)
}

func (scan *autoRegionScan) structLiteralAllocatesContainerArg(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	lit, ok := value.(*ast.StructLitExpr)
	if !ok || lit == nil {
		return false
	}
	for _, arg := range lit.Args {
		if scan.isAllocatingLiteral(arg) {
			return true
		}
	}
	return false
}

// isAllocatingLiteral is a syntax-level approximation for whether a value is or
// contains a container literal or builder that may allocate. Tuple fields carry
// their element values, so a tuple used as a struct field can allocate through one
// of its elements.
func isAllocatingLiteral(value ast.Expr) bool {
	var scan autoRegionScan
	return scan.isAllocatingLiteral(value)
}

func (scan *autoRegionScan) isAllocatingLiteral(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch literal := value.(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr:
		return true
	case *ast.TupleExpr:
		if literal == nil {
			return false
		}
		for _, elem := range literal.Elems {
			if scan.isAllocatingLiteral(elem) {
				return true
			}
		}
	case *ast.TernaryExpr:
		return literal != nil && (scan.isAllocatingLiteral(literal.Value) || scan.isAllocatingLiteral(literal.Alt))
	case *ast.MatchExpr:
		if literal != nil {
			for _, arm := range literal.Arms {
				if scan.isAllocatingLiteral(matchArmValueTail(arm.Body)) {
					return true
				}
			}
		}
	case *ast.ExprBlock:
		return literal != nil && (scan.exprBlockBodyNeedsAutoRegion(literal) || scan.isAllocatingLiteral(literal.Value))
	}
	return isInferredDArrayBuilder(value)
}

// isInferredDArrayBuilder reports whether value is an expression that constructs
// a darray without needing a declared container type as context.
func isInferredDArrayBuilder(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch v := value.(type) {
	case *ast.ListComprehensionExpr:
		return true
	case *ast.QueryExpr:
		return v.Kind == ast.QueryExprEach
	}
	return false
}

func blockLaterUsesUntypedListLiteralAsDArray(stmts []ast.Stmt, name string) bool {
	if name == "" {
		return false
	}
	seenDecl := false
	for _, stmt := range stmts {
		if !seenDecl {
			decl, ok := stmt.(*ast.VarDeclStmt)
			if !ok || decl.Name != name {
				continue
			}
			seenDecl = decl.Type == nil && isListLiteralExpr(decl.Value)
			continue
		}
		if stmtUsesDArrayBuilderLocal(stmt, name) {
			return true
		}
		if decl, ok := stmt.(*ast.VarDeclStmt); ok && decl.Name == name {
			return false
		}
	}
	return false
}

func isListLiteralExpr(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	_, ok := value.(*ast.ListLitExpr)
	return ok
}

func stmtUsesDArrayBuilderLocal(stmt ast.Stmt, name string) bool {
	found := false
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		if found || expr == nil {
			return
		}
		switch e := expr.(type) {
		case *ast.CallExpr:
			if field, ok := e.Func.(*ast.FieldExpr); ok && field != nil {
				if ident, ok := field.Object.(*ast.Ident); ok && ident.Name == name {
					switch field.Field {
					case "push", "extend", "reserve", "resize", "clear", "truncate", "as_sview", "as_cstr":
						found = true
						return
					}
				}
			}
			visitExpr(e.Func)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
		case *ast.FieldExpr:
			if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == name {
				switch e.Field {
				case "count", "capacity", "items":
					found = true
					return
				}
			}
			visitExpr(e.Object)
		case *ast.IndexExpr:
			if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == name {
				found = true
				return
			}
			visitExpr(e.Object)
			visitExpr(e.Index)
		case *ast.BinaryExpr:
			visitExpr(e.Left)
			visitExpr(e.Right)
		case *ast.UnaryExpr:
			visitExpr(e.Operand)
		case *ast.ParenExpr:
			visitExpr(e.Inner)
		}
	}
	var visitStmt func(ast.Stmt)
	visitStmts := func(list []ast.Stmt) {
		for _, st := range list {
			if found {
				return
			}
			visitStmt(st)
		}
	}
	// Descend into nested control-flow BODIES, not just their headers: the canonical builder
	// shape fills the local inside a loop (`out = []; while ...: out.push(x)`). Without this
	// the auto-region wrap would miss a builder whose only use is loop-nested, leaving the
	// empty literal with no ambient region. Mirrors the semantic-side use-scan recursion.
	visitStmt = func(st ast.Stmt) {
		if found || st == nil {
			return
		}
		switch s := st.(type) {
		case *ast.ExprStmt:
			visitExpr(s.Expr)
		case *ast.ReturnStmt:
			visitExpr(s.Value)
		case *ast.VarDeclStmt:
			visitExpr(s.Value)
		case *ast.AssignStmt:
			visitExpr(s.Target)
			visitExpr(s.Value)
		case *ast.AugAssignStmt:
			visitExpr(s.Target)
			visitExpr(s.Value)
		case *ast.IfStmt:
			visitExpr(s.Cond)
			visitStmts(s.Then)
			for _, elif := range s.Elifs {
				visitExpr(elif.Cond)
				visitStmts(elif.Body)
			}
			visitStmts(s.Else)
		case *ast.ForStmt:
			visitExpr(s.Start)
			visitExpr(s.End)
			visitExpr(s.Step)
			visitStmts(s.Body)
		case *ast.IterForStmt:
			visitExpr(s.Source)
			visitExpr(s.Filter)
			visitStmts(s.Body)
		case *ast.WhileStmt:
			visitExpr(s.Cond)
			visitStmts(s.Body)
		case *ast.ScopeStmt:
			visitStmts(s.Body)
		case *ast.CanStmt:
			visitStmts(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				visitStmts(arm.Body)
			}
		}
	}
	visitStmt(stmt)
	return found
}

// isRegionlessContainerType reports whether a declared type is a growable container
// builtin with no `@r` region annotation — the signal that it will allocate and needs
// an ambient region.
func isRegionlessContainerType(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	// `dstr` is an owned string (the u8 specialization of darray, docs/26) but parses
	// as a NamedType, not a BuiltinTypeExpr — so a region-less `out: dstr = []` builder
	// must be recognized here too, or it never gets its inferred region (the auto-wrap
	// is what lets a string builder `push` bytes without an explicit `in <arena>:`).
	if nt, ok := typ.(*ast.NamedType); ok {
		return nt.Name == "dstr"
	}
	bt, ok := typ.(*ast.BuiltinTypeExpr)
	if !ok {
		return false
	}
	switch bt.Name {
	case "darray", "dict", "set", "dstr":
		return bt.Region == ""
	}
	return false
}

// desugarDStrStringLiteralInit rewrites `s: dstr = "abc"` into `s: dstr = [97, 98, 99]` — the
// byte-list literal a user would otherwise hand-write (`['a'.u8(), ...]`). `dstr` is the u8
// specialization of darray, so a bare string literal (otherwise a static `cstr`) does not assign to
// it. Desugaring at parse time — before functionBodyNeedsAutoRegion runs — means the result is an
// ordinary allocating list literal: it triggers the inferred auto-region wrap and flows through the
// identical growable-darray path (region backing, escape checks, codegen), adding no new surface.
//
// StringLit.Value is the already-decoded raw byte sequence (escapes resolved by the lexer; the
// backend emits these bytes verbatim), so each byte becomes a suffix-less IntLit whose type is
// driven to u8 by the darray[u8] list-literal context (no discouraged-suffix warning). An empty
// string yields an empty `[]`. The value is only rewritten when the declared type is a region-less
// `dstr`; any other type (including a `cstr`/view) is left untouched.
func desugarDStrStringLiteralInit(typ ast.TypeExpr, value ast.Expr) ast.Expr {
	if value == nil || !typeExprIsRegionlessDStr(typ) {
		return value
	}
	lit, ok := value.(*ast.StringLit)
	if !ok {
		return value
	}
	elems := make([]ast.Expr, 0, len(lit.Value))
	for i := 0; i < len(lit.Value); i++ {
		elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
	}
	return &ast.ListLitExpr{Position: lit.Position, Elems: elems}
}

// desugarDStrReturnLiterals rewrites every `return "..."` in a function body whose declared return
// type is a region-less `dstr` into the equivalent byte-list literal (`return [97, ...]`). Like the
// var-decl desugar, this must run at parse time: the region-polymorphic-return classification is a
// syntactic pre-pass that keys on a directly-returned list literal, so the StringLit must already be
// a list literal before it runs. Recurses through statement-level control flow (returns anywhere in
// the body still return from this function) but not into expressions, so a nested lambda's own
// returns — governed by a different return type — are untouched.
func desugarDStrReturnLiterals(body []ast.Stmt, retType ast.TypeExpr) {
	if !typeExprIsRegionlessDStr(retType) {
		return
	}
	rewriteDStrReturnsIn(body)
}

func rewriteDStrReturnsIn(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if lit, ok := s.Value.(*ast.StringLit); ok {
				elems := make([]ast.Expr, 0, len(lit.Value))
				for i := 0; i < len(lit.Value); i++ {
					elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
				}
				s.Value = &ast.ListLitExpr{Position: lit.Position, Elems: elems}
			}
		case *ast.CanStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.RegionStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.InStoreStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.ScopeStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.IfStmt:
			rewriteDStrReturnsIn(s.Then)
			rewriteDStrReturnsIn(s.Else)
		case *ast.ForStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.WhileStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				rewriteDStrReturnsIn(arm.Body)
			}
		}
	}
}

// typeExprIsRegionlessDStr reports whether typ is the region-less owned `dstr` string (a NamedType,
// optionally behind `mutable`). cstr/views and annotated `dstr @r` are excluded.
func typeExprIsRegionlessDStr(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	nt, ok := typ.(*ast.NamedType)
	return ok && nt.Name == "dstr"
}

func (p *Parser) parseCanStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("can")
	permissions := p.parsePermissionRefs(false)
	var handlerName string
	var handlerArgs []ast.Expr
	if p.match(lexer.TOKEN_WITH) {
		handlerName = p.expect(lexer.TOKEN_IDENT).Text
		// A zero-capture handler may be installed as `with Noop` as well as
		// `with Noop()`. Keeping both forms preserves the concise static-handler
		// spelling without changing the AST or lowering strategy.
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					handlerArgs = append(handlerArgs, p.parseExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
	}
	var asTarget string
	if p.match(lexer.TOKEN_AS) {
		asTarget = p.expect(lexer.TOKEN_IDENT).Text
		for p.match(lexer.TOKEN_DOT) {
			asTarget += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body, As: asTarget, HandlerName: handlerName, HandlerArgs: handlerArgs}
}
func (p *Parser) parseTrustedStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("trusted")
	permissions := p.parsePermissionRefs(false)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body, SuppressPermissionInference: true}
}
func (p *Parser) parseWithArenaStmt() *ast.RegionStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WITH)
	p.expectIdentText("arena")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var capacity ast.Expr
	if p.match(lexer.TOKEN_LPAREN) {
		capacity = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_AS)
	ownerName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity, OwnerName: ownerName, Body: body}
}

// parseCascadeStmt recognizes the removed `cascade <target>:` statement and rejects it. The cascade
// feature (method-cascade sugar) has been removed; the target and body are consumed for recovery.
func (p *Parser) parseCascadeStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("cascade")
	p.errorAt(pos, "the `cascade` statement has been removed")
	_ = p.parseExpr()
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		_ = p.parseBlock()
	}
	return &ast.PassStmt{Position: pos}
}
