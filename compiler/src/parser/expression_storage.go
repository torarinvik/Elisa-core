package parser

import (
	"elisacore/src/ast"
	"unsafe"
)

// Large parses allocate these common expression nodes in typed blocks. The
// tree still owns independent, stable node pointers, and the collector can
// trace their fields normally.
type expressionStorage struct {
	idents []ast.Ident
	fields []ast.FieldExpr
	calls  []ast.CallExpr
}

func storeExpression[T any](remaining *[]T, expr T) *T {
	if len(*remaining) == 0 {
		const blockBytes = 16 << 10
		*remaining = make([]T, blockBytes/unsafe.Sizeof(expr))
	}
	slot := &(*remaining)[0]
	*remaining = (*remaining)[1:]
	*slot = expr
	return slot
}

func (p *Parser) newIdent(expr ast.Ident) *ast.Ident {
	if p.expressionStorage == nil {
		slot := new(ast.Ident)
		*slot = expr
		return slot
	}
	return storeExpression(&p.expressionStorage.idents, expr)
}

func (p *Parser) newField(expr ast.FieldExpr) *ast.FieldExpr {
	if p.expressionStorage == nil {
		slot := new(ast.FieldExpr)
		*slot = expr
		return slot
	}
	return storeExpression(&p.expressionStorage.fields, expr)
}

func (p *Parser) newCall(expr ast.CallExpr) *ast.CallExpr {
	if p.expressionStorage == nil {
		slot := new(ast.CallExpr)
		*slot = expr
		return slot
	}
	return storeExpression(&p.expressionStorage.calls, expr)
}
