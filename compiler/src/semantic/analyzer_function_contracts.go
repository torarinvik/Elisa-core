package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"sort"
	"strings"
)

// analyzeRequiresClauses type-checks a function's value-contract preconditions in the parameter
// scope (no `result` binding). Each must be bool. Analysis attaches types so the backend can emit
// the entry checks (debug builds only). Called from each body-analysis branch before the body.
func (a *Analyzer) analyzeRequiresClauses(fn *ast.FuncDecl) {
	// Reset the cross-variable bound-equality relation at every function entry. The relation is a
	// global analyzer field (like currentIndexBounds) but a top-level `n = xs.count` binding records
	// into it without a save/restore, so it must be cleared per function to avoid leaking equalities
	// from one function body into the next. This is the single per-function entry hook (called
	// unconditionally before each body, and nowhere else).
	a.currentBoundEqual = nil
	a.boundEqualShared = false
	a.ghostReadAllowed++ // `requires` is a spec position: ghost vars/functions are readable here.
	defer func() { a.ghostReadAllowed-- }()
	for i, req := range fn.Requires {
		if req == nil {
			continue
		}
		reqType := a.analyzeSpecClauseExpr(req, "requires")
		if reqType != nil && !IsBoolType(reqType) {
			a.errorf(req.Pos(), "requires clause must be bool, got %s", reqType)
		}
		// A `requires n == xs.count` precondition is an ASSUMPTION inside the body (the callee may rely
		// on it; callers must establish it — enforced by the runtime check and brick 86-5's static
		// discharge). Seed it as a bound equality so `for i in 0..<n: xs[i]` discharges in the body.
		a.collectBoundEqualitiesForCondition(req, true)
		// Likewise seed the interval prover from a numeric-comparison precondition (`requires
		// alignment >= 1`, `requires n < cap`): the clause holds throughout the body, so recording
		// its range fact lets tier-1/tier-2 AND the SMT modulo-soundness gate (provablyPositive) see
		// the bound — which is what makes a guarded `value % alignment` use the precise Euclidean
		// model rather than an opaque symbol. Reuses the branch-fact narrower (truthy = assumed).
		a.seedRangeFactsFromCondition(req)
		if proof := proofAt(fn.RequiresProofs, i); proof != nil {
			a.analyzeScopedProofGoal(proof.Pos(), proof.Goal, proof.Proof, true, "", "requires by scoped")
		}
	}
}

// seedRangeFactsFromCondition records the integer range fact implied by a boolean precondition onto
// the function-entry scope. A bare comparison (`x >= 1`) narrows directly; a conjunction (`a >= 0 and
// a < n`) seeds each side. Anything else is ignored (sound: fewer facts only forgoes proofs). This is
// the `requires`-clause analogue of the branch-flow narrowing already done for `if` guards.
func (a *Analyzer) seedRangeFactsFromCondition(cond ast.Expr) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		a.seedRangeFactsFromCondition(n.Inner)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.seedRangeFactsFromCondition(n.Left)
			a.seedRangeFactsFromCondition(n.Right)
		case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_EQEQ:
			a.gatherNumericRangeRefinement(a.currentScope, n, true)
		}
	}
}

// analyzeEnsureClauses type-checks a function's value-contract postconditions in a child scope that
// binds `result` to the return type (so `ensure result >= 0` works). Each must be bool. The backend
// emits the checks at every return (debug builds only). `old(...)` (the entry-time value of an
// expression) is recognized here via inEnsureContext and captured at function entry by the backend.
func (a *Analyzer) analyzeEnsureClauses(fn *ast.FuncDecl, fnType *FuncType) {
	if len(fn.EnsureValues) == 0 {
		return
	}
	saved := a.currentScope
	scope := NewScope(saved)
	if fnType != nil && fnType.Return != nil && !isVoidType(fnType.Return) {
		scope.Define(&Symbol{Name: "result", Kind: SymbolLocal, Type: fnType.Return})
	}
	a.currentScope = scope
	a.inEnsureContext = true
	a.ghostReadAllowed++ // `ensure` is a contract: ghost body locals are readable here.
	defer func() { a.inEnsureContext = false; a.ghostReadAllowed-- }()
	for i, e := range fn.EnsureValues {
		if e == nil {
			continue
		}
		t := a.analyzeSpecClauseExpr(e, "ensure")
		if t != nil && !IsBoolType(t) {
			a.errorf(e.Pos(), "ensure clause must be bool, got %s", t)
		}
		if proof := proofAt(fn.EnsureProofs, i); proof != nil {
			a.analyzeScopedProofGoal(proof.Pos(), proof.Goal, proof.Proof, true, "", "ensure by scoped")
		}
		// Left-to-right well-definedness: a later clause is evaluated (by the debug postcondition
		// checks, in declaration order, each aborting on failure) only after every earlier clause
		// held or was proven by its scoped proof, and a caller assumes the clauses jointly. So the
		// call preconditions inside later clauses may assume the earlier ones. The fact lives in this
		// clause-only scope and is gone when the ensure analysis ends.
		if t != nil && IsBoolType(t) {
			a.recordSMTAssertFact(e)
		}
	}
	a.currentScope = saved
}

// analyzeSpecClauseExpr type-checks a `requires`/`ensure` clause expression AND enforces that it is
// pure / side-effect-free — defense-in-depth mirroring the `where`/`refine` predicate and `law` body
// purity checks. A spec-position call `p(x)` is canonicalized by the verifier to a single
// deterministic uninterpreted symbol; that canonicalization is only SOUND when `p` is pure. An
// effectful or non-deterministic call (reading a mutable global, `random()`, `time()`, IO, mutation,
// allocation, …) would make the same syntactic clause denote different values at different points,
// so it must be rejected.
//
// The check reuses the existing effect-accumulation machinery rather than re-deriving purity: every
// effect a sub-expression performs is recorded into a.currentFunctionUsedPermissionRefs (the same set
// the law-purity check and @hot contract are judged against). We snapshot that set, analyze the
// clause, and reject if the clause introduced ANY effect. Pure user-helper calls (`ensure result ==
// sorted(xs)`) record no effects and are therefore allowed — exactly the law-body rule. Spec-position
// effects are not attributed to the enclosing function, so the snapshot is restored afterwards.
func (a *Analyzer) analyzeSpecClauseExpr(expr ast.Expr, clause string) Type {
	if expr == nil {
		return nil
	}
	// Track each clause independently. Ghost values/functions have no runtime
	// representation, so their clauses must be erased from debug codegen too.
	savedGhostReadSeen := a.ghostReadSeen
	a.ghostReadSeen = false
	defer func() { a.ghostReadSeen = savedGhostReadSeen }()
	savedRefs := a.currentFunctionUsedPermissionRefs
	start := len(savedRefs)
	t := a.analyzeExpr(expr)
	if a.ghostReadSeen {
		a.markGhostContract(expr)
	}
	if introduced := specClauseEffects(a.currentFunctionUsedPermissionRefs[start:]); len(introduced) > 0 {
		msg := fmt.Sprintf("%s clause must be pure but uses the `%s` effect; a contract may not perform effects (no IO, allocation, mutation, time, or randomness) — an effectful or non-deterministic clause is unsound because the verifier treats a spec-position call as a single deterministic value", clause, lawEffectName(introduced[0]))
		// A common cause is reading a module-level `global` inside the clause. If the offending read is
		// of an IMMUTABLE global, `const` is the working fix: a `const` read is pure (records no effect),
		// so the clause is admissible and the value still folds in the prover.
		if hint := a.contractGlobalReadConstHint(expr, introduced); hint != "" {
			msg += "; " + hint
		}
		a.errorf(expr.Pos(), "%s", msg)
	}
	// Spec-position effects are verification-only and must not leak into the enclosing function's
	// inferred effect set; restore the snapshot regardless of outcome.
	a.currentFunctionUsedPermissionRefs = savedRefs
	return t
}

// specClauseEffects drops pointer arithmetic: computing an address reads and changes nothing, so
// `result.data == view.data + k` is pure. Reading through that address stays an effect.
func specClauseEffects(refs []ast.PermissionRef) []ast.PermissionRef {
	var effects []ast.PermissionRef
	for _, ref := range refs {
		if ref.Name == "Unsafe" && ref.Member == "PointerArithmetic" {
			continue
		}
		effects = append(effects, ref)
	}
	return effects
}

// contractGlobalReadConstHint returns an actionable "make it a `const`" suggestion when a rejected
// contract clause's effect is a `global` read AND the clause references at least one IMMUTABLE global.
// Reading a `const` is pure (no effect), so converting the immutable global fixes the purity error
// while keeping the value foldable in the prover. Returns "" when the effect is not a global read or
// no immutable global is named (e.g. the effect is a mutable-global read, IO, or a call).
func (a *Analyzer) contractGlobalReadConstHint(expr ast.Expr, introduced []ast.PermissionRef) string {
	readsGlobal := false
	for _, ref := range introduced {
		if ref.Name == "Global" {
			readsGlobal = true
			break
		}
	}
	if !readsGlobal {
		return ""
	}
	var names []string
	seen := map[string]bool{}
	for name := range collectExprIdents(expr, nil) {
		if seen[name] {
			continue
		}
		sym, _, ok := a.lookupVisibleGlobal(name)
		if !ok || sym == nil || sym.Kind != SymbolGlobal || sym.Mutable {
			continue
		}
		if _, ok := sym.Node.(*ast.GlobalDecl); !ok {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	if len(names) == 1 {
		return fmt.Sprintf("declare %q as `const` (not `global`) so it can be read in a contract — a `const` read is pure", names[0])
	}
	return fmt.Sprintf("declare %s as `const` (not `global`) so they can be read in a contract — a `const` read is pure", strings.Join(quoteEach(names), ", "))
}

// quoteEach returns each string wrapped in double quotes, for readable name lists in diagnostics.
func quoteEach(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}

// exprReferencesGhostField reports whether an analyzed contract expression reads a `ghost` struct
// field. Such a clause is verification-only: the ghost field is erased in codegen, so a runtime
// check over it would dereference a field that does not exist at runtime. The clause is kept for
// static discharge but must be dropped from the backend's runtime-check set — mirroring the
// ghost-invariant split in analyzeStructInvariants. Walks the contract-expression shapes.
func (a *Analyzer) exprReferencesGhostField(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.ParenExpr:
		return a.exprReferencesGhostField(n.Inner)
	case *ast.UnaryExpr:
		return a.exprReferencesGhostField(n.Operand)
	case *ast.BinaryExpr:
		return a.exprReferencesGhostField(n.Left) || a.exprReferencesGhostField(n.Right)
	case *ast.TernaryExpr:
		return a.exprReferencesGhostField(n.Cond) || a.exprReferencesGhostField(n.Value) || a.exprReferencesGhostField(n.Alt)
	case *ast.IndexExpr:
		return a.exprReferencesGhostField(n.Object) || a.exprReferencesGhostField(n.Index) || a.exprReferencesGhostField(n.Fallback)
	case *ast.CallExpr:
		if a.exprReferencesGhostField(n.Func) {
			return true
		}
		for _, arg := range n.Args {
			if a.exprReferencesGhostField(arg) {
				return true
			}
		}
		return false
	case *ast.FieldExpr:
		if st, ok := stripRefForBounds(a.exprType(n.Object)).(*StructType); ok && st != nil {
			if f, ok := st.Fields[n.Field]; ok && f.Ghost {
				return true
			}
		}
		return a.exprReferencesGhostField(n.Object)
	default:
		return false
	}
}

// stripGhostFieldContractsForRuntime drops ghost-field-referencing `requires`/`ensure` clauses from
// the codegen-visible contract slices of the current function, AFTER static discharge has run over
// them. The backend emits debug runtime checks from FuncDecl.Requires/EnsureValues; a clause over an
// erased ghost field has no runtime representation, so leaving it in produces a "no field <ghost>"
// codegen error under non-strict / -emit test. Soundness: this runs post-discharge, so a false
// ghost-referencing refinement is still reported statically; only the runtime check is suppressed.
func (a *Analyzer) stripGhostFieldContractsForRuntime() {
	fn := a.currentFuncDecl
	if fn == nil {
		return
	}
	if len(fn.EnsureValues) != 0 {
		kept := fn.EnsureValues[:0:0]
		for _, e := range fn.EnsureValues {
			if a.exprReferencesGhostField(e) {
				continue
			}
			kept = append(kept, e)
		}
		fn.EnsureValues = kept
	}
	if len(fn.Requires) != 0 {
		kept := fn.Requires[:0:0]
		for _, r := range fn.Requires {
			if a.exprReferencesGhostField(r) {
				continue
			}
			kept = append(kept, r)
		}
		fn.Requires = kept
	}
}

func proofAt(proofs []*ast.ProofBlockStmt, i int) *ast.ProofBlockStmt {
	if i < 0 || i >= len(proofs) {
		return nil
	}
	return proofs[i]
}
