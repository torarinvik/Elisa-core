package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

// Parameter bindings belong to the current analyzer. Products retain names and
// compact type tags, never symbols, scopes or parameter declaration pointers.
func primitiveParameterSyntax(params []ast.ParamDecl) bool {
	if len(params) == 0 {
		return true
	}
	if !primitiveParameterSchemaValid || len(params) > 256 {
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
			if typ.Name != "i64" || !reflect.ValueOf(copy).IsZero() {
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
			if typ.Name != "i64" || !reflect.ValueOf(copy).IsZero() {
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
	canonical, ok := a.namedTypes["i64"].(*BuiltinType)
	if !ok || canonical.Name != "i64" {
		return false
	}
	for index, param := range fn.Params {
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
		switch expr.(type) {
		case *ast.ParenExpr, *ast.UnaryExpr:
			next = replay(next)
		case *ast.BinaryExpr:
			next = replay(next)
			next = replay(next)
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
