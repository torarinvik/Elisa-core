package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type enumMapArm struct {
	position lexer.Pos
	source   []string
	target   []string
	def      bool
}

func (p *Parser) parseEnumMapDecl() *ast.FuncDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ENUM)
	p.expectIdentText("map")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	sourceType := p.parseTypeExpr()
	p.expect(lexer.TOKEN_ARROW)
	targetType := p.parseTypeExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]enumMapArm, 0, p.estimateIndentedItemCount())
	var fallback ast.Expr
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arm := p.parseEnumMapArm()
		if arm.def {
			if fallback != nil {
				p.errorAt(arm.position, "enum map can only have one default arm")
			}
			fallback = p.enumMapTargetExpr(arm.position, targetType, arm.target)
		} else {
			arms = append(arms, arm)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	if fallback == nil {
		p.errorAt(pos, "enum map requires a default `_ => ...` arm")
		fallback = &ast.ZeroedLit{Position: pos}
	}

	body := make([]ast.Stmt, 0, len(arms)+1)
	for _, arm := range arms {
		condition := &ast.BinaryExpr{
			Position: arm.position,
			Op:       lexer.TOKEN_EQEQ,
			Left:     &ast.Ident{Position: arm.position, Name: "value"},
			Right:    p.enumMapSourceExpr(arm.position, sourceType, arm.source),
		}
		body = append(body, &ast.IfStmt{
			Position: arm.position,
			Cond:     condition,
			Then: []ast.Stmt{
				&ast.ReturnStmt{Position: arm.position, Value: p.enumMapTargetExpr(arm.position, targetType, arm.target)},
			},
		})
	}
	body = append(body, &ast.ReturnStmt{Position: fallback.Pos(), Value: fallback})

	return &ast.FuncDecl{
		Position:   pos,
		Name:       name,
		Params:     []ast.ParamDecl{{Position: pos, Name: "value", Type: sourceType}},
		ReturnType: targetType,
		Body:       body,
	}
}

func (p *Parser) parseEnumMapArm() enumMapArm {
	pos := p.cur().Pos
	def := p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_"
	source := p.parseEnumMapName()
	p.expect(lexer.TOKEN_FATARROW)
	target := p.parseEnumMapName()
	p.expectNewline()
	return enumMapArm{position: pos, source: source, target: target, def: def}
}

func (p *Parser) parseEnumMapName() []string {
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.match(lexer.TOKEN_DOT) {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	return parts
}

func (p *Parser) enumMapSourceExpr(pos lexer.Pos, typ ast.TypeExpr, parts []string) ast.Expr {
	return p.enumMapQualifiedExpr(pos, typ, parts, "source")
}

func (p *Parser) enumMapTargetExpr(pos lexer.Pos, typ ast.TypeExpr, parts []string) ast.Expr {
	return p.enumMapQualifiedExpr(pos, typ, parts, "target")
}

func (p *Parser) enumMapQualifiedExpr(pos lexer.Pos, typ ast.TypeExpr, parts []string, side string) ast.Expr {
	if len(parts) == 0 {
		return &ast.Ident{Position: pos, Name: "<error>"}
	}
	if len(parts) == 1 && parts[0] != "_" {
		if named, ok := typ.(*ast.NamedType); ok && named.Name != "" {
			return buildQualifiedMatchValueExpr(pos, []string{named.Name, parts[0]})
		}
		p.errorAt(pos, "bare enum map %s member %q requires a named enum type", side, parts[0])
	}
	return buildQualifiedMatchValueExpr(pos, parts)
}
