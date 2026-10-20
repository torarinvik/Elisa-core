package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"reflect"
	"strings"
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
	textOpaque := strings.Contains(want, "<expr>")
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			// The formatter prints unsupported nodes (quantifiers) as `<expr>`, so equal text is
			// only trusted when it has none; otherwise the trees must match structurally.
			if fact.Expr == nil || ((textOpaque || unparse.FormatExpr(fact.Expr) != want) && !astEqualModuloParens(reflect.ValueOf(fact.Expr), reflect.ValueOf(goal))) {
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

// astEqualModuloParens is astEqualIgnoringPositions that also looks through grouping parentheses:
// `implies` desugars to an unparenthesized `(not a) or b` that an assert spells `not (a) or (b)`.
// Only ensureGoalIsLiveFact uses it, after factNamesResolveAlike pins every name to one symbol.
func astEqualModuloParens(x, y reflect.Value) bool {
	x, y = unwrapParenValue(x), unwrapParenValue(y)
	if x.IsValid() != y.IsValid() {
		return false
	}
	if !x.IsValid() {
		return true
	}
	if x.Kind() == reflect.Pointer && y.Kind() == reflect.Pointer && !x.IsNil() && !y.IsNil() {
		// A range quantifier and its lowered runtime flag-and-loop block are the same fact.
		if left, ok := rangeQuantifierShapeOf(x.Interface()); ok {
			right, ok := rangeQuantifierShapeOf(y.Interface())
			return ok && left.equalModuloParens(right)
		}
	}
	if x.Type() != y.Type() {
		return false
	}
	if x.Type() == lexerPosType {
		return true
	}
	switch x.Kind() {
	case reflect.Pointer:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return astEqualModuloParens(x.Elem(), y.Elem())
	case reflect.Struct:
		for i := 0; i < x.NumField(); i++ {
			if !x.Type().Field(i).IsExported() {
				continue
			}
			if !astEqualModuloParens(x.Field(i), y.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if x.Len() != y.Len() {
			return false
		}
		for i := 0; i < x.Len(); i++ {
			if !astEqualModuloParens(x.Index(i), y.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map, reflect.Func, reflect.Chan:
		return x.IsNil() && y.IsNil()
	default:
		return x.CanInterface() && y.CanInterface() && reflect.DeepEqual(x.Interface(), y.Interface())
	}
}

func unwrapParenValue(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && !v.IsNil() {
		if v.Kind() == reflect.Interface {
			v = v.Elem()
			continue
		}
		paren, ok := v.Interface().(*ast.ParenExpr)
		if !ok || paren == nil || paren.Inner == nil {
			return v
		}
		v = reflect.ValueOf(paren.Inner)
	}
	return v
}

func (q rangeQuantifierShape) equalModuloParens(o rangeQuantifierShape) bool {
	return q.exists == o.exists && q.name == o.name &&
		astEqualModuloParens(reflect.ValueOf(q.lo), reflect.ValueOf(o.lo)) &&
		astEqualModuloParens(reflect.ValueOf(q.hi), reflect.ValueOf(o.hi)) &&
		astEqualModuloParens(reflect.ValueOf(q.predicate), reflect.ValueOf(o.predicate))
}
