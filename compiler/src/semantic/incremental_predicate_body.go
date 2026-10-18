package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

const predicateOperatorAbsence = "canonical-predicate-no-comparison-candidates-v1"

func predicateTypeTag(name string) byte {
	switch name {
	case "int":
		return 'n'
	case "i64":
		return 'i'
	case "bool":
		return 'b'
	case "char":
		return 'c'
	case "i8":
		return '0'
	case "i16":
		return '1'
	case "i32":
		return '2'
	case "u8":
		return '3'
	case "u16":
		return '4'
	case "u32":
		return '5'
	case "u64":
		return '6'
	case "isize":
		return '7'
	case "usize":
		return '8'
	case "uintptr":
		return '9'
	default:
		return 0
	}
}

func predicateTypeName(tag byte) string {
	switch tag {
	case 'n':
		return "int"
	case 'i':
		return "i64"
	case 'b':
		return "bool"
	case 'c':
		return "char"
	case '0':
		return "i8"
	case '1':
		return "i16"
	case '2':
		return "i32"
	case '3':
		return "u8"
	case '4':
		return "u16"
	case '5':
		return "u32"
	case '6':
		return "u64"
	case '7':
		return "isize"
	case '8':
		return "usize"
	case '9':
		return "uintptr"
	default:
		return ""
	}
}

func validPredicateTyping(types string) bool {
	if len(types) == 0 || len(types) > 4096 || predicateTypeName(types[0]) != "bool" {
		return false
	}
	for i := range types {
		if predicateTypeName(types[i]) == "" {
			return false
		}
	}
	return true
}

func predicateCurrentBindingsAndOperators(a *Analyzer, fn *ast.FuncDecl, nodes []ast.Expr) bool {
	if a == nil || fn == nil || len(fn.Params) > 256 || len(fn.Params) != 0 && a.currentScope == nil {
		return false
	}
	if !predicateCanonicalBuiltin(a, "bool") {
		return false
	}
	names := make([]string, 0, len(fn.Params)+2)
	parameterTypes := make(map[string]string, len(fn.Params))
	for index, param := range fn.Params {
		name := predicateBuiltinSyntaxName(param.Type)
		if name == "" || !predicateCanonicalBuiltin(a, name) {
			return false
		}
		symbol, found := a.currentScope.Lookup(param.Name)
		if !found || symbol == nil || symbol.Kind != SymbolParam || symbol.Node != fn || symbol.ParamIndex != index || symbol.Type != a.namedTypes[name] || symbol.Mutable || symbol.Ghost || symbol.AliasOf != nil {
			return false
		}
		names = append(names, name)
		parameterTypes[param.Name] = name
	}
	for _, node := range nodes {
		switch node.(type) {
		case *ast.BoolLit:
			names = append(names, "bool")
		case *ast.CharLit:
			names = append(names, "char")
		case *ast.BinaryExpr:
			binary := node.(*ast.BinaryExpr)
			if predicateComparisonOperator(binary.Op) {
				name := predicateComparisonReceiverTypeName(binary, parameterTypes)
				if name == "" || !predicateCanonicalBuiltin(a, name) {
					return false
				}
				names = append(names, name)
			}
		}
	}
	return a.incrementalOperators.predicateComparisonsAbsent(a, names)
}

func predicateCanonicalBuiltin(a *Analyzer, name string) bool {
	builtin, ok := a.namedTypes[name].(*BuiltinType)
	return ok && builtin != nil && builtin.Name == name
}

func predicateUnaryMinusIsIntrinsic(expr ast.Expr, parameterTypes map[string]string) bool {
	if expr == nil || reflect.ValueOf(expr).Kind() == reflect.Ptr && reflect.ValueOf(expr).IsNil() {
		return false
	}
	switch node := expr.(type) {
	case *ast.Ident:
		return IsNumericType(&BuiltinType{Name: parameterTypes[node.Name]})
	case *ast.IntLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return predicateUnaryMinusIsIntrinsic(node.Inner, parameterTypes)
	case *ast.UnaryExpr:
		return node.Op == lexer.TOKEN_MINUS && predicateUnaryMinusIsIntrinsic(node.Operand, parameterTypes)
	default:
		return false
	}
}

func predicateComparisonOperator(op lexer.TokenKind) bool {
	switch op {
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		return true
	default:
		return false
	}
}

// Comparison analysis contextually types an unsuffixed integer literal from
// the other operand before attempting overload dispatch. The receiver is the
// left operand, so a literal on the left may dispatch on the right operand's
// integral type rather than on `int`.
func predicateComparisonReceiverTypeName(expr *ast.BinaryExpr, parameterTypes map[string]string) string {
	if expr == nil {
		return ""
	}
	left := predicateExprBuiltinTypeName(expr.Left, parameterTypes)
	if !isUnsuffixedIntegerValueExpr(expr.Left) {
		return left
	}
	right := predicateExprBuiltinTypeName(expr.Right, parameterTypes)
	if IsIntegralStorageType(&BuiltinType{Name: right}) {
		return right
	}
	return left
}

func predicateExprBuiltinTypeName(expr ast.Expr, parameterTypes map[string]string) string {
	if expr == nil || reflect.ValueOf(expr).Kind() == reflect.Ptr && reflect.ValueOf(expr).IsNil() {
		return ""
	}
	switch node := expr.(type) {
	case *ast.Ident:
		return parameterTypes[node.Name]
	case *ast.IntLit:
		if node.Suffix == "" {
			return "int"
		}
	case *ast.CharLit:
		return "char"
	case *ast.BoolLit:
		return "bool"
	case *ast.ParenExpr:
		return predicateExprBuiltinTypeName(node.Inner, parameterTypes)
	case *ast.UnaryExpr:
		if node.Op == lexer.TOKEN_NOT {
			return "bool"
		}
		if node.Op == lexer.TOKEN_MINUS && predicateUnaryMinusIsIntrinsic(node.Operand, parameterTypes) {
			return predicateExprBuiltinTypeName(node.Operand, parameterTypes)
		}
	case *ast.BinaryExpr:
		if node.Op == lexer.TOKEN_AND || node.Op == lexer.TOKEN_OR || predicateComparisonOperator(node.Op) {
			return "bool"
		}
	}
	return ""
}

// Predicate admission is deliberately separate from integer-local planning.
// Only canonical scalar syntax is accepted; current semantic bindings and
// operator candidates must still be validated at the replay boundary.
func predicateBuiltinSyntaxName(input ast.TypeExpr) string {
	var name string
	switch typ := input.(type) {
	case *ast.NamedType:
		if typ == nil {
			return ""
		}
		copy := *typ
		name = copy.Name
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if !reflect.ValueOf(copy).IsZero() {
			return ""
		}
	case *ast.BuiltinTypeExpr:
		if typ == nil {
			return ""
		}
		copy := *typ
		name = copy.Name
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
			return ""
		}
		copy.TypeArgs = nil
		copy.ValueArgs = nil
		if !reflect.ValueOf(copy).IsZero() {
			return ""
		}
	default:
		return ""
	}
	switch name {
	case "bool", "char", "int", "i64", "i8", "i16", "i32", "u8", "u16", "u32", "u64", "isize", "usize", "uintptr":
		return name
	}
	return ""
}

func predicateReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	if fn == nil || len(fn.Params) > 256 || len(fn.Body) != 1 || len(fn.Name) > primitiveSourceByteBudget || predicateBuiltinSyntaxName(fn.ReturnType) != "bool" {
		return nil, false
	}
	if ret, ok := fn.Body[0].(*ast.ReturnStmt); !ok || ret == nil {
		return nil, false
	}
	// Reuse the exhaustive declaration-header schema gate without broadening
	// the existing i64 local initializer cache.
	header := *fn
	header.ReturnType = &ast.NamedType{Name: "i64"}
	header.Params = append([]ast.ParamDecl(nil), fn.Params...)
	parameters := map[string]bool{}
	parameterTypes := make(map[string]string, len(fn.Params))
	for i, param := range fn.Params {
		name := predicateBuiltinSyntaxName(param.Type)
		if name == "" {
			return nil, false
		}
		header.Params[i].Type = &ast.NamedType{Name: "i64"}
		parameters[param.Name] = true
		parameterTypes[param.Name] = name
	}
	ret, ok := primitiveReturnHeader(&header)
	if !ok {
		return nil, false
	}
	bytes := len(fn.Name)
	for _, param := range fn.Params {
		bytes += len(param.Name) + len(predicateBuiltinSyntaxName(param.Type))
	}
	var nodes []ast.Expr
	seen := map[ast.Expr]bool{}
	var visit func(ast.Expr, int) bool
	visit = func(expr ast.Expr, depth int) bool {
		if expr == nil || (reflect.ValueOf(expr).Kind() == reflect.Ptr && reflect.ValueOf(expr).IsNil()) || depth > 128 || len(nodes) >= 4096 || seen[expr] || bytes > primitiveSourceByteBudget {
			return false
		}
		seen[expr] = true
		nodes = append(nodes, expr)
		switch node := expr.(type) {
		case *ast.Ident:
			if !primitiveParameterSchemaValid || !parameters[node.Name] {
				return false
			}
			bytes += len(node.Name)
		case *ast.IntLit:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			copy.IsHex = false
			if node.Suffix != "" || !reflect.ValueOf(copy).IsZero() {
				return false
			}
			bytes += len(node.Value)
		case *ast.CharLit:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
			bytes += len(node.Value)
		case *ast.BoolLit:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = false
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
		case *ast.ParenExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Inner = nil
			return reflect.ValueOf(copy).IsZero() && visit(node.Inner, depth+1)
		case *ast.UnaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Operand = nil
			// Unary minus on a syntactically numeric canonical scalar is intrinsic.
			// Other receivers may lower to __neg__ and mutate the AST, which a
			// type-only replay cannot reconstruct.
			admissible := node.Op == lexer.TOKEN_NOT || node.Op == lexer.TOKEN_MINUS && predicateUnaryMinusIsIntrinsic(node.Operand, parameterTypes)
			return admissible && reflect.ValueOf(copy).IsZero() && visit(node.Operand, depth+1)
		case *ast.BinaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Left = nil
			copy.Right = nil
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
			switch node.Op {
			case lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			default:
				return false
			}
			return visit(node.Left, depth+1) && visit(node.Right, depth+1)
		default:
			return false
		}
		return bytes <= primitiveSourceByteBudget
	}
	if !visit(ret.Value, 0) {
		return nil, false
	}
	return nodes, true
}
