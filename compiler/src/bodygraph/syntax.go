package bodygraph

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func LastSegment(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' || name[i] == ':' {
			return name[i+1:]
		}
	}
	return name
}

func BodyFunctions(d ast.Decl) []*ast.FuncDecl {
	switch n := d.(type) {
	case *ast.FuncDecl:
		return []*ast.FuncDecl{n}
	case *ast.ImplDecl:
		var funcs []*ast.FuncDecl
		for _, member := range n.Members {
			if fn, ok := member.(*ast.FuncDecl); ok {
				funcs = append(funcs, fn)
			}
		}
		return funcs
	}
	return nil
}

func BodyDefinedNames(d ast.Decl) []string {
	switch n := d.(type) {
	case *ast.FuncDecl:
		return []string{LastSegment(n.Name)}
	case *ast.ImplDecl:
		var names []string
		for _, m := range n.Members {
			if fn, ok := m.(*ast.FuncDecl); ok {
				names = append(names, LastSegment(fn.Name))
			}
		}
		return names
	}
	return nil
}

func BodyStmts(d ast.Decl) []ast.Stmt {
	switch n := d.(type) {
	case *ast.FuncDecl:
		return FuncScanStmts(n)
	case *ast.ImplDecl:
		var stmts []ast.Stmt
		for _, m := range n.Members {
			if fn, ok := m.(*ast.FuncDecl); ok {
				stmts = append(stmts, FuncScanStmts(fn)...)
			}
		}
		return stmts
	}
	return nil
}

// bodyGraphBinaryOperatorMethod returns a possible statically dispatched dunder
// for an operator before expression analysis has populated LoweredCall. These
// are body-scheduling edges only: without receiver types, treating them as
// function-call edges would create false recursive-summary classifications.
func BinaryOperatorMethod(op lexer.TokenKind) string {
	switch op {
	case lexer.TOKEN_PLUS:
		return "__add__"
	case lexer.TOKEN_MINUS:
		return "__sub__"
	case lexer.TOKEN_STAR:
		return "__mul__"
	case lexer.TOKEN_SLASH:
		return "__div__"
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		return "__eq__"
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		return "__cmp__"
	default:
		return ""
	}
}

func UnaryOperatorMethod(op lexer.TokenKind) string {
	if op == lexer.TOKEN_MINUS {
		return "__neg__"
	}
	return ""
}

// funcScanStmts is a function's body plus its contract clauses, which name
// callees too (a ghost spec unfolded in an `ensure`).
func FuncScanStmts(fn *ast.FuncDecl) []ast.Stmt {
	stmts := append([]ast.Stmt(nil), fn.Body...)
	for _, group := range [][]ast.Expr{fn.Requires, fn.EnsureValues, fn.Decreases} {
		for _, e := range group {
			stmts = append(stmts, &ast.ExprStmt{Position: e.Pos(), Expr: e})
		}
	}
	for _, group := range [][]*ast.ProofBlockStmt{fn.RequiresProofs, fn.EnsureProofs} {
		for _, p := range group {
			stmts = append(stmts, p)
		}
	}
	return stmts
}

// funcDefaultArgStmts returns defaults as ordering dependencies. They are
// evaluated at call sites, so references here must not enter recursive-summary
// classification for the function whose parameter has the default.
func FuncDefaultArgStmts(fn *ast.FuncDecl) []ast.Stmt {
	var stmts []ast.Stmt
	for _, param := range fn.Params {
		if param.DefaultValue != nil {
			stmts = append(stmts, &ast.ExprStmt{Position: param.DefaultValue.Pos(), Expr: param.DefaultValue})
		}
	}
	return stmts
}
