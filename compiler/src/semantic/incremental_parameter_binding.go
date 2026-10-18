package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

const primitiveParameterLimit = 256

// Parameter bindings belong to the current analyzer. Products retain names and
// compact type tags, never symbols, scopes or parameter declaration pointers.
func primitiveParameterSyntax(params []ast.ParamDecl) bool {
	if len(params) == 0 {
		return true
	}
	if !primitiveParameterSchemaValid || len(params) > primitiveParameterLimit {
		return false
	}
	names := map[string]bool{}
	total := 0
	for _, param := range params {
		total += len(param.Name)
		if param.Name == "" || total > primitiveSourceByteBudget || names[param.Name] || param.Mutable || param.DefaultValue != nil {
			return false
		}
		names[param.Name] = true
		switch typ := param.Type.(type) {
		case *ast.NamedType:
			if typ == nil {
				return false
			}
			copy := *typ
			copy.Position = lexer.Pos{}
			copy.Name = ""
			if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
				return false
			}
		case *ast.BuiltinTypeExpr:
			if typ == nil {
				return false
			}
			copy := *typ
			copy.Position = lexer.Pos{}
			copy.Name = ""
			if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
				return false
			}
			copy.TypeArgs = nil
			copy.ValueArgs = nil
			if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func primitiveCurrentParameterBindings(a *Analyzer, fn *ast.FuncDecl) bool {
	if len(fn.Params) == 0 {
		return true
	}
	if a.currentScope == nil {
		return false
	}
	for index, param := range fn.Params {
		name := predicateBuiltinSyntaxName(param.Type)
		canonical, ok := a.namedTypes[name].(*BuiltinType)
		if !ok || (name != "i64" && name != "int") || canonical.Name != name {
			return false
		}
		symbol, found := a.currentScope.Lookup(param.Name)
		if !found || symbol == nil || symbol.Kind != SymbolParam || symbol.Node != fn || symbol.ParamIndex != index || symbol.Type != canonical || symbol.Mutable || symbol.Ghost || symbol.AliasOf != nil {
			return false
		}
	}
	return true
}

var primitiveParameterSchemaValid = func() bool {
	for typ, fields := range map[reflect.Type][]string{
		reflect.TypeOf(ast.ParamDecl{}): {"Position:lexer.Pos", "Name:string", "Mutable:bool", "Type:ast.TypeExpr", "DefaultValue:ast.Expr"},
		reflect.TypeOf(ast.Ident{}):     {"Position:lexer.Pos", "Name:string"},
	} {
		if typ.NumField() != len(fields) {
			return false
		}
		for i, expected := range fields {
			field := typ.Field(i)
			if field.Name+":"+field.Type.String() != expected {
				return false
			}
		}
	}
	return true
}()

// The source protocol is preorder; restore in cold evaluation order so parent
// optimization facts see current child facts. Identifier reads execute normally.
func replayPrimitiveParameterNodes(a *Analyzer, nodes []ast.Expr, types string) int {
	restored := 0
	var replay func(int) int
	replay = func(index int) int {
		expr := nodes[index]
		next := index + 1
		switch node := expr.(type) {
		case *ast.ParenExpr, *ast.UnaryExpr:
			next = replay(next)
		case *ast.MembershipRangeExpr:
			next = replay(next)
			next = replay(next)
			a.consumeAffineValueExpr(node.Start, a.exprTypes[node.Start], "move into membership range start")
			a.consumeAffineValueExpr(node.End, a.exprTypes[node.End], "move into membership range end")
			return next // Cold analysis does not publish a standalone range type.
		case *ast.BinaryExpr:
			next = replay(next)
			next = replay(next)
			if node.Op == lexer.TOKEN_IN {
				rangeNode, list, _ := predicateMembershipRange(node.Right)
				if list == nil {
					list = &ast.ListLitExpr{Position: rangeNode.Pos(), Elems: []ast.Expr{rangeNode}}
					node.Right = list
				}
				element := MergeTypes(a.exprTypes[rangeNode.Start], a.exprTypes[rangeNode.End])
				if IsInvalidType(element) {
					element = a.exprTypes[node.Left]
				}
				a.recordAnalyzedExprType(list, &ArrayType{Elem: element, Size: "1", HasConstSize: true, ConstSize: 1})
			}
		}
		if _, parameter := expr.(*ast.Ident); parameter {
			a.analyzeExpr(expr)
		} else {
			name := predicateTypeName(types[index])
			a.recordAnalyzedExprType(expr, a.namedTypes[name])
			restored++
		}
		return next
	}
	replay(0)
	return restored
}
