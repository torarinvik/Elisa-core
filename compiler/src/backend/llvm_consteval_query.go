//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (s *functionState) evalConstQueryExpr(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	source, ok := s.evalConstExpr(expr.Source)
	if !ok || (source.Kind != semantic.ConstList && source.Kind != semantic.ConstTuple) {
		return semantic.ConstValue{}, false
	}
	switch expr.Kind {
	case ast.QueryExprAny:
		for _, elem := range source.Elems {
			match, ok := s.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
	case ast.QueryExprAll:
		for _, elem := range source.Elems {
			match, ok := s.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if !match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
	case ast.QueryExprCount:
		total := int64(0)
		for _, elem := range source.Elems {
			match, ok := s.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				total++
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: total}, true
	case ast.QueryExprFirst:
		for _, elem := range source.Elems {
			include, value, ok := s.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				return backendConstOptionalFromFirstProjection(value), true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstOptional}, true
	case ast.QueryExprEach:
		elems := make([]semantic.ConstValue, 0, len(source.Elems))
		for _, elem := range source.Elems {
			include, value, ok := s.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				elems = append(elems, cloneBackendConstValue(value))
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstList, Elems: elems}, true
	default:
		return semantic.ConstValue{}, false
	}
}

func (s *functionState) evalConstQueryFilter(expr *ast.QueryExpr, item semantic.ConstValue) (bool, bool) {
	result, ok := s.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		if expr.Filter == nil {
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
		}
		return s.evalConstExpr(expr.Filter)
	})
	if !ok || result.Kind != semantic.ConstBool {
		return false, false
	}
	return result.Bool, true
}

func (s *functionState) evalConstQueryProjectedValue(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	if expr.Projection == nil {
		include, ok := s.evalConstQueryFilter(expr, item)
		return include, item, ok
	}
	return s.evalConstQueryProjection(expr, item)
}

func (s *functionState) evalConstQueryProjection(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	include, ok := s.evalConstQueryFilter(expr, item)
	if !ok || !include {
		return include, semantic.ConstValue{}, ok
	}
	value, ok := s.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		return s.evalConstExpr(expr.Projection)
	})
	return true, value, ok
}

func (s *functionState) evalConstQueryInItemScope(expr *ast.QueryExpr, item semantic.ConstValue, eval func() (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	scope := map[string]semantic.ConstValue{}
	if expr != nil {
		pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name})
		if expr.Pattern != nil {
			pattern = expr.Pattern
		}
		var ok bool
		scope, ok = bindBackendConstMovePattern(pattern, item)
		if !ok {
			return semantic.ConstValue{}, false
		}
	}
	s.g.constEvalScopes = append(s.g.constEvalScopes, scope)
	if expr != nil && expr.PatternFilter != nil {
		matched, bindings, ok := s.evalConstQueryPatternFilter(expr)
		if !ok {
			s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
			return semantic.ConstValue{}, ok
		}
		if !matched {
			s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
		}
		for name, value := range bindings {
			scope[name] = value
		}
		s.g.constEvalScopes[len(s.g.constEvalScopes)-1] = scope
	}
	value, ok := eval()
	s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
	return value, ok
}

func (s *functionState) evalConstQueryPatternFilter(expr *ast.QueryExpr) (bool, map[string]semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return true, nil, true
	}
	subject, ok := s.evalConstQueryPatternFilterSubject(expr)
	if !ok {
		return false, nil, false
	}
	matched, bindings, ok := s.evalStaticMatchPattern(expr.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func (s *functionState) evalConstQueryPatternFilterSubject(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return semantic.ConstValue{}, false
	}
	if expr.PatternFilterSubject != "" {
		if value, _, ok := s.constEvalValueScope(expr.PatternFilterSubject); ok {
			return value, true
		}
		return semantic.ConstValue{}, false
	}
	if expr.Name != "" && expr.Name != "_" {
		if value, _, ok := s.constEvalValueScope(expr.Name); ok {
			return value, true
		}
	}
	return semantic.ConstValue{}, false
}

func (g *llvmGenerator) evalConstQueryExpr(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	source, ok := g.evalConstExpr(expr.Source)
	if !ok || (source.Kind != semantic.ConstList && source.Kind != semantic.ConstTuple) {
		return semantic.ConstValue{}, false
	}
	switch expr.Kind {
	case ast.QueryExprAny:
		for _, elem := range source.Elems {
			match, ok := g.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
	case ast.QueryExprAll:
		for _, elem := range source.Elems {
			match, ok := g.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if !match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
	case ast.QueryExprCount:
		total := int64(0)
		for _, elem := range source.Elems {
			match, ok := g.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				total++
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: total}, true
	case ast.QueryExprFirst:
		for _, elem := range source.Elems {
			include, value, ok := g.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				return backendConstOptionalFromFirstProjection(value), true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstOptional}, true
	case ast.QueryExprEach:
		elems := make([]semantic.ConstValue, 0, len(source.Elems))
		for _, elem := range source.Elems {
			include, value, ok := g.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				elems = append(elems, cloneBackendConstValue(value))
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstList, Elems: elems}, true
	default:
		return semantic.ConstValue{}, false
	}
}

func (g *llvmGenerator) evalConstQueryFilter(expr *ast.QueryExpr, item semantic.ConstValue) (bool, bool) {
	result, ok := g.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		if expr.Filter == nil {
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
		}
		return g.evalConstExpr(expr.Filter)
	})
	if !ok || result.Kind != semantic.ConstBool {
		return false, false
	}
	return result.Bool, true
}

func (g *llvmGenerator) evalConstQueryProjectedValue(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	if expr.Projection == nil {
		include, ok := g.evalConstQueryFilter(expr, item)
		return include, item, ok
	}
	return g.evalConstQueryProjection(expr, item)
}

func (g *llvmGenerator) evalConstQueryProjection(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	include, ok := g.evalConstQueryFilter(expr, item)
	if !ok || !include {
		return include, semantic.ConstValue{}, ok
	}
	value, ok := g.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		return g.evalConstExpr(expr.Projection)
	})
	return true, value, ok
}

func (g *llvmGenerator) evalConstQueryInItemScope(expr *ast.QueryExpr, item semantic.ConstValue, eval func() (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	scope := map[string]semantic.ConstValue{}
	if expr != nil {
		pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name})
		if expr.Pattern != nil {
			pattern = expr.Pattern
		}
		var ok bool
		scope, ok = bindBackendConstMovePattern(pattern, item)
		if !ok {
			return semantic.ConstValue{}, false
		}
	}
	g.constEvalScopes = append(g.constEvalScopes, scope)
	if expr != nil && expr.PatternFilter != nil {
		matched, bindings, ok := g.evalConstQueryPatternFilter(expr)
		if !ok {
			g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
			return semantic.ConstValue{}, ok
		}
		if !matched {
			g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
		}
		for name, value := range bindings {
			scope[name] = value
		}
		g.constEvalScopes[len(g.constEvalScopes)-1] = scope
	}
	value, ok := eval()
	g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
	return value, ok
}

func (g *llvmGenerator) evalConstQueryPatternFilter(expr *ast.QueryExpr) (bool, map[string]semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return true, nil, true
	}
	subject, ok := g.evalConstQueryPatternFilterSubject(expr)
	if !ok {
		return false, nil, false
	}
	matched, bindings, ok := g.evalStaticMatchPattern(expr.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func (g *llvmGenerator) evalConstQueryPatternFilterSubject(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return semantic.ConstValue{}, false
	}
	if expr.PatternFilterSubject != "" {
		if value, _, ok := g.constEvalValueScope(expr.PatternFilterSubject); ok {
			return value, true
		}
		return semantic.ConstValue{}, false
	}
	if expr.Name != "" && expr.Name != "_" {
		if value, _, ok := g.constEvalValueScope(expr.Name); ok {
			return value, true
		}
	}
	return semantic.ConstValue{}, false
}
