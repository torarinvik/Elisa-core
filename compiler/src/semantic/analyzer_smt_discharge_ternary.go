package semantic

import (
	"fmt"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// ternaryProjectionTerm lowers a field read of a conditional value to the conditional of the field
// reads: only the selected arm is evaluated, and the ite masks the other arm's term.
func (tr *smtTranslator) ternaryProjectionTerm(n *ast.FieldExpr, tern *ast.TernaryExpr, env map[string]string) (string, bool) {
	cond, ok := tr.boolTerm(tern.Cond, env)
	if !ok {
		return "", false
	}
	arm := func(value ast.Expr) (string, bool) {
		field := &ast.FieldExpr{Position: n.Position, Object: value, Field: n.Field}
		if t := tr.a.exprType(n); t != nil {
			tr.a.exprTypes[field] = t
		}
		return tr.termEnv(field, env)
	}
	then, ok := arm(tern.Value)
	if !ok {
		return "", false
	}
	alt, ok := arm(tern.Alt)
	if !ok {
		return "", false
	}
	return "(ite " + cond + " " + then + " " + alt + ")", true
}

// smtPathConjuncts flattens a branch path's top-level `and` tree, including `not (A or B)` as
// `not A` and `not B`, into the guards that each hold on the path.
func smtPathConjuncts(path ast.Expr) []ast.Expr {
	switch e := path.(type) {
	case *ast.ParenExpr:
		return smtPathConjuncts(e.Inner)
	case *ast.BinaryExpr:
		if e.Op == lexer.TOKEN_AND {
			return append(smtPathConjuncts(e.Left), smtPathConjuncts(e.Right)...)
		}
	case *ast.UnaryExpr:
		if e.Op == lexer.TOKEN_NOT {
			inner := e.Operand
			for {
				p, ok := inner.(*ast.ParenExpr)
				if !ok {
					break
				}
				inner = p.Inner
			}
			if b, ok := inner.(*ast.BinaryExpr); ok && b.Op == lexer.TOKEN_OR {
				return append(
					smtPathConjuncts(&ast.UnaryExpr{Position: e.Position, Op: lexer.TOKEN_NOT, Operand: b.Left}),
					smtPathConjuncts(&ast.UnaryExpr{Position: e.Position, Op: lexer.TOKEN_NOT, Operand: b.Right})...)
			}
		}
	}
	return []ast.Expr{path}
}

// callResultStateIndependent reports whether a callee's result is a function of its arguments
// alone, so two calls with equal arguments may share one SMT symbol. A callee whose effect set
// includes anything beyond Abort (a global read or write, I/O, allocation, unsafe access, a user
// effect) may observe or change state between calls: `tick() - tick()` must not fold to zero.
// Each call still receives its own verified ensures.
func (a *Analyzer) callResultStateIndependent(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil {
		return false
	}
	if decl.IsGhost {
		return true
	}
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	if len(fnType.MutableGlobalPermissionRefs) != 0 || len(fnType.MutableGlobalSourceNames) != 0 {
		return false
	}
	for _, family := range fnType.Permissions {
		if family != "Abort" {
			return false
		}
	}
	for _, ref := range fnType.PermissionRefs {
		if ref.Name != "Abort" {
			return false
		}
	}
	return true
}

// smtStableDeclID names a declaration inside solver text without its heap address, so identical
// programs produce byte-identical queries across runs (persistent proof-cache keys depend on it).
// Distinct declarations stay distinct: declarations that share a name and source position (for
// example clones of one source declaration) get ordinals in first-use order.
func (a *Analyzer) smtStableDeclID(decl *ast.FuncDecl) string {
	if id, ok := a.smtDeclIDs[decl]; ok {
		return id
	}
	if a.smtDeclIDs == nil {
		a.smtDeclIDs = map[*ast.FuncDecl]string{}
	}
	if a.smtDeclIDOrdinals == nil {
		a.smtDeclIDOrdinals = map[string]int{}
	}
	pos := decl.Pos()
	base := fmt.Sprintf("%s@%s:%d:%d", decl.Name, pos.File, pos.Offset, pos.EndOffset)
	ordinal := a.smtDeclIDOrdinals[base]
	a.smtDeclIDOrdinals[base] = ordinal + 1
	id := base + "#" + strconv.Itoa(ordinal)
	a.smtDeclIDs[decl] = id
	return id
}

// ternaryBranchType is the type of a conditional's branches when its own node is untyped.
func (a *Analyzer) ternaryBranchType(expr ast.Expr) Type {
	ternary, ok := stripOptimizationParens(expr).(*ast.TernaryExpr)
	if !ok || ternary == nil {
		return nil
	}
	for _, branch := range []ast.Expr{ternary.Value, ternary.Alt} {
		if t := a.exprType(branch); t != nil {
			return t
		}
		if t := a.ternaryBranchType(branch); t != nil {
			return t
		}
	}
	return nil
}

// opaqueGuardedQuantifierBody keeps the range guard of `forall k in lo..<hi: P` (parsed as
// `not (lo <= k and k < hi) or P`) when P itself has no translation, and reads P as a fresh
// uninterpreted predicate of k. The predicate is unconstrained, so any query decided with it
// is decided for every P: an empty range proves the quantifier, and as a hypothesis it adds
// nothing about P.
func (tr *smtTranslator) opaqueGuardedQuantifierBody(n *ast.QuantifierExpr, qenv map[string]string) (string, bool) {
	if n == nil || n.Exists || len(n.Vars) != 1 {
		return "", false
	}
	or, ok := stripOptimizationParens(n.Body).(*ast.BinaryExpr)
	if !ok || or == nil || or.Op != lexer.TOKEN_OR {
		return "", false
	}
	not, ok := stripOptimizationParens(or.Left).(*ast.UnaryExpr)
	if !ok || not == nil || not.Op != lexer.TOKEN_NOT {
		return "", false
	}
	guard, ok := tr.boolTerm(not.Operand, qenv)
	if !ok {
		return "", false
	}
	tr.opaqueQuantifierBodies++
	name := "|@quantified_body_" + strconv.Itoa(tr.opaqueQuantifierBodies) + "|"
	tr.auxDecls = append(tr.auxDecls, "(declare-fun "+name+" (Int) Bool)\n")
	return "(or (not " + guard + ") (" + name + " " + qenv[n.Vars[0]] + "))", true
}
