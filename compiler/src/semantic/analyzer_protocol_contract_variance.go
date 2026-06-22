package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Behavioral subtyping (Liskov–Wing) for protocol conformance (docs P1).
//
// A protocol method may carry value contracts (`requires`/`ensure`). Every conforming `impl`
// method must REFINE that contract along the correct variance axis:
//
//   - PRECONDITION is CONTRAVARIANT: the impl may require LESS, never more. The obligation is
//     `protocol.requires ⟹ impl.requires` — the protocol's precondition must already establish
//     the impl's. An impl that adds an unentailed conjunct (a STRENGTHENED precondition) is
//     unsound: a generic caller, knowing only the protocol's contract, would not establish it.
//
//   - POSTCONDITION is COVARIANT: the impl may promise MORE, never less. The obligation is
//     `impl.ensure ⟹ protocol.ensure` — whatever the impl guarantees must imply what the
//     protocol promised, so a generic caller may soundly ASSUME the protocol's `ensure`.
//
// The two obligations are discharged with the same prover tiers as ordinary function contracts,
// treating each shared parameter (`self`, `i`, …) as a fresh free symbol and each method call in a
// predicate (`self.count()`) as an uninterpreted function (canonicalized so identical calls denote
// one symbol). This is exactly the entailment the higher-order param-contract check performs
// between two function contracts; here it is attached to impl-vs-protocol conformance.
//
// P4 NOTE: the variance axes for effects/errors/changes will be added here. Keep each axis a
// separate, clearly-named obligation builder (see contractVarianceObligation) so the extension is
// additive: a new axis is a new call to the shared entailment primitive, not a rewrite.

// methodValueContracts is the requires/ensure pair extracted from either a bodiless protocol
// method (ExternFuncDecl), a default/impl method (FuncDecl), or an extern impl method.
type methodValueContracts struct {
	requires []ast.Expr
	ensures  []ast.Expr
	pos      lexer.Pos
}

func protocolMethodContracts(m *StaticInterfaceMethod) methodValueContracts {
	var c methodValueContracts
	if m == nil {
		return c
	}
	if m.Decl != nil {
		c.requires = m.Decl.Requires
		c.ensures = m.Decl.EnsureValues
		c.pos = m.Decl.Position
		return c
	}
	if m.Default != nil {
		c.requires = m.Default.Requires
		c.ensures = m.Default.EnsureValues
		c.pos = m.Default.Position
	}
	return c
}

func implMethodContracts(member ast.Node) methodValueContracts {
	var c methodValueContracts
	switch n := member.(type) {
	case *ast.FuncDecl:
		c.requires = n.Requires
		c.ensures = n.EnsureValues
		c.pos = n.Position
	case *ast.ExternFuncDecl:
		c.requires = n.Requires
		c.ensures = n.EnsureValues
		c.pos = n.Position
	}
	return c
}

// checkProtocolImplContractVariance enforces behavioral subtyping for one impl method against its
// protocol method declaration. It emits an error when the impl strengthens a precondition or
// weakens a postcondition. `methodInfo` is the protocol-side method; `member` is the impl AST node;
// `interfaceName`/`receiver`/`name` are for diagnostics.
func (a *Analyzer) checkProtocolImplContractVariance(methodInfo *StaticInterfaceMethod, member ast.Node, interfaceName string, receiver Type, name string) {
	if a == nil || methodInfo == nil {
		return
	}
	proto := protocolMethodContracts(methodInfo)
	impl := implMethodContracts(member)

	// Precondition (contravariant): protocol.requires ⟹ each impl.requires conjunct.
	// If the protocol has NO precondition the premise set is empty (the function's own facts only),
	// so any non-trivial impl precondition is a strengthening and is rejected.
	for _, clause := range impl.requires {
		if clause == nil {
			continue
		}
		if a.contractVarianceObligation(proto.requires, clause) {
			continue
		}
		a.errorf(clause.Pos(), "impl method %q for protocol %q on %s strengthens the precondition: %s is not entailed by the protocol's `requires` (preconditions are contravariant — an impl may require LESS, not more)",
			name, interfaceName, receiver.String(), renderContractClause(clause))
	}

	// Postcondition (covariant): impl.ensure ⟹ each protocol.ensure conjunct.
	// If the impl has NO postcondition the premise set is empty, so any protocol postcondition the
	// impl fails to re-establish is a weakening and is rejected.
	for _, clause := range proto.ensures {
		if clause == nil {
			continue
		}
		if a.contractVarianceObligation(impl.ensures, clause) {
			continue
		}
		a.errorf(impl.pos, "impl method %q for protocol %q on %s weakens the postcondition: it does not guarantee the protocol's `ensure` %s (postconditions are covariant — an impl must promise AT LEAST as much)",
			name, interfaceName, receiver.String(), renderContractClause(clause))
	}
}

// contractVarianceObligation is the shared entailment primitive for the variance check: it proves
// `(and premises...) ⟹ goal` where premises and goal are boolean predicates over the SAME parameter
// names. Each free identifier is treated as an unconstrained symbol and each method/function call as
// an uninterpreted function (canonicalized so an identical call on both sides denotes one symbol).
// Returns true iff the implication is PROVED (sound: only `unsat` of the negation concludes).
//
// This is the single point P4 will reuse for effect/error/change variance: build the premise/goal
// for the new axis and call here.
func (a *Analyzer) contractVarianceObligation(premises []ast.Expr, goal ast.Expr) bool {
	if goal == nil {
		return true
	}
	// A goal that is syntactically among the premises is trivially entailed — handles the common
	// "same precondition" / "same postcondition" case without invoking the solver (and works even
	// when the predicate is outside the SMT fragment).
	for _, p := range premises {
		if p != nil && sameContractClause(p, goal) {
			return true
		}
	}
	solver := a.openSMT()
	if solver == nil {
		// No solver available: fall back to syntactic entailment only (already checked above).
		// Decline (return false) on anything non-syntactic so the rule stays SOUND — a missed
		// proof becomes a (conservative) variance error rather than an unsound acceptance.
		return false
	}
	tr := a.newSMTTranslator(nil)
	// Build a shared env: every free identifier referenced by either side maps to a declared Int
	// symbol of its own name, so `self`/`i` denote the same symbol in premises and goal.
	env := map[string]string{}
	bindContractIdents := func(e ast.Expr) {
		forEachFreeIdent(e, func(name string) {
			if _, seen := env[name]; seen {
				return
			}
			tr.decls[name] = true
			env[name] = smtVar(name)
		})
	}
	for _, p := range premises {
		bindContractIdents(p)
	}
	bindContractIdents(goal)

	// Lower the goal first (it must be in the SMT fragment, else we soundly decline).
	goalTerm, ok := tr.boolTerm(goal, env)
	if !ok {
		return false
	}
	// Lower each premise; a premise outside the fragment is simply dropped (omitting a hypothesis
	// only makes the proof HARDER, never unsound).
	hyps := ""
	for _, p := range premises {
		if p == nil {
			continue
		}
		h, ok := tr.boolTerm(p, env)
		if !ok {
			continue
		}
		hyps += "(assert " + h + ")\n"
	}
	proven, _ := a.smtCheckQuery(tr, hyps, goalTerm)
	return proven
}

// sameContractClause reports whether two contract predicates are structurally identical (after
// stripping parens). Used as the cheap reflexive entailment fast-path so the common "same
// precondition" / "same postcondition" case never needs the solver and works even outside the SMT
// fragment.
func sameContractClause(x, y ast.Expr) bool {
	x, y = stripContractParens(x), stripContractParens(y)
	switch xn := x.(type) {
	case *ast.Ident:
		yn, ok := y.(*ast.Ident)
		return ok && xn.Name == yn.Name
	case *ast.IntLit:
		yn, ok := y.(*ast.IntLit)
		return ok && xn.Value == yn.Value
	case *ast.BoolLit:
		yn, ok := y.(*ast.BoolLit)
		return ok && xn.Value == yn.Value
	case *ast.BinaryExpr:
		yn, ok := y.(*ast.BinaryExpr)
		return ok && xn.Op == yn.Op && sameContractClause(xn.Left, yn.Left) && sameContractClause(xn.Right, yn.Right)
	case *ast.UnaryExpr:
		yn, ok := y.(*ast.UnaryExpr)
		return ok && xn.Op == yn.Op && sameContractClause(xn.Operand, yn.Operand)
	case *ast.FieldExpr:
		yn, ok := y.(*ast.FieldExpr)
		return ok && xn.Field == yn.Field && sameContractClause(xn.Object, yn.Object)
	case *ast.CallExpr:
		yn, ok := y.(*ast.CallExpr)
		if !ok || len(xn.Args) != len(yn.Args) || !sameContractClause(xn.Func, yn.Func) {
			return false
		}
		for i := range xn.Args {
			if !sameContractClause(xn.Args[i], yn.Args[i]) {
				return false
			}
		}
		return true
	case *ast.IndexExpr:
		yn, ok := y.(*ast.IndexExpr)
		return ok && sameContractClause(xn.Object, yn.Object) && sameContractClause(xn.Index, yn.Index)
	}
	return false
}

func stripContractParens(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok || p == nil {
			return e
		}
		e = p.Inner
	}
}

// renderContractClause produces a compact human-readable form of a contract predicate for the
// variance diagnostic. It is best-effort (falls back to a generic placeholder for forms it does not
// render); only the message text depends on it, never the soundness verdict.
func renderContractClause(e ast.Expr) string {
	switch n := stripContractParens(e).(type) {
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		return n.Value
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.BinaryExpr:
		return renderContractClause(n.Left) + " " + lexer.TokenName(n.Op) + " " + renderContractClause(n.Right)
	case *ast.UnaryExpr:
		return lexer.TokenName(n.Op) + renderContractClause(n.Operand)
	case *ast.FieldExpr:
		return renderContractClause(n.Object) + "." + n.Field
	case *ast.CallExpr:
		s := renderContractClause(n.Func) + "("
		for i, arg := range n.Args {
			if i > 0 {
				s += ", "
			}
			s += renderContractClause(arg)
		}
		return s + ")"
	case *ast.IndexExpr:
		return renderContractClause(n.Object) + "[" + renderContractClause(n.Index) + "]"
	}
	return "<clause>"
}

// protocolMethodForBoundCall resolves a (already-rewritten) bound-type-param protocol-method call to
// the protocol method it dispatches to, returning the StaticInterfaceMethod and the protocol's
// declared parameter list. A bound-type-param call is rewritten by rewriteBoundTypeParamMethodCall to
// the static type-path form `T.method(receiver, args...)`, where `T` is a bare type-param Ident whose
// bound declares `method`. Returns ok=false for any call that is not exactly that form (so a concrete
// impl call, a free function, etc. inject nothing).
func (a *Analyzer) protocolMethodForBoundCall(call *ast.CallExpr) (*StaticInterfaceMethod, []ast.ParamDecl, bool) {
	if a == nil || call == nil {
		return nil, nil, false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field == "" {
		return nil, nil, false
	}
	obj, ok := field.Object.(*ast.Ident)
	if !ok || obj == nil || obj.Name == "" {
		return nil, nil, false
	}
	iface, ok := a.lookupTypeParamInterface(obj.Name)
	if !ok || iface == nil {
		return nil, nil, false
	}
	method, ok := iface.Methods[field.Field]
	if !ok || method == nil || method.Signature == nil {
		return nil, nil, false
	}
	// The protocol method's declared params (receiver-first, e.g. [self, p]) align positionally with
	// the rewritten static-call args [receiver, args...].
	var params []ast.ParamDecl
	switch {
	case method.Decl != nil:
		params = method.Decl.Params
	case method.Default != nil:
		params = method.Default.Params
	default:
		return nil, nil, false
	}
	return method, params, true
}

// seedProtocolEnsureFacts completes protocol-contract composition (docs P1, behavioral subtyping): at
// a generic call to a protocol method through a bounded type param, after the caller half PROVED the
// protocol method's `requires`, the caller may ASSUME the protocol method's `ensure` — because every
// conforming impl must (covariantly) re-establish it (checkProtocolImplContractVariance). This injects
// each protocol `ensure`, with `result` substituted by the binding the call value is assigned to and
// the method's params substituted by the actual receiver/args, as a flow fact the SMT lane and affine
// prover can USE downstream (e.g. `text = r.read(p)` then `ensure text.count >= 0`).
//
// SOUNDNESS:
//   - We inject ONLY the PROTOCOL's declared `ensure`, never any concrete impl's (the generic caller
//     does not know the impl). An impl promising MORE never leaks its extra ensure here.
//   - Substitution is complete-or-decline per clause (substituteLemmaEnsure): a clause that cannot be
//     faithfully rewritten into the caller's terms is dropped — it never records a fact whose meaning
//     drifted.
//   - `result` binds to the call value's binding (the immutable local just declared), so a downstream
//     read of that binding (`text.count`) resolves to the SAME SMT length symbol as the injected fact.
func (a *Analyzer) seedProtocolEnsureFacts(name string, value ast.Expr) {
	if a == nil || a.currentScope == nil || value == nil || name == "" {
		return
	}
	call, ok := a.proofCallExpr(value)
	if !ok || call == nil {
		return
	}
	method, params, ok := a.protocolMethodForBoundCall(call)
	if !ok {
		return
	}
	proto := protocolMethodContracts(method)
	if len(proto.ensures) == 0 {
		return
	}
	args := proofCallArgs(call)
	// Map `result` to the binding, and each protocol param (self-first) to the rewritten call's
	// positional arg. The static-call form already carries the receiver as args[0], aligned with the
	// protocol method's first param (`self`).
	subst := map[string]ast.Expr{
		"result": &ast.Ident{Position: call.Pos(), Name: name},
	}
	for i, param := range params {
		if param.Name == "" || i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	for _, clause := range proto.ensures {
		if clause == nil {
			continue
		}
		rewritten, ok := substituteLemmaEnsure(clause, subst)
		if !ok {
			continue
		}
		a.recordSMTAssertFact(rewritten)
		a.recordProofWithClass(call.Pos(), "postcondition of protocol method "+method.Name, "ensure", ProofProvenContract, ProofClassContract, nil, "")
		if a.currentProofCitation != nil {
			a.currentProofCitation.Facts = append(a.currentProofCitation.Facts, rewritten)
		}
	}
}

// forEachFreeIdent visits every identifier name referenced in a contract predicate. It is an
// over-approximation (it also visits field/call sub-identifiers), which is fine: declaring an extra
// unconstrained Int symbol never affects soundness.
func forEachFreeIdent(e ast.Expr, visit func(name string)) {
	switch n := e.(type) {
	case nil:
		return
	case *ast.Ident:
		visit(n.Name)
	case *ast.ParenExpr:
		forEachFreeIdent(n.Inner, visit)
	case *ast.BinaryExpr:
		forEachFreeIdent(n.Left, visit)
		forEachFreeIdent(n.Right, visit)
	case *ast.UnaryExpr:
		forEachFreeIdent(n.Operand, visit)
	case *ast.FieldExpr:
		forEachFreeIdent(n.Object, visit)
	case *ast.CallExpr:
		forEachFreeIdent(n.Func, visit)
		for _, arg := range n.Args {
			forEachFreeIdent(arg, visit)
		}
	case *ast.IndexExpr:
		forEachFreeIdent(n.Object, visit)
		forEachFreeIdent(n.Index, visit)
	}
}
