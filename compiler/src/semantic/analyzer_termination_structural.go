package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) isStructuralDecreaseMeasure(fn *ast.FuncDecl, measure ast.Expr) bool {
	if a == nil || fn == nil || measure == nil || len(fn.Decreases) != 1 {
		return false
	}
	id, ok := measure.(*ast.Ident)
	if !ok || id == nil {
		return false
	}
	if a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(id.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return false
	}
	et, ok := stripRefForBounds(sym.Type).(*EnumType)
	return ok && et != nil && et.RecursivePlain
}

func (a *Analyzer) checkStructuralTermination(fn *ast.FuncDecl, measure ast.Expr) bool {
	id, ok := measure.(*ast.Ident)
	if !ok || id == nil {
		return false
	}
	measureEnum := a.structuralMeasureEnumForFunc(fn, id.Name)
	if measureEnum == nil {
		return false
	}
	calls := a.collectSelfRecursiveCalls(fn)
	if len(calls) == 0 {
		a.warnf(measure.Pos(), "`decreases` on %q, which makes no direct recursive call; the termination clause is unused", fn.Name)
		return true
	}
	for _, call := range calls {
		if _, ok := a.structuralTerminationCertificate(fn, call); !ok {
			return false
		}
	}
	return true
}

func (a *Analyzer) structuralMeasureEnum(measureName string) *EnumType {
	if a == nil || a.currentScope == nil || measureName == "" {
		return nil
	}
	sym, ok := a.currentScope.Lookup(measureName)
	if !ok || sym == nil {
		return nil
	}
	et, ok := stripRefForBounds(sym.Type).(*EnumType)
	if !ok || et == nil || !et.RecursivePlain {
		return nil
	}
	return et
}

func (a *Analyzer) structuralMeasureEnumForFunc(fn *ast.FuncDecl, measureName string) *EnumType {
	if a == nil || fn == nil || measureName == "" {
		return nil
	}
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return nil
	}
	for i, param := range fn.Params {
		if param.Name != measureName || i >= len(fnType.Params) {
			continue
		}
		et, ok := stripRefForBounds(fnType.Params[i]).(*EnumType)
		if ok && et != nil && et.RecursivePlain {
			return et
		}
	}
	return nil
}

func (a *Analyzer) collectStructuralRecursiveCalls(stmts []ast.Stmt, measureName string, measureEnum *EnumType, smaller map[string]bool, allowed map[*ast.CallExpr]bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.MatchStmt:
			next := smaller
			if id, ok := stripOptimizationParens(n.Value).(*ast.Ident); ok && (id.Name == measureName || smaller[id.Name]) {
				next = cloneStringBoolMap(smaller)
				for _, arm := range n.Arms {
					collectStructuralChildBindNames(arm.Pattern, measureEnum, ast.EnumPayloadRelationNone, measureEnum, next)
					a.collectStructuralRecursiveCalls(arm.Body, measureName, measureEnum, next, allowed)
				}
				continue
			}
			for _, arm := range n.Arms {
				a.collectStructuralRecursiveCalls(arm.Body, measureName, measureEnum, next, allowed)
			}
		case *ast.IfStmt:
			a.collectStructuralRecursiveCalls(n.Then, measureName, measureEnum, smaller, allowed)
			a.collectStructuralRecursiveCalls(n.Else, measureName, measureEnum, smaller, allowed)
			for _, elif := range n.Elifs {
				a.collectStructuralRecursiveCalls(elif.Body, measureName, measureEnum, smaller, allowed)
			}
		case *ast.ExprStmt:
			a.markStructuralCallIfSmaller(n.Expr, smaller, allowed)
		case *ast.ReturnStmt:
			a.markStructuralCallIfSmaller(n.Value, smaller, allowed)
		}
	}
}

func (a *Analyzer) markStructuralCallIfSmaller(expr ast.Expr, smaller map[string]bool, allowed map[*ast.CallExpr]bool) {
	a.walkStaticExpr(expr, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl == a.currentFuncDecl && len(call.Args) > 0 {
			if id, ok := stripOptimizationParens(call.Args[0]).(*ast.Ident); ok && smaller[id.Name] {
				allowed[call] = true
			}
		}
		return false
	})
}

func collectStructuralChildBindNames(pattern ast.MatchPattern, expected Type, relation ast.EnumPayloadRelation, measureEnum *EnumType, out map[string]bool) {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Name != "" && isStructuralChildType(expected, measureEnum, relation) {
			out[p.Name] = true
		}
		if p.Binder != "" && isStructuralChildType(expected, measureEnum, relation) {
			out[p.Binder] = true
		}
	case *ast.MatchVariantPattern:
		enumType, ok := stripRefForBounds(expected).(*EnumType)
		if !ok || enumType == nil {
			return
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok || variant == nil {
			return
		}
		args := p.ResolvedArgs
		if len(args) == 0 {
			args = make([]*ast.MatchPatternArg, len(p.Args))
			for i := range p.Args {
				args[i] = &p.Args[i]
			}
		}
		for i, arg := range args {
			if arg == nil || i >= len(variant.Payload) {
				continue
			}
			collectStructuralChildBindNames(arg.Pattern, variant.Payload[i], variant.PayloadRelation(i), measureEnum, out)
		}
	case *ast.MatchStructPattern:
		for _, arg := range p.Args {
			collectStructuralChildBindNames(arg.Pattern, nil, ast.EnumPayloadRelationNone, measureEnum, out)
		}
		for _, arg := range p.ResolvedArgs {
			if arg != nil {
				collectStructuralChildBindNames(arg.Pattern, nil, ast.EnumPayloadRelationNone, measureEnum, out)
			}
		}
	case *ast.MatchTuplePattern:
		for _, elem := range p.Elems {
			collectStructuralChildBindNames(elem, nil, ast.EnumPayloadRelationNone, measureEnum, out)
		}
	case *ast.MatchListPattern:
		for _, elem := range p.Elems {
			collectStructuralChildBindNames(elem, nil, ast.EnumPayloadRelationNone, measureEnum, out)
		}
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			collectStructuralChildBindNames(option, expected, relation, measureEnum, out)
		}
	}
}

func isStructuralChildType(t Type, measureEnum *EnumType, relation ast.EnumPayloadRelation) bool {
	if measureEnum == nil || t == nil {
		return false
	}
	et, ok := stripRefForBounds(t).(*EnumType)
	if !ok || et == nil || et.Name != measureEnum.Name || !et.RecursivePlain {
		return false
	}
	switch relation {
	case ast.EnumPayloadRelationNone, ast.EnumPayloadRelationChild, ast.EnumPayloadRelationChildren:
		return true
	default:
		return false
	}
}

func cloneStringBoolMap(src map[string]bool) map[string]bool {
	dst := map[string]bool{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
