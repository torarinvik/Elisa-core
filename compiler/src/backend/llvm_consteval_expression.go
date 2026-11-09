//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"strconv"
)

// typeOf supplies a subexpression's static type where a fold depends on it (a cstr's `.len` is
// strlen); it may return nil, which only refuses the folds that need a type.
func evalConstExprWithLookup(expr ast.Expr, lookup func(string) (semantic.ConstValue, bool), call func(*ast.CallExpr) (semantic.ConstValue, bool), typeOf func(ast.Expr) semantic.Type) (semantic.ConstValue, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		// Use the semantic parser so unsigned-typed literals with the top bit set
		// (e.g. 0xFFFFFFFFFFFFFFFFu64) parse via ParseUint instead of overflowing
		// a signed parse.
		value, ok := semantic.ParseIntLiteral(n)
		if !ok {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: value}, true
	case *ast.FloatLit:
		value, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: value}, true
	case *ast.BoolLit:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return semantic.ConstValue{Kind: semantic.ConstString, String: n.Value}, true
	case *ast.CharLit:
		value, ok := semantic.ParseCharLiteral(n)
		if !ok {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: value}, true
	case *ast.Ident:
		if lookup != nil {
			if value, ok := lookupConstEvalValue(lookup, n.Name); ok {
				return value, true
			}
		}
		if lookup != nil {
			if value, ok := lookup(n.Name); ok {
				return value, true
			}
		}
		return semantic.ConstValue{}, false
	case *ast.FieldExpr:
		if value, ok := evalBackendConstAggregateFieldExpr(n, lookup, call, typeOf); ok {
			return value, true
		}
		ident, ok := n.Object.(*ast.Ident)
		if !ok || lookup == nil {
			return semantic.ConstValue{}, false
		}
		return lookup(ident.Name + "." + n.Field)
	case *ast.ParenExpr:
		return evalConstExprWithLookup(n.Inner, lookup, call, typeOf)
	case *ast.CastExpr:
		return evalConstExprWithLookup(n.Operand, lookup, call, typeOf)
	case *ast.MoveExpr:
		return evalConstExprWithLookup(n.Operand, lookup, call, typeOf)
	case *ast.UnaryExpr:
		operand, ok := evalConstExprWithLookup(n.Operand, lookup, call, typeOf)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			switch operand.Kind {
			case semantic.ConstInt:
				return semantic.ConstValue{Kind: semantic.ConstInt, Int: -operand.Int}, true
			case semantic.ConstFloat:
				return semantic.ConstValue{Kind: semantic.ConstFloat, Float: -operand.Float}, true
			default:
				return semantic.ConstValue{}, false
			}
		case lexer.TOKEN_TILDE:
			if operand.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: ^operand.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := evalConstExprWithLookup(n.Left, lookup, call, typeOf)
		if !ok {
			return semantic.ConstValue{}, false
		}
		right, ok := evalConstExprWithLookup(n.Right, lookup, call, typeOf)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return evalConstEquality(left, right, false)
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
			lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			if result, ok := evalBackendConstNumericBinary(n.Op, left, right); ok {
				return result, true
			}
			return semantic.ConstValue{}, false
		case lexer.TOKEN_PERCENT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt || right.Int == 0 {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int % right.Int}, true
		case lexer.TOKEN_LSHIFT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int << right.Int}, true
		case lexer.TOKEN_RSHIFT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int >> right.Int}, true
		case lexer.TOKEN_AMPERSAND:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int & right.Int}, true
		case lexer.TOKEN_PIPE:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int | right.Int}, true
		case lexer.TOKEN_CARET:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int ^ right.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.TernaryExpr:
		condValue, ok := evalConstExprWithLookup(n.Cond, lookup, call, typeOf)
		if !ok || condValue.Kind != semantic.ConstBool {
			return semantic.ConstValue{}, false
		}
		if condValue.Bool {
			return evalConstExprWithLookup(n.Value, lookup, call, typeOf)
		}
		return evalConstExprWithLookup(n.Alt, lookup, call, typeOf)
	case *ast.UnwrapElseExpr:
		value, ok := evalConstExprWithLookup(n.Value, lookup, call, typeOf)
		if !ok || value.Kind != semantic.ConstOptional {
			return semantic.ConstValue{}, false
		}
		if value.Some {
			if value.Value == nil {
				return semantic.ConstValue{}, false
			}
			return cloneBackendConstValue(*value.Value), true
		}
		recovery := backendConstRecoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
			return semantic.ConstValue{}, false
		}
		return evalConstExprWithLookup(recovery.Value, lookup, call, typeOf)
	case *ast.GetExpr:
		if idx, ok := n.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
			return evalConstExprWithLookup(idx, lookup, call, typeOf)
		}
		value, ok := evalConstExprWithLookup(n.Value, lookup, call, typeOf)
		if !ok || value.Kind != semantic.ConstOptional {
			return semantic.ConstValue{}, false
		}
		if value.Some {
			if value.Value == nil {
				return semantic.ConstValue{}, false
			}
			return cloneBackendConstValue(*value.Value), true
		}
		recovery := backendConstRecoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
			return semantic.ConstValue{}, false
		}
		return evalConstExprWithLookup(recovery.Value, lookup, call, typeOf)
	case *ast.TupleExpr:
		elems := make([]semantic.ConstValue, 0, len(n.Elems))
		for _, elem := range n.Elems {
			value, ok := evalConstExprWithLookup(elem, lookup, call, typeOf)
			if !ok {
				return semantic.ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return semantic.ConstValue{Kind: semantic.ConstTuple, Elems: elems}, true
	case *ast.ListLitExpr:
		if n.Owner != nil {
			return semantic.ConstValue{}, false
		}
		elems := make([]semantic.ConstValue, 0, len(n.Elems))
		for i, elem := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				return semantic.ConstValue{}, false
			}
			value, ok := evalConstExprWithLookup(elem, lookup, call, typeOf)
			if !ok {
				return semantic.ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return semantic.ConstValue{Kind: semantic.ConstList, Elems: elems}, true
	case *ast.StructLitExpr:
		if len(n.Spreads) != 0 {
			for _, spread := range n.Spreads {
				if spread != nil {
					return semantic.ConstValue{}, false
				}
			}
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		fields := map[string]semantic.ConstValue{}
		for i, arg := range args {
			if i >= len(n.ArgNames) {
				return semantic.ConstValue{}, false
			}
			fieldName := n.ArgNames[i]
			if fieldName == "" {
				return semantic.ConstValue{}, false
			}
			value, ok := evalConstExprWithLookup(arg, lookup, call, typeOf)
			if !ok {
				return semantic.ConstValue{}, false
			}
			fields[fieldName] = value
		}
		return semantic.ConstValue{Kind: semantic.ConstRecord, Fields: fields}, true
	case *ast.IndexExpr:
		object, ok := evalConstExprWithLookup(n.Object, lookup, call, typeOf)
		if !ok {
			return semantic.ConstValue{}, false
		}
		index, ok := evalConstExprWithLookup(n.Index, lookup, call, typeOf)
		if !ok || index.Kind != semantic.ConstInt {
			return semantic.ConstValue{}, false
		}
		if index.Int >= 0 {
			slot := int(index.Int)
			if slot < len(object.Elems) && (object.Kind == semantic.ConstTuple || object.Kind == semantic.ConstList) {
				return object.Elems[slot], true
			}
		}
		if n.Fallback != nil {
			return evalConstExprWithLookup(n.Fallback, lookup, call, typeOf)
		}
		return semantic.ConstValue{}, false
	case *ast.CallExpr:
		if call == nil {
			return semantic.ConstValue{}, false
		}
		return call(n)
	default:
		return semantic.ConstValue{}, false
	}
}

func evalBackendConstAggregateFieldExpr(expr *ast.FieldExpr, lookup func(string) (semantic.ConstValue, bool), call func(*ast.CallExpr) (semantic.ConstValue, bool), typeOf func(ast.Expr) semantic.Type) (semantic.ConstValue, bool) {
	if expr == nil || expr.Object == nil {
		return semantic.ConstValue{}, false
	}
	object, ok := evalConstExprWithLookup(expr.Object, lookup, call, typeOf)
	if !ok {
		return semantic.ConstValue{}, false
	}
	switch expr.Field {
	case "count":
		if object.Kind == semantic.ConstList || object.Kind == semantic.ConstTuple {
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: int64(len(object.Elems))}, true
		}
	case "len":
		// The analyzer's evalConstAggregateFieldExpr folds a compile-time string's `.len` to its
		// byte length (strlen for a cstr receiver); this copy must agree, or the analyzer accepts
		// `"abc".len` and the backend has no way to lower it.
		if object.Kind == semantic.ConstString {
			var receiverType semantic.Type
			if typeOf != nil {
				receiverType = typeOf(expr.Object)
			}
			if length, ok := semantic.ConstStringLen(object.String, receiverType); ok {
				return semantic.ConstValue{Kind: semantic.ConstInt, Int: length}, true
			}
		}
	default:
		if value, ok := semantic.ConstReflectionRecordField(object, expr.Field); ok {
			return value, true
		}
	}
	return semantic.ConstValue{}, false
}
