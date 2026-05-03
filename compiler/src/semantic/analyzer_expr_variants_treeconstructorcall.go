package semantic

import "llcontext/src/ast"

func (a *Analyzer) treeConstructorCall(expr *ast.CallExpr) (*TreeCategoryType, *EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	a.treeConstructorCallees[fieldExpr] = true
	treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(fieldExpr)
	if !ok {
		return nil, nil, false
	}
	if variant == nil {
		a.errorf(fieldExpr.Pos(), "tree category %q has no variant %q", treeType.Name, fieldExpr.Field)
		return treeType, nil, true
	}
	return treeType, variant, true
}
