package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

func normalizeParseBuilderName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func lastQualifiedNamePart(value string) string {
	if idx := strings.LastIndex(value, "."); idx >= 0 && idx+1 < len(value) {
		return value[idx+1:]
	}
	return value
}
func qualifiedExpr(pos lexer.Pos, qualified string) ast.Expr {
	parts := strings.Split(qualified, ".")
	if len(parts) == 0 {
		return &ast.Ident{Position: pos, Name: qualified}
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: parts[0]}
	for _, part := range parts[1:] {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: part}
	}
	return expr
}
