package grammar

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"sort"
	"strings"
)

func lowerLexerDecls(decl *ast.LexerDecl) []ast.Decl {
	if decl == nil {
		return nil
	}
	out := make([]ast.Decl, 0, len(decl.CharClasses)+3)
	if len(decl.Modes) != 0 {
		out = append(out, lowerLexerModeEnumDecl(decl))
	}
	for _, class := range decl.CharClasses {
		out = append(out, lowerLexerCharClassDecl(decl, class))
	}
	if decl.Keywords != nil {
		out = append(out, lowerLexerKeywordDecl(decl, *decl.Keywords))
	}
	if decl.Literals != nil {
		out = append(out, lowerLexerLiteralDecl(decl, *decl.Literals))
	}
	return out
}

func lowerLexerModeEnumDecl(decl *ast.LexerDecl) *ast.EnumDecl {
	name := decl.ModeEnumName
	if name == "" {
		name = decl.Name + "Mode"
	}
	variants := make([]ast.EnumVariantDecl, 0, len(decl.Modes))
	for _, mode := range decl.Modes {
		variants = append(variants, ast.EnumVariantDecl{Position: mode.Position, Name: mode.Name})
	}
	return &ast.EnumDecl{Position: decl.Position, Name: name, Variants: variants}
}

func lowerLexerCharClassDecl(decl *ast.LexerDecl, class ast.LexerCharClassDecl) *ast.FuncDecl {
	pos := class.Position
	return &ast.FuncDecl{
		Position: pos,
		Name:     lexerCharClassFuncName(decl.Name, class.Name),
		Params: []ast.ParamDecl{{
			Position: pos,
			Name:     "ch",
			Type:     builtinTypeExpr(pos, "char"),
		}},
		ReturnType: builtinTypeExpr(pos, "bool"),
		Body: []ast.Stmt{&ast.ReturnStmt{
			Position: pos,
			Value:    lowerLexerCharClassTerms(decl, pos, class.Terms),
		}},
	}
}

func lowerLexerKeywordDecl(decl *ast.LexerDecl, keywords ast.LexerKeywordDecl) *ast.FuncDecl {
	pos := keywords.Position
	arms := make([]ast.MatchArm, 0, len(keywords.Entries)+1)
	for _, entry := range keywords.Entries {
		arms = append(arms, ast.MatchArm{
			Position: entry.Position,
			Pattern:  &ast.MatchStringLiteralPattern{Position: entry.Position, Value: entry.Text},
			Body: []ast.Stmt{&ast.ExprStmt{
				Position: entry.Position,
				Expr:     lexerKindExpr(decl, entry.Position, entry.Kind),
			}},
		})
	}
	arms = append(arms, ast.MatchArm{
		Position: pos,
		Pattern:  &ast.MatchWildcardPattern{Position: pos},
		Body: []ast.Stmt{&ast.ExprStmt{
			Position: pos,
			Expr:     lexerKindExpr(decl, pos, keywords.Fallback),
		}},
	})
	return &ast.FuncDecl{
		Position: pos,
		Name:     lexerHelperName(decl.Name, "keyword_kind"),
		Params: []ast.ParamDecl{{
			Position: pos,
			Name:     "text",
			Type:     builtinTypeExpr(pos, "sview"),
		}},
		ReturnType: lexerTokenKindType(decl, pos),
		Body: []ast.Stmt{&ast.ReturnStmt{
			Position: pos,
			Value: &ast.MatchExpr{
				Position: pos,
				Value:    &ast.Ident{Position: pos, Name: "text"},
				Arms:     arms,
			},
		}},
	}
}

func lowerLexerLiteralDecl(decl *ast.LexerDecl, literals ast.LexerLiteralDecl) *ast.FuncDecl {
	pos := literals.Position
	entries := append([]ast.LexerLiteralEntry(nil), literals.Entries...)
	if literals.Longest {
		sort.SliceStable(entries, func(i, j int) bool {
			return len(entries[i].Text) > len(entries[j].Text)
		})
	}
	body := make([]ast.Stmt, 0, len(entries)+1)
	for _, entry := range entries {
		length := len(entry.Text)
		body = append(body, &ast.IfStmt{
			Position: entry.Position,
			Cond:     lexerLiteralMatchCond(entry.Position, entry.Text),
			Then: []ast.Stmt{&ast.ReturnStmt{
				Position: entry.Position,
				Value:    lexerLiteralReturnTuple(decl, entry.Position, entry.Kind, length),
			}},
		})
	}
	body = append(body, &ast.ReturnStmt{Position: pos, Value: lexerLiteralReturnTuple(decl, pos, literals.Fallback, 0)})
	return &ast.FuncDecl{
		Position: pos,
		Name:     lexerHelperName(decl.Name, "match_literal"),
		Params: []ast.ParamDecl{
			{Position: pos, Name: "source", Type: builtinTypeExpr(pos, "dstr")},
			{Position: pos, Name: "offset", Type: builtinTypeExpr(pos, "usize")},
		},
		ReturnType: &ast.TupleTypeExpr{Position: pos, Fields: []ast.TupleTypeField{
			{Position: pos, Name: "kind", Type: lexerTokenKindType(decl, pos)},
			{Position: pos, Name: "len", Type: builtinTypeExpr(pos, "usize")},
		}},
		Body: body,
	}
}

func lowerLexerCharClassTerms(decl *ast.LexerDecl, pos lexer.Pos, terms []ast.LexerCharClassTerm) ast.Expr {
	if len(terms) == 0 {
		return &ast.BoolLit{Position: pos, Value: false}
	}
	var expr ast.Expr
	for _, term := range terms {
		termExpr := lowerLexerCharClassTerm(decl, term)
		if expr == nil {
			expr = termExpr
			continue
		}
		expr = &ast.BinaryExpr{Position: term.Position, Op: lexer.TOKEN_OR, Left: expr, Right: termExpr}
	}
	return expr
}

func lowerLexerCharClassTerm(decl *ast.LexerDecl, term ast.LexerCharClassTerm) ast.Expr {
	pos := term.Position
	ch := &ast.Ident{Position: pos, Name: "ch"}
	if term.Ref {
		return &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: lexerCharClassFuncName(decl.Name, term.Name)}, Args: []ast.Expr{ch}}
	}
	start := &ast.CharLit{Position: pos, Value: term.Start}
	if term.Range {
		return &ast.BinaryExpr{
			Position: pos,
			Op:       lexer.TOKEN_AND,
			Left:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_GTEQ, Left: ch, Right: start},
			Right:    &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LTEQ, Left: ch, Right: &ast.CharLit{Position: pos, Value: term.End}},
		}
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_EQEQ, Left: ch, Right: start}
}

func lexerLiteralMatchCond(pos lexer.Pos, text string) ast.Expr {
	length := len(text)
	offsetPlusLength := &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: &ast.Ident{Position: pos, Name: "offset"}, Right: lexerInt(pos, length)}
	lengthGuard := &ast.BinaryExpr{
		Position: pos,
		Op:       lexer.TOKEN_LTEQ,
		Left:     offsetPlusLength,
		Right:    &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: "source"}, Field: "len"},
	}
	sliceMatch := &ast.BinaryExpr{
		Position: pos,
		Op:       lexer.TOKEN_EQEQ,
		Left: &ast.SliceExpr{
			Position: pos,
			Object:   &ast.Ident{Position: pos, Name: "source"},
			Start:    &ast.Ident{Position: pos, Name: "offset"},
			End:      &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: &ast.Ident{Position: pos, Name: "offset"}, Right: lexerInt(pos, length)},
		},
		Right: &ast.StringLit{Position: pos, Value: text},
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AND, Left: lengthGuard, Right: sliceMatch}
}

func lexerLiteralReturnTuple(decl *ast.LexerDecl, pos lexer.Pos, kind string, length int) ast.Expr {
	return &ast.TupleExpr{Position: pos, Elems: []ast.Expr{lexerKindExpr(decl, pos, kind), lexerInt(pos, length)}}
}

func lexerKindExpr(decl *ast.LexerDecl, pos lexer.Pos, kind string) ast.Expr {
	return grammarTokenKindExpr(pos, lexerTokenKindType(decl, pos), kind)
}

func lexerTokenKindType(decl *ast.LexerDecl, pos lexer.Pos) ast.TypeExpr {
	if decl != nil && decl.TokenKindType != nil {
		return decl.TokenKindType
	}
	return builtinTypeExpr(pos, "TokenKind")
}

func lexerCharClassFuncName(lexerName string, className string) string {
	return lexerHelperName(lexerName, "is_"+className)
}

func lexerHelperName(lexerName string, suffix string) string {
	return lexerHelperPrefix(lexerName) + "_" + suffix
}

func lexerHelperPrefix(name string) string {
	if name == "" {
		return "lexer"
	}
	var builder strings.Builder
	prevLowerOrDigit := false
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 && prevLowerOrDigit {
				builder.WriteByte('_')
			}
			builder.WriteByte(ch - 'A' + 'a')
			prevLowerOrDigit = true
			continue
		}
		builder.WriteByte(ch)
		prevLowerOrDigit = (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
	}
	return builder.String()
}

func lexerInt(pos lexer.Pos, value int) ast.Expr {
	return &ast.IntLit{Position: pos, Value: itoa(value)}
}
