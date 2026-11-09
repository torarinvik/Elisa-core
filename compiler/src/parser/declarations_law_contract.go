package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseLayoutStructDecl() ast.Decl {
	return p.parseLayoutStructDeclWithAnnotations(nil)
}

// looksLikeLawDecl distinguishes a `law` declaration from an identifier named `law`. At decl
// position a law is `law <name> [generics] ( … )`, so require an identifier name followed by
// `(` or `[`.
func (p *Parser) looksLikeLawDecl() bool {
	if !p.hasToken(p.pos + 2) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokenAt(p.pos + 2).Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
		return true
	case lexer.TOKEN_IDENT:
		// Subject-free function-level law with no param list: effect law `law NoAlloc forbids ...`
		// (docs/85 §4) or composite law `law Leaf includes ...` (docs/85 §6).
		return p.tokenAt(p.pos+2).Text == "forbids" || p.tokenAt(p.pos+2).Text == "includes"
	default:
		return false
	}
}

// looksLikeLemmaDecl distinguishes a `lemma` declaration (`lemma Name(...)` / `lemma Name[...]`) from
// an identifier that merely happens to be spelled `lemma` (contextual keyword, like `law`).
func (p *Parser) looksLikeLemmaDecl() bool {
	if !p.hasToken(p.pos + 2) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokenAt(p.pos + 2).Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
		return true
	default:
		return false
	}
}

// parseLemmaDecl parses a ghost lemma (`lemma Name[generics](params) requires … ensure …:` body). It
// reuses the entire function grammar via parseFuncDeclRest and only sets IsLemma — the analyzer then
// verifies the body proves the ensures and erases both the decl and its call sites from codegen.
func (p *Parser) parseLemmaDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("lemma")
	fn := p.parseFuncDeclRest(pos, nil, false)
	if fn != nil {
		fn.IsLemma = true
	}
	return fn
}

func (p *Parser) looksLikeGhostFuncDecl() bool {
	return p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_DEF
}

func (p *Parser) parseGhostFuncDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("ghost")
	p.expect(lexer.TOKEN_DEF)
	fn := p.parseFuncDeclRest(pos, nil, false)
	fn.IsGhost = true
	return fn
}

// parseLawDecl parses a predicate law (docs/85 Stage 1): `law Name[generics](self: T, …) = <bool-expr>`.
// It is represented as a bool-returning FuncDecl with IsLaw set, so it reuses every bit of the
// function machinery (generics, modules, calls, type checking); the analyzer separately enforces
// purity, totality, and the bool return that make it a sound predicate.
func (p *Parser) parseLawDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("law")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()
	// An EFFECT law (docs/85 §4, Stage 4) is subject-free: `law NoAlloc forbids Memory.Allocate`. It
	// constrains the whole function, not a value or a place, so it takes no parameter list. Value and
	// frame laws keep their `(subject ...)` list.
	var params []ast.ParamDecl
	if p.match(lexer.TOKEN_LPAREN) {
		params, _ = p.parseExplicitSignatureParamList(true, false)
		p.expect(lexer.TOKEN_RPAREN)
	}
	if p.peekIdentText("forbids") {
		p.matchIdentText("forbids")
		var forbids []ast.PermissionRef
		for {
			forbids = append(forbids, p.parsePermissionRefGroup()...)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expectNewline()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			Forbids:          forbids,
			IsLaw:            true,
		}
	}
	// A COMPOSITE law (docs/85 §6): `law Leaf includes NoAlloc, NoBoundsChecks` — a function-level
	// law whose obligation is the union of its named members'. Members are bare law-name identifiers.
	if p.peekIdentText("includes") {
		p.matchIdentText("includes")
		var includes []string
		for {
			includes = append(includes, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expectNewline()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			Includes:         includes,
			IsLaw:            true,
		}
	}
	// A FRAME law (docs/88) carries a `changes`/`preserves` clause instead of an `= <bool-expr>`
	// body: `law MovesPlayerOnly(self: Render&) changes self.px, self.py`. It is a named, reusable
	// frame applied with `fulfills`; it has no predicate body.
	if p.peekIdentText("changes") || p.peekIdentText("preserves") {
		var changes, preserves []ast.EnsuresPath
		if p.matchIdentText("changes") {
			changes = p.parseChangesPathsAfterKeyword()
		}
		if p.matchIdentText("preserves") {
			preserves = p.parseChangesPathsAfterKeyword()
		}
		p.expectNewline()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			Changes:          changes,
			Preserves:        preserves,
			IsLaw:            true,
		}
	}
	// A BLOCK law (docs/90 brick 90-15): `law Name(...):` with an indented body of zero or more
	// `name = <expr>` sub-predicate bindings followed by a single `return <bool-expr>`. The bindings
	// are inlined into the return expression at parse time, so the law is represented exactly like the
	// `= <expr>` form (a single predicate) and every downstream tier (SMT, linear, flow) is unchanged.
	// Lets an author name the parts of a complex law (e.g. `sorted`, `firstMin`) instead of packing it
	// onto one line.
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		savedQuant := p.allowQuantifiers
		p.allowQuantifiers = true
		body := p.parseBlock()
		p.allowQuantifiers = savedQuant
		predicate, ok := desugarLawBlock(body)
		if !ok {
			p.errorf("a law block body must be zero or more `name = <expr>` bindings followed by a single `return <bool-expr>`")
			predicate = &ast.BoolLit{Position: pos, Value: false}
		}
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			ReturnType:       &ast.NamedType{Position: pos, Name: "bool"},
			Body:             []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: predicate}},
			IsLaw:            true,
		}
	}
	p.expect(lexer.TOKEN_ASSIGN)
	savedQuant := p.allowQuantifiers
	p.allowQuantifiers = true
	predicate := p.parseExpr()
	p.allowQuantifiers = savedQuant
	p.expectNewline()
	return &ast.FuncDecl{
		Position:         pos,
		Name:             name,
		TypeParams:       typeParams,
		RegionParams:     regionParams,
		PermissionParams: permissionParams,
		GenericParams:    genericParams,
		Params:           params,
		ReturnType:       &ast.NamedType{Position: pos, Name: "bool"},
		Body:             []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: predicate}},
		IsLaw:            true,
	}
}

// looksLikeRefineDecl distinguishes a `refine` named-refinement-alias declaration from an identifier
// merely spelled `refine` (contextual keyword, like `law`). At decl position a refine alias is
// `refine <name> [generics]? (params)? = <base> where <pred>`, so require an identifier name followed
// by `[`, `(`, or `=`.
func (p *Parser) looksLikeRefineDecl() bool {
	if !p.hasToken(p.pos + 2) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokenAt(p.pos + 2).Kind {
	case lexer.TOKEN_LBRACKET, lexer.TOKEN_LPAREN, lexer.TOKEN_ASSIGN:
		return true
	default:
		return false
	}
}

// parseRefineDecl parses a NAMED refinement alias (docs/85 "Level 2"):
//
//	refine Positive = i64 where self > 0
//	refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count
//
// It is pure desugaring sugar; the analyzer expands a binder-position use of `Name`/`Name[..](..)`
// into the equivalent anonymous `Base where PRED` (a WhereRefinementTypeExpr). No new type family.
func (p *Parser) parseRefineDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("refine")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, _, _, _ := p.parseFuncGenericParams()
	var params []ast.ParamDecl
	if p.match(lexer.TOKEN_LPAREN) {
		params, _ = p.parseExplicitSignatureParamList(false, false)
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_ASSIGN)
	savedQuant := p.allowQuantifiers
	p.allowQuantifiers = true
	// parseTypeExpr already folds a trailing `where <pred>` into a WhereRefinementTypeExpr, so the
	// `= Base where Pred` body is parsed in one shot; unwrap it back into the RefineDecl's Base +
	// Predicate fields. A base with no `where` suffix is a malformed refine alias.
	parsed := p.parseTypeExpr()
	p.allowQuantifiers = savedQuant
	wt, ok := parsed.(*ast.WhereRefinementTypeExpr)
	if !ok || wt == nil || wt.Predicate == nil {
		p.errorf("a `refine` alias must be `refine Name[..](..) = Base where <predicate>`")
		return &ast.RefineDecl{Position: pos, Name: name, TypeParams: typeParams, Params: params, Base: parsed}
	}
	base := wt.Base
	predicate := wt.Predicate
	p.expectNewline()
	return &ast.RefineDecl{
		Position:   pos,
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		Base:       base,
		Predicate:  predicate,
	}
}

// looksLikeContractDecl distinguishes a `contract Name(...)` declaration (docs/97) from an identifier
// merely spelled `contract`. At decl position a contract is `contract <name> [generics] ( … ):`, so
// require an identifier name followed by `(` or `[`.
func (p *Parser) looksLikeContractDecl() bool {
	if !p.hasToken(p.pos + 2) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokenAt(p.pos + 2).Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
		return true
	default:
		return false
	}
}

// parseContractDecl parses a named composable contract (docs/97):
//
//	contract Name[generics](params):
//	    requires <bool>
//	    ensure   <bool>
//	    changes  <path>, …
//	    preserves <path>, …
//
// It is represented as a bool-less FuncDecl with IsContract set, carrying the bundled value contracts
// in Requires/EnsureValues and the frame conditions in Changes/Preserves. The body is exactly a run
// of requires/ensure/changes/preserves clauses — no executable statements, no `decreases`.
func (p *Parser) parseContractDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("contract")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_LPAREN)
	params, _ := p.parseExplicitSignatureParamList(true, false)
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	var requires, ensures []ast.Expr
	var requireProofs, ensureProofs []*ast.ProofBlockStmt
	var changes, preserves []ast.EnsuresPath
	var includes []ast.ContractInclude
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peekIdentText("requires"):
			p.advance()
			saved := p.allowQuantifiers
			p.allowQuantifiers = true
			cond := p.parseExpr()
			p.allowQuantifiers = saved
			proof, scoped := p.parseOptionalContractScopedProof(cond)
			requires = append(requires, cond)
			if scoped {
				requireProofs = append(requireProofs, &ast.ProofBlockStmt{Position: cond.Pos(), Goal: cond, Proof: proof})
			} else {
				requireProofs = append(requireProofs, nil)
			}
		case p.peekIdentText("ensure"):
			p.advance()
			saved := p.allowQuantifiers
			p.allowQuantifiers = true
			cond := p.parseExpr()
			p.allowQuantifiers = saved
			proof, scoped := p.parseOptionalContractScopedProof(cond)
			ensures = append(ensures, cond)
			if scoped {
				ensureProofs = append(ensureProofs, &ast.ProofBlockStmt{Position: cond.Pos(), Goal: cond, Proof: proof})
			} else {
				ensureProofs = append(ensureProofs, nil)
			}
		case p.matchIdentText("changes"):
			changes = append(changes, p.parseChangesPathsAfterKeyword()...)
			p.expectNewline()
		case p.matchIdentText("preserves"):
			preserves = append(preserves, p.parseChangesPathsAfterKeyword()...)
			p.expectNewline()
		case p.peekIdentText("includes"):
			incPos := p.cur().Pos
			p.advance()
			law := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_LPAREN)
			var args []ast.Expr
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
			includes = append(includes, ast.ContractInclude{Position: incPos, Law: law, Args: args})
			p.expectNewline()
		case p.peekIdentText("decreases"):
			p.errorf("a `contract` body may not contain `decreases`: a termination measure is a property of a recursion, not a reusable contract (docs/97 §5)")
			p.advance()
			p.parseExpr()
			p.expectNewline()
		default:
			p.errorf("a `contract` body may only contain requires/ensure/changes/preserves/includes clauses, got %s", p.cur())
			for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
				p.advance()
			}
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.FuncDecl{
		Position:         pos,
		Name:             name,
		TypeParams:       typeParams,
		RegionParams:     regionParams,
		PermissionParams: permissionParams,
		GenericParams:    genericParams,
		Params:           params,
		Requires:         requires,
		RequiresProofs:   requireProofs,
		EnsureValues:     ensures,
		EnsureProofs:     ensureProofs,
		Changes:          changes,
		Preserves:        preserves,
		ContractIncludes: includes,
		IsContract:       true,
	}
}

// desugarLawBlock inlines a law block's `name = <expr>` bindings into its final `return <expr>`,
// yielding the single predicate expression the rest of the pipeline expects. Bindings are resolved in
// order (a later binding may reference an earlier one). Returns false if the block is not the
// expected shape (some non-binding/non-return statement, no trailing return, or a binding without a
// value). A binding name that shadows a quantifier binder inside an expression is left untouched there
// (the binder wins), so the inline is capture-safe.
func desugarLawBlock(stmts []ast.Stmt) (ast.Expr, bool) {
	subst := map[string]ast.Expr{}
	for i, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			// Only immutable, untyped/typed value bindings — a law is pure, no mutation.
			if n.Mutable || n.Value == nil || n.Name == "" || i == len(stmts)-1 {
				return nil, false
			}
			subst[n.Name] = substituteLawIdents(n.Value, subst, nil)
		case *ast.ReturnStmt:
			if n.Value == nil || i != len(stmts)-1 {
				return nil, false
			}
			return substituteLawIdents(n.Value, subst, nil), true
		default:
			return nil, false
		}
	}
	return nil, false
}

// substituteLawIdents replaces each free identifier named in `subst` with a parenthesized clone of
// its bound expression, over the expression fragment laws use. `bound` is the set of quantifier
// binders currently in scope; a name in `bound` is NOT substituted (the binder shadows the binding).
// An unrecognized node is returned unchanged — any surviving binding-name reference then surfaces as
// a normal undefined-identifier error downstream, never a silent miscompile.
func substituteLawIdents(expr ast.Expr, subst map[string]ast.Expr, bound map[string]bool) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		if bound != nil && bound[n.Name] {
			return n
		}
		if repl, ok := subst[n.Name]; ok {
			return &ast.ParenExpr{Position: n.Position, Inner: ast.CloneExpr(repl)}
		}
		return n
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: substituteLawIdents(n.Inner, subst, bound)}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: substituteLawIdents(n.Operand, subst, bound)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op,
			Left:  substituteLawIdents(n.Left, subst, bound),
			Right: substituteLawIdents(n.Right, subst, bound)}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: substituteLawIdents(n.Object, subst, bound), Field: n.Field}
	case *ast.IndexExpr:
		return &ast.IndexExpr{Position: n.Position,
			Object: substituteLawIdents(n.Object, subst, bound),
			Index:  substituteLawIdents(n.Index, subst, bound)}
	case *ast.CallExpr:
		out := *n
		out.Func = substituteLawIdents(n.Func, subst, bound)
		out.Args = make([]ast.Expr, len(n.Args))
		for i, arg := range n.Args {
			out.Args[i] = substituteLawIdents(arg, subst, bound)
		}
		return &out
	case *ast.QuantifierExpr:
		inner := bound
		if len(n.Vars) > 0 {
			inner = map[string]bool{}
			for k := range bound {
				inner[k] = true
			}
			for _, v := range n.Vars {
				inner[v] = true
			}
		}
		return &ast.QuantifierExpr{Position: n.Position, Exists: n.Exists, Vars: n.Vars,
			In:   substituteLawIdents(n.In, subst, bound),
			Body: substituteLawIdents(n.Body, subst, inner)}
	default:
		return n
	}
}
