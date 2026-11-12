package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/classifier"
)

func foldCharClassifierTables(file *ast.File) []string {
	return classifier.FoldCharClassifierTables(file)
}
func isCharType(t ast.TypeExpr) bool              { return classifier.IsCharType(t) }
func namedTypeName(t ast.TypeExpr) (string, bool) { return classifier.NamedTypeName(t) }

// checkUnfoldableClassifier emits a -Wperf nudge for a function that is CLASSIFIER-SHAPED
// (one `char` param, returning a closed const enum) but was NOT folded to a lookup table —
// its body still dispatches by a branch chain. Foldable classifiers were rewritten to an
// indexed load before analysis, so any classifier-shaped function whose body is still a
// `return <match>` carries a fold-blocker (a guarded arm, a non-literal pattern, or a
// non-member result). Under `-Wperf` this is an error; otherwise an advisory warning. The
// goal is the docs/125 §9 promise: a total char classifier should compile to what a
// production lexer hand-writes — a single 256-byte table lookup.
func (a *Analyzer) checkUnfoldableClassifier(fn *ast.FuncDecl) {
	if fn == nil || len(fn.Params) != 1 || fn.Params[0].Mutable || !isCharType(fn.Params[0].Type) {
		return
	}
	enumName, ok := namedTypeName(fn.ReturnType)
	if !ok {
		return
	}
	if _, isConstEnum := a.namedTypes[enumName].(*ConstEnumType); !isConstEnum {
		return
	}
	// Body still a single `return <match over the param>`? (A folded classifier's body is a
	// `return TABLE[c]` IndexExpr, so it is skipped here — correctly not flagged.)
	if len(fn.Body) != 1 {
		return
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		return
	}
	me, ok := ret.Value.(*ast.MatchExpr)
	if !ok {
		return
	}
	scrut, ok := me.Value.(*ast.Ident)
	if !ok || scrut.Name != fn.Params[0].Name {
		return
	}
	// It dispatches on the char param but did not fold — surface the blocker.
	for _, arm := range me.Arms {
		if arm.Guard != nil {
			a.perfLint(fn.Position, "char classifier %q is not foldable to a lookup table because an arm carries a guard; a guardless total classifier over literal/range/tag patterns compiles to a single 256-byte indexed load (docs/125 §9)", fn.Name)
			return
		}
	}
	a.perfLint(fn.Position, "char classifier %q dispatches by a branch chain, not a folded lookup table; keep the body a single total `when`/`match` over literal/range/alternation patterns yielding %s members so it compiles to one indexed load (docs/125 §9)", fn.Name, enumName)
}
