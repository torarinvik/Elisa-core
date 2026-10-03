package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// One-shot closures (fuzz finding F12). A lambda whose body moves a captured affine value
// (`fn() => take(move h)`) can run that move only once: calling it twice would hand the same
// value to two owners. The rule:
//
//   - the move consumes the capture at closure CREATION: h is unusable afterwards;
//   - the closure must be bound directly to a local (`g = fn() => take(move h)`);
//   - that local may only be CALLED, at most once on any path (the call consumes it, with the
//     ordinary affine flow rules for branches and loops);
//   - any other use of it (copying, passing, storing, returning, capturing) is rejected, since the
//     receiver could call it again.

const oneShotClosureKind = "one-shot closure"

// lambdaMovedCaptures lists the captures the lambda body consumed (on any path), from the
// lambda-local affine state; it must run before that state is restored.
func (a *Analyzer) lambdaMovedCaptures(captureSyms []*Symbol) []string {
	var moved []string
	for _, sym := range captureSyms {
		if sym == nil {
			continue
		}
		for key, state := range a.currentAffineValues {
			if key.Root == sym && state.ConsumedBy != "" {
				moved = append(moved, sym.Name)
				break
			}
		}
	}
	return moved
}

// consumeOneShotClosureCaptures runs in the ENCLOSING scope once the lambda body is analyzed: each
// moved capture is consumed at the closure's creation, and the closure must be bound to a local.
func (a *Analyzer) consumeOneShotClosureCaptures(expr *ast.LambdaExpr, moved []string) {
	if len(moved) == 0 || a.currentScope == nil {
		return
	}
	for _, name := range moved {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam) {
			continue
		}
		key := affineValueKey{Root: sym}
		if state, consumed := a.lookupAffineValueStateForKey(key); consumed && state.ConsumedBy != "" {
			a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(sym.Type), name, state.ConsumedBy))
			continue
		}
		a.recordAffineConsumption(key, fmt.Sprintf("move into closure (captured %q)", name))
	}
	if a.oneShotLambdas == nil {
		a.oneShotLambdas = map[*ast.LambdaExpr]string{}
	}
	a.oneShotLambdas[expr] = moved[0]
	if a.pendingClosureBinding != expr {
		a.errorf(expr.Pos(), "a closure that moves captured %q can be called at most once; bind it directly to a local (`g = fn() => ...`) and call that local once", moved[0])
	}
}

// bindOneShotClosure marks a local initialized by a one-shot lambda.
func (a *Analyzer) bindOneShotClosure(sym *Symbol, value ast.Expr) {
	lambda, ok := stripOptimizationParens(value).(*ast.LambdaExpr)
	if !ok || lambda == nil || sym == nil {
		return
	}
	captured, isOneShot := a.oneShotLambdas[lambda]
	if !isOneShot {
		return
	}
	if a.oneShotClosureSyms == nil {
		a.oneShotClosureSyms = map[*Symbol]string{}
	}
	a.oneShotClosureSyms[sym] = captured
}

// oneShotClosureSymbol resolves an identifier naming a one-shot closure local.
func (a *Analyzer) oneShotClosureSymbol(ident *ast.Ident) (*Symbol, string, bool) {
	if a == nil || ident == nil || a.currentScope == nil || len(a.oneShotClosureSyms) == 0 {
		return nil, "", false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil, "", false
	}
	captured, isOneShot := a.oneShotClosureSyms[sym]
	return sym, captured, isOneShot
}

// checkOneShotClosureUse reports a use of a one-shot closure local other than a direct call.
func (a *Analyzer) checkOneShotClosureUse(ident *ast.Ident) {
	if a.oneShotCallee == ident {
		return
	}
	if _, captured, isOneShot := a.oneShotClosureSymbol(ident); isOneShot {
		a.errorf(ident.Pos(), "%s %q can only be called directly: it moves captured %q, so copying, passing, storing or returning it could run that move twice", oneShotClosureKind, ident.Name, captured)
	}
}

// consumeOneShotClosureCall records a direct call of a one-shot closure local: the first call
// consumes it, a second one (on any path) is a use after consumption.
func (a *Analyzer) consumeOneShotClosureCall(call *ast.CallExpr) {
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	sym, _, isOneShot := a.oneShotClosureSymbol(ident)
	if !isOneShot {
		return
	}
	key := affineValueKey{Root: sym}
	if state, consumed := a.lookupAffineValueStateForKey(key); consumed && state.ConsumedBy != "" {
		a.errorf(call.Pos(), consumedFactUseMessage(oneShotClosureKind, ident.Name, state.ConsumedBy))
		return
	}
	a.recordAffineConsumption(key, fmt.Sprintf("call to %q", ident.Name))
}

// checkOneShotClosureCaptures rejects capturing a one-shot closure local into another closure: the
// capture is a copy the outer closure could call any number of times.
func (a *Analyzer) checkOneShotClosureCaptures(expr *ast.LambdaExpr, captures []lambdaCaptureBinding) {
	if len(a.oneShotClosureSyms) == 0 || a.currentScope == nil {
		return
	}
	for _, capture := range captures {
		sym, ok := a.currentScope.Lookup(capture.name)
		if !ok || sym == nil {
			continue
		}
		if captured, isOneShot := a.oneShotClosureSyms[sym]; isOneShot {
			a.errorf(expr.Pos(), "%s %q can only be called directly: it moves captured %q, so copying, passing, storing or returning it could run that move twice", oneShotClosureKind, capture.name, captured)
		}
	}
}
