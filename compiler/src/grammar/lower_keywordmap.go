package grammar

import (
	"elisacore/src/ast"
)

func lowerKeywordMapDecl(decl *ast.KeywordMapDecl) *ast.FuncDecl {
	if decl == nil {
		return nil
	}
	pos := decl.Position
	arms := make([]ast.MatchArm, 0, len(decl.Entries)+1)
	for _, entry := range decl.Entries {
		arms = append(arms, ast.MatchArm{
			Position: entry.Position,
			Pattern:  &ast.MatchStringLiteralPattern{Position: entry.Position, Value: entry.Text},
			Body: []ast.Stmt{&ast.ReturnStmt{
				Position: entry.Position,
				Value:    entry.Value,
			}},
		})
	}
	arms = append(arms, ast.MatchArm{
		Position: pos,
		Pattern:  &ast.MatchWildcardPattern{Position: pos},
		Body: []ast.Stmt{&ast.ReturnStmt{
			Position: pos,
			Value:    decl.Fallback,
		}},
	})
	inputType := decl.InputType
	if inputType == nil {
		inputType = builtinTypeExpr(pos, "sview")
	}
	return &ast.FuncDecl{
		Position: pos,
		Name:     decl.Name,
		Params: []ast.ParamDecl{{
			Position: pos,
			Name:     "text",
			Type:     inputType,
		}},
		ReturnType: decl.ReturnType,
		Body: []ast.Stmt{&ast.MatchStmt{
			Position: pos,
			Value:    &ast.Ident{Position: pos, Name: "text"},
			Arms:     arms,
		}},
	}
}
