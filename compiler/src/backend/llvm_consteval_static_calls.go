//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"math"
	"strings"
)

func backendConstMoveBindFieldName(arg ast.MoveBindArg) string {
	if arg.Field != "" {
		return arg.Field
	}
	return arg.Name
}

func bindBackendConstMovePattern(pattern ast.MoveBindPattern, item semantic.ConstValue) (map[string]semantic.ConstValue, bool) {
	scope := map[string]semantic.ConstValue{}
	if pattern == nil {
		return scope, true
	}
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p == nil || p.Name == "" || p.Name == "_" {
			return scope, true
		}
		scope[p.Name] = cloneBackendConstValue(item)
		return scope, true
	case *ast.MoveBindTuplePattern:
		if p == nil || (item.Kind != semantic.ConstTuple && item.Kind != semantic.ConstList) || len(item.Elems) < len(p.Args) {
			return nil, false
		}
		for index, arg := range p.Args {
			if arg.Name == "" || arg.Name == "_" {
				continue
			}
			scope[arg.Name] = cloneBackendConstValue(item.Elems[index])
		}
		return scope, true
	case *ast.MoveBindStructPattern:
		if p == nil || item.Kind != semantic.ConstRecord {
			return nil, false
		}
		for _, arg := range p.Args {
			if arg.Name == "" || arg.Name == "_" {
				continue
			}
			fieldValue, ok := semantic.ConstReflectionRecordField(item, backendConstMoveBindFieldName(arg))
			if !ok {
				return nil, false
			}
			scope[arg.Name] = cloneBackendConstValue(fieldValue)
		}
		return scope, true
	default:
		return nil, false
	}
}

func lookupConstEvalValue(lookup func(string) (semantic.ConstValue, bool), name string) (semantic.ConstValue, bool) {
	if lookup == nil {
		return semantic.ConstValue{}, false
	}
	return lookup("$consteval." + name)
}

func (g *llvmGenerator) evalStaticFunctionCall(expr *ast.CallExpr) (semantic.ConstValue, bool) {
	return g.evalStaticFunctionCallWith(expr, g.evalConstExpr)
}

// evalStaticFunctionCallWith evaluates a static call whose argument expressions are evaluated by
// evalArg -- the caller's own evaluator, which knows the caller's scope. The callee body is
// evaluated in a fresh state that sees only its parameters and the globals.
func (g *llvmGenerator) evalStaticFunctionCallWith(expr *ast.CallExpr, evalArg func(ast.Expr) (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	if expr == nil || g == nil || g.staticCallDepth >= semantic.StaticEvalCallDepthLimit {
		return semantic.ConstValue{}, false
	}
	if value, ok := g.evalConstReflectionCallWith(expr, evalArg); ok {
		return value, true
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return semantic.ConstValue{}, false
	}
	sym, ok := g.lookupStaticFunctionSymbol(ident.Name)
	if !ok || sym == nil {
		return semantic.ConstValue{}, false
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil || !fnType.Static {
		return semantic.ConstValue{}, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil {
		return semantic.ConstValue{}, false
	}
	args := expr.Args
	if expr.ResolvedArgsValid {
		args = expr.ResolvedArgs
	}
	if len(decl.Params) != len(args) {
		return semantic.ConstValue{}, false
	}
	scope := make(map[string]semantic.ConstValue, len(decl.Params))
	for i, arg := range args {
		value, ok := evalArg(arg)
		if !ok {
			return semantic.ConstValue{}, false
		}
		scope[decl.Params[i].Name] = value
	}
	g.staticCallDepth++
	g.constEvalScopes = append(g.constEvalScopes, scope)
	state := &functionState{g: g}
	value, returned, ok := state.evalStaticStmtBlock(g.functionBodyForInputRead(decl, "static-function-evaluation"), true)
	g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
	g.staticCallDepth--
	if !returned {
		if isVoidType(fnType.Return) {
			return semantic.ConstValue{Kind: semantic.ConstUnknown}, true
		}
		return semantic.ConstValue{}, false
	}
	return value, ok
}

func (g *llvmGenerator) evalConstReflectionCall(expr *ast.CallExpr) (semantic.ConstValue, bool) {
	return g.evalConstReflectionCallWith(expr, g.evalConstExpr)
}

func (g *llvmGenerator) evalConstReflectionCallWith(expr *ast.CallExpr, evalArg func(ast.Expr) (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	if expr == nil || g == nil || g.result == nil {
		return semantic.ConstValue{}, false
	}
	if fieldExpr, ok := expr.Func.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Field == "has_field" && len(expr.Args) == 1 && expr.NamedArgCount() == 0 {
		object, objectOK := evalArg(fieldExpr.Object)
		fieldName, fieldOK := evalArg(expr.Args[0])
		if !objectOK || !fieldOK || fieldName.Kind != semantic.ConstString {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstReflectionRecordHasField(object, fieldName.String)
	}
	name, _ := semantic.QualifiedNameExpr(expr.Func)
	if name != "variants" && name != "fields" {
		return semantic.ConstValue{}, false
	}
	return semantic.ConstReflectionCallValue(name, expr.Args, func(typeName string) (semantic.Type, bool) {
		return g.lookupConstReflectionType(typeName)
	})
}

func (g *llvmGenerator) lookupConstReflectionType(typeName string) (semantic.Type, bool) {
	if g == nil || g.result == nil || typeName == "" {
		return nil, false
	}
	if t, ok := g.lookupNamedType(typeName); ok {
		return t, true
	}
	var matched semantic.Type
	suffix := "." + typeName
	for name, t := range g.namedTypesMap("const-reflection-suffix-scan") {
		if strings.HasSuffix(name, suffix) {
			if matched != nil {
				return nil, false
			}
			matched = t
		}
	}
	return matched, matched != nil
}

func (g *llvmGenerator) lookupStaticFunctionSymbol(name string) (result *semantic.Symbol, found bool) {
	if g == nil || g.result == nil || !g.hasGlobalScope() {
		return nil, false
	}
	if sym, ok := g.lookupGlobalSymbol(name, "static-function-resolution"); ok {
		return sym, true
	}
	var matched *semantic.Symbol
	suffix := "." + name
	for symbolName, sym := range g.globalSymbolsMap("static-function-suffix-scan") {
		if strings.HasSuffix(symbolName, suffix) {
			if matched != nil {
				return nil, false
			}
			matched = sym
		}
	}
	return matched, matched != nil
}

func cloneBackendConstValue(value semantic.ConstValue) semantic.ConstValue {
	return semantic.CloneConstValue(value)
}

func backendConstOptionalSome(value semantic.ConstValue) semantic.ConstValue {
	cloned := cloneBackendConstValue(value)
	return semantic.ConstValue{Kind: semantic.ConstOptional, Some: true, Value: &cloned}
}

func backendConstOptionalFromFirstProjection(value semantic.ConstValue) semantic.ConstValue {
	if value.Kind == semantic.ConstOptional {
		return cloneBackendConstValue(value)
	}
	return backendConstOptionalSome(value)
}

func (g *llvmGenerator) setConstEvalValue(name string, value semantic.ConstValue) {
	if len(g.constEvalScopes) == 0 {
		g.constEvalScopes = append(g.constEvalScopes, map[string]semantic.ConstValue{})
	}
	g.constEvalScopes[len(g.constEvalScopes)-1][name] = cloneBackendConstValue(value)
}

func (g *llvmGenerator) updateConstEvalValue(name string, value semantic.ConstValue) bool {
	for i := len(g.constEvalScopes) - 1; i >= 0; i-- {
		if _, ok := g.constEvalScopes[i][name]; ok {
			g.constEvalScopes[i][name] = cloneBackendConstValue(value)
			return true
		}
	}
	return false
}

func (g *llvmGenerator) constEvalValueScope(name string) (semantic.ConstValue, int, bool) {
	for i := len(g.constEvalScopes) - 1; i >= 0; i-- {
		if value, ok := g.constEvalScopes[i][name]; ok {
			return value, i, true
		}
	}
	return semantic.ConstValue{}, -1, false
}

func (s *functionState) constEvalValueScope(name string) (semantic.ConstValue, int, bool) {
	if s == nil || s.g == nil {
		return semantic.ConstValue{}, -1, false
	}
	return s.g.constEvalValueScope(name)
}

func (g *llvmGenerator) evalStaticMatchPattern(pattern ast.MatchPattern, value semantic.ConstValue) (bool, map[string]semantic.ConstValue, bool) {
	if g == nil {
		return false, nil, false
	}
	return (&functionState{g: g}).evalStaticMatchPattern(pattern, value)
}
func evalConstEquality(left, right semantic.ConstValue, equal bool) (semantic.ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == semantic.ConstInt && right.Kind == semantic.ConstInt:
		matched = left.Int == right.Int
	case (left.Kind == semantic.ConstInt || left.Kind == semantic.ConstFloat) && (right.Kind == semantic.ConstInt || right.Kind == semantic.ConstFloat):
		matched = math.Float64bits(backendConstNumericAsFloat64(left)) == math.Float64bits(backendConstNumericAsFloat64(right))
	case left.Kind == semantic.ConstBool && right.Kind == semantic.ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == semantic.ConstString && right.Kind == semantic.ConstString:
		matched = left.String == right.String
	default:
		return semantic.ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return semantic.ConstValue{Kind: semantic.ConstBool, Bool: matched}, true
}
func evalBackendConstNumericBinary(op lexer.TokenKind, left, right semantic.ConstValue) (semantic.ConstValue, bool) {
	if left.Kind == semantic.ConstInt && right.Kind == semantic.ConstInt {
		switch op {
		case lexer.TOKEN_LT:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int < right.Int}, true
		case lexer.TOKEN_GT:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int > right.Int}, true
		case lexer.TOKEN_LTEQ:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int <= right.Int}, true
		case lexer.TOKEN_GTEQ:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int >= right.Int}, true
		case lexer.TOKEN_PLUS:
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int + right.Int}, true
		case lexer.TOKEN_MINUS:
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int - right.Int}, true
		case lexer.TOKEN_STAR:
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int * right.Int}, true
		case lexer.TOKEN_SLASH:
			if right.Int == 0 {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int / right.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	}
	if !backendIsConstNumeric(left) || !backendIsConstNumeric(right) {
		return semantic.ConstValue{}, false
	}
	leftFloat := backendConstNumericAsFloat64(left)
	rightFloat := backendConstNumericAsFloat64(right)
	switch op {
	case lexer.TOKEN_LT:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat < rightFloat}, true
	case lexer.TOKEN_GT:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat > rightFloat}, true
	case lexer.TOKEN_LTEQ:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat <= rightFloat}, true
	case lexer.TOKEN_GTEQ:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat >= rightFloat}, true
	case lexer.TOKEN_PLUS:
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat + rightFloat}, true
	case lexer.TOKEN_MINUS:
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat - rightFloat}, true
	case lexer.TOKEN_STAR:
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat * rightFloat}, true
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat / rightFloat}, true
	default:
		return semantic.ConstValue{}, false
	}
}
func backendIsConstNumeric(value semantic.ConstValue) bool {
	return value.Kind == semantic.ConstInt || value.Kind == semantic.ConstFloat
}
func backendConstNumericAsFloat64(value semantic.ConstValue) float64 {
	if value.Kind == semantic.ConstFloat {
		return value.Float
	}
	return float64(value.Int)
}
