//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitFoldExpr(expr *ast.FoldExpr) (llvmValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing fold expression")
	}
	if expr.Keyword == "rewrite" {
		if _, ok := sequenceRewriteTargetTypeExprBackend(expr.Root); ok {
			return s.emitSequenceRewriteExpr(expr)
		}
	}
	return nil, nil, fmt.Errorf("fold/rewrite over tree is not supported (tree construct removed)")
}
