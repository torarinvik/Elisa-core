package semantic

import "elisacore/src/ast"

// desugarStructLiteralCopyBase rewrites a copy-update literal
// `T{..base, f: v}` into the ordinary literal `T{f: v, g: base.g, ...}`: every
// declared field not written explicitly is read from `base`. Field defaults
// never apply when a base is present. It runs just before the field defaults
// are filled in, and clears CopyBase, so type checking, ownership and codegen
// see exactly the literal a user would have written by hand.
func (a *Analyzer) desugarStructLiteralCopyBase(expr *ast.StructLitExpr, base *StructType, targetType Type) {
	if expr == nil || expr.CopyBase == nil {
		return
	}
	copyBase := expr.CopyBase
	expr.CopyBase = nil
	if base == nil || base.Decl == nil {
		return
	}
	probe := cloneDefaultArgExpr(copyBase)
	if probe == nil {
		probe = copyBase
	}
	actual := a.analyzeExpr(probe)
	if IsInvalidType(actual) {
		return
	}
	if !SameType(targetType, actual) && !(AssignableTo(targetType, actual) && AssignableTo(actual, targetType)) {
		a.errorf(copyBase.Pos(), "struct literal %q copy source `..base` must have type %s, got %s", expr.Name, targetType, actual)
		return
	}
	written := make(map[string]bool, len(expr.ArgNames))
	for i := range expr.Args {
		written[expr.ArgName(i)] = true
	}
	for _, fieldDecl := range base.Decl.Fields {
		if written[fieldDecl.Name] {
			continue
		}
		object := cloneDefaultArgExpr(copyBase)
		if object == nil {
			object = copyBase
		}
		expr.Args = append(expr.Args, &ast.FieldExpr{Position: copyBase.Pos(), Object: object, Field: fieldDecl.Name})
		expr.ArgNames = append(expr.ArgNames, fieldDecl.Name)
	}
}
