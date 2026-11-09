package semantic

import (
	"elisacore/src/ast"
)

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
	case "len":
		// A string LITERAL has a compile-time-known byte length: `"abc".len == 3`.
		// We deliberately restrict this to ConstString only — a non-literal string's
		// length is opaque and must not be fabricated as a constant. A cstr receiver's
		// `.len` is strlen, which differs on an embedded NUL (ConstStringLen).
		if object.Kind == ConstString {
			if length, ok := ConstStringLen(object.String, a.constLenReceiverType(expr.Object)); ok {
				return ConstValue{Kind: ConstInt, Int: length}, true
			}
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
