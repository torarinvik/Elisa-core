package parser

import (
	"fmt"
	"llcontext/ast"
	"llcontext/lexer"
)

type Parser struct {
	tokens []lexer.Token
	pos    int
	errors []string
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens}
}

func (p *Parser) Errors() []string { return p.errors }

func (p *Parser) cur() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Kind: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peek() lexer.TokenKind {
	return p.cur().Kind
}

func (p *Parser) advance() lexer.Token {
	tok := p.cur()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) expect(kind lexer.TokenKind) lexer.Token {
	tok := p.cur()
	if tok.Kind != kind {
		p.errorf("expected %s, got %s", lexer.TokenName(kind), tok)
	}
	return p.advance()
}

func (p *Parser) match(kind lexer.TokenKind) bool {
	if p.peek() == kind {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) errorf(format string, args ...interface{}) {
	pos := p.cur().Pos
	msg := fmt.Sprintf("%s:%d:%d: %s", pos.File, pos.Line, pos.Col, fmt.Sprintf(format, args...))
	p.errors = append(p.errors, msg)
}

func (p *Parser) skipNewlines() {
	for p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	}
}

// ---------- Top-level parsing ----------

func (p *Parser) ParseFile(filename string) *ast.File {
	file := &ast.File{Filename: filename}
	p.skipNewlines()
	for p.peek() != lexer.TOKEN_EOF {
		decl := p.parseDecl()
		if decl != nil {
			file.Decls = append(file.Decls, decl)
		}
		p.skipNewlines()
	}
	return file
}

func (p *Parser) parseDecl() ast.Decl {
	switch p.peek() {
	case lexer.TOKEN_CONST:
		return p.parseConstDecl()
	case lexer.TOKEN_GLOBAL:
		return p.parseGlobalDecl()
	case lexer.TOKEN_REPR:
		return p.parseStructDecl()
	case lexer.TOKEN_STRUCT:
		return p.parseStructDecl()
	case lexer.TOKEN_DEF:
		return p.parseFuncDecl()
	case lexer.TOKEN_EXTERN:
		return p.parseExternDecl()
	case lexer.TOKEN_STATIC:
		return p.parseStaticIfDecl()
	default:
		p.errorf("unexpected token %s at top level", p.cur())
		p.advance()
		return nil
	}
}

func (p *Parser) parseConstDecl() *ast.ConstDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_COLON) {
		typ = p.parseTypeExpr()
	}

	p.expect(lexer.TOKEN_ASSIGN)
	value := p.parseExpr()
	p.expectNewline()

	return &ast.ConstDecl{Position: pos, Name: name, Type: typ, Value: value}
}

func (p *Parser) parseGlobalDecl() *ast.GlobalDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_GLOBAL)
	mutable := p.match(lexer.TOKEN_MUTABLE)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	typ := p.parseTypeExpr()

	var value ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		value = p.parseExpr()
	}
	p.expectNewline()

	return &ast.GlobalDecl{Position: pos, Mutable: mutable, Name: name, Type: typ, Value: value}
}

func (p *Parser) parseStructDecl() *ast.StructDecl {
	pos := p.cur().Pos
	reprC := false
	if p.peek() == lexer.TOKEN_REPR {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		p.expect(lexer.TOKEN_IDENT) // "c"
		p.expect(lexer.TOKEN_RPAREN)
		reprC = true
	}
	p.expect(lexer.TOKEN_STRUCT)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typeParams []string
	if p.match(lexer.TOKEN_LBRACKET) {
		for {
			typeParams = append(typeParams, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACKET)
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var fields []ast.FieldDecl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.StructDecl{Position: pos, Name: name, TypeParams: typeParams, ReprC: reprC, Fields: fields}
}

func (p *Parser) parseFieldDecl() ast.FieldDecl {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)

	mutable := false
	isTail := false

	if p.match(lexer.TOKEN_MUTABLE) {
		mutable = true
	}
	if p.match(lexer.TOKEN_TAIL) {
		isTail = true
	}

	typ := p.parseTypeExpr()
	p.expectNewline()

	return ast.FieldDecl{Position: pos, Name: name, Mutable: mutable, IsTail: isTail, Type: typ}
}

func (p *Parser) parseFuncDecl() *ast.FuncDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DEF)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typeParams []string
	if p.match(lexer.TOKEN_LBRACKET) {
		for {
			typeParams = append(typeParams, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACKET)
	}

	p.expect(lexer.TOKEN_LPAREN)
	params := p.parseParamList()
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	body := p.parseBlock()
	return &ast.FuncDecl{Position: pos, Name: name, TypeParams: typeParams, Params: params, ReturnType: retType, Body: body}
}

func (p *Parser) parseParamList() []ast.ParamDecl {
	var params []ast.ParamDecl
	if p.peek() == lexer.TOKEN_RPAREN {
		return params
	}
	for {
		params = append(params, p.parseParam())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return params
}

func (p *Parser) parseParam() ast.ParamDecl {
	pos := p.cur().Pos
	mutable := false
	if p.match(lexer.TOKEN_MUTABLE) {
		mutable = true
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	typ := p.parseTypeExpr()
	return ast.ParamDecl{Position: pos, Name: name, Mutable: mutable, Type: typ}
}

func (p *Parser) parseExternDecl() ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_EXTERN)
	name := p.expect(lexer.TOKEN_IDENT).Text

	// extern TypeName  (opaque type - no parens, no colon)
	if p.peek() == lexer.TOKEN_NEWLINE || p.peek() == lexer.TOKEN_EOF {
		p.expectNewline()
		return &ast.ExternTypeDecl{Position: pos, Name: name}
	}

	// extern name: Type  (variable)
	if p.peek() == lexer.TOKEN_COLON {
		p.advance()
		typ := p.parseTypeExpr()
		p.expectNewline()
		return &ast.ExternVarDecl{Position: pos, Name: name, Type: typ}
	}

	// extern name(params...) [-> RetType]  (function)
	p.expect(lexer.TOKEN_LPAREN)
	var params []ast.ParamDecl
	variadic := false
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			if p.peek() == lexer.TOKEN_ELLIPSIS {
				p.advance()
				variadic = true
				break
			}
			params = append(params, p.parseParam())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	p.expectNewline()

	return &ast.ExternFuncDecl{Position: pos, Name: name, Params: params, ReturnType: retType, Variadic: variadic}
}

func (p *Parser) parseStaticIfDecl() *ast.StaticIfDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	thenBlock := p.parseDeclBlock()

	var elifs []ast.StaticElifDecl
	var elseBlock []ast.Decl

	for p.skipNewlines(); p.peek() == lexer.TOKEN_STATIC; p.skipNewlines() {
		saved := p.pos
		p.advance()
		if p.peek() == lexer.TOKEN_ELIF {
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseDeclBlock()
			elifs = append(elifs, ast.StaticElifDecl{Position: p.tokens[saved].Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseDeclBlock()
			break
		} else {
			p.pos = saved
			break
		}
	}

	return &ast.StaticIfDecl{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseDeclBlock() []ast.Decl {
	p.expect(lexer.TOKEN_INDENT)
	var decls []ast.Decl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		decl := p.parseDecl()
		if decl != nil {
			decls = append(decls, decl)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return decls
}

// ---------- Block / Statements ----------

func (p *Parser) parseBlock() []ast.Stmt {
	p.expect(lexer.TOKEN_INDENT)
	var stmts []ast.Stmt
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		stmt := p.parseStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return stmts
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		return p.parseReturn()
	case lexer.TOKEN_PASS:
		return p.parsePass()
	case lexer.TOKEN_PANIC:
		return p.parsePanic()
	case lexer.TOKEN_IF:
		return p.parseIf()
	case lexer.TOKEN_WHILE:
		return p.parseWhile()
	case lexer.TOKEN_STATIC:
		return p.parseStaticStmt()
	default:
		return p.parseExprOrAssignStmt()
	}
}

func (p *Parser) parseReturn() *ast.ReturnStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_RETURN)
	var value ast.Expr
	if p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		value = p.parseExpr()
	}
	p.expectNewline()
	return &ast.ReturnStmt{Position: pos, Value: value}
}

func (p *Parser) parsePass() *ast.PassStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PASS)
	p.expectNewline()
	return &ast.PassStmt{Position: pos}
}

func (p *Parser) parsePanic() *ast.PanicStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PANIC)
	p.expect(lexer.TOKEN_LPAREN)
	msg := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	p.expectNewline()
	return &ast.PanicStmt{Position: pos, Message: msg}
}

func (p *Parser) parseIf() *ast.IfStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	thenBlock := p.parseBlock()

	var elifs []ast.ElifClause
	var elseBlock []ast.Stmt

	for p.peek() == lexer.TOKEN_ELIF {
		elifPos := p.cur().Pos
		p.advance()
		elifCond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		elifBody := p.parseBlock()
		elifs = append(elifs, ast.ElifClause{Position: elifPos, Cond: elifCond, Body: elifBody})
	}

	if p.match(lexer.TOKEN_ELSE) {
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		elseBlock = p.parseBlock()
	}

	return &ast.IfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseWhile() *ast.WhileStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WHILE)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.WhileStmt{Position: pos, Cond: cond, Body: body}
}

func (p *Parser) parseStaticStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)

	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "error" {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		msg := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		p.expectNewline()
		return &ast.StaticErrorStmt{Position: pos, Message: msg}
	}

	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	thenBlock := p.parseBlock()

	var elifs []ast.StaticElifClause
	var elseBlock []ast.Stmt

	for p.skipNewlines(); p.peek() == lexer.TOKEN_STATIC; p.skipNewlines() {
		saved := p.pos
		p.advance()
		if p.peek() == lexer.TOKEN_ELIF {
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseBlock()
			elifs = append(elifs, ast.StaticElifClause{Position: p.tokens[saved].Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseBlock()
			break
		} else {
			p.pos = saved
			break
		}
	}

	return &ast.StaticIfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

// parseExprOrAssignStmt: handles expressions, assignments, var decls, discards
func (p *Parser) parseExprOrAssignStmt() ast.Stmt {
	pos := p.cur().Pos

	// Discard: _ = expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		p.advance() // _
		p.advance() // =
		value := p.parseExpr()
		p.expectNewline()
		return &ast.DiscardStmt{Position: pos, Value: value}
	}

	// Variable declaration: name: [mutable] Type [= value]
	// But NOT name:mutable (no space) which would be field:Type
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		// Check if the token after colon looks like a type (identifier, mutable, tail)
		// and not an assignment target / expression
		colonPos := p.pos + 1
		afterColon := lexer.TOKEN_EOF
		if colonPos+1 < len(p.tokens) {
			afterColon = p.tokens[colonPos+1].Kind
		}
		if afterColon == lexer.TOKEN_IDENT || afterColon == lexer.TOKEN_MUTABLE || afterColon == lexer.TOKEN_TAIL {
			name := p.cur().Text
			p.advance() // ident
			p.advance() // colon

			mutable := false
			if p.match(lexer.TOKEN_MUTABLE) {
				mutable = true
			}

			typ := p.parseTypeExpr()

			var value ast.Expr
			if p.match(lexer.TOKEN_ASSIGN) {
				value = p.parseExpr()
			}
			p.expectNewline()
			return &ast.VarDeclStmt{Position: pos, Name: name, Mutable: mutable, Type: typ, Value: value}
		}
	}

	// Parse LHS expression
	expr := p.parseExpr()

	// Check for assignment operators
	switch p.peek() {
	case lexer.TOKEN_LARROW:
		p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.AssignStmt{Position: pos, Target: expr, Value: value}

	case lexer.TOKEN_PLUSEQ, lexer.TOKEN_MINUSEQ, lexer.TOKEN_STAREQ, lexer.TOKEN_SLASHEQ,
		lexer.TOKEN_CARETEQ, lexer.TOKEN_PIPEEQ, lexer.TOKEN_AMPEQ,
		lexer.TOKEN_LSHIFTEQ, lexer.TOKEN_RSHIFTEQ:
		op := p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.AugAssignStmt{Position: pos, Op: op.Kind, Target: expr, Value: value}

	case lexer.TOKEN_AS:
		p.advance()
		var asKind string
		if p.match(lexer.TOKEN_AMPERSAND) {
			asKind = "&"
		} else if p.match(lexer.TOKEN_BANG) {
			asKind = "!"
		}
		p.expect(lexer.TOKEN_LARROW)
		value := p.parseExpr()
		p.expectNewline()
		return &ast.AsRefAssignStmt{Position: pos, Target: expr, AsKind: asKind, Value: value}
	}

	// Bare function call: ident expr (no parens) — e.g., assert raw != null
	if ident, ok := expr.(*ast.Ident); ok && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		arg := p.parseExpr()
		p.expectNewline()
		return &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
			Position: pos,
			Func:     ident,
			Args:     []ast.Expr{arg},
		}}
	}

	// Expression statement
	p.expectNewline()
	return &ast.ExprStmt{Position: pos, Expr: expr}
}

// ---------- Type expressions ----------

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	if p.match(lexer.TOKEN_MUTABLE) {
		elem := p.parseTypeExpr()
		return &ast.MutableType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_TAIL) {
		elem := p.parseTypeExpr()
		return &ast.TailType{Position: elem.Pos(), Elem: elem}
	}
	return p.parseBaseType()
}

func (p *Parser) parseBaseType() ast.TypeExpr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	var typ ast.TypeExpr = &ast.NamedType{Position: pos, Name: name}

	// Bracket: could be Generic Name[T, U] or Array Name[Size]
	if p.peek() == lexer.TOKEN_LBRACKET {
		// Look ahead: if first token after [ is INT/HEX, it's an array
		// If it's IDENT and looks like a type (followed by &, ], ,), it's generic
		afterBracket := lexer.TOKEN_EOF
		if p.pos+1 < len(p.tokens) {
			afterBracket = p.tokens[p.pos+1].Kind
		}
		isArray := afterBracket == lexer.TOKEN_INT_LIT || afterBracket == lexer.TOKEN_HEX_LIT
		if afterBracket == lexer.TOKEN_IDENT && p.pos+2 < len(p.tokens) {
			// If IDENT is followed by something NOT type-like (not &, ?, ], ,, [), it's an array index
			afterIdent := p.tokens[p.pos+2].Kind
			isArray = afterIdent != lexer.TOKEN_AMPERSAND && afterIdent != lexer.TOKEN_QUESTION &&
				afterIdent != lexer.TOKEN_RBRACKET && afterIdent != lexer.TOKEN_COMMA &&
				afterIdent != lexer.TOKEN_LBRACKET
		}

		if isArray {
			p.advance() // [
			size := p.parseExpr()
			p.expect(lexer.TOKEN_RBRACKET)
			typ = &ast.ArrayType{Position: pos, Elem: typ, Size: size}
		} else {
			p.advance() // [
			var args []ast.TypeExpr
			for {
				args = append(args, p.parseTypeExpr())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expect(lexer.TOKEN_RBRACKET)
			typ = &ast.GenericType{Position: pos, Name: name, Args: args}
		}
	}

	// Reference chain: Type& or Type&&? etc.
	for p.peek() == lexer.TOKEN_AMPERSAND {
		p.advance()
		nullable := p.match(lexer.TOKEN_QUESTION)
		typ = &ast.RefType{Position: pos, Elem: typ, Nullable: nullable}
	}

	// Array type after ref: Type&?[Size]
	if p.peek() == lexer.TOKEN_LBRACKET {
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		typ = &ast.ArrayType{Position: pos, Elem: typ, Size: size}
	}

	return typ
}

// ---------- Expression parsing (precedence climbing) ----------

func (p *Parser) parseExpr() ast.Expr {
	expr := p.parseOr()

	// Ternary: value if cond else alt
	if p.peek() == lexer.TOKEN_IF {
		pos := p.cur().Pos
		p.advance()
		cond := p.parseOr()
		p.expect(lexer.TOKEN_ELSE)
		alt := p.parseExpr()
		return &ast.TernaryExpr{Position: pos, Value: expr, Cond: cond, Alt: alt}
	}

	return expr
}

func (p *Parser) parseOr() ast.Expr {
	left := p.parseAnd()
	for p.peek() == lexer.TOKEN_OR {
		pos := p.cur().Pos
		p.advance()
		right := p.parseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_OR, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseAnd() ast.Expr {
	left := p.parseNot()
	for p.peek() == lexer.TOKEN_AND {
		pos := p.cur().Pos
		p.advance()
		right := p.parseNot()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AND, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseNot() ast.Expr {
	if p.peek() == lexer.TOKEN_NOT {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNot()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: operand}
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() ast.Expr {
	left := p.parseBitwiseOr()
	for p.peek() == lexer.TOKEN_EQEQ || p.peek() == lexer.TOKEN_BANGEQ ||
		p.peek() == lexer.TOKEN_LT || p.peek() == lexer.TOKEN_GT ||
		p.peek() == lexer.TOKEN_LTEQ || p.peek() == lexer.TOKEN_GTEQ {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseBitwiseOr()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseBitwiseOr() ast.Expr {
	left := p.parseBitwiseXor()
	for p.peek() == lexer.TOKEN_PIPE {
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseXor()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PIPE, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseBitwiseXor() ast.Expr {
	left := p.parseBitwiseAnd()
	for p.peek() == lexer.TOKEN_CARET {
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_CARET, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseBitwiseAnd() ast.Expr {
	left := p.parseShift()
	for p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		right := p.parseShift()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AMPERSAND, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseShift() ast.Expr {
	left := p.parseAddSub()
	for p.peek() == lexer.TOKEN_LSHIFT || p.peek() == lexer.TOKEN_RSHIFT {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseAddSub()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseAddSub() ast.Expr {
	left := p.parseMulDiv()
	for p.peek() == lexer.TOKEN_PLUS || p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseMulDiv()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseMulDiv() ast.Expr {
	left := p.parseUnary()
	for p.peek() == lexer.TOKEN_STAR || p.peek() == lexer.TOKEN_SLASH {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseUnary()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseUnary() ast.Expr {
	if p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_TILDE {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_TILDE, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.AddrOfExpr{Position: pos, Operand: operand}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
		switch p.peek() {
		case lexer.TOKEN_DOT:
			pos := p.cur().Pos
			p.advance()
			field := p.expect(lexer.TOKEN_IDENT).Text

			// Check for cast: expr.Type&() or expr.Type&&?() or expr.Type()
			if p.peek() == lexer.TOKEN_AMPERSAND {
				// Could be cast to ref type (possibly chained: &&, &&&, etc.)
				castPos := pos
				castName := field
				savedCastPos := p.pos
				var target ast.TypeExpr = &ast.NamedType{Position: castPos, Name: castName}
				ampCount := 0
				for p.peek() == lexer.TOKEN_AMPERSAND {
					p.advance()
					ampCount++
					nullable := p.match(lexer.TOKEN_QUESTION)
					target = &ast.RefType{Position: castPos, Elem: target, Nullable: nullable}
				}
				if p.peek() == lexer.TOKEN_LPAREN {
					p.advance() // (
					p.expect(lexer.TOKEN_RPAREN)
					expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target}
					continue
				}
				// Not a cast, rewind
				p.pos = savedCastPos
			}

			// Check for cast: expr.Type()
			// Check for cast: expr.Type() — any .ident() with empty parens is a cast
			if p.peek() == lexer.TOKEN_LPAREN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
				castPos := pos
				p.advance() // (
				p.advance() // )
				target := &ast.NamedType{Position: castPos, Name: field}
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target}
				continue
			}

			// Simple field access or method-like
			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field}

		case lexer.TOKEN_LBRACKET:
			pos := p.cur().Pos
			p.advance()
			index := p.parseExpr()
			p.expect(lexer.TOKEN_RBRACKET)
			expr = &ast.IndexExpr{Position: pos, Object: expr, Index: index}

		case lexer.TOKEN_LPAREN:
			pos := p.cur().Pos
			p.advance()
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
			expr = &ast.CallExpr{Position: pos, Func: expr, Args: args}

		case lexer.TOKEN_IF:
			// Ternary: value if cond else alt
			pos := p.cur().Pos
			p.advance()
			cond := p.parseExpr()
			p.expect(lexer.TOKEN_ELSE)
			alt := p.parseExpr()
			expr = &ast.TernaryExpr{Position: pos, Value: expr, Cond: cond, Alt: alt}

		default:
			return expr
		}
	}
}

func (p *Parser) parsePrimary() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: false}

	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: true}

	case lexer.TOKEN_STRING_LIT:
		tok := p.advance()
		return &ast.StringLit{Position: tok.Pos, Value: tok.Text}

	case lexer.TOKEN_NULL:
		tok := p.advance()
		return &ast.NullLit{Position: tok.Pos}

	case lexer.TOKEN_TRUE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: true}

	case lexer.TOKEN_FALSE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: false}

	case lexer.TOKEN_ZEROED:
		tok := p.advance()
		return &ast.ZeroedLit{Position: tok.Pos}

	case lexer.TOKEN_SIZEOF:
		pos := p.cur().Pos
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		typ := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.SizeofExpr{Position: pos, Type: typ}

	case lexer.TOKEN_IDENT:
		tok := p.advance()
		// Check if this is a struct literal: Name(args...)
		// Only uppercase names are struct constructors
		if p.peek() == lexer.TOKEN_LPAREN && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance() // (
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
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args}
		}
		return &ast.Ident{Position: tok.Pos, Name: tok.Text}

	case lexer.TOKEN_LPAREN:
		p.advance()
		inner := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: inner.Pos(), Inner: inner}

	default:
		p.errorf("unexpected token %s in expression", p.cur())
		tok := p.advance()
		return &ast.Ident{Position: tok.Pos, Name: "<error>"}
	}
}

func (p *Parser) expectNewline() {
	if p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	} else if p.peek() == lexer.TOKEN_EOF || p.peek() == lexer.TOKEN_DEDENT {
		// OK at end of file or block
	} else {
		p.errorf("expected newline, got %s", p.cur())
	}
}
