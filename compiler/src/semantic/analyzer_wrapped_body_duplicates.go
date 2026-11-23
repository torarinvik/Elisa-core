package semantic

import "elisacore/src/ast"

// wrappedBodyFrame lets the duplicate-local rule see through a synthesized auto-region wrap.
// When a function body allocates through region-polymorphic calls, the whole body becomes one
// `__auto_N` region block, so its top-level locals live two scopes below the parameters and a
// top-level `x: T = ...` naming a parameter looked like a legal nested shadow instead of the
// `duplicate local` it is in an unwrapped body (and in Stage1, which never wraps).
type wrappedBodyFrame struct {
	paramScope *Scope
	topLevel   map[ast.Stmt]bool
}

// enterWrappedBody records the frame for fn's body and returns the frame to restore afterwards.
func (a *Analyzer) enterWrappedBody(fn *ast.FuncDecl) wrappedBodyFrame {
	saved := a.wrappedBody
	a.wrappedBody = wrappedBodyFrame{}
	if fn == nil || len(fn.Body) != 1 {
		return saved
	}
	region, ok := fn.Body[0].(*ast.RegionStmt)
	if !ok || !isCompilerGeneratedAutoRegionStmt(region) {
		return saved
	}
	topLevel := make(map[ast.Stmt]bool, len(region.Body))
	for _, stmt := range region.Body {
		topLevel[stmt] = true
	}
	a.wrappedBody = wrappedBodyFrame{paramScope: a.currentScope, topLevel: topLevel}
	return saved
}

// reportWrappedBodyParamRedeclaration reports a top-level declaration of a wrapped body that
// redeclares a parameter, exactly as defineLocal does in an unwrapped body.
func (a *Analyzer) reportWrappedBodyParamRedeclaration(n *ast.VarDeclStmt) bool {
	frame := a.wrappedBody
	if n == nil || frame.paramScope == nil || !frame.topLevel[n] || a.currentScope == frame.paramScope {
		return false
	}
	param, ok := frame.paramScope.Symbols[n.Name]
	if !ok || param.Kind != SymbolParam {
		return false
	}
	a.errorf(n.Pos(), "%s", DuplicateLocalMessage(param.Name, param.Kind))
	return true
}
