package semantic

import (
	"elisacore/src/ast"
)

// Branch-local projection of flow facts. A branch's end facts often talk about locals declared in
// the branch (`k: usize = find(s, ","); if k >= s.len: return ...; close <- open + 1 + k`). Those
// locals go out of scope at the join, so the if-join used to drop every fact that mentioned them
// and with it everything known about `close`.
//
// projectBranchLocalFacts rewrites such a fact by replacing each branch-local IMMUTABLE local with
// its initializer (repeatedly, for locals defined through other branch locals), keeping the fact
// only when no branch-local name remains.
//
// SOUNDNESS: at the branch end an immutable local equals its initializer re-evaluated there, as long
// as every binding the initializer read still holds the value it had at the declaration. That is
// exactly what the defining-equation machinery already relies on (smtImmutableLocalHypotheses):
//   - the local must not be stale (no input written since: markLocalDefinitionsStale runs at every
//     mutation, and the local is visible at every write inside the branch);
//   - every name in the initializer resolves from the branch scope to the binding it denoted at the
//     declaration (a branch local declared AFTER the local, which could shadow it, rejects);
//   - the initializer is converted to the local's type exactly as the store does (implicitIntConversion),
//     and only integer/bool locals and sview locals bound to a slice or view name are replaced.
//
// The rewritten fact keeps the original pins of the names it already had and pins the names the
// initializers introduced, so the join and later writes retire it like any other fact.
const branchLocalProjectionRounds = 4

func (a *Analyzer) projectBranchLocalFacts(scope *Scope, facts []smtFact) []smtFact {
	if a == nil || scope == nil || len(facts) == 0 || len(scope.Symbols) == 0 {
		return facts
	}
	var defs map[string]ast.Expr
	out := make([]smtFact, 0, len(facts))
	for _, fact := range facts {
		if fact.Expr == nil || fact.Binders != nil || !factMentionsScopeSymbol(fact, scope) {
			out = append(out, fact)
			continue
		}
		if defs == nil {
			defs = a.branchLocalDefinitions(scope)
		}
		if projected, ok := a.projectBranchLocalFact(scope, fact, defs); ok {
			out = append(out, projected)
			continue
		}
		out = append(out, fact)
	}
	return out
}

func factMentionsScopeSymbol(fact smtFact, scope *Scope) bool {
	for name, pinned := range fact.Pins {
		if pinned != nil && scope.Symbols[name] == pinned {
			return true
		}
	}
	return false
}

// branchLocalDefinitions maps each replaceable branch-local immutable local to its (type-converted)
// initializer.
func (a *Analyzer) branchLocalDefinitions(scope *Scope) map[string]ast.Expr {
	defs := map[string]ast.Expr{}
	for name, sym := range scope.Symbols {
		if sym == nil || sym.Mutable || sym.Kind != SymbolLocal || a.smtStaleLocalDefs[sym] {
			continue
		}
		vd, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || vd == nil || vd.Value == nil || vd.Mutable || vd.Ghost || vd.Owner != nil || vd.Name != name {
			continue
		}
		var value ast.Expr
		switch t := stripRefForBounds(sym.Type).(type) {
		case *SViewType:
			if _, isRef := sym.Type.(*RefType); isRef {
				continue
			}
			switch stripOptimizationParens(vd.Value).(type) {
			case *ast.SliceExpr, *ast.Ident:
			default:
				continue
			}
			if _, same := stripRefForBounds(a.exprTypes[vd.Value]).(*SViewType); !same {
				continue
			}
			value = vd.Value
		default:
			if !isSMTExactAssignmentType(t) || !isSMTExactAssignmentType(a.exprTypes[vd.Value]) {
				continue
			}
			value = a.implicitIntConversion(vd.Value, a.exprTypes[vd.Value], sym.Type)
		}
		if !a.branchLocalInitializerCurrent(scope, sym, vd) {
			continue
		}
		defs[name] = &ast.ParenExpr{Position: vd.Value.Pos(), Inner: value}
		a.exprTypes[defs[name]] = sym.Type
	}
	return defs
}

// branchLocalInitializerCurrent: every name the initializer reads resolves from the branch scope to
// the binding it read at the declaration.
func (a *Analyzer) branchLocalInitializerCurrent(scope *Scope, sym *Symbol, vd *ast.VarDeclStmt) bool {
	declPos := vd.Position
	names := map[string]bool{}
	for name := range smtFactDeps(vd.Value) {
		names[name] = true
	}
	for _, name := range exprIdentNames(vd.Value) {
		names[name] = true
	}
	for name := range names {
		if name == sym.Name {
			return false
		}
		then, _ := scope.Lookup(name)
		if then == nil || then.Node == nil {
			continue
		}
		if then.Kind == SymbolLocal {
			if p := then.Node.Pos(); p.File == declPos.File && p.Offset > declPos.Offset {
				return false
			}
		}
		if then.Kind == SymbolGlobal && then.Mutable {
			return false
		}
	}
	return true
}

func (a *Analyzer) projectBranchLocalFact(scope *Scope, fact smtFact, defs map[string]ast.Expr) (smtFact, bool) {
	expr := fact.Expr
	for round := 0; round < branchLocalProjectionRounds; round++ {
		subst := map[string]ast.Expr{}
		for _, name := range exprIdentNames(expr) {
			sym, _ := scope.Lookup(name)
			if sym == nil || scope.Symbols[name] != sym {
				continue
			}
			if pinned, has := fact.Pins[name]; has && pinned != sym {
				return smtFact{}, false // the fact's own name denotes an outer binding the local shadows
			}
			def, ok := defs[name]
			if !ok {
				return smtFact{}, false
			}
			subst[name] = def
		}
		if len(subst) == 0 {
			break
		}
		rewritten, ok := a.substituteTypedIdents(expr, subst, 0)
		if !ok {
			return smtFact{}, false
		}
		expr = rewritten
	}
	pins := map[string]*Symbol{}
	for name, sym := range a.smtFactPins(scope, expr) {
		if sym != nil && scope.Symbols[name] == sym {
			return smtFact{}, false // a branch-local name survived
		}
		if pinned, has := fact.Pins[name]; has && pinned != sym {
			return smtFact{}, false // the original fact meant a different binding of this name
		}
		pins[name] = sym
	}
	return smtFact{Expr: expr, Deps: smtFactDeps(expr), Pins: pins}, true
}

// substituteTypedIdents replaces identifiers by expressions, shallow-copying every node on a path to
// a replacement (so resolved fields stay) and carrying the analyzed type of each copied node.
// Unsupported node kinds that contain a replaced name decline.
func (a *Analyzer) substituteTypedIdents(expr ast.Expr, subst map[string]ast.Expr, depth int) (ast.Expr, bool) {
	if expr == nil {
		return nil, true
	}
	if depth > 64 {
		return nil, false
	}
	mentions := false
	for _, name := range exprIdentNames(expr) {
		if _, hit := subst[name]; hit {
			mentions = true
			break
		}
	}
	if !mentions {
		return expr, true
	}
	sub := func(e ast.Expr) (ast.Expr, bool) { return a.substituteTypedIdents(e, subst, depth+1) }
	var out ast.Expr
	switch n := expr.(type) {
	case *ast.Ident:
		if v, ok := subst[n.Name]; ok {
			return v, true
		}
		return n, true
	case *ast.ParenExpr:
		inner, ok := sub(n.Inner)
		if !ok {
			return nil, false
		}
		c := *n
		c.Inner = inner
		out = &c
	case *ast.UnaryExpr:
		if n.LoweredCall != nil {
			return nil, false
		}
		operand, ok := sub(n.Operand)
		if !ok {
			return nil, false
		}
		c := *n
		c.Operand = operand
		out = &c
	case *ast.BinaryExpr:
		if n.LoweredCall != nil {
			return nil, false
		}
		left, ok := sub(n.Left)
		if !ok {
			return nil, false
		}
		right, ok := sub(n.Right)
		if !ok {
			return nil, false
		}
		c := *n
		c.Left, c.Right = left, right
		out = &c
	case *ast.TernaryExpr:
		cond, ok := sub(n.Cond)
		if !ok {
			return nil, false
		}
		value, ok := sub(n.Value)
		if !ok {
			return nil, false
		}
		alt, ok := sub(n.Alt)
		if !ok {
			return nil, false
		}
		c := *n
		c.Cond, c.Value, c.Alt = cond, value, alt
		out = &c
	case *ast.FieldExpr:
		if n.Safe {
			return nil, false
		}
		object, ok := sub(n.Object)
		if !ok {
			return nil, false
		}
		c := *n
		c.Object = object
		out = &c
	case *ast.SliceExpr:
		object, ok := sub(n.Object)
		if !ok {
			return nil, false
		}
		start, ok := sub(n.Start)
		if !ok {
			return nil, false
		}
		end, ok := sub(n.End)
		if !ok {
			return nil, false
		}
		c := *n
		c.Object, c.Start, c.End = object, start, end
		out = &c
	case *ast.IndexExpr:
		if n.Index2 != nil || n.Fallback != nil || n.AsOverlayCall != nil {
			return nil, false
		}
		object, ok := sub(n.Object)
		if !ok {
			return nil, false
		}
		index, ok := sub(n.Index)
		if !ok {
			return nil, false
		}
		c := *n
		c.Object, c.Index = object, index
		out = &c
	case *ast.CastExpr:
		operand, ok := sub(n.Operand)
		if !ok {
			return nil, false
		}
		c := *n
		c.Operand = operand
		out = &c
	case *ast.CallExpr:
		if fnNames := exprIdentNames(n.Func); len(fnNames) != 0 {
			for _, name := range fnNames {
				if _, hit := subst[name]; hit {
					return nil, false // a receiver or callee path names a replaced local
				}
			}
		}
		if n.SafeReceiver != nil || n.HasArgForward || len(n.ResolvedCommonArgs) != 0 || len(n.ResolvedImplicitArgs) != 0 {
			return nil, false
		}
		subAll := func(list []ast.Expr) ([]ast.Expr, bool) {
			if list == nil {
				return nil, true
			}
			res := make([]ast.Expr, len(list))
			for i, arg := range list {
				r, ok := sub(arg)
				if !ok {
					return nil, false
				}
				res[i] = r
			}
			return res, true
		}
		args, ok := subAll(n.Args)
		if !ok {
			return nil, false
		}
		resolved, ok := subAll(n.ResolvedArgs)
		if !ok {
			return nil, false
		}
		c := *n
		c.Args = args
		c.ResolvedArgs = resolved
		out = &c
	case *ast.QuantifierExpr:
		// `In` is analyzed outside the quantifier's binder scope, while `Body` is
		// analyzed with every name in Vars bound. Substitute the source with the
		// caller's map, then mask shadowed names for the body. Decline a replacement
		// that would capture one of the body's binders; alpha-renaming here would
		// also require updating resolved binder references and their type metadata.
		in, ok := sub(n.In)
		if !ok {
			return nil, false
		}
		boundNames := make(map[string]bool, len(n.Vars))
		for _, name := range n.Vars {
			boundNames[name] = true
		}
		bodySubst := make(map[string]ast.Expr, len(subst))
		for name, value := range subst {
			if !boundNames[name] {
				bodySubst[name] = value
			}
		}
		if len(bodySubst) != 0 {
			bodyNames := make(map[string]bool)
			for _, name := range exprIdentNames(n.Body) {
				bodyNames[name] = true
			}
			for name, value := range bodySubst {
				if !bodyNames[name] {
					continue
				}
				replacementNames := exprIdentNames(value)
				for _, bound := range n.Vars {
					for _, replacementName := range replacementNames {
						if replacementName == bound {
							return nil, false
						}
					}
				}
			}
		}
		body, ok := a.substituteTypedIdents(n.Body, bodySubst, depth+1)
		if !ok {
			return nil, false
		}
		c := *n
		c.In, c.Body = in, body
		out = &c
	default:
		return nil, false
	}
	if t := a.exprTypes[expr]; t != nil {
		a.exprTypes[out] = t
	}
	return out, true
}
