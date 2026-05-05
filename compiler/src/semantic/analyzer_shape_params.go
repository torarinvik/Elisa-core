package semantic

import (
	"elisacore/src/ast"
	"strings"
	"unicode"
)

func (a *Analyzer) paramIsMutable(param ast.ParamDecl) bool {
	if param.Mutable {
		return true
	}
	mutableType, ok := param.Type.(*ast.MutableType)
	if !ok {
		return false
	}
	_, refLike := mutableType.Elem.(*ast.RefType)
	return !refLike
}

func isLegacyOutParamName(name string) bool {
	return name == "out" || strings.HasSuffix(name, "_out")
}

func (a *Analyzer) withShapeParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Shape, len(names))
	for _, name := range names {
		bindings[name] = &ShapeParam{Name: name}
	}
	a.shapeParamScopes = append(a.shapeParamScopes, bindings)
	fn()
	a.shapeParamScopes = a.shapeParamScopes[:len(a.shapeParamScopes)-1]
}

func (a *Analyzer) collectImplicitShapeParams(params []ast.ParamDecl, ret ast.TypeExpr) []string {
	seen := map[string]bool{}
	order := make([]string, 0)
	for _, param := range params {
		a.collectImplicitShapeParamsFromType(param.Type, seen, &order)
	}
	if ret != nil {
		a.collectImplicitShapeParamsFromType(ret, seen, &order)
	}
	return order
}

func (a *Analyzer) collectImplicitShapeParamsFromType(expr ast.TypeExpr, seen map[string]bool, order *[]string) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ErrorUnionTypeExpr:
		a.collectImplicitShapeParamsFromType(n.Value, seen, order)
		a.collectImplicitShapeParamsFromType(n.Errors, seen, order)
	case *ast.ErrorSetExpr:
		return
	case *ast.RefType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.MutableType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.TailType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.ArrayType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.TupleTypeExpr:
		for _, field := range n.Fields {
			a.collectImplicitShapeParamsFromType(field.Type, seen, order)
		}
	case *ast.BuiltinTypeExpr:
		switch n.Name {
		case "array", "view", "dview":
			for _, arg := range n.TypeArgs {
				a.collectImplicitShapeParamsFromType(arg, seen, order)
			}
		case "dict":
			for _, arg := range n.TypeArgs {
				a.collectImplicitShapeParamsFromType(arg, seen, order)
			}
		case "darray":
			if len(n.TypeArgs) > 0 {
				a.collectImplicitShapeParamsFromType(n.TypeArgs[0], seen, order)
			}
			if len(n.ValueArgs) > 0 {
				if name, ok := shapeNameFromValueExpr(n.ValueArgs[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		case "cstr":
			if len(n.ValueArgs) > 0 {
				if name, ok := shapeNameFromValueExpr(n.ValueArgs[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		}
	case *ast.GenericType:
		for _, arg := range n.Args {
			a.collectImplicitShapeParamsFromType(arg, seen, order)
		}
	}
}

func shapeNameFromTypeExpr(expr ast.TypeExpr) (string, bool) {
	name, ok := expr.(*ast.NamedType)
	if !ok {
		return "", false
	}
	return name.Name, true
}

func shapeNameFromValueExpr(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func isImplicitShapeWitnessName(name string) bool {
	if strings.HasPrefix(name, "shape_") || strings.HasPrefix(name, "s_") {
		return true
	}
	runes := []rune(name)
	if len(runes) != 1 {
		return false
	}
	return unicode.In(runes[0], unicode.Greek)
}
