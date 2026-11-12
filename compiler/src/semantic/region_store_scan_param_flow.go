package semantic

import (
	"elisacore/src/ast"
	"strings"
)

// storeScanFlowsFrom: value may carry storage shared with one of alias. Structural: a call result, literal or
// comprehension carries it only through a part that does (`remove_name(name, xs)` with
// `name: sview` aliasing nothing new; `[item for item in xs]` copies sview elements).
func (a *Analyzer) storeScanFlowsFrom(decl *ast.FuncDecl, idx *storeScanDeclIndex, value ast.Expr, alias map[string]bool) bool {
	decl = a.readDecl(decl)
	flows := func(v ast.Expr) bool { return a.storeScanFlowsFrom(decl, idx, v, alias) }
	{
		if value == nil || !idx.mentions(value, alias) || !a.storeScanMayShare(decl, value) {
			return false
		}
		switch e := stripParenExpr(value).(type) {
		case *ast.TernaryExpr:
			return flows(e.Value) || flows(e.Alt)
		case *ast.CallExpr:
			if field, isMethod := e.Func.(*ast.FieldExpr); isMethod && field != nil && flows(field.Object) {
				return true
			}
			if callee, ok := a.resolveReadOnlyScanCallee(e); ok && callee != nil && storeFlowCallFitsParams(callee, e) {
				// A resolved callee's result carries only the parameters its (completed) store-flow
				// summary connects to the result.
				if sum := a.storeFlowKindsFor(callee); sum != nil && a.storeFlowInProgress[callee] == nil {
					args := returnBorrowCallArgs(e)
					if len(sum.retConn) == len(callee.Params) {
						for i, arg := range args {
							if i >= len(sum.retConn) || a.storeFlowRetKind(sum, i) == storeFlowEdgeHeader {
								if flows(arg) {
									return true
								}
							}
						}
						return false
					}
				}
			}
			for _, arg := range e.Args {
				if flows(arg) {
					return true
				}
			}
			return false
		case *ast.StructLitExpr:
			for _, arg := range e.Args {
				if flows(arg) {
					return true
				}
			}
			for _, spread := range e.Spreads {
				if idx.mentions(spread, alias) {
					return true
				}
			}
			return false
		case *ast.TupleExpr:
			for _, elem := range e.Elems {
				if flows(elem) {
					return true
				}
			}
			return false
		case *ast.ListLitExpr:
			for i, elem := range e.Elems {
				if (i < len(e.Spreads) && e.Spreads[i] && idx.mentions(elem, alias)) || flows(elem) {
					return true
				}
			}
			for _, key := range e.Keys {
				if flows(key) {
					return true
				}
			}
			return false
		case *ast.ListComprehensionExpr:
			// Elements read out of an alias share its storage only if their type can.
			return a.storeScanMayShare(decl, e.Value)
		}
		return true
	}
}

// storeScanParamWriterOf: trace only -- the writing node behind a true answer.
var storeScanParamWriterOf = map[storeScanParamKey]string{}

// storeScanIsEnumConstructor: storeFlowEnumVariantConstructor judged inside decl (it consults the
// current declaration's binders and type parameters).
func (a *Analyzer) storeScanIsEnumConstructor(decl *ast.FuncDecl, call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	if field, ok := call.Func.(*ast.FieldExpr); !ok || field == nil || field.Object == nil {
		return false
	}
	decl = a.readDecl(decl)
	saved := a.storeFlowCurrentDecl
	a.storeFlowCurrentDecl = decl
	defer func() { a.storeFlowCurrentDecl = saved }()
	// Resolve the enum name where decl is declared: scanned as a callee, the analyzer's current
	// namespace/usings are the caller's, and `Expr.Ident(..)` in another module looked unresolved.
	result := false
	a.withResolutionContext(a.declNamespace(decl), a.declarationUsings(decl), func() {
		result = a.storeFlowEnumVariantConstructor(call)
	})
	return result
}

// declNamespace: the namespace decl is declared in ("" at top level or when unknown).
func (a *Analyzer) declNamespace(decl *ast.FuncDecl) string {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return a.currentNamespace
	}
	if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
		if idx := strings.LastIndex(fnType.Name, "."); idx >= 0 {
			return fnType.Name[:idx]
		}
		return ""
	}
	return a.currentNamespace
}

// storeScanHolds: locals that contain parts of some storage, and under which first projection.
type storeScanHolds struct {
	names  map[string]bool
	fields map[string]map[string]bool
}

// through: expr is a place at least depth projections under a holder, entered through a
// projection that leads to a part.
func (h storeScanHolds) through(expr ast.Expr, depth int) bool {
	root := rootIdentExpr(stripAddrAndParens(expr))
	if root == nil || !h.names[root.Name] || storeScanPlaceDepth(expr) < depth {
		return false
	}
	f := h.fields[root.Name]
	return f == nil || f["*"] || f[storeScanFirstProjection(expr)]
}

// storeScanFirstProjection: the field name of the projection applied directly to the root of a
// place (`e.target.x` -> "target"); "*" for an index or a bare root.
func storeScanFirstProjection(expr ast.Expr) string {
	proj := "*"
	for {
		switch e := stripAddrAndParens(expr).(type) {
		case *ast.FieldExpr:
			if e == nil {
				return proj
			}
			proj = e.Field
			expr = e.Object
		case *ast.IndexExpr:
			if e == nil {
				return proj
			}
			proj = "*"
			expr = e.Object
		default:
			return proj
		}
	}
}
