package semantic

import (
	"elisacore/src/ast"
)

// docs/119 §1 — W1: a value silently discarded in statement position. Every construct
// now has a type; when a bare `expr` statement produces a non-void value that is
// thrown away, the intent is ambiguous — write `_ = expr` to discard on purpose, or
// use the value. OFF by default (opt-in `WarnDiscardedValues`): the corpus is full of
// side-effecting calls that also happen to return a value, so a default lint would be
// pure noise. void-typed statements (the common case) never warn.
//
// Bindings already cover the deliberate-discard escape hatch: `_ = expr` parses as a
// VarDeclStmt / discard, not an ExprStmt, so it never reaches here.

func (a *Analyzer) checkDiscardedValue(stmt *ast.ExprStmt, t Type) {
	if !a.warnDiscardedValues || stmt == nil || stmt.Expr == nil {
		return
	}
	if t == nil || IsInvalidType(t) || isVoidType(t) || IsNeverType(t) {
		return
	}
	// An assignment/mutation expression-statement, or anything whose value is
	// structurally the point (e.g. a `<-` that returns void) has already been screened
	// by the void check above. What remains is a genuinely dropped value.
	a.warnf(stmt.Pos(), "value of type %s is discarded; bind it (`_ = …` to discard on purpose) or use it (docs/119 W1)", t)
}
