package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §6/§6b — function-scoped flow detectors, run alongside the loop-scoped R1–R6
// (docs/121) from checkFlowComplexity. Three rules, all keyed on PARSER-SET marks so they
// are zero-FP by construction (they can only ever fire on syntax the programmer wrote,
// never on a desugar):
//
//   - Strict block-`if` ban (§6b, strict mode ONLY): a written block `if:` statement is an
//     error — every decision must be a value, an exit, an arm, or a transition. Keys on
//     IfStmt.FromSource (set only in lowerIfClauses' plain-condition path); postfix-guard
//     desugars, loop-header wrappers, machine lowerings etc. never carry the mark. A
//     condition containing an `is`-test is exempt (the checked-destructure family, docs/80).
//
//   - Shape re-tests (§6, warn tier): a ladder of source `if … is PATTERN:` probes ≥3 deep,
//     each level re-probing a value bound by the previous level's pattern — this is ONE deep
//     pattern (docs/122). Keys on MatchStmt.FromSourceIf.
//
//   - Shadow-prone elif tables (§6, warn tier): a source if/elif chain ≥3 conditions long
//     whose conditions are ALL equality tests of the same scrutinee against literals — this
//     is a decision table; `when` declares the disjointness/totality the ladder only implies.
//
// The `can ComplexFlow:` grant silences all three for the statements it lexically covers.

// checkFlowFunctionShapes is the per-function entry, called from checkFlowComplexity (so it
// is a no-op when the flow lints are off).
func (a *Analyzer) checkFlowFunctionShapes(fn *ast.FuncDecl) {
	if a == nil || fn == nil || a.flowLintMode == FlowLintOff {
		return
	}
	seenChain := map[*ast.IfStmt]bool{}
	a.walkFlowFunctionStmts(fn.Body, false, seenChain)
}

func (a *Analyzer) walkFlowFunctionStmts(stmts []ast.Stmt, inGrant bool, seenChain map[*ast.IfStmt]bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			if n.FromSource && !inGrant && !seenChain[n] {
				a.checkShadowElifTable(n, seenChain)
				a.checkShapeRetestLadder(n, seenChain)
				a.checkStrictBlockIf(n)
			}
			a.walkFlowFunctionStmts(n.Then, inGrant, seenChain)
			for _, elif := range n.Elifs {
				a.walkFlowFunctionStmts(elif.Body, inGrant, seenChain)
			}
			a.walkFlowFunctionStmts(n.Else, inGrant, seenChain)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				a.walkFlowFunctionStmts(arm.Body, inGrant, seenChain)
			}
		case *ast.WhileStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.ForStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.IterForStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.ScopeStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.InStoreStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.RegionStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.PoolStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.LockStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.DeferStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant, seenChain)
		case *ast.CanStmt:
			a.walkFlowFunctionStmts(n.Body, inGrant || refsGrantComplexFlow(n.Permissions), seenChain)
		}
	}
}

// --- strict block-`if` ban (§6b) ---

// checkStrictBlockIf errors on a written block `if:` under strict flow. Exempt when any
// condition in the chain contains an `is`-test: `if EXPR is …` is a checked destructure
// (a guard with a binding), not a decision — the canonical refinement spelling (docs/80).
func (a *Analyzer) checkStrictBlockIf(n *ast.IfStmt) {
	if a.flowLintMode != FlowLintStrict {
		return
	}
	if condContainsIsTest(n.Cond) {
		return
	}
	if isLegitimateGuardBlock(n) {
		return
	}
	a.errorf(n.Position, "strict flow [-Wflow-strict]: block `if` statement — every decision must be "+
		"a value, an exit, an arm, or a transition (docs/125 §6b). Use a postfix guard (`STMT if COND`), "+
		"a value selection (`x = A if c else B`, `when`), a `match` arm, or a `machine` state; or state "+
		"the exception with `can ComplexFlow:`")
}

// isLegitimateGuardBlock reports whether a source block `if` is a legitimate conditional
// COMPUTATION (a guard), not a hidden decision tree — docs/125 §6b exemption (ratified
// 2026-07-14). The distinguishing, zero-FP feature: the branch BINDS a local (a `=`), which
// no postfix guard, value-selection, or ternary can express (you cannot conditionally bind).
// Such a block — `if COND: x = <compute>; <use x>` — is the ONLY way to conditionally
// destructure/compute-and-act, so flagging it would force a `can ComplexFlow:` grant (which
// docs/125 wants to FALL, not rise). Kept STRICT so genuine hidden machines still fire:
//   - an if/elif chain is a decision TABLE (→ `when`/`match`), never exempt;
//   - a body that itself branches (a nested `if`/`match`) is a decision TREE, never exempt;
//   - a binding-FREE block (single or multi postfix-reducible effects) stays flagged — the
//     programmer can split it to postfix guards (they know the condition's purity).
func isLegitimateGuardBlock(n *ast.IfStmt) bool {
	if len(n.Elifs) > 0 {
		return false
	}
	if bodyHasNestedDecision(n.Then) || bodyHasNestedDecision(n.Else) {
		return false
	}
	if bodyBindsLocal(n.Then) || bodyBindsLocal(n.Else) {
		return true
	}
	// Guarded loop or guarded MATCH (docs/125 §6b broadening, ratified 2026-07-14 wave
	// audits): `if COND:` whose body is exactly one loop or one match. A loop is a
	// computation, not a decision — there is no arm/transition form for "maybe iterate",
	// and hoisting the guard into the loop condition changes evaluation. A match IS the
	// sanctioned decision form and cannot take a postfix guard, so `if c: match …` is the
	// only spelling of a guarded, already-modelled decision. The one-statement shape keeps
	// this exact: extra trailing effects fall to the multi-statement rule below.
	if len(n.Else) == 0 && len(n.Then) == 1 && (stmtIsLoop(n.Then[0]) || stmtIsMatch(n.Then[0])) {
		return true
	}
	// Straight-line multi-effect guard: `if COND: eff1; eff2; …` (binding-free, no nested
	// decisions). Splitting to per-statement postfix guards would re-evaluate COND once per
	// statement — wrong if COND is impure and noisy even when pure — so the block is the
	// honest spelling. A SINGLE-statement body stays flagged: it is exactly one postfix
	// guard with no duplication, so the pressure to fold it remains.
	if len(n.Else) == 0 && len(n.Then) >= 2 {
		return true
	}
	// Two-way effect alternation: a plain if/else (no elif — a chain is a table) whose
	// branches are straight-line and at least one side carries 2+ statements. With no
	// shared scrutinee there is no when/match arm to give it, and a ternary cannot carry
	// statements. The both-sides-single case stays flagged — that is ternary/value-if
	// territory (`x = A if c else B`).
	if len(n.Else) > 0 && (len(n.Then) >= 2 || len(n.Else) >= 2) {
		return true
	}
	return false
}

// stmtIsLoop reports whether the statement is a loop construct (while/for/iterator-for/
// parallel-for) — the shapes the guarded-loop exemption covers. A captured/header loop
// (docs/119 §6 — `while cond |x|:`, `for … |acc=0|:`) does NOT reach here as a bare loop:
// wrapLoopHeader lowers it to a synthesized `if true:` scope wrapper around its capture-init
// decls + the loop. That wrapper is transparent scoping, not a decision, so we look through it
// (else `if COND: while … |x|:` — the ubiquitous "skip the loop when empty" guard — would be
// flagged though its uncaptured twin is exempt).
func stmtIsLoop(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt:
		return true
	}
	if body, ok := loopHeaderScopeBody(stmt); ok {
		return stmtsAreHeaderLoop(body)
	}
	return false
}

// loopHeaderScopeBody returns the body of a synthesized loop-header scope wrapper and true, or
// (nil, false). wrapLoopHeader (parser/loop_header.go) emits `if true: <decls…, loop>` for a
// captured/header loop: a NON-source `if` whose condition is the literal `true`, no elif/else.
// A hand-written `if true:` is FromSource and never matches, so this keys only on the synthesized
// shape (zero false positives).
func loopHeaderScopeBody(stmt ast.Stmt) ([]ast.Stmt, bool) {
	s, ok := stmt.(*ast.IfStmt)
	if !ok || s.FromSource || len(s.Elifs) > 0 || len(s.Else) > 0 {
		return nil, false
	}
	if b, ok := s.Cond.(*ast.BoolLit); ok && b.Value {
		return s.Then, true
	}
	return nil, false
}

// stmtsAreHeaderLoop reports whether a loop-header scope-wrapper body is exactly a run of
// capture-initializer VarDecls followed by a single loop — the desugaring of one captured loop,
// nothing more (so an unrelated `if true:` block carrying other statements is not mistaken for a
// guarded loop).
func stmtsAreHeaderLoop(stmts []ast.Stmt) bool {
	sawLoop := false
	for _, s := range stmts {
		switch s.(type) {
		case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt:
			if sawLoop {
				return false
			}
			sawLoop = true
		case *ast.VarDeclStmt:
			if sawLoop {
				return false
			}
		default:
			return false
		}
	}
	return sawLoop
}

// bodyHasNestedDecision reports whether a branch body directly contains a SOURCE block `if`
// — a hidden nested decision that makes the block a tree (hidden state machine), not a
// straight guard. A `match` does NOT disqualify: it is the sanctioned arm-based decision
// form, so a guard wrapping one is a guard over an already-modelled decision. Loops are not
// decisions and do not disqualify (they recurse through the normal walk).
func bodyHasNestedDecision(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.IfStmt:
			// Only a SOURCE block `if` can make the parent a tree. Parser-synthesized ifs —
			// postfix guards (`x <- v if c`), `break if c`, guarded returns, loop-header scope
			// wrappers — are sanctioned forms; their desugared IfStmt children must not
			// disqualify the parent.
			if !s.FromSource {
				continue
			}
			// ...and only a REAL nested decision makes a tree. A checked destructure (is-test
			// condition) or an already-exempt guard (guarded loop/match, binding guard,
			// straight-line block, uneven alternation) is a sanctioned NON-decision by §6b's
			// own reasoning ("a loop is a computation, not a decision"; an is-test is a
			// "checked destructure") — so a guard wrapping only such forms plus effects is not
			// a HIDDEN machine; it is judged by its own shape below. A plain nested `if` or an
			// if/elif table IS a real decision and still makes a tree; a real decision buried
			// deeper is surfaced at its own site by the ordinary walk, never lost.
			if condContainsIsTest(s.Cond) || isLegitimateGuardBlock(s) {
				continue
			}
			return true
		}
	}
	return false
}

// stmtIsMatch reports whether the statement is a match — the sanctioned arm-based decision
// form. A body containing one is already modelled, so it never makes the parent a "tree".
func stmtIsMatch(stmt ast.Stmt) bool {
	_, ok := stmt.(*ast.MatchStmt)
	return ok
}

// bodyBindsLocal reports whether a branch body introduces a NEW local binding — the feature
// that makes the block irreducible to any guard/value form (you cannot conditionally *bind*).
// Two spellings qualify, both genuine new bindings:
//   - `x = <compute>`            → *ast.VarDeclStmt
//   - `rebind a, b = <destr>`    → *ast.TupleBindStmt with Declare (a declaring destructure)
// A reassignment (`x <- y`, a non-declaring TupleBindStmt/AssignStmt) does NOT qualify: it is
// postfix-guardable (`x <- y if COND`), so a block of only reassignments stays flagged.
func bodyBindsLocal(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			return true
		case *ast.TupleBindStmt:
			if s.Declare {
				return true
			}
		}
	}
	return false
}

// condContainsIsTest reports whether a condition tree contains an `is` refinement test —
// `x is E.A`, `x is E.B(v)` — as a bare operand or under not/and/or/parens.
func condContainsIsTest(cond ast.Expr) bool {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		if e.Op == lexer.TOKEN_IS {
			return true
		}
		if e.Op == lexer.TOKEN_AND || e.Op == lexer.TOKEN_OR {
			return condContainsIsTest(e.Left) || condContainsIsTest(e.Right)
		}
	case *ast.UnaryExpr:
		if e.Op == lexer.TOKEN_NOT {
			return condContainsIsTest(e.Operand)
		}
	case *ast.ParenExpr:
		return condContainsIsTest(e.Inner)
	case *ast.VariantTestExpr:
		return true
	case *ast.StructTestExpr:
		return true
	case *ast.OptionalBindExpr:
		// The bare refinement bind `if maybe is value:` (docs/80) parses to its own
		// node, not a TOKEN_IS BinaryExpr — same checked-destructure family, same
		// exemption.
		return true
	case *ast.IsAliasExpr:
		return true
	}
	return false
}

// --- shape re-tests (§6) ---

// checkShapeRetestLadder warns when source `if … is PATTERN:` probes nest ≥3 deep, each
// level re-probing a value the previous probe's pattern bound — one deep pattern (docs/122)
// expresses the whole ladder in a single arm. A probe is an IfStmt whose ENTIRE condition
// is one `is`-test (a mixed `a > 0 and x is P` condition is not cleanly mergeable, so it
// never counts), and depth counts only DIRECT nesting (the next probe is a direct statement
// of the previous probe's then-block) — both restrictions serve the zero-FP bar. Fires once,
// at the ladder head; members are recorded so they are not re-reported as sub-ladders.
func (a *Analyzer) checkShapeRetestLadder(n *ast.IfStmt, seenChain map[*ast.IfStmt]bool) {
	const shapeRetestDepth = 3
	if _, ok := isProbeCondition(n.Cond); !ok {
		return
	}
	if depth := ifIsProbeDepth(n, seenChain); depth >= shapeRetestDepth {
		a.flowLint(n.Position, "flow warning [-Wflow]: %d nested `if … is PATTERN:` probes, each "+
			"re-probing a value bound by the previous pattern — this is one deep pattern; write it as a "+
			"single `match` arm (docs/122 nested patterns: `Expr.Unary(TokenKind.Minus, Expr.IntLit(v), _)`). "+
			"To keep the ladder, wrap it in `can ComplexFlow:`", depth)
	}
}

// ifIsProbeDepth returns the longest probe chain rooted at n, marking every chained member
// in seenChain. The next link must be a DIRECT statement of n's then-block, itself a source
// if-is probe, and its probed value must be a name n's pattern bound.
func ifIsProbeDepth(n *ast.IfStmt, seenChain map[*ast.IfStmt]bool) int {
	binds, ok := isProbeCondition(n.Cond)
	if !ok {
		return 0
	}
	seenChain[n] = true
	best := 0
	for _, stmt := range n.Then {
		inner, isIf := stmt.(*ast.IfStmt)
		if !isIf || !inner.FromSource {
			continue
		}
		probed, isProbe := probedValueName(inner.Cond)
		if !isProbe || !binds[probed] {
			continue
		}
		if d := ifIsProbeDepth(inner, seenChain); d > best {
			best = d
		}
	}
	return 1 + best
}

// isProbeCondition reports whether cond is exactly one `is`-test and returns the names its
// pattern side binds: `x is name` binds `name`; `x is E.B(v, _)` binds the bare-identifier
// payload args. Parens are transparent; anything else (and/or/not, comparisons) is not a
// pure probe.
func isProbeCondition(cond ast.Expr) (map[string]bool, bool) {
	switch e := cond.(type) {
	case *ast.ParenExpr:
		return isProbeCondition(e.Inner)
	case *ast.BinaryExpr:
		if e.Op != lexer.TOKEN_IS {
			return nil, false
		}
		binds := map[string]bool{}
		collectIsRHSBindNames(e.Right, binds)
		return binds, true
	}
	return nil, false
}

// probedValueName returns the probed value's name when cond is a pure `is`-test over a bare
// identifier (`operand is …`).
func probedValueName(cond ast.Expr) (string, bool) {
	switch e := cond.(type) {
	case *ast.ParenExpr:
		return probedValueName(e.Inner)
	case *ast.BinaryExpr:
		if e.Op != lexer.TOKEN_IS {
			return "", false
		}
		if name := identNameOf(e.Left); name != "" {
			return name, true
		}
	}
	return "", false
}

// collectIsRHSBindNames records the names an `is` right-hand side binds. A variant test
// (`x is E.B(v, _)`) carries a real MatchVariantPattern — its bind-pattern args and as-alias
// are the binds; a struct test likewise; a bare identifier binds the whole value
// (`v is value`); a bare member reference (`x is E.A`) binds nothing.
func collectIsRHSBindNames(rhs ast.Expr, out map[string]bool) {
	switch e := rhs.(type) {
	case *ast.Ident:
		if e.Name != "" && e.Name != "_" {
			out[e.Name] = true
		}
	case *ast.VariantTestExpr:
		if e.Pattern != nil {
			collectMatchPatternBindNames(e.Pattern, out)
		}
	case *ast.StructTestExpr:
		if e.Pattern != nil {
			collectMatchPatternBindNames(e.Pattern, out)
		}
	case *ast.ParenExpr:
		collectIsRHSBindNames(e.Inner, out)
	}
}

// collectMatchPatternBindNames records the names a match pattern binds — bind args of a
// variant/struct pattern (recursively) and as-aliases.
func collectMatchPatternBindNames(pattern ast.MatchPattern, out map[string]bool) {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Name != "" && p.Name != "_" {
			out[p.Name] = true
		}
	case *ast.MatchVariantPattern:
		if p.As != "" {
			out[p.As] = true
		}
		for _, arg := range p.Args {
			collectMatchPatternBindNames(arg.Pattern, out)
		}
	case *ast.MatchStructPattern:
		for _, arg := range p.Args {
			collectMatchPatternBindNames(arg.Pattern, out)
		}
	}
}

// --- shadow-prone elif tables (§6) ---

// checkShadowElifTable warns when a source if/elif chain of ≥3 conditions tests ONE scrutinee
// for equality against literals in every condition — a decision table wearing a ladder. The
// ladder silently allows shadowing (an earlier arm swallowing a later one); `when` declares
// the disjointness and totality outright. Fires once, at the chain head; the chain's member
// IfStmts are recorded so they are not re-reported as heads of their own sub-chains.
func (a *Analyzer) checkShadowElifTable(head *ast.IfStmt, seenChain map[*ast.IfStmt]bool) {
	const shadowTableLen = 3
	scrutinee := ""
	length := 0
	node := head
	for node != nil {
		path := a.equalityScrutineePath(node.Cond)
		if path == "" {
			break
		}
		if scrutinee == "" {
			scrutinee = path
		} else if path != scrutinee {
			break
		}
		length++
		seenChain[node] = true
		// The parser flattens `elif` into a single nested IfStmt in Else.
		if len(node.Else) == 1 {
			if next, ok := node.Else[0].(*ast.IfStmt); ok && next.FromSource {
				node = next
				continue
			}
		}
		node = nil
	}
	if length >= shadowTableLen {
		a.flowLint(head.Position, "flow warning [-Wflow]: %d-arm elif ladder testing `%s` for equality "+
			"against literals — this is a decision table; `when %s:` declares the disjointness and totality "+
			"the ladder only implies (docs/125 §4). To keep the ladder, wrap it in `can ComplexFlow:`",
			length, scrutinee, scrutinee)
	}
}

// equalityScrutineePath returns the rendered path of the single scrutinee a condition tests
// for equality against literals ("" when the condition is any other shape). Accepts
// `SCRUT == LIT` and or-chains `SCRUT == L1 or SCRUT == L2` over the same scrutinee — the
// exact shapes a `when` row expresses (`L1 | L2`).
func (a *Analyzer) equalityScrutineePath(cond ast.Expr) string {
	switch e := cond.(type) {
	case *ast.ParenExpr:
		return a.equalityScrutineePath(e.Inner)
	case *ast.BinaryExpr:
		switch e.Op {
		case lexer.TOKEN_EQEQ:
			// A literal on BOTH sides is a constant-vs-constant comparison, not a
			// scrutinee test — no table here.
			if a.isFlowTableLiteral(e.Right) && !a.isFlowTableLiteral(e.Left) {
				return flowScrutineePath(e.Left)
			}
			if a.isFlowTableLiteral(e.Left) && !a.isFlowTableLiteral(e.Right) {
				return flowScrutineePath(e.Right)
			}
		case lexer.TOKEN_OR:
			left := a.equalityScrutineePath(e.Left)
			if left != "" && left == a.equalityScrutineePath(e.Right) {
				return left
			}
		}
	}
	return ""
}

// isFlowTableLiteral reports whether an expression is a value a `when` row could carry:
// int/char/string literals, or a payload-less enum-tag reference (`TokenKind.Plus`).
// Bool literals are excluded (`flag == true` ladders are R2's flow-flag jurisdiction).
//
// The enum-tag case is SEMANTIC, not syntactic: `Obj.Field` only counts when Obj resolves
// through the TYPE namespace to an enum whose Field is a payload-free variant, AND the
// analyzer actually typed the expression as that enum (a member reference) — so a variable
// field (`other.kind`) or module constant (`Limits.max`) never matches, and neither does a
// payload-carrying variant (not a legal `when` row; R3 forbids destructuring). This keeps
// the rewrite advice exactly as strong as what `when` accepts (whenAtomTag).
func (a *Analyzer) isFlowTableLiteral(e ast.Expr) bool {
	switch e.(type) {
	case *ast.IntLit, *ast.CharLit, *ast.StringLit:
		return true
	}
	if fieldExpr, ok := isEnumVariantExpr(e); ok && a != nil {
		enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
		if ok && enumType != nil && variant != nil && len(variant.Payload) == 0 {
			if recorded, typed := a.exprTypes[ast.Expr(fieldExpr)]; typed && SameType(recorded, Type(enumType)) {
				return true
			}
		}
	}
	return false
}

// flowScrutineePath renders an Ident / FieldExpr chain (`lexer.kind`, `token.text`) as a
// stable path string for same-scrutinee comparison; "" for any other expression shape.
func flowScrutineePath(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.FieldExpr:
		if base := flowScrutineePath(v.Object); base != "" {
			return base + "." + v.Field
		}
	case *ast.ParenExpr:
		return flowScrutineePath(v.Inner)
	}
	return ""
}
