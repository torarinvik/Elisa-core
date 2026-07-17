package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"strings"
)

func (a *Analyzer) expandActiveAndGeneratedDecls(decls []ast.Decl, generated map[ast.Decl]bool) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.StaticIfDecl:
			out = append(out, a.expandActiveAndGeneratedDecls(a.activeDeclBranch(n), generated)...)
		case *ast.NamespaceDecl:
			copied := *n
			copied.Decls = a.expandActiveAndGeneratedDecls(n.Decls, generated)
			out = append(out, &copied)
		case *ast.StaticGenerateDecl:
			emitted := a.evalStaticGenerateDecl(n)
			for _, generatedDecl := range emitted {
				generated[generatedDecl] = true
			}
			out = append(out, emitted...)
		default:
			out = append(out, decl)
		}
	}
	return out
}

func (a *Analyzer) evalStaticGenerateDecl(decl *ast.StaticGenerateDecl) []ast.Decl {
	a.staticContextDepth++
	a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
	defer func() {
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
		a.staticContextDepth--
	}()
	out := make([]ast.Decl, 0)
	a.evalStaticGenerateStmts(decl.Body, &out)
	return out
}

func (a *Analyzer) evalStaticGenerateStmts(stmts []ast.StaticGenerateStmt, out *[]ast.Decl) bool {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.StaticGenerateEmitDecl:
			decls, ok := a.evalStaticGenerateEmit(n)
			if !ok {
				return false
			}
			*out = append(*out, decls...)
		case *ast.StaticGenerateForDecl:
			source, ok := a.evalConstExpr(n.Source)
			if !ok || source.Kind != ConstList {
				a.errorf(n.Pos(), "static generate for source must evaluate to a compile-time list")
				return false
			}
			for _, elem := range source.Elems {
				if n.Filter != nil {
					a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{n.Name: CloneConstValue(elem)})
					include, ok := a.evalConstBoolExpr(n.Filter)
					a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
					if !ok {
						a.errorf(n.Filter.Pos(), "static generate where filter must evaluate to a compile-time bool")
						return false
					}
					if !include {
						continue
					}
				}
				a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{n.Name: CloneConstValue(elem)})
				ok := a.evalStaticGenerateStmts(n.Body, out)
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				if !ok {
					return false
				}
			}
		case *ast.StaticGenerateIfDecl:
			branch, ok := a.staticGenerateIfBranch(n)
			if !ok {
				return false
			}
			if !a.evalStaticGenerateStmts(branch, out) {
				return false
			}
		default:
			a.errorf(stmt.Pos(), "unsupported static generate statement %T", stmt)
			return false
		}
	}
	return true
}

func (a *Analyzer) staticGenerateIfBranch(stmt *ast.StaticGenerateIfDecl) ([]ast.StaticGenerateStmt, bool) {
	selected, ok := a.evalConstBoolExpr(stmt.Cond)
	if !ok {
		a.errorf(stmt.Pos(), "static generate if condition must evaluate to a compile-time bool")
		return stmt.Then, false
	}
	if selected {
		return stmt.Then, true
	}
	for _, elif := range stmt.Elifs {
		selected, ok := a.evalConstBoolExpr(elif.Cond)
		if !ok {
			a.errorf(elif.Position, "static generate elif condition must evaluate to a compile-time bool")
			return nil, false
		}
		if selected {
			return elif.Body, true
		}
	}
	return stmt.Else, true
}

func (a *Analyzer) evalStaticGenerateEmit(stmt *ast.StaticGenerateEmitDecl) ([]ast.Decl, bool) {
	source, ok := a.renderStaticGenerateTokens(stmt.Tokens)
	if !ok {
		return nil, false
	}
	l := lexer.New(stmt.Pos().File, []byte(source))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		for _, err := range errs {
			a.errorf(stmt.Pos(), "generated declaration lex error: %s", err)
		}
		return nil, false
	}
	// The rendered source is lexed as its OWN text, so token positions are offsets within the
	// GENERATED fragment — while the filename here is the user's REAL file. Left unrebased, a
	// diagnostic on a generated declaration names the real file with a line number from the
	// fragment, i.e. it points at an arbitrary innocent line: a bad `emit` body on line 8
	// reported at line 2, on an unrelated enum variant, once per generated instance.
	//
	// Rebasing onto the `emit` site makes every generated node point at the code the user
	// actually wrote and can edit, which is the only position that means anything here — the
	// generated text has no independent existence on disk. The lex/parse errors above already
	// report against exactly this position; this extends the same anchoring to the nodes they
	// produce. (Same shape as the f-string sub-lexer; see parser_fstring.go.)
	for i := range tokens {
		tokens[i].Pos = stmt.Pos()
	}
	p := parser.New(tokens)
	file := p.ParseFile(stmt.Pos().File)
	if errs := p.Errors(); len(errs) != 0 {
		for _, err := range errs {
			a.errorf(stmt.Pos(), "generated declaration parse error: %s", err)
		}
		return nil, false
	}
	return file.Decls, true
}

func (a *Analyzer) renderStaticGenerateTokens(tokens []lexer.Token) (string, bool) {
	var b strings.Builder
	indent := 0
	atLineStart := true
	prev := lexer.TOKEN_EOF
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok.Kind {
		case lexer.TOKEN_NEWLINE:
			b.WriteByte('\n')
			atLineStart = true
			prev = tok.Kind
			continue
		case lexer.TOKEN_INDENT:
			indent++
			prev = tok.Kind
			continue
		case lexer.TOKEN_DEDENT:
			if indent > 0 {
				indent--
			}
			prev = tok.Kind
			continue
		}
		if tok.Kind == lexer.TOKEN_IDENT && strings.HasSuffix(tok.Text, "$") && i+1 < len(tokens) && tokens[i+1].Kind == lexer.TOKEN_LBRACE {
			prefix := strings.TrimSuffix(tok.Text, "$")
			if prefix != "" {
				a.writeGeneratedTokenText(&b, &atLineStart, indent, prev, prefix)
				prev = lexer.TOKEN_IDENT
			}
			value, next, ok := a.evalStaticGenerateInterpolation(tokens, i+2)
			if !ok {
				return "", false
			}
			if prefix != "" {
				b.WriteString(value)
			} else {
				a.writeGeneratedTokenText(&b, &atLineStart, indent, prev, value)
			}
			prev = lexer.TOKEN_IDENT
			i = next
			continue
		}
		text := staticGenerateTokenText(tok)
		a.writeGeneratedTokenText(&b, &atLineStart, indent, prev, text)
		prev = tok.Kind
	}
	return b.String(), true
}

func (a *Analyzer) evalStaticGenerateInterpolation(tokens []lexer.Token, start int) (string, int, bool) {
	depth := 1
	exprTokens := make([]lexer.Token, 0, 4)
	for i := start; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok.Kind {
		case lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RBRACE:
			depth--
			if depth == 0 {
				value, ok := a.evalStaticGenerateTemplateExpr(exprTokens)
				return value, i, ok
			}
		}
		exprTokens = append(exprTokens, tok)
	}
	a.errorf(tokens[start-2].Pos, "unterminated static generate interpolation")
	return "", len(tokens), false
}

func (a *Analyzer) evalStaticGenerateTemplateExpr(tokens []lexer.Token) (string, bool) {
	source, ok := a.renderStaticGenerateTokens(tokens)
	if !ok {
		return "", false
	}
	expr, ok := parseGeneratedConstExpr(source)
	if !ok {
		a.errorf(firstTokenPos(tokens), "invalid static generate interpolation expression")
		return "", false
	}
	value, ok := a.evalConstExpr(expr)
	if !ok {
		a.errorf(expr.Pos(), "static generate interpolation must evaluate at compile time")
		return "", false
	}
	switch value.Kind {
	case ConstString:
		return value.String, true
	case ConstInt:
		return fmt.Sprintf("%d", value.Int), true
	case ConstRecord:
		if name, ok := value.Fields["name"]; ok && name.Kind == ConstString {
			return name.String, true
		}
	}
	a.errorf(expr.Pos(), "static generate interpolation must be a string, integer, or reflection record with a name")
	return "", false
}

func parseGeneratedConstExpr(source string) (ast.Expr, bool) {
	l := lexer.New("<static-generate-expr>", []byte("const __generated = "+source+"\n"))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		return nil, false
	}
	p := parser.New(tokens)
	file := p.ParseFile("<static-generate-expr>")
	if len(p.Errors()) != 0 || len(file.Decls) != 1 {
		return nil, false
	}
	decl, ok := file.Decls[0].(*ast.ConstDecl)
	if !ok {
		return nil, false
	}
	return decl.Value, true
}

func (a *Analyzer) writeGeneratedTokenText(b *strings.Builder, atLineStart *bool, indent int, prev lexer.TokenKind, text string) {
	if text == "" {
		return
	}
	if *atLineStart {
		for i := 0; i < indent; i++ {
			b.WriteString("    ")
		}
		*atLineStart = false
	} else if staticGenerateNeedsSpace(prev, text) {
		b.WriteByte(' ')
	}
	b.WriteString(text)
}

func staticGenerateNeedsSpace(prev lexer.TokenKind, text string) bool {
	if prev == lexer.TOKEN_EOF || prev == lexer.TOKEN_NEWLINE || prev == lexer.TOKEN_INDENT || prev == lexer.TOKEN_DEDENT {
		return false
	}
	switch text {
	case ")", "]", "}", ":", ",", ".", "?", "!", "=>":
		return false
	}
	switch prev {
	case lexer.TOKEN_DOT, lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE, lexer.TOKEN_AT:
		return false
	}
	return true
}

func staticGenerateTokenText(tok lexer.Token) string {
	if tok.Text != "" {
		if tok.Kind == lexer.TOKEN_STRING_LIT {
			return fmt.Sprintf("%q", tok.Text)
		}
		if tok.Kind == lexer.TOKEN_CHAR_LIT {
			return fmt.Sprintf("'%s'", tok.Text)
		}
		return tok.Text
	}
	switch tok.Kind {
	case lexer.TOKEN_DEF:
		return "def"
	case lexer.TOKEN_ERROR:
		return "error"
	case lexer.TOKEN_ENUM:
		return "enum"
	case lexer.TOKEN_STRUCT:
		return "struct"
	case lexer.TOKEN_CONST:
		return "const"
	case lexer.TOKEN_GLOBAL:
		return "global"
	case lexer.TOKEN_EXTERN:
		return "extern"
	case lexer.TOKEN_EXPORT:
		return "export"
	case lexer.TOKEN_IF:
		return "if"
	case lexer.TOKEN_MATCH:
		return "match"
	case lexer.TOKEN_ELIF:
		return "elif"
	case lexer.TOKEN_ELSE:
		return "else"
	case lexer.TOKEN_WHILE:
		return "while"
	case lexer.TOKEN_RETURN:
		return "return"
	case lexer.TOKEN_STATIC:
		return "static"
	case lexer.TOKEN_MUTABLE:
		return "mutable"
	case lexer.TOKEN_PASS:
		return "pass"
	case lexer.TOKEN_NOT:
		return "not"
	case lexer.TOKEN_AND:
		return "and"
	case lexer.TOKEN_OR:
		return "or"
	case lexer.TOKEN_IN:
		return "in"
	case lexer.TOKEN_AS:
		return "as"
	case lexer.TOKEN_IS:
		return "is"
	case lexer.TOKEN_NULL:
		return "null"
	case lexer.TOKEN_TRUE:
		return "true"
	case lexer.TOKEN_FALSE:
		return "false"
	case lexer.TOKEN_COLON:
		return ":"
	case lexer.TOKEN_ARROW:
		return "->"
	case lexer.TOKEN_DOT:
		return "."
	case lexer.TOKEN_COMMA:
		return ","
	case lexer.TOKEN_LPAREN:
		return "("
	case lexer.TOKEN_RPAREN:
		return ")"
	case lexer.TOKEN_LBRACKET:
		return "["
	case lexer.TOKEN_RBRACKET:
		return "]"
	case lexer.TOKEN_LBRACE:
		return "{"
	case lexer.TOKEN_RBRACE:
		return "}"
	case lexer.TOKEN_ASSIGN:
		return "="
	case lexer.TOKEN_LARROW:
		return "<-"
	case lexer.TOKEN_FATARROW:
		return "=>"
	case lexer.TOKEN_EQEQ:
		return "=="
	case lexer.TOKEN_BANGEQ:
		return "!="
	case lexer.TOKEN_LT:
		return "<"
	case lexer.TOKEN_GT:
		return ">"
	case lexer.TOKEN_LTEQ:
		return "<="
	case lexer.TOKEN_GTEQ:
		return ">="
	case lexer.TOKEN_PLUS:
		return "+"
	case lexer.TOKEN_MINUS:
		return "-"
	case lexer.TOKEN_STAR:
		return "*"
	case lexer.TOKEN_SLASH:
		return "/"
	case lexer.TOKEN_PIPE:
		return "|"
	case lexer.TOKEN_AMPERSAND:
		return "&"
	default:
		return tok.Text
	}
}

func firstTokenPos(tokens []lexer.Token) lexer.Pos {
	if len(tokens) == 0 {
		return lexer.Pos{}
	}
	return tokens[0].Pos
}
