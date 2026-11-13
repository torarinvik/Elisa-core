package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/predicatetypes"
)

const predicateOperatorAbsence = "canonical-predicate-no-comparison-candidates-v1"

const predicateRangeTypeTag = predicatetypes.RangeTypeTag

func predicateTypeTag(name string) byte           { return predicatetypes.TypeTag(name) }
func predicateTypeName(tag byte) string           { return predicatetypes.TypeName(tag) }
func validPredicateTyping(types string) bool      { return predicatetypes.ValidTyping(types) }
func validRangePredicateTyping(types string) bool { return predicatetypes.ValidRangeTyping(types) }

func predicateTypingMatchesNodes(nodes []ast.Expr, types string) bool {
	return predicatetypes.TypingMatchesNodes(nodes, types)
}
func predicateMembershipRange(expr ast.Expr) (*ast.MembershipRangeExpr, *ast.ListLitExpr, bool) {
	return predicatetypes.MembershipRange(expr)
}

func predicateCurrentBindingsAndOperators(a *Analyzer, fn *ast.FuncDecl, nodes []ast.Expr) bool {
	if a == nil || fn == nil || len(fn.Params) > primitiveParameterLimit || len(fn.Params) != 0 && a.currentScope == nil {
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
	return predicatetypes.UnaryMinusIsIntrinsic(expr, parameterTypes)
}
func predicateComparisonOperator(op lexer.TokenKind) bool {
	return predicatetypes.ComparisonOperator(op)
}
func predicateComparisonReceiverTypeName(expr *ast.BinaryExpr, parameterTypes map[string]string) string {
	return predicatetypes.ComparisonReceiverTypeName(expr, parameterTypes)
}

func predicateBuiltinSyntaxName(input ast.TypeExpr) string {
	return predicatetypes.BuiltinSyntaxName(input)
}

func predicateReturnExpressionsWithNodeBudget(fn *ast.FuncDecl, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, bool) {
	if fn == nil || len(fn.Params) > primitiveParameterLimit || len(fn.Body) != 1 || len(fn.Name) > primitiveSourceByteBudget || predicateBuiltinSyntaxName(fn.ReturnType) != "bool" {
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
	ret, ok := primitiveReturnTypingHeader(&header)
	if !ok {
		return nil, false
	}
	bytes := len(fn.Name)
	for _, param := range fn.Params {
		bytes += len(param.Name) + len(predicateBuiltinSyntaxName(param.Type))
	}
	return predicatetypes.ReturnExpressionNodes(ret.Value, parameters, parameterTypes, bytes, primitiveSourceByteBudget, primitiveParameterSchemaValid, remainingNodes, budgetExhausted)
}
