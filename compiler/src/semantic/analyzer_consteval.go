package semantic

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

const StaticEvalCallDepthLimit = 64
const staticEvalLoopIterationLimit = 100000

func (a *Analyzer) resolveArrayType(expr *ast.ArrayType) Type {
	arr := &ArrayType{Elem: a.resolveType(expr.Elem), Size: a.exprSummary(expr.Size)}
	value, ok := a.evalConstExpr(expr.Size)
	if !ok || value.Kind != ConstInt {
		if ident, identOK := expr.Size.(*ast.Ident); identOK {
			if _, paramOK := a.lookupConstParam(ident.Name); paramOK {
				arr.ConstParam = ident.Name
				return arr
			}
		}
		a.errorf(expr.Size.Pos(), "array size must be a compile-time integer")
		return arr
	}
	if value.Int < 0 {
		a.errorf(expr.Size.Pos(), "array size must be non-negative, got %d", value.Int)
		return arr
	}
	arr.HasConstSize = true
	arr.ConstSize = value.Int
	return arr
}

func (a *Analyzer) checkConstantArrayIndexBounds(arr *ArrayType, indexExpr ast.Expr) {
	if arr == nil || !arr.HasConstSize {
		return
	}
	value, ok := a.evalConstExpr(indexExpr)
	if !ok || value.Kind != ConstInt {
		return
	}
	if value.Int < 0 || value.Int >= arr.ConstSize {
		a.errorf(indexExpr.Pos(), "constant index %d out of bounds for %s", value.Int, arr)
	}
}

func (a *Analyzer) checkConstantArraySliceBounds(arr *ArrayType, startExpr ast.Expr, endExpr ast.Expr) {
	if arr == nil || !arr.HasConstSize {
		return
	}
	start, startOK := a.evalConstExpr(startExpr)
	end, endOK := a.evalConstExpr(endExpr)
	if !startOK || !endOK || start.Kind != ConstInt || end.Kind != ConstInt {
		return
	}
	if start.Int < 0 || start.Int > arr.ConstSize {
		a.errorf(startExpr.Pos(), "constant slice start %d out of bounds for %s", start.Int, arr)
	}
	if end.Int < 0 || end.Int > arr.ConstSize {
		a.errorf(endExpr.Pos(), "constant slice end %d out of bounds for %s", end.Int, arr)
	}
	if start.Int > end.Int {
		a.errorf(startExpr.Pos(), "constant slice start %d is after end %d for %s", start.Int, end.Int, arr)
	}
}

func (a *Analyzer) evalConstBoolExpr(expr ast.Expr) (bool, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstBool {
		return false, false
	}
	return value.Bool, true
}

func (a *Analyzer) evalConstStringExpr(expr ast.Expr) (string, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstString {
		return "", false
	}
	return value.String, true
}

func (a *Analyzer) analyzeStaticAssert(pos lexer.Pos, cond ast.Expr, message ast.Expr) {
	a.staticContextDepth++
	defer func() { a.staticContextDepth-- }()
	condType := a.analyzeCondExpr(cond)
	if !IsBoolType(condType) {
		a.errorf(pos, "static assert condition must be bool, got %s", condType)
		return
	}
	if message != nil {
		a.analyzeExpr(message)
	}
	if cond, ok := a.evalConstBoolExpr(cond); ok && !cond {
		if message != nil {
			if msg, msgOK := a.evalConstStringExpr(message); msgOK {
				a.errorf(pos, "static assert failed: %s", msg)
			} else {
				a.errorf(pos, "static assert failed")
			}
		} else {
			a.errorf(pos, "static assert failed")
		}
	}
}

func (a *Analyzer) analyzeStaticOnlyStmts(stmts []ast.Stmt) {
	a.staticContextDepth++
	a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
	_, _, ok := a.evalStaticStmtBlock(stmts, false)
	a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	a.staticContextDepth--
	if !ok {
		return
	}
}

func (a *Analyzer) evalConstExpr(expr ast.Expr) (ConstValue, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		// constEvalExpectedType lets a declared u64/usize/uintptr const accept a suffixless literal
		// that exceeds i64 max (it stands in for the `u64` suffix). nil in non-const contexts, where
		// this reduces to ParseIntLiteral.
		value, ok := ParseIntLiteralForType(n, a.constEvalExpectedType)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.FloatLit:
		value, ok := ParseFloatLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstFloat, Float: value}, true
	case *ast.BoolLit:
		return ConstValue{Kind: ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return ConstValue{Kind: ConstString, String: n.Value}, true
	case *ast.CharLit:
		value, ok := ParseCharLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.Ident:
		if value, ok := a.lookupConstEvalValue(n.Name); ok {
			return value, true
		}
		if t, ok := a.lookupConstParam(n.Name); ok {
			if valueType, ok := t.(*ConstValueType); ok && valueType != nil {
				return valueType.Value, true
			}
			return ConstValue{}, false
		}
		value, ok := a.lookupVisibleConst(n.Name)
		return value, ok
	case *ast.FieldExpr:
		if value, ok := a.evalConstAggregateFieldExpr(n); ok {
			return value, true
		}
		if name, ok := constFieldExprName(n); ok {
			if value, exists := a.constValues[name]; exists {
				return value, true
			}
		}
		ident, ok := n.Object.(*ast.Ident)
		if !ok {
			return ConstValue{}, false
		}
		for _, candidate := range a.visibleNameCandidates(ident.Name) {
			if value, ok := a.constValues[candidate+"."+n.Field]; ok {
				return value, true
			}
		}
		return ConstValue{}, false
	case *ast.ShorthandMemberExpr:
		constEnumType, ok := a.exprTypes[n].(*ConstEnumType)
		if !ok || constEnumType == nil {
			return ConstValue{}, false
		}
		member, ok := constEnumType.Member(strings.Join(n.Parts, "."))
		if !ok || member == nil {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: member.Value}, true
	case *ast.ParenExpr:
		return a.evalConstExpr(n.Inner)
	case *ast.CastExpr:
		operand, ok := a.evalConstExpr(n.Operand)
		if !ok {
			return ConstValue{}, false
		}
		return CastConstValue(operand, a.resolveType(n.Target))
	case *ast.MoveExpr:
		return a.evalConstExpr(n.Operand)
	case *ast.UnaryExpr:
		operand, ok := a.evalConstExpr(n.Operand)
		if !ok {
			return ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			switch operand.Kind {
			case ConstInt:
				return ConstValue{Kind: ConstInt, Int: -operand.Int}, true
			case ConstFloat:
				return ConstValue{Kind: ConstFloat, Float: -operand.Float}, true
			default:
				return ConstValue{}, false
			}
		case lexer.TOKEN_TILDE:
			if operand.Kind != ConstInt {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: ^operand.Int}, true
		default:
			return ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := a.evalConstExpr(n.Left)
		if !ok {
			return ConstValue{}, false
		}
		if n.Op == lexer.TOKEN_IN {
			list, ok := a.membershipCandidateList(n.Right)
			if !ok || list == nil {
				return ConstValue{}, false
			}
			for _, elem := range list.Elems {
				if rangeExpr, ok := elem.(*ast.MembershipRangeExpr); ok {
					matched, ok := a.evalConstMembershipRange(left, rangeExpr)
					if !ok {
						return ConstValue{}, false
					}
					if matched.Bool {
						return matched, true
					}
					continue
				}
				candidate, ok := a.evalConstExpr(elem)
				if !ok {
					return ConstValue{}, false
				}
				matched, ok := a.evalConstEquality(left, candidate, true)
				if !ok {
					return ConstValue{}, false
				}
				if matched.Bool {
					return matched, true
				}
			}
			return ConstValue{Kind: ConstBool, Bool: false}, true
		}
		right, ok := a.evalConstExpr(n.Right)
		if !ok {
			return ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != ConstBool || right.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != ConstBool || right.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return a.evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return a.evalConstEquality(left, right, false)
		case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			if result, ok := evalConstNumericBinary(n.Op, left, right); ok {
				return result, true
			}
			return ConstValue{}, false
		default:
			return ConstValue{}, false
		}
	case *ast.TernaryExpr:
		cond, ok := a.evalConstBoolExpr(n.Cond)
		if !ok {
			return ConstValue{}, false
		}
		if cond {
			return a.evalConstExpr(n.Value)
		}
		return a.evalConstExpr(n.Alt)
	case *ast.UnwrapElseExpr:
		return a.evalConstUnwrapElseExpr(n)
	case *ast.GetExpr:
		return a.evalConstGetExpr(n)
	case *ast.TupleExpr:
		elems := make([]ConstValue, 0, len(n.Elems))
		for _, elem := range n.Elems {
			value, ok := a.evalConstExpr(elem)
			if !ok {
				return ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return ConstValue{Kind: ConstTuple, Elems: elems}, true
	case *ast.ListLitExpr:
		if n.Owner != nil {
			return ConstValue{}, false
		}
		elems := make([]ConstValue, 0, len(n.Elems))
		for i, elem := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				return ConstValue{}, false
			}
			value, ok := a.evalConstExpr(elem)
			if !ok {
				return ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return ConstValue{Kind: ConstList, Elems: elems}, true
	case *ast.StructLitExpr:
		if len(n.Spreads) != 0 {
			for _, spread := range n.Spreads {
				if spread != nil {
					return ConstValue{}, false
				}
			}
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		fields := map[string]ConstValue{}
		for i, arg := range args {
			if i >= len(n.ArgNames) {
				return ConstValue{}, false
			}
			fieldName := n.ArgNames[i]
			if fieldName == "" {
				return ConstValue{}, false
			}
			value, ok := a.evalConstExpr(arg)
			if !ok {
				return ConstValue{}, false
			}
			fields[fieldName] = value
		}
		return ConstValue{Kind: ConstRecord, Fields: fields}, true
	case *ast.IndexExpr:
		return a.evalConstIndexExpr(n)
	case *ast.QueryExpr:
		return a.evalConstQueryExpr(n)
	case *ast.CallExpr:
		if value, ok := a.evalConstReflectionCall(n); ok {
			return value, true
		}
		return a.evalStaticFunctionCall(n)
	default:
		return ConstValue{}, false
	}
}

func constFieldExprName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := constFieldExprName(n.Object)
		if !ok {
			return "", false
		}
		return base + "." + n.Field, true
	default:
		return "", false
	}
}

func (a *Analyzer) evalConstIndexExpr(expr *ast.IndexExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	object, ok := a.evalConstExpr(expr.Object)
	if !ok {
		return ConstValue{}, false
	}
	index, ok := a.evalConstExpr(expr.Index)
	if !ok || index.Kind != ConstInt {
		return ConstValue{}, false
	}
	if index.Int >= 0 {
		slot := int(index.Int)
		if slot < len(object.Elems) && (object.Kind == ConstTuple || object.Kind == ConstList) {
			return object.Elems[slot], true
		}
	}
	if expr.Fallback != nil {
		return a.evalConstExpr(expr.Fallback)
	}
	return ConstValue{}, false
}

func (a *Analyzer) evalConstQueryExpr(expr *ast.QueryExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	source, ok := a.evalConstExpr(expr.Source)
	if !ok || (source.Kind != ConstList && source.Kind != ConstTuple) {
		return ConstValue{}, false
	}
	switch expr.Kind {
	case ast.QueryExprAny:
		for _, elem := range source.Elems {
			match, ok := a.evalConstQueryFilter(expr, elem)
			if !ok {
				return ConstValue{}, false
			}
			if match {
				return ConstValue{Kind: ConstBool, Bool: true}, true
			}
		}
		return ConstValue{Kind: ConstBool, Bool: false}, true
	case ast.QueryExprAll:
		for _, elem := range source.Elems {
			match, ok := a.evalConstQueryFilter(expr, elem)
			if !ok {
				return ConstValue{}, false
			}
			if !match {
				return ConstValue{Kind: ConstBool, Bool: false}, true
			}
		}
		return ConstValue{Kind: ConstBool, Bool: true}, true
	case ast.QueryExprCount:
		total := int64(0)
		for _, elem := range source.Elems {
			match, ok := a.evalConstQueryFilter(expr, elem)
			if !ok {
				return ConstValue{}, false
			}
			if match {
				total++
			}
		}
		return ConstValue{Kind: ConstInt, Int: total}, true
	case ast.QueryExprFirst:
		for _, elem := range source.Elems {
			include, value, ok := a.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return ConstValue{}, false
			}
			if include {
				return constOptionalFromFirstProjection(value), true
			}
		}
		return ConstValue{Kind: ConstOptional}, true
	case ast.QueryExprEach:
		elems := make([]ConstValue, 0, len(source.Elems))
		for _, elem := range source.Elems {
			include, value, ok := a.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return ConstValue{}, false
			}
			if include {
				elems = append(elems, cloneConstValue(value))
			}
		}
		return ConstValue{Kind: ConstList, Elems: elems}, true
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) evalConstQueryFilter(expr *ast.QueryExpr, item ConstValue) (bool, bool) {
	if expr == nil {
		return false, false
	}
	result, ok := a.evalConstQueryInItemScope(expr, item, func() (ConstValue, bool) {
		if expr.Filter == nil {
			return ConstValue{Kind: ConstBool, Bool: true}, true
		}
		return a.evalConstExpr(expr.Filter)
	})
	if !ok || result.Kind != ConstBool {
		return false, false
	}
	return result.Bool, true
}

func (a *Analyzer) evalConstQueryProjectedValue(expr *ast.QueryExpr, item ConstValue) (bool, ConstValue, bool) {
	if expr.Projection == nil {
		include, ok := a.evalConstQueryFilter(expr, item)
		return include, item, ok
	}
	return a.evalConstQueryProjection(expr, item)
}

func (a *Analyzer) evalConstQueryProjection(expr *ast.QueryExpr, item ConstValue) (bool, ConstValue, bool) {
	include, ok := a.evalConstQueryFilter(expr, item)
	if !ok || !include {
		return include, ConstValue{}, ok
	}
	value, ok := a.evalConstQueryInItemScope(expr, item, func() (ConstValue, bool) {
		return a.evalConstExpr(expr.Projection)
	})
	return true, value, ok
}

func (a *Analyzer) evalConstQueryInItemScope(expr *ast.QueryExpr, item ConstValue, eval func() (ConstValue, bool)) (ConstValue, bool) {
	scope := map[string]ConstValue{}
	if expr != nil {
		pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name})
		if expr.Pattern != nil {
			pattern = expr.Pattern
		}
		var ok bool
		scope, ok = a.bindConstMovePattern(pattern, item)
		if !ok {
			return ConstValue{}, false
		}
	}
	a.constEvalScopes = append(a.constEvalScopes, scope)
	if expr != nil && expr.PatternFilter != nil {
		matched, bindings, ok := a.evalConstQueryPatternFilter(expr)
		if !ok {
			a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
			return ConstValue{}, ok
		}
		if !matched {
			a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
			return ConstValue{Kind: ConstBool, Bool: false}, true
		}
		for name, value := range bindings {
			scope[name] = value
		}
		a.constEvalScopes[len(a.constEvalScopes)-1] = scope
	}
	value, ok := eval()
	a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	return value, ok
}

func (a *Analyzer) evalConstQueryPatternFilter(expr *ast.QueryExpr) (bool, map[string]ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return true, nil, true
	}
	subject, ok := a.evalConstQueryPatternFilterSubject(expr)
	if !ok {
		return false, nil, false
	}
	matched, bindings, ok := a.evalStaticMatchPattern(expr.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func (a *Analyzer) evalConstQueryPatternFilterSubject(expr *ast.QueryExpr) (ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return ConstValue{}, false
	}
	if expr.PatternFilterSubject != "" {
		if value, _, ok := a.constEvalValueScope(expr.PatternFilterSubject); ok {
			return value, true
		}
		return ConstValue{}, false
	}
	if expr.Name != "" && expr.Name != "_" {
		if value, _, ok := a.constEvalValueScope(expr.Name); ok {
			return value, true
		}
	}
	return ConstValue{}, false
}

func constMoveBindFieldName(arg ast.MoveBindArg) string {
	if arg.Field != "" {
		return arg.Field
	}
	return arg.Name
}

func (a *Analyzer) bindConstMovePattern(pattern ast.MoveBindPattern, item ConstValue) (map[string]ConstValue, bool) {
	scope := map[string]ConstValue{}
	if pattern == nil {
		return scope, true
	}
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p == nil || p.Name == "" || p.Name == "_" {
			return scope, true
		}
		scope[p.Name] = cloneConstValue(item)
		return scope, true
	case *ast.MoveBindTuplePattern:
		if p == nil || (item.Kind != ConstTuple && item.Kind != ConstList) || len(item.Elems) < len(p.Args) {
			return nil, false
		}
		for index, arg := range p.Args {
			if arg.Name == "" || arg.Name == "_" {
				continue
			}
			scope[arg.Name] = cloneConstValue(item.Elems[index])
		}
		return scope, true
	case *ast.MoveBindStructPattern:
		if p == nil || item.Kind != ConstRecord {
			return nil, false
		}
		for _, arg := range p.Args {
			if arg.Name == "" || arg.Name == "_" {
				continue
			}
			fieldValue, ok := ConstReflectionRecordField(item, constMoveBindFieldName(arg))
			if !ok {
				return nil, false
			}
			scope[arg.Name] = cloneConstValue(fieldValue)
		}
		return scope, true
	default:
		return nil, false
	}
}

func (a *Analyzer) evalConstAggregateFieldExpr(expr *ast.FieldExpr) (ConstValue, bool) {
	if expr == nil || expr.Object == nil {
		return ConstValue{}, false
	}
	object, ok := a.evalConstExpr(expr.Object)
	if !ok {
		return ConstValue{}, false
	}
	switch expr.Field {
	case "count":
		if object.Kind == ConstList || object.Kind == ConstTuple {
			return ConstValue{Kind: ConstInt, Int: int64(len(object.Elems))}, true
		}
	default:
		if value, ok := ConstReflectionRecordField(object, expr.Field); ok {
			return value, true
		}
	}
	return ConstValue{}, false
}

func (a *Analyzer) evalConstReflectionCall(expr *ast.CallExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	if fieldExpr, ok := expr.Func.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Field == "has_field" && len(expr.Args) == 1 && expr.NamedArgCount() == 0 {
		object, objectOK := a.evalConstExpr(fieldExpr.Object)
		fieldName, fieldOK := a.evalConstExpr(expr.Args[0])
		if !objectOK || !fieldOK || fieldName.Kind != ConstString {
			return ConstValue{}, false
		}
		return ConstReflectionRecordHasField(object, fieldName.String)
	}
	name, _ := QualifiedNameExpr(expr.Func)
	if name != "variants" && name != "fields" {
		return ConstValue{}, false
	}
	return ConstReflectionCallValue(name, expr.Args, func(typeName string) (Type, bool) {
		t, _, ok := a.lookupVisibleType(typeName)
		return t, ok
	})
}

func (a *Analyzer) lookupConstEvalValue(name string) (ConstValue, bool) {
	for i := len(a.constEvalScopes) - 1; i >= 0; i-- {
		if value, ok := a.constEvalScopes[i][name]; ok {
			return value, true
		}
	}
	return ConstValue{}, false
}

func (a *Analyzer) evalConstUnwrapElseExpr(expr *ast.UnwrapElseExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	value, ok := a.evalConstExpr(expr.Value)
	if !ok || value.Kind != ConstOptional {
		return ConstValue{}, false
	}
	if value.Some {
		if value.Value == nil {
			return ConstValue{}, false
		}
		return cloneConstValue(*value.Value), true
	}
	recovery := expr.Recovery
	if recovery == nil && expr.Fallback != nil {
		recovery = &ast.RecoveryClause{Position: expr.Fallback.Pos(), Kind: ast.RecoveryValue, Value: expr.Fallback}
	}
	if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
		return ConstValue{}, false
	}
	return a.evalConstExpr(recovery.Value)
}

func (a *Analyzer) evalConstGetExpr(expr *ast.GetExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	// `get arr[i] else <value>`: the index already carries its value fallback.
	if idx, ok := expr.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
		return a.evalConstExpr(idx)
	}
	value, ok := a.evalConstExpr(expr.Value)
	if !ok || value.Kind != ConstOptional {
		return ConstValue{}, false
	}
	if value.Some {
		if value.Value == nil {
			return ConstValue{}, false
		}
		return cloneConstValue(*value.Value), true
	}
	recovery := expr.Recovery
	if recovery == nil && expr.Fallback != nil {
		recovery = &ast.RecoveryClause{Position: expr.Fallback.Pos(), Kind: ast.RecoveryValue, Value: expr.Fallback}
	}
	if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
		return ConstValue{}, false
	}
	return a.evalConstExpr(recovery.Value)
}

func (a *Analyzer) evalStaticFunctionCall(expr *ast.CallExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	if a.staticCallDepth >= StaticEvalCallDepthLimit {
		a.errorf(expr.Pos(), "static function evaluation exceeded %d calls", StaticEvalCallDepthLimit)
		return ConstValue{}, false
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return ConstValue{}, false
	}
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
	if !ok || sym == nil {
		return ConstValue{}, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return ConstValue{}, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil {
		return ConstValue{}, false
	}
	// Static functions and laws are both const-evaluable: a law is enforced pure (docs/85), so
	// evaluating its body on constant arguments is sound — this is how a refinement obligation on a
	// constant value is discharged (or refuted) at compile time.
	if !fnType.Static && !decl.IsLaw {
		return ConstValue{}, false
	}
	args := expr.Args
	if expr.ResolvedArgsValid {
		args = expr.ResolvedArgs
	}
	if len(decl.Params) != len(args) {
		return ConstValue{}, false
	}
	scope := make(map[string]ConstValue, len(decl.Params))
	for i, arg := range args {
		value, ok := a.evalConstExpr(arg)
		if !ok {
			return ConstValue{}, false
		}
		scope[decl.Params[i].Name] = value
	}
	a.staticCallDepth++
	a.constEvalScopes = append(a.constEvalScopes, scope)
	value, returned, ok := a.evalStaticStmtBlock(decl.Body, true)
	a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	a.staticCallDepth--
	if !returned {
		if isVoidType(fnType.Return) {
			return ConstValue{Kind: ConstUnknown}, true
		}
		return ConstValue{}, false
	}
	return value, ok
}

func (a *Analyzer) evalStaticStmtBlock(stmts []ast.Stmt, allowReturn bool) (ConstValue, bool, bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.ReturnStmt:
			if !allowReturn {
				a.errorf(n.Pos(), "return is not allowed in a static block")
				return ConstValue{}, false, false
			}
			value, ok := a.evalConstExpr(n.Value)
			return value, true, ok
		case *ast.VarDeclStmt:
			if n.Value == nil {
				a.errorf(n.Pos(), "static local %q must have a compile-time initializer", n.Name)
				return ConstValue{}, false, false
			}
			value, ok := a.evalConstExpr(n.Value)
			if !ok {
				a.errorf(n.Pos(), "static local %q initializer must evaluate at compile time", n.Name)
				return ConstValue{}, false, false
			}
			a.setConstEvalValue(n.Name, value)
		case *ast.AssignStmt:
			ident, ok := n.Target.(*ast.Ident)
			if !ok {
				a.errorf(n.Pos(), "static assignment target must be a local name")
				return ConstValue{}, false, false
			}
			value, ok := a.evalConstExpr(n.Value)
			if !ok {
				a.errorf(n.Pos(), "static assignment value must evaluate at compile time")
				return ConstValue{}, false, false
			}
			if !a.updateConstEvalValue(ident.Name, value) {
				a.errorf(n.Pos(), "unknown static local %q", ident.Name)
				return ConstValue{}, false, false
			}
		case *ast.StaticAssertStmt:
			if cond, ok := a.evalConstBoolExpr(n.Cond); ok && !cond {
				a.reportStaticAssertFailure(n.Pos(), n.Message)
				return ConstValue{}, false, false
			}
		case *ast.StaticAssertBlockStmt:
			for _, item := range n.Assertions {
				if cond, ok := a.evalConstBoolExpr(item.Cond); ok && !cond {
					a.reportStaticAssertFailure(item.Position, item.Message)
					return ConstValue{}, false, false
				}
			}
		case *ast.StaticErrorStmt:
			if msg, ok := a.evalConstStringExpr(n.Message); ok {
				a.errorf(n.Pos(), "static error: %s", msg)
			} else {
				a.errorf(n.Pos(), "static error triggered")
			}
			return ConstValue{}, false, false
		case *ast.StaticIfStmt:
			value, returned, ok := a.evalStaticStmtBlock(a.activeStmtBranch(n), allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.StaticBlockStmt:
			a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
			value, returned, ok := a.evalStaticStmtBlock(n.Body, allowReturn)
			a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.IfStmt:
			cond, ok := a.evalConstBoolExpr(n.Cond)
			if !ok {
				a.errorf(n.Pos(), "static if condition must evaluate to a compile-time bool")
				return ConstValue{}, false, false
			}
			branch := n.Else
			if cond {
				branch = n.Then
			} else {
				for _, elif := range n.Elifs {
					elifCond, ok := a.evalConstBoolExpr(elif.Cond)
					if !ok {
						a.errorf(elif.Position, "static elif condition must evaluate to a compile-time bool")
						return ConstValue{}, false, false
					}
					if elifCond {
						branch = elif.Body
						break
					}
				}
			}
			value, returned, ok := a.evalStaticStmtBlock(branch, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.WhileStmt:
			for i := 0; i < staticEvalLoopIterationLimit; i++ {
				cond, ok := a.evalConstBoolExpr(n.Cond)
				if !ok {
					a.errorf(n.Pos(), "static while condition must evaluate to a compile-time bool")
					return ConstValue{}, false, false
				}
				if !cond {
					break
				}
				value, returned, ok := a.evalStaticStmtBlock(n.Body, allowReturn)
				if !ok || returned {
					return value, returned, ok
				}
				if i == staticEvalLoopIterationLimit-1 {
					a.errorf(n.Pos(), "static while exceeded %d iterations", staticEvalLoopIterationLimit)
					return ConstValue{}, false, false
				}
			}
		case *ast.ForStmt:
			value, returned, ok := a.evalStaticForStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.IterForStmt:
			value, returned, ok := a.evalStaticIterForStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.MatchStmt:
			value, returned, ok := a.evalStaticMatchStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.PassStmt:
		case *ast.ExprStmt:
			if ok := a.evalStaticExprStmt(n.Expr); !ok {
				a.errorf(n.Pos(), "static expression statement must evaluate at compile time")
				return ConstValue{}, false, false
			}
		default:
			a.errorf(stmt.Pos(), "static block only allows compile-time assertions, errors, local bindings, assignments, conditionals, returns, nested static blocks, and expression statements")
			return ConstValue{}, false, false
		}
	}
	return ConstValue{}, false, true
}

func (a *Analyzer) evalStaticExprStmt(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if a.evalStaticDArrayPushExpr(expr) {
		return true
	}
	if a.evalStaticDArrayExtendExpr(expr) {
		return true
	}
	_, ok := a.evalConstExpr(expr)
	return ok
}

func (a *Analyzer) evalStaticDArrayPushExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" {
		return false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || ident == nil || len(call.Args) != 1 || call.NamedArgCount() != 0 {
		return false
	}
	current, scopeIndex, ok := a.constEvalValueScope(ident.Name)
	if !ok || current.Kind != ConstList {
		return false
	}
	value, ok := a.evalConstExpr(call.Args[0])
	if !ok {
		return false
	}
	updated := cloneConstValue(current)
	updated.Elems = append(updated.Elems, cloneConstValue(value))
	a.constEvalScopes[scopeIndex][ident.Name] = updated
	return true
}

func (a *Analyzer) evalStaticDArrayExtendExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" {
		return false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || ident == nil || len(call.Args) != 1 || call.NamedArgCount() != 0 {
		return false
	}
	current, scopeIndex, ok := a.constEvalValueScope(ident.Name)
	if !ok || current.Kind != ConstList {
		return false
	}
	source, ok := a.evalConstExpr(call.Args[0])
	if !ok || source.Kind != ConstList {
		return false
	}
	updated := cloneConstValue(current)
	for _, elem := range source.Elems {
		updated.Elems = append(updated.Elems, cloneConstValue(elem))
	}
	a.constEvalScopes[scopeIndex][ident.Name] = updated
	return true
}

func (a *Analyzer) evalStaticMatchStmt(stmt *ast.MatchStmt, allowReturn bool) (ConstValue, bool, bool) {
	if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "static match does not support in-store clauses")
		return ConstValue{}, false, false
	}
	value, ok := a.evalConstExpr(stmt.Value)
	if !ok {
		a.errorf(stmt.Value.Pos(), "static match value must evaluate at compile time")
		return ConstValue{}, false, false
	}
	for _, arm := range stmt.Arms {
		matched, bindings, ok := a.evalStaticMatchPattern(arm.Pattern, value)
		if !ok {
			a.errorf(arm.Pattern.Pos(), "static match only supports compile-time literal patterns, `_`, name binds, and `|` patterns")
			return ConstValue{}, false, false
		}
		if !matched {
			continue
		}
		scope := map[string]ConstValue{}
		for name, bindingValue := range bindings {
			scope[name] = bindingValue
		}
		a.constEvalScopes = append(a.constEvalScopes, scope)
		result, returned, ok := a.evalStaticStmtBlock(arm.Body, allowReturn)
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
		return result, returned, ok
	}
	return ConstValue{}, false, true
}

func (a *Analyzer) evalStaticMatchPattern(pattern ast.MatchPattern, value ConstValue) (bool, map[string]ConstValue, bool) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return true, nil, true
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return true, nil, true
		}
		return true, map[string]ConstValue{p.Name: value}, true
	case *ast.MatchLiteralPattern:
		patternValue, ok := a.evalConstExpr(p.Value)
		if !ok {
			return false, nil, false
		}
		equal, ok := a.evalConstEquality(value, patternValue, true)
		if !ok || equal.Kind != ConstBool {
			return false, nil, false
		}
		return equal.Bool, nil, true
	case *ast.MatchStringLiteralPattern:
		if value.Kind != ConstString {
			return false, nil, true
		}
		return value.String == p.Value, nil, true
	case *ast.MatchTuplePattern:
		if value.Kind != ConstTuple && value.Kind != ConstList {
			return false, nil, true
		}
		return a.evalStaticSequenceMatchPatterns(p.Elems, value.Elems)
	case *ast.MatchListPattern:
		if value.Kind != ConstList {
			return false, nil, true
		}
		return a.evalStaticSequenceMatchPatterns(p.Elems, value.Elems)
	case *ast.MatchStructPattern:
		if value.Kind != ConstRecord {
			return false, nil, true
		}
		return a.evalStaticStructMatchPatterns(matchStructPatternArgs(p), value)
	case *ast.MatchVariantPattern:
		if len(p.Args) != 0 {
			return false, nil, false
		}
		patternValue, ok := a.lookupVisibleConst(p.EnumName + "." + p.Variant)
		if !ok {
			return false, nil, false
		}
		equal, ok := a.evalConstEquality(value, patternValue, true)
		if !ok || equal.Kind != ConstBool {
			return false, nil, false
		}
		return equal.Bool, nil, true
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			matched, bindings, ok := a.evalStaticMatchPattern(option, value)
			if !ok {
				return false, nil, false
			}
			if matched {
				return true, bindings, true
			}
		}
		return false, nil, true
	default:
		return false, nil, false
	}
}

func (a *Analyzer) evalStaticSequenceMatchPatterns(patterns []ast.MatchPattern, elems []ConstValue) (bool, map[string]ConstValue, bool) {
	if len(patterns) == 0 {
		return len(elems) == 0, nil, true
	}
	if _, ok := patterns[len(patterns)-1].(*ast.MatchRestPattern); ok {
		if len(elems) < len(patterns)-1 {
			return false, nil, true
		}
	} else if len(elems) != len(patterns) {
		return false, nil, true
	}
	bindings := map[string]ConstValue{}
	limit := len(patterns)
	if _, ok := patterns[len(patterns)-1].(*ast.MatchRestPattern); ok {
		limit--
	}
	for i := 0; i < limit; i++ {
		matched, itemBindings, ok := a.evalStaticMatchPattern(patterns[i], elems[i])
		if !ok {
			return false, nil, false
		}
		if !matched {
			return false, nil, true
		}
		for name, value := range itemBindings {
			bindings[name] = value
		}
	}
	return true, bindings, true
}

func matchStructPatternArgs(pattern *ast.MatchStructPattern) []*ast.MatchPatternArg {
	if pattern == nil {
		return nil
	}
	if len(pattern.ResolvedArgs) != 0 {
		return pattern.ResolvedArgs
	}
	args := make([]*ast.MatchPatternArg, 0, len(pattern.Args))
	for i := range pattern.Args {
		arg := pattern.Args[i]
		args = append(args, &ast.MatchPatternArg{Position: arg.Position, Name: arg.Name, Pattern: arg.Pattern})
	}
	return args
}

func (a *Analyzer) evalStaticStructMatchPatterns(args []*ast.MatchPatternArg, value ConstValue) (bool, map[string]ConstValue, bool) {
	bindings := map[string]ConstValue{}
	for _, arg := range args {
		if arg == nil || arg.Name == "" {
			continue
		}
		fieldValue, ok := ConstReflectionRecordField(value, arg.Name)
		if !ok {
			return false, nil, true
		}
		if arg.Pattern == nil {
			continue
		}
		matched, itemBindings, ok := a.evalStaticMatchPattern(arg.Pattern, fieldValue)
		if !ok {
			return false, nil, false
		}
		if !matched {
			return false, nil, true
		}
		for name, value := range itemBindings {
			bindings[name] = value
		}
	}
	return true, bindings, true
}

func (a *Analyzer) evalStaticForStmt(stmt *ast.ForStmt, allowReturn bool) (ConstValue, bool, bool) {
	start, ok := a.evalConstExpr(stmt.Start)
	if !ok || start.Kind != ConstInt {
		a.errorf(stmt.Pos(), "static for start must evaluate to a compile-time integer")
		return ConstValue{}, false, false
	}
	end, ok := a.evalConstExpr(stmt.End)
	if !ok || end.Kind != ConstInt {
		a.errorf(stmt.Pos(), "static for end must evaluate to a compile-time integer")
		return ConstValue{}, false, false
	}
	step := int64(1)
	if stmt.Step != nil {
		stepValue, ok := a.evalConstExpr(stmt.Step)
		if !ok || stepValue.Kind != ConstInt {
			a.errorf(stmt.Pos(), "static for step must evaluate to a compile-time integer")
			return ConstValue{}, false, false
		}
		step = stepValue.Int
		if step < 0 {
			step = -step
		}
	}
	if step == 0 {
		a.errorf(stmt.Pos(), "static for step must not be zero")
		return ConstValue{}, false, false
	}
	ascending := start.Int <= end.Int
	if stmt.Op == lexer.TOKEN_RANGE_LT {
		ascending = true
	} else if stmt.Op == lexer.TOKEN_RANGE_GT {
		ascending = false
	}
	current := start.Int
	for i := 0; i < staticEvalLoopIterationLimit; i++ {
		if !staticForLoopContinue(stmt.Op, current, end.Int, ascending) {
			return ConstValue{}, false, true
		}
		a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{stmt.Name: ConstValue{Kind: ConstInt, Int: current}})
		value, returned, ok := a.evalStaticStmtBlock(stmt.Body, allowReturn)
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
		if !ok || returned {
			return value, returned, ok
		}
		if ascending {
			current += step
		} else {
			current -= step
		}
	}
	a.errorf(stmt.Pos(), "static for exceeded %d iterations", staticEvalLoopIterationLimit)
	return ConstValue{}, false, false
}

func (a *Analyzer) evalStaticIterForStmt(stmt *ast.IterForStmt, allowReturn bool) (ConstValue, bool, bool) {
	if stmt == nil {
		return ConstValue{}, false, false
	}
	if stmt.Mode != ast.IterBindValue {
		a.errorf(stmt.Pos(), "static iterable for currently supports value binding only")
		return ConstValue{}, false, false
	}
	source, ok := a.evalConstExpr(stmt.Source)
	if !ok || (source.Kind != ConstList && source.Kind != ConstTuple) {
		a.errorf(stmt.Pos(), "static iterable for source must evaluate to a compile-time list or tuple")
		return ConstValue{}, false, false
	}
	pattern := stmt.Pattern
	if pattern == nil {
		pattern = &ast.MoveBindNamePattern{Position: stmt.Pos(), Name: "_"}
	}
	elems := source.Elems
	for i := 0; i < len(elems); i++ {
		item := elems[i]
		if stmt.Reverse {
			item = elems[len(elems)-1-i]
		}
		scope, ok := a.bindConstMovePattern(pattern, item)
		if !ok {
			a.errorf(stmt.Pos(), "static iterable for pattern does not match compile-time item")
			return ConstValue{}, false, false
		}
		a.constEvalScopes = append(a.constEvalScopes, scope)
		if stmt.PatternFilter != nil {
			matched, bindings, ok := a.evalStaticIterPatternFilter(stmt)
			if !ok {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				a.errorf(stmt.PatternFilter.Pos(), "static iterable for pattern filter must be a compile-time matchable pattern")
				return ConstValue{}, false, false
			}
			if !matched {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				continue
			}
			for name, value := range bindings {
				scope[name] = value
			}
			a.constEvalScopes[len(a.constEvalScopes)-1] = scope
		}
		include := true
		if stmt.WhereFilter != nil {
			filter, ok := a.evalConstExpr(stmt.WhereFilter)
			if !ok || filter.Kind != ConstBool {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				a.errorf(stmt.WhereFilter.Pos(), "static iterable for where filter must evaluate to a compile-time bool")
				return ConstValue{}, false, false
			}
			include = include && filter.Bool
		}
		if stmt.Filter != nil {
			filter, ok := a.evalConstExpr(stmt.Filter)
			if !ok || filter.Kind != ConstBool {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				a.errorf(stmt.Filter.Pos(), "static iterable for filter must evaluate to a compile-time bool")
				return ConstValue{}, false, false
			}
			include = include && filter.Bool
		}
		if include {
			value, returned, ok := a.evalStaticStmtBlock(stmt.Body, allowReturn)
			if !ok || returned {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				return value, returned, ok
			}
		}
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	}
	return ConstValue{}, false, true
}

func (a *Analyzer) evalStaticIterPatternFilter(stmt *ast.IterForStmt) (bool, map[string]ConstValue, bool) {
	if stmt == nil || stmt.PatternFilter == nil {
		return true, nil, true
	}
	subjectName := stmt.PatternFilterSubject
	if subjectName == "" {
		if bind, ok := stmt.Pattern.(*ast.MoveBindNamePattern); ok && bind != nil && bind.Name != "" && bind.Name != "_" {
			subjectName = bind.Name
		}
	}
	subject, _, ok := a.constEvalValueScope(subjectName)
	if !ok {
		if subjectName == "" {
			return false, nil, true
		}
		return false, nil, false
	}
	matched, bindings, ok := a.evalStaticMatchPattern(stmt.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func staticForLoopContinue(op lexer.TokenKind, current int64, end int64, ascending bool) bool {
	switch op {
	case lexer.TOKEN_RANGE:
		if ascending {
			return current <= end
		}
		return current >= end
	case lexer.TOKEN_RANGE_LT:
		return current < end
	case lexer.TOKEN_RANGE_GT:
		return current > end
	default:
		return false
	}
}

func (a *Analyzer) validateStaticFunctionTotality(fn *ast.FuncDecl) {
	if fn == nil || !fn.Static {
		return
	}
	if !a.staticFunctionReturnsVoid(fn) && !staticStmtBlockAlwaysTerminates(fn.Body) {
		if pos, detail, ok := staticStmtBlockTerminationIssue(fn.Body); ok {
			a.errorf(pos, "static function %q must return on all paths: %s", fn.Name, detail)
		} else {
			a.errorf(fn.Pos(), "static function %q must return on all paths", fn.Name)
		}
	}
	a.validateStaticFunctionRecursion(fn)
}

func (a *Analyzer) staticFunctionReturnsVoid(fn *ast.FuncDecl) bool {
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	return ok && fnType != nil && isVoidType(fnType.Return)
}

func staticStmtBlockAlwaysTerminates(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if staticStmtAlwaysTerminates(stmt) {
			return true
		}
	}
	return false
}

func staticStmtBlockTerminationIssue(stmts []ast.Stmt) (lexer.Pos, string, bool) {
	if len(stmts) == 0 {
		return lexer.Pos{}, "the function body can fall through", true
	}
	for _, stmt := range stmts {
		if staticStmtAlwaysTerminates(stmt) {
			return lexer.Pos{}, "", false
		}
		if pos, detail, ok := staticStmtTerminationIssue(stmt); ok {
			return pos, detail, true
		}
	}
	last := stmts[len(stmts)-1]
	return last.Pos(), "control can fall through after this statement", true
}

func staticStmtAlwaysTerminates(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.StaticErrorStmt:
		return true
	case *ast.StaticBlockStmt:
		return staticStmtBlockAlwaysTerminates(n.Body)
	case *ast.IfStmt:
		return staticIfStmtAlwaysTerminates(n.Then, n.Elifs, n.Else)
	case *ast.StaticIfStmt:
		return staticStaticIfStmtAlwaysTerminates(n.Then, n.Elifs, n.Else)
	case *ast.MatchStmt:
		return staticMatchStmtAlwaysTerminates(n)
	default:
		return false
	}
}

func staticStmtTerminationIssue(stmt ast.Stmt) (lexer.Pos, string, bool) {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		if len(n.Else) == 0 {
			return n.Pos(), "this if statement has no else branch", true
		}
		if !staticStmtBlockAlwaysTerminates(n.Then) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Then); ok {
				return pos, "the then branch does not terminate: " + detail, true
			}
			return n.Pos(), "the then branch does not terminate", true
		}
		for _, elif := range n.Elifs {
			if !staticStmtBlockAlwaysTerminates(elif.Body) {
				if pos, detail, ok := staticStmtBlockTerminationIssue(elif.Body); ok {
					return pos, "an elif branch does not terminate: " + detail, true
				}
				return elif.Position, "an elif branch does not terminate", true
			}
		}
		if !staticStmtBlockAlwaysTerminates(n.Else) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Else); ok {
				return pos, "the else branch does not terminate: " + detail, true
			}
			return n.Pos(), "the else branch does not terminate", true
		}
	case *ast.StaticIfStmt:
		if len(n.Else) == 0 {
			return n.Pos(), "this static if statement has no else branch", true
		}
		if !staticStmtBlockAlwaysTerminates(n.Then) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Then); ok {
				return pos, "the then branch does not terminate: " + detail, true
			}
			return n.Pos(), "the then branch does not terminate", true
		}
		for _, elif := range n.Elifs {
			if !staticStmtBlockAlwaysTerminates(elif.Body) {
				if pos, detail, ok := staticStmtBlockTerminationIssue(elif.Body); ok {
					return pos, "a static elif branch does not terminate: " + detail, true
				}
				return elif.Position, "a static elif branch does not terminate", true
			}
		}
		if !staticStmtBlockAlwaysTerminates(n.Else) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Else); ok {
				return pos, "the else branch does not terminate: " + detail, true
			}
			return n.Pos(), "the else branch does not terminate", true
		}
	case *ast.MatchStmt:
		hasCatchAll := false
		for _, arm := range n.Arms {
			switch arm.Pattern.(type) {
			case *ast.MatchWildcardPattern, *ast.MatchBindPattern:
				hasCatchAll = true
			}
			if !staticStmtBlockAlwaysTerminates(arm.Body) {
				if pos, detail, ok := staticStmtBlockTerminationIssue(arm.Body); ok {
					return pos, "a match arm does not terminate: " + detail, true
				}
				return arm.Position, "a match arm does not terminate", true
			}
		}
		if !hasCatchAll {
			return n.Pos(), "this match statement has no catch-all arm", true
		}
	case *ast.StaticBlockStmt:
		return staticStmtBlockTerminationIssue(n.Body)
	}
	return stmt.Pos(), "this statement can fall through", true
}

func staticMatchStmtAlwaysTerminates(stmt *ast.MatchStmt) bool {
	if stmt == nil || len(stmt.Arms) == 0 {
		return false
	}
	hasWildcard := false
	for _, arm := range stmt.Arms {
		switch arm.Pattern.(type) {
		case *ast.MatchWildcardPattern, *ast.MatchBindPattern:
			hasWildcard = true
		}
		if !staticStmtBlockAlwaysTerminates(arm.Body) {
			return false
		}
	}
	return hasWildcard
}

func staticIfStmtAlwaysTerminates(then []ast.Stmt, elifs []ast.ElifClause, elseStmts []ast.Stmt) bool {
	if len(elseStmts) == 0 {
		return false
	}
	if !staticStmtBlockAlwaysTerminates(then) || !staticStmtBlockAlwaysTerminates(elseStmts) {
		return false
	}
	for _, elif := range elifs {
		if !staticStmtBlockAlwaysTerminates(elif.Body) {
			return false
		}
	}
	return true
}

func staticStaticIfStmtAlwaysTerminates(then []ast.Stmt, elifs []ast.StaticElifClause, elseStmts []ast.Stmt) bool {
	if len(elseStmts) == 0 {
		return false
	}
	if !staticStmtBlockAlwaysTerminates(then) || !staticStmtBlockAlwaysTerminates(elseStmts) {
		return false
	}
	for _, elif := range elifs {
		if !staticStmtBlockAlwaysTerminates(elif.Body) {
			return false
		}
	}
	return true
}

func (a *Analyzer) validateStaticFunctionRecursion(fn *ast.FuncDecl) {
	a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if ok {
			a.validateStaticRecursiveCall(fn, call)
		}
		return false
	})
}

func (a *Analyzer) validateStaticRecursiveCall(fn *ast.FuncDecl, call *ast.CallExpr) {
	if fn == nil || call == nil {
		return
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		return
	}
	if ident.Name != fn.Name {
		a.validateStaticIndirectRecursiveCall(fn, call, ident.Name)
		return
	}
	if paramName, ok := a.staticRecursiveCallDecreasingParam(fn, call); ok {
		if a.staticFunctionHasBaseCaseForParam(fn, paramName) {
			return
		}
		if !a.staticFunctionParamIsUnsigned(fn, paramName) && a.staticFunctionHasWeakSignedZeroBaseCaseForParam(fn, paramName) {
			a.errorf(call.Pos(), "recursive static call to %q uses `== 0` as the only visible base case for signed parameter %q; use a lower-bound check such as `%s <= 0`", fn.Name, paramName, paramName)
			return
		}
		a.errorf(call.Pos(), "recursive static call to %q must have a visible terminating base case for parameter %q", fn.Name, paramName)
		return
	}
	a.errorf(call.Pos(), "recursive static call to %q must decrease a parameter using parameter - positive_constant", fn.Name)
}

func (a *Analyzer) validateStaticIndirectRecursiveCall(fn *ast.FuncDecl, call *ast.CallExpr, calleeName string) {
	callee := a.staticFunctionDeclByName(calleeName)
	if callee == nil || callee == fn {
		return
	}
	if !a.staticFunctionEventuallyCalls(callee, fn.Name, map[*ast.FuncDecl]bool{}) {
		return
	}
	a.errorf(call.Pos(), "indirect recursive static call cycle involving %q and %q is not supported; use direct structurally decreasing recursion", fn.Name, callee.Name)
}

func (a *Analyzer) staticFunctionDeclByName(name string) *ast.FuncDecl {
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || !fnType.Static {
		return nil
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil || !decl.Static {
		return nil
	}
	return decl
}

func (a *Analyzer) staticFunctionEventuallyCalls(fn *ast.FuncDecl, targetName string, seen map[*ast.FuncDecl]bool) bool {
	if fn == nil {
		return false
	}
	if seen[fn] {
		return false
	}
	seen[fn] = true
	return a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Func.(*ast.Ident)
		if !ok {
			return false
		}
		if ident.Name == targetName {
			return true
		}
		callee := a.staticFunctionDeclByName(ident.Name)
		return callee != nil && a.staticFunctionEventuallyCalls(callee, targetName, seen)
	})
}

func (a *Analyzer) walkStaticStmts(stmts []ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	for _, stmt := range stmts {
		if a.walkStaticStmt(stmt, visitExpr) {
			return true
		}
	}
	return false
}

func (a *Analyzer) walkStaticStmt(stmt ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.VarDeclStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AugAssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AsRefAssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.ExprStmt:
		return a.walkStaticExpr(n.Expr, visitExpr)
	case *ast.StaticAssertStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticExpr(n.Message, visitExpr)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if a.walkStaticExpr(item.Cond, visitExpr) || a.walkStaticExpr(item.Message, visitExpr) {
				return true
			}
		}
		return false
	case *ast.StaticErrorStmt:
		return a.walkStaticExpr(n.Message, visitExpr)
	case *ast.IfStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Then, visitExpr) || a.walkStaticStmts(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if a.walkStaticExpr(elif.Cond, visitExpr) || a.walkStaticStmts(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticIfStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Then, visitExpr) || a.walkStaticStmts(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if a.walkStaticExpr(elif.Cond, visitExpr) || a.walkStaticStmts(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticBlockStmt:
		return a.walkStaticStmts(n.Body, visitExpr)
	case *ast.WhileStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.ForStmt:
		return a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr) || a.walkStaticExpr(n.Step, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.IterForStmt:
		return a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.WhereFilter, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.MatchStmt:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Store, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkStaticStmts(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.InStoreStmt:
		return a.walkStaticExpr(n.Store, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.CanStmt:
		return a.walkStaticStmts(n.Body, visitExpr)
	}
	return false
}

func (a *Analyzer) walkStaticExpr(expr ast.Expr, visitExpr func(ast.Expr) bool) bool {
	if expr == nil {
		return false
	}
	if visitExpr != nil && visitExpr(expr) {
		return true
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		return a.walkStaticExpr(n.Left, visitExpr) || a.walkStaticExpr(n.Right, visitExpr)
	case *ast.UnaryExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.MoveExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.ParenExpr:
		return a.walkStaticExpr(n.Inner, visitExpr)
	case *ast.CastExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.TernaryExpr:
		return a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticExpr(n.Alt, visitExpr)
	case *ast.FieldExpr:
		return a.walkStaticExpr(n.Object, visitExpr)
	case *ast.IndexExpr:
		return a.walkStaticExpr(n.Object, visitExpr) || a.walkStaticExpr(n.Index, visitExpr) || a.walkStaticExpr(n.Fallback, visitExpr)
	case *ast.SliceExpr:
		return a.walkStaticExpr(n.Object, visitExpr) || a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if a.walkStaticExpr(elem, visitExpr) {
				return true
			}
		}
		return a.walkStaticExpr(n.Owner, visitExpr)
	case *ast.MembershipRangeExpr:
		return a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if a.walkStaticExpr(elem, visitExpr) {
				return true
			}
		}
	case *ast.CallExpr:
		if a.walkStaticExpr(n.Func, visitExpr) || a.walkStaticExpr(n.SafeReceiver, visitExpr) {
			return true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
	case *ast.StructLitExpr:
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
		for _, spread := range n.Spreads {
			if a.walkStaticExpr(spread, visitExpr) {
				return true
			}
		}
	case *ast.RecordUpdateExpr:
		if a.walkStaticExpr(n.Base, visitExpr) {
			return true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
	case *ast.ListComprehensionExpr:
		return a.walkStaticStmts(n.Bindings, visitExpr) || a.walkStaticExpr(n.Key, visitExpr) || a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.RangeEnd, visitExpr) || a.walkStaticExpr(n.RangeStep, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkStaticExpr(n.Owner, visitExpr)
	case *ast.QueryExpr:
		return a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkStaticExpr(n.Projection, visitExpr) || a.walkStaticExpr(n.Owner, visitExpr)
	case *ast.LambdaExpr:
		return a.walkStaticStmts(n.Body, visitExpr) || a.walkStaticExpr(n.BodyExpr, visitExpr)
	case *ast.AddrOfExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.SpecializeExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	}
	return false
}

func (a *Analyzer) staticRecursiveCallDecreasingParam(fn *ast.FuncDecl, call *ast.CallExpr) (string, bool) {
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	if len(args) != len(fn.Params) {
		return "", false
	}
	for index, param := range fn.Params {
		if a.staticExprIsPositiveDecrementOfParam(args[index], param.Name) {
			return param.Name, true
		}
	}
	return "", false
}

func (a *Analyzer) staticExprIsPositiveDecrementOfParam(expr ast.Expr, paramName string) bool {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary.Op != lexer.TOKEN_MINUS {
		return false
	}
	ident, ok := binary.Left.(*ast.Ident)
	if !ok || ident.Name != paramName {
		return false
	}
	value, ok := a.evalStaticProofIntegerExpr(binary.Right)
	return ok && value > 0
}

func (a *Analyzer) evalStaticProofIntegerExpr(expr ast.Expr) (int64, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstInt {
		return 0, false
	}
	return value.Int, true
}

func (a *Analyzer) staticFunctionHasBaseCaseForParam(fn *ast.FuncDecl, paramName string) bool {
	if fn == nil {
		return false
	}
	return a.staticStmtsContainBaseCaseForParam(fn.Body, paramName, a.staticFunctionParamIsUnsigned(fn, paramName))
}

func (a *Analyzer) staticFunctionHasWeakSignedZeroBaseCaseForParam(fn *ast.FuncDecl, paramName string) bool {
	if fn == nil {
		return false
	}
	return a.staticStmtsContainWeakSignedZeroBaseCaseForParam(fn.Body, paramName)
}

func (a *Analyzer) staticFunctionParamIsUnsigned(fn *ast.FuncDecl, paramName string) bool {
	paramIndex := -1
	for index, param := range fn.Params {
		if param.Name == paramName {
			paramIndex = index
			break
		}
	}
	if paramIndex < 0 {
		return false
	}
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || paramIndex >= len(fnType.Params) {
		return false
	}
	signed, _, ok := BitIntInfo(fnType.Params[paramIndex])
	if ok {
		return !signed
	}
	builtin, ok := fnType.Params[paramIndex].(*BuiltinType)
	if !ok || builtin == nil {
		return false
	}
	switch builtin.Name {
	case "usize", "uintptr":
		return true
	default:
		return false
	}
}

func (a *Analyzer) staticStmtsContainBaseCaseForParam(stmts []ast.Stmt, paramName string, allowZeroEquality bool) bool {
	for index, stmt := range stmts {
		if a.staticStmtContainsBaseCaseForParam(stmt, paramName, allowZeroEquality) {
			return true
		}
		if a.staticStmtContainsFallthroughBaseCaseForParam(stmt, stmts[index+1:], paramName, allowZeroEquality) {
			return true
		}
	}
	return false
}

func (a *Analyzer) staticStmtsContainWeakSignedZeroBaseCaseForParam(stmts []ast.Stmt, paramName string) bool {
	for index, stmt := range stmts {
		if a.staticStmtContainsWeakSignedZeroBaseCaseForParam(stmt, paramName) {
			return true
		}
		if a.staticStmtContainsFallthroughWeakSignedZeroBaseCaseForParam(stmt, stmts[index+1:], paramName) {
			return true
		}
	}
	return false
}

func (a *Analyzer) staticStmtContainsFallthroughBaseCaseForParam(stmt ast.Stmt, following []ast.Stmt, paramName string, allowZeroEquality bool) bool {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName, allowZeroEquality)
	case *ast.StaticIfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName, allowZeroEquality)
	default:
		return false
	}
}

func (a *Analyzer) staticStmtContainsFallthroughWeakSignedZeroBaseCaseForParam(stmt ast.Stmt, following []ast.Stmt, paramName string) bool {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName)
	case *ast.StaticIfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName)
	default:
		return false
	}
}

func appendStaticStmtLists(first []ast.Stmt, second []ast.Stmt) []ast.Stmt {
	if len(first) == 0 {
		return second
	}
	if len(second) == 0 {
		return first
	}
	combined := make([]ast.Stmt, 0, len(first)+len(second))
	combined = append(combined, first...)
	combined = append(combined, second...)
	return combined
}

func (a *Analyzer) staticStmtContainsBaseCaseForParam(stmt ast.Stmt, paramName string, allowZeroEquality bool) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.staticReturnExprContainsBaseCaseForParam(n.Value, paramName, allowZeroEquality)
	case *ast.IfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, n.Else, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Then, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Else, paramName, allowZeroEquality)
	case *ast.StaticIfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, n.Else, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Then, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Else, paramName, allowZeroEquality)
	case *ast.StaticBlockStmt:
		return a.staticStmtsContainBaseCaseForParam(n.Body, paramName, allowZeroEquality)
	default:
		return false
	}
}

func (a *Analyzer) staticStmtContainsWeakSignedZeroBaseCaseForParam(stmt ast.Stmt, paramName string) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.staticReturnExprContainsWeakSignedZeroBaseCaseForParam(n.Value, paramName)
	case *ast.IfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, n.Else, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Then, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Else, paramName)
	case *ast.StaticIfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, n.Else, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Then, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Else, paramName)
	case *ast.StaticBlockStmt:
		return a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Body, paramName)
	default:
		return false
	}
}

func (a *Analyzer) staticReturnExprContainsBaseCaseForParam(expr ast.Expr, paramName string, allowZeroEquality bool) bool {
	ternary, ok := expr.(*ast.TernaryExpr)
	if !ok {
		return false
	}
	then := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Value.Pos(), Value: ternary.Value}}
	alt := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Alt.Pos(), Value: ternary.Alt}}
	return a.staticIfContainsBaseCaseForParam(ternary.Cond, then, alt, paramName, allowZeroEquality)
}

func (a *Analyzer) staticReturnExprContainsWeakSignedZeroBaseCaseForParam(expr ast.Expr, paramName string) bool {
	ternary, ok := expr.(*ast.TernaryExpr)
	if !ok {
		return false
	}
	then := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Value.Pos(), Value: ternary.Value}}
	alt := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Alt.Pos(), Value: ternary.Alt}}
	return a.staticIfContainsWeakSignedZeroBaseCaseForParam(ternary.Cond, then, alt, paramName)
}

func (a *Analyzer) staticIfContainsBaseCaseForParam(cond ast.Expr, then []ast.Stmt, elseStmts []ast.Stmt, paramName string, allowZeroEquality bool) bool {
	comparison, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	leftParam, leftOK := staticExprIdentName(comparison.Left)
	rightValue, rightOK := a.evalStaticProofIntegerExpr(comparison.Right)
	if leftOK && rightOK && leftParam == paramName {
		return staticComparisonBranchTerminates(comparison.Op, rightValue, then, elseStmts, allowZeroEquality)
	}
	rightParam, rightParamOK := staticExprIdentName(comparison.Right)
	leftValue, leftValueOK := a.evalStaticProofIntegerExpr(comparison.Left)
	if rightParamOK && leftValueOK && rightParam == paramName {
		return staticComparisonBranchTerminates(staticReverseComparisonOp(comparison.Op), leftValue, then, elseStmts, allowZeroEquality)
	}
	return false
}

func (a *Analyzer) staticIfContainsWeakSignedZeroBaseCaseForParam(cond ast.Expr, then []ast.Stmt, elseStmts []ast.Stmt, paramName string) bool {
	comparison, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	leftParam, leftOK := staticExprIdentName(comparison.Left)
	rightValue, rightOK := a.evalStaticProofIntegerExpr(comparison.Right)
	if leftOK && rightOK && leftParam == paramName {
		return staticWeakSignedZeroComparisonBranchTerminates(comparison.Op, rightValue, then, elseStmts)
	}
	rightParam, rightParamOK := staticExprIdentName(comparison.Right)
	leftValue, leftValueOK := a.evalStaticProofIntegerExpr(comparison.Left)
	if rightParamOK && leftValueOK && rightParam == paramName {
		return staticWeakSignedZeroComparisonBranchTerminates(staticReverseComparisonOp(comparison.Op), leftValue, then, elseStmts)
	}
	return false
}

func staticComparisonBranchTerminates(op lexer.TokenKind, value int64, then []ast.Stmt, elseStmts []ast.Stmt, allowZeroEquality bool) bool {
	if staticParamComparisonTerminatesOnThen(op, value, allowZeroEquality) && staticStmtBlockAlwaysTerminates(then) {
		return true
	}
	return staticParamComparisonTerminatesOnElse(op, value, allowZeroEquality) && staticStmtBlockAlwaysTerminates(elseStmts)
}

func staticWeakSignedZeroComparisonBranchTerminates(op lexer.TokenKind, value int64, then []ast.Stmt, elseStmts []ast.Stmt) bool {
	if value != 0 {
		return false
	}
	switch op {
	case lexer.TOKEN_EQEQ:
		return staticStmtBlockAlwaysTerminates(then)
	case lexer.TOKEN_BANGEQ:
		return staticStmtBlockAlwaysTerminates(elseStmts)
	default:
		return false
	}
}

func staticExprIdentName(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func staticParamComparisonTerminatesOnThen(op lexer.TokenKind, value int64, allowZeroEquality bool) bool {
	switch op {
	case lexer.TOKEN_EQEQ:
		return allowZeroEquality && value == 0
	case lexer.TOKEN_LTEQ:
		return value >= 0
	case lexer.TOKEN_LT:
		return value > 0
	default:
		return false
	}
}

func staticParamComparisonTerminatesOnElse(op lexer.TokenKind, value int64, allowZeroEquality bool) bool {
	switch op {
	case lexer.TOKEN_BANGEQ:
		return allowZeroEquality && value == 0
	case lexer.TOKEN_GT:
		return value >= 0
	case lexer.TOKEN_GTEQ:
		return value > 0
	default:
		return false
	}
}

func staticReverseComparisonOp(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_LT:
		return lexer.TOKEN_GT
	case lexer.TOKEN_GT:
		return lexer.TOKEN_LT
	case lexer.TOKEN_LTEQ:
		return lexer.TOKEN_GTEQ
	case lexer.TOKEN_GTEQ:
		return lexer.TOKEN_LTEQ
	default:
		return op
	}
}

func cloneConstValue(value ConstValue) ConstValue {
	return CloneConstValue(value)
}

func constOptionalSome(value ConstValue) ConstValue {
	cloned := cloneConstValue(value)
	return ConstValue{Kind: ConstOptional, Some: true, Value: &cloned}
}

func constOptionalFromFirstProjection(value ConstValue) ConstValue {
	if value.Kind == ConstOptional {
		return cloneConstValue(value)
	}
	return constOptionalSome(value)
}

func (a *Analyzer) setConstEvalValue(name string, value ConstValue) {
	if len(a.constEvalScopes) == 0 {
		a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
	}
	a.constEvalScopes[len(a.constEvalScopes)-1][name] = cloneConstValue(value)
}

func (a *Analyzer) updateConstEvalValue(name string, value ConstValue) bool {
	for i := len(a.constEvalScopes) - 1; i >= 0; i-- {
		if _, ok := a.constEvalScopes[i][name]; ok {
			a.constEvalScopes[i][name] = cloneConstValue(value)
			return true
		}
	}
	return false
}

func (a *Analyzer) constEvalValueScope(name string) (ConstValue, int, bool) {
	for i := len(a.constEvalScopes) - 1; i >= 0; i-- {
		if value, ok := a.constEvalScopes[i][name]; ok {
			return value, i, true
		}
	}
	return ConstValue{}, -1, false
}

func (a *Analyzer) reportStaticAssertFailure(pos lexer.Pos, message ast.Expr) {
	if message != nil {
		if msg, ok := a.evalConstStringExpr(message); ok {
			a.errorf(pos, "static assert failed: %s", msg)
			return
		}
	}
	a.errorf(pos, "static assert failed")
}

func (a *Analyzer) evalConstEquality(left, right ConstValue, equal bool) (ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == ConstInt && right.Kind == ConstInt:
		matched = left.Int == right.Int
	case isConstNumeric(left) && isConstNumeric(right):
		matched = constNumericEqual(left, right)
	case left.Kind == ConstBool && right.Kind == ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == ConstString && right.Kind == ConstString:
		matched = left.String == right.String
	default:
		return ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return ConstValue{Kind: ConstBool, Bool: matched}, true
}

func evalConstNumericBinary(op lexer.TokenKind, left, right ConstValue) (ConstValue, bool) {
	if left.Kind == ConstInt && right.Kind == ConstInt {
		return evalConstIntBinary(op, left.Int, right.Int)
	}
	if !isConstNumeric(left) || !isConstNumeric(right) {
		return ConstValue{}, false
	}
	leftFloat := constNumericAsFloat64(left)
	rightFloat := constNumericAsFloat64(right)
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: leftFloat < rightFloat}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: leftFloat > rightFloat}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: leftFloat <= rightFloat}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: leftFloat >= rightFloat}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstFloat, Float: leftFloat + rightFloat}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstFloat, Float: leftFloat - rightFloat}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstFloat, Float: leftFloat * rightFloat}, true
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstFloat, Float: leftFloat / rightFloat}, true
	default:
		return ConstValue{}, false
	}
}

func isConstNumeric(value ConstValue) bool {
	return value.Kind == ConstInt || value.Kind == ConstFloat
}

func constNumericAsFloat64(value ConstValue) float64 {
	if value.Kind == ConstFloat {
		return value.Float
	}
	return float64(value.Int)
}

func constNumericEqual(left, right ConstValue) bool {
	return math.Float64bits(constNumericAsFloat64(left)) == math.Float64bits(constNumericAsFloat64(right))
}

func evalConstIntBinary(op lexer.TokenKind, left, right int64) (ConstValue, bool) {
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: left < right}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: left > right}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: left <= right}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: left >= right}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstInt, Int: left + right}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstInt, Int: left - right}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstInt, Int: left * right}, true
	case lexer.TOKEN_SLASH:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left / right}, true
	case lexer.TOKEN_PERCENT:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left % right}, true
	case lexer.TOKEN_CARET:
		return ConstValue{Kind: ConstInt, Int: left ^ right}, true
	case lexer.TOKEN_PIPE:
		return ConstValue{Kind: ConstInt, Int: left | right}, true
	case lexer.TOKEN_AMPERSAND:
		return ConstValue{Kind: ConstInt, Int: left & right}, true
	case lexer.TOKEN_LSHIFT:
		return ConstValue{Kind: ConstInt, Int: left << right}, true
	case lexer.TOKEN_RSHIFT:
		return ConstValue{Kind: ConstInt, Int: left >> right}, true
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) errorf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityError, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
}

func (a *Analyzer) warnf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityWarning, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
}

// warnOncef emits a warning at most once per position+message, for checks that
// run on paths visited more than once per declaration (e.g. funcTypeFromDecl).
func (a *Analyzer) warnOncef(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	message := fmt.Sprintf(format, formatDiagnosticArgs(args)...)
	key := fmt.Sprintf("%s:%d:%d:%s", pos.File, pos.Line, pos.Col, message)
	if a.warnOnceSeen == nil {
		a.warnOnceSeen = make(map[string]bool)
	}
	if a.warnOnceSeen[key] {
		return
	}
	a.warnOnceSeen[key] = true
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityWarning, Message: message})
}

// intLitRequiresSuffix reports whether an integer literal's value exceeds the int64 range
// (but still fits uint64), in which case an unsuffixed literal would fail default `int`
// inference and the explicit unsigned suffix is necessary -- so it should not be flagged as
// discouraged. Values that fit int64 (the vast majority) return false and stay lint-eligible.
func intLitRequiresSuffix(n *ast.IntLit) bool {
	if n == nil {
		return false
	}
	text := strings.ReplaceAll(n.Value, "_", "")
	base := 10
	if n.IsHex {
		base = 16
		text = strings.TrimPrefix(text, "0x")
		text = strings.TrimPrefix(text, "0X")
	}
	if _, err := strconv.ParseInt(text, base, 64); err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			if _, uerr := strconv.ParseUint(text, base, 64); uerr == nil {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) warnNumericLiteralSuffix(expr ast.Expr, suffix string) {
	if suffix == "" || expr == nil {
		return
	}
	if a.numericLiteralSuffixWarnings == nil {
		a.numericLiteralSuffixWarnings = map[ast.Expr]bool{}
	}
	if a.numericLiteralSuffixWarnings[expr] {
		return
	}
	a.numericLiteralSuffixWarnings[expr] = true
	a.warnf(expr.Pos(), "numeric literal suffix %q is discouraged; let the literal type be inferred from context or use an explicit cast", suffix)
}

func (a *Analyzer) deprecatedf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	message := fmt.Sprintf(format, formatDiagnosticArgs(args)...)
	key := fmt.Sprintf("%s:%d:%d:%s", pos.File, pos.Line, pos.Col, message)
	if a.deprecatedSeen == nil {
		a.deprecatedSeen = make(map[string]bool)
	}
	if a.deprecatedSeen[key] {
		return
	}
	a.deprecatedSeen[key] = true
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityDeprecated, Message: message})
}

func isNullableRef(t Type) bool {
	r, ok := t.(*RefType)
	return ok && r.State == RefStateNullable
}

func isRefLike(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

func ParseIntLiteral(expr *ast.IntLit) (int64, bool) {
	base := 10
	text := expr.Value
	if expr.IsHex {
		base = 0
	}
	// Unsigned-typed literals (u8/u16/u32/u64/usize) may have their top bit
	// set, which overflows a signed parse. Parse them as unsigned and store
	// the bit pattern in the int64 const value (consteval stores ints as
	// int64, matching the representation produced by e.g. ~0u64).
	if strings.HasPrefix(expr.Suffix, "u") {
		if u, err := strconv.ParseUint(text, base, 64); err == nil {
			return int64(u), true
		}
		return 0, false
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// isUnsigned64CapableType reports whether a type is one of the 64-bit-wide unsigned builtins
// (u64/usize/uintptr on a 64-bit target), the only integer types that can hold a literal value
// exceeding i64 max. Peels a non-null reference wrapper.
func isUnsigned64CapableType(t Type) bool {
	b, ok := stripRefForBounds(t).(*BuiltinType)
	if !ok || b == nil {
		return false
	}
	switch b.Name {
	case "u64", "usize", "uintptr":
		return true
	}
	return false
}

// ParseIntLiteralForType parses an integer literal in the context of an expected type. It behaves
// exactly like ParseIntLiteral, EXCEPT that a suffixless literal whose value overflows signed i64 but
// fits u64 is accepted when the target is a 64-bit unsigned type — so a declared `u64`/`usize`/
// `uintptr` stands in for the explicit `u64` suffix (e.g. `global X: u64 = 0xcbf29ce484222325`). The
// bit pattern is stored in the returned int64 (matching how `~0u64` / suffixed literals are stored).
// Signed-typed contexts are unaffected: a too-large literal there still fails, so overflow stays an
// error. A nil/unknown target reduces to ParseIntLiteral.
func ParseIntLiteralForType(expr *ast.IntLit, target Type) (int64, bool) {
	if v, ok := ParseIntLiteral(expr); ok {
		return v, true
	}
	if expr == nil || strings.HasPrefix(expr.Suffix, "u") {
		// A `u`-suffixed literal already attempted ParseUint inside ParseIntLiteral; nothing more to try.
		return 0, false
	}
	if !isUnsigned64CapableType(target) {
		return 0, false
	}
	base := 10
	if expr.IsHex {
		base = 0
	}
	if u, err := strconv.ParseUint(expr.Value, base, 64); err == nil {
		return int64(u), true
	}
	return 0, false
}

func ParseFloatLiteral(expr *ast.FloatLit) (float64, bool) {
	v, err := strconv.ParseFloat(expr.Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func ParseCharLiteral(expr *ast.CharLit) (int64, bool) {
	if expr == nil || len(expr.Value) != 1 {
		return 0, false
	}
	return int64(expr.Value[0]), true
}

func CastConstValue(value ConstValue, dst Type) (ConstValue, bool) {
	if storage, ok := ConstEnumStorageType(dst); ok {
		dst = storage
	}
	if idType, ok := dst.(*IDType); ok {
		dst = idType.Storage
	}
	if !IsNumericType(dst) || !isConstNumeric(value) {
		return ConstValue{}, false
	}
	if IsFloatType(dst) {
		floatValue := constNumericAsFloat64(value)
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return ConstValue{}, false
		}
		if builtin, ok := dst.(*BuiltinType); ok && builtin.Name == "f32" {
			return ConstValue{Kind: ConstFloat, Float: float64(float32(floatValue))}, true
		}
		return ConstValue{Kind: ConstFloat, Float: floatValue}, true
	}
	intValue, ok := castConstNumericToInt64(value, dst)
	if !ok {
		return ConstValue{}, false
	}
	return ConstValue{Kind: ConstInt, Int: intValue}, true
}

func castConstNumericToInt64(value ConstValue, dst Type) (int64, bool) {
	floatValue := constNumericAsFloat64(value)
	if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
		return 0, false
	}
	truncated := math.Trunc(floatValue)
	name, ok := builtinNumericTypeName(dst)
	if !ok {
		return 0, false
	}
	if isSignedConstCastBuiltin(name) {
		if usesInt64ConstRange(name) {
			// float64(math.MaxInt64) rounds up to 2^63, so compare against the exact
			// exclusive upper bound instead of float64(maxValue) to avoid accepting
			// out-of-range values like 9223372036854775808.0.
			if truncated < float64(math.MinInt64) || truncated >= math.Exp2(63) {
				return 0, false
			}
			return int64(truncated), true
		}
		minValue, maxValue, ok := signedConstCastRange(name)
		if !ok || truncated < float64(minValue) || truncated > float64(maxValue) {
			return 0, false
		}
		return narrowSignedConstCast(int64(truncated), name), true
	}
	if truncated < 0 {
		return 0, false
	}
	if usesInt64BackedUnsignedConstRange(name) {
		// The current ConstValue representation stores integers as int64, so
		// compile-time unsigned constants must stay below 2^63 to remain
		// representable without wrapping.
		if truncated >= math.Exp2(63) {
			return 0, false
		}
		return int64(truncated), true
	}
	maxValue, ok := unsignedConstCastMax(name)
	if !ok || truncated > float64(maxValue) {
		return 0, false
	}
	return int64(narrowUnsignedConstCast(uint64(truncated), name)), true
}

func builtinNumericTypeName(t Type) (string, bool) {
	if bit, ok := t.(*BitIntType); ok {
		return BitIntName(bit.Signed, bit.Width), true
	}
	if builtin, ok := t.(*BuiltinType); ok {
		return builtin.Name, true
	}
	return "", false
}

func isSignedConstCastBuiltin(name string) bool {
	if signed, _, ok := ParseBitIntName(name); ok {
		return signed
	}
	switch name {
	case "char", "int", "isize", "i8", "i16", "i32", "i64":
		return true
	default:
		return false
	}
}

func usesInt64ConstRange(name string) bool {
	if signed, width, ok := ParseBitIntName(name); ok {
		return signed && width == 64
	}
	switch name {
	case "char", "int", "isize", "i64":
		return true
	default:
		return false
	}
}

func signedConstCastRange(name string) (int64, int64, bool) {
	if signed, width, ok := ParseBitIntName(name); ok && signed {
		if width >= 64 {
			return math.MinInt64, math.MaxInt64, true
		}
		return -(int64(1) << (width - 1)), (int64(1) << (width - 1)) - 1, true
	}
	switch name {
	case "i8":
		return math.MinInt8, math.MaxInt8, true
	case "i16":
		return math.MinInt16, math.MaxInt16, true
	case "i32":
		return math.MinInt32, math.MaxInt32, true
	case "char", "int", "isize", "i64":
		return math.MinInt64, math.MaxInt64, true
	default:
		return 0, 0, false
	}
}

func unsignedConstCastMax(name string) (uint64, bool) {
	if signed, width, ok := ParseBitIntName(name); ok && !signed {
		if width >= 64 {
			return math.MaxInt64, true
		}
		return (uint64(1) << width) - 1, true
	}
	switch name {
	case "u8":
		return math.MaxUint8, true
	case "u16":
		return math.MaxUint16, true
	case "u32":
		return math.MaxUint32, true
	case "u64", "usize", "uintptr":
		return math.MaxInt64, true
	default:
		return 0, false
	}
}

func (a *Analyzer) evalConstMembershipRange(value ConstValue, expr *ast.MembershipRangeExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	start, ok := a.evalConstExpr(expr.Start)
	if !ok {
		return ConstValue{}, false
	}
	end, ok := a.evalConstExpr(expr.End)
	if !ok {
		return ConstValue{}, false
	}
	if value.Kind != ConstInt || start.Kind != ConstInt || end.Kind != ConstInt {
		return ConstValue{}, false
	}
	inRange := value.Int >= start.Int
	if expr.Op == lexer.TOKEN_RANGE_LT {
		inRange = inRange && value.Int < end.Int
	} else {
		inRange = inRange && value.Int <= end.Int
	}
	return ConstValue{Kind: ConstBool, Bool: inRange}, true
}

func usesInt64BackedUnsignedConstRange(name string) bool {
	if signed, width, ok := ParseBitIntName(name); ok {
		return !signed && width >= 64
	}
	switch name {
	case "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func narrowSignedConstCast(value int64, name string) int64 {
	switch name {
	case "i8":
		return int64(int8(value))
	case "i16":
		return int64(int16(value))
	case "i32":
		return int64(int32(value))
	default:
		return value
	}
}

func narrowUnsignedConstCast(value uint64, name string) uint64 {
	switch name {
	case "u8":
		return uint64(uint8(value))
	case "u16":
		return uint64(uint16(value))
	case "u32":
		return uint64(uint32(value))
	default:
		return value
	}
}
