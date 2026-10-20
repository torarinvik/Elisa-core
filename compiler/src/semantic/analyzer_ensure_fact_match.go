package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// ensureGoalIsLiveFact reports whether a postcondition, with `result` replaced by the
// returned value, is literally one of the asserted facts still live at this return. Facts
// are dropped when anything they read is written, so a live fact holds here. The SMT tier
// can miss this when the fact contains terms it models as fresh symbols per occurrence.
//
// The match is syntactic, so it is guarded twice: every name in the fact must resolve to the
// same binding here as where the fact was recorded (no shadowing), and every call in it must
// have a result that depends only on its arguments.
func (a *Analyzer) ensureGoalIsLiveFact(clause ast.Expr, subst map[string]ast.Expr) bool {
	if a == nil || clause == nil || a.currentScope == nil {
		return false
	}
	goal, ok := substituteLemmaEnsure(clause, subst)
	if !ok || goal == nil {
		return false
	}
	want := unparse.FormatExpr(goal)
	if want == "" {
		return false
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == nil || unparse.FormatExpr(fact.Expr) != want {
				continue
			}
			if a.factNamesResolveAlike(fact.Expr, sc) && a.callsAreStateIndependent(fact.Expr) {
				return true
			}
		}
		if sc.closedWorld {
			break
		}
	}
	return false
}

func (a *Analyzer) factNamesResolveAlike(expr ast.Expr, recorded *Scope) bool {
	ok := true
	walkPruned(expr, func(node any) bool {
		if !ok {
			return false
		}
		id, isIdent := node.(*ast.Ident)
		if !isIdent || id == nil {
			return true
		}
		here, foundHere := a.currentScope.Lookup(id.Name)
		there, foundThere := recorded.Lookup(id.Name)
		if foundHere != foundThere || here != there {
			ok = false
			return false
		}
		// A local or parameter must be declared before the fact: a later redeclaration in the
		// fact's own scope resolves alike from both places but is a different value.
		if here != nil && (here.Kind == SymbolLocal || here.Kind == SymbolParam) {
			if here.Node == nil || !posBefore(here.Node.Pos(), expr.Pos()) {
				ok = false
			}
		}
		return ok
	})
	return ok
}

func posBefore(decl, use lexer.Pos) bool {
	if decl.File != use.File {
		return false
	}
	return decl.Line < use.Line || (decl.Line == use.Line && decl.Col < use.Col)
}
