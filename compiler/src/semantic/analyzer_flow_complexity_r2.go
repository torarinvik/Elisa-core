package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// R2 — state-flag ban (docs/121 §3-R2). A loop-carried integer/bool binding that is
// compared against literals in branch conditions AND reassigned in two or more branches is an
// untyped state machine encoded as a flag (`depth == 0`, `in_string`). The fix is to name the
// states in an enum and dispatch on them with `match` — exactly the `read_fstring` rewrite.
//
// This is the highest-precision rule and the one that catches the original `read_fstring` on
// its own. The classification is by HOW a binding is compared, not by its resolved type (no
// type query needed): a bare truth-test (`if flag:`) makes it bool-like; a `== <int literal>`
// makes it int-like. The two thresholds differ because a single boundary check on a counter
// (`if depth == 0` used once) is legitimate — an int flag needs to be *dispatched* on
// (compared in ≥2 places) before it reads as a discriminant, whereas one bool test already
// carries a yes/no state.
func (a *Analyzer) checkFlowStateFlag(info *loopFlowInfo) {
	if info == nil {
		return
	}
	for name := range info.carried {
		boolTests, intCmps := info.comparisonSites(name)
		assignBranches := info.assignBranchCount(name)
		if assignBranches < 2 {
			continue // a flag set on one path is not a machine
		}
		isBoolFlag := boolTests >= 1 && intCmps == 0
		isIntFlag := intCmps >= 2
		if !isBoolFlag && !isIntFlag {
			continue
		}
		if isIntFlag && info.bindingIsMonotoneCounter(name) {
			continue // a genuine counter, not a discriminant (see bindingIsMonotoneCounter)
		}
		if isBoolFlag && info.bindingIsStickyLatch(name) {
			continue // a one-way latch (see bindingIsStickyLatch), not a toggled 2-state machine
		}
		a.flowLint(info.pos, "%s", flowStateFlagMessage(name, isBoolFlag))
	}
}

func flowStateFlagMessage(name string, isBool bool) string {
	kind := "integer"
	if isBool {
		kind = "flag"
	}
	return "flow warning [-Wflow]: loop-carried " + kind + " `" + name +
		"` encodes an untyped state machine — it is compared against literals and reassigned in " +
		"multiple branches. Name the states in an enum and dispatch on them:\n" +
		"    const enum Mode of u8:\n" +
		"        StateA\n" +
		"        StateB\n" +
		"    while COND |…, mode: Mode = Mode.StateA|:\n" +
		"        match mode: …\n" +
		"  (this is the read_fstring rewrite; the enum compiles to the same register `" + name +
		"` occupied — zero overhead). To keep it as-is, wrap the loop in `can ComplexFlow:`."
}

// comparisonSites counts, for a carried binding, the branch-condition sites that (a) test it as
// a bare boolean and (b) compare it with an integer/char literal. Sites are per if/elif/match —
// a binding compared twice within one condition still counts that condition once.
func (info *loopFlowInfo) comparisonSites(name string) (boolTests int, intCmps int) {
	countConditions(info.body, name, &boolTests, &intCmps)
	return boolTests, intCmps
}

func countConditions(stmts []ast.Stmt, name string, boolTests *int, intCmps *int) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			classifyCondition(n.Cond, name, boolTests, intCmps)
			countConditions(n.Then, name, boolTests, intCmps)
			for _, elif := range n.Elifs {
				classifyCondition(elif.Cond, name, boolTests, intCmps)
				countConditions(elif.Body, name, boolTests, intCmps)
			}
			countConditions(n.Else, name, boolTests, intCmps)
		case *ast.MatchStmt:
			if identNameOf(n.Value) == name {
				for _, arm := range n.Arms {
					switch lit := arm.Pattern.(type) {
					case *ast.MatchLiteralPattern:
						if isBoolLitExpr(lit.Value) {
							*boolTests++
						} else if isIntOrCharLitExpr(lit.Value) {
							*intCmps++
						}
					}
				}
			}
			for _, arm := range n.Arms {
				countConditions(arm.Body, name, boolTests, intCmps)
			}
		case *ast.ScopeStmt:
			countConditions(n.Body, name, boolTests, intCmps)
		case *ast.CanStmt:
			countConditions(n.Body, name, boolTests, intCmps)
		case *ast.InStoreStmt:
			countConditions(n.Body, name, boolTests, intCmps)
		case *ast.RegionStmt:
			countConditions(n.Body, name, boolTests, intCmps)
		}
	}
}

// classifyCondition inspects one branch condition. It increments boolTests if the binding is
// used as a bare truth value anywhere in the condition, and intCmps if the binding is compared
// with `==`/`!=` against an integer/char literal. A single condition may do both.
func classifyCondition(cond ast.Expr, name string, boolTests *int, intCmps *int) {
	if cond == nil {
		return
	}
	if conditionComparesToIntLiteral(cond, name) {
		*intCmps++
	}
	if conditionTestsAsBool(cond, name) {
		*boolTests++
	}
}

// conditionComparesToIntLiteral reports whether the condition contains `name == LIT` /
// `name != LIT` / `LIT == name` with an integer or char literal, anywhere in its boolean tree.
func conditionComparesToIntLiteral(cond ast.Expr, name string) bool {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		switch e.Op {
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			if identNameOf(e.Left) == name && isIntOrCharLitExpr(e.Right) {
				return true
			}
			if identNameOf(e.Right) == name && isIntOrCharLitExpr(e.Left) {
				return true
			}
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			return conditionComparesToIntLiteral(e.Left, name) || conditionComparesToIntLiteral(e.Right, name)
		}
	case *ast.UnaryExpr:
		if e.Op == lexer.TOKEN_NOT {
			return conditionComparesToIntLiteral(e.Operand, name)
		}
	case *ast.ParenExpr:
		return conditionComparesToIntLiteral(e.Inner, name)
	}
	return false
}

// conditionTestsAsBool reports whether the binding is used as a bare boolean operand anywhere
// in the condition tree — `if flag:`, `elif flag and other:`, `if not flag:` — as opposed to
// being an operand of a comparison (which conditionComparesToIntLiteral handles).
func conditionTestsAsBool(cond ast.Expr, name string) bool {
	switch e := cond.(type) {
	case *ast.Ident:
		return e.Name == name
	case *ast.BinaryExpr:
		switch e.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			return conditionTestsAsBool(e.Left, name) || conditionTestsAsBool(e.Right, name)
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			// `flag == true` is a bool test; `depth == 0` is not (handled as an int compare).
			if identNameOf(e.Left) == name && isBoolLitExpr(e.Right) {
				return true
			}
			if identNameOf(e.Right) == name && isBoolLitExpr(e.Left) {
				return true
			}
		}
	case *ast.UnaryExpr:
		if e.Op == lexer.TOKEN_NOT {
			return conditionTestsAsBool(e.Operand, name)
		}
	case *ast.ParenExpr:
		return conditionTestsAsBool(e.Inner, name)
	}
	return false
}

// assignBranchCount counts distinct branch arms that directly assign the binding. An arm and
// its parent both appear in info.arms, but only the arm whose OWN direct statements assign the
// binding is counted, so a nested assignment is attributed to exactly one arm.
func (info *loopFlowInfo) assignBranchCount(name string) int {
	count := 0
	for _, arm := range info.arms {
		if armDirectlyAssigns(arm.body, name) {
			count++
		}
	}
	return count
}

// bindingIsMonotoneCounter reports whether EVERY assignment to the binding is a ±constant step
// (`name + c` / `name - c` / `+= c` / `-= c`) — a counter, never a discriminant, so it is exempt
// from R2. This deliberately includes BALANCED counters that step both directions: a bracket/
// nesting `depth` (`depth+1` on open, `depth-1` on close, checked `depth == 0` in several places)
// is the canonical counter, not a state machine, even though it clears the int thresholds. The
// distinguishing mark of a real discriminant is assigning DISTINCT constants to select behavior
// (`state <- 2`), which is not a step and so is never exempted here.
func (info *loopFlowInfo) bindingIsMonotoneCounter(name string) bool {
	sawStep := false
	allSteps := true
	visitAssignments(info.body, name, func(stmt ast.Stmt) {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if isConstantStep(n.Value, name) {
				sawStep = true
			} else {
				allSteps = false
			}
		case *ast.AugAssignStmt:
			switch n.Op {
			case lexer.TOKEN_PLUSEQ, lexer.TOKEN_MINUSEQ:
				sawStep = true
			default:
				allSteps = false
			}
		}
	})
	return sawStep && allSteps
}

// isConstantStep reports whether `value` is `name + c` / `name - c` with an integer/char literal
// step — the RHS of a counter update. Any other RHS disqualifies the counter exemption.
func isConstantStep(value ast.Expr, name string) bool {
	be, ok := value.(*ast.BinaryExpr)
	if !ok || identNameOf(be.Left) != name {
		return false
	}
	switch be.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		return isIntOrCharLitExpr(be.Right)
	}
	return false
}

func visitAssignments(stmts []ast.Stmt, name string, visit func(ast.Stmt)) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if identNameOf(n.Target) == name {
				visit(n)
			}
		case *ast.AugAssignStmt:
			if identNameOf(n.Target) == name {
				visit(n)
			}
		case *ast.IfStmt:
			visitAssignments(n.Then, name, visit)
			for _, elif := range n.Elifs {
				visitAssignments(elif.Body, name, visit)
			}
			visitAssignments(n.Else, name, visit)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				visitAssignments(arm.Body, name, visit)
			}
		case *ast.ScopeStmt:
			visitAssignments(n.Body, name, visit)
		case *ast.CanStmt:
			visitAssignments(n.Body, name, visit)
		case *ast.InStoreStmt:
			visitAssignments(n.Body, name, visit)
		case *ast.RegionStmt:
			visitAssignments(n.Body, name, visit)
		}
	}
}

// bindingIsStickyLatch reports whether a bool binding is only ever assigned a SINGLE literal value
// across the loop body (always `true`, or always `false`) — a one-way sticky latch recording a
// fact ("have we seen an explicit arg yet", "has the tail terminated"), never reset. That is a
// boolean fact, not a toggled 2-state machine, so an enum rewrite is the wrong advice. A genuine
// state flag (`in_string`) is assigned BOTH `true` and `false`, so it is not exempted.
func (info *loopFlowInfo) bindingIsStickyLatch(name string) bool {
	sawTrue := false
	sawFalse := false
	sawNonLiteral := false
	visitAssignments(info.body, name, func(stmt ast.Stmt) {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			sawNonLiteral = true
			return
		}
		lit, ok := assign.Value.(*ast.BoolLit)
		if !ok {
			sawNonLiteral = true
			return
		}
		if lit.Value {
			sawTrue = true
		} else {
			sawFalse = true
		}
	})
	if sawNonLiteral {
		return false // assigned a computed bool — not a simple latch, keep analyzing
	}
	return sawTrue != sawFalse // exactly one of the two literals ever assigned
}

func isBoolLitExpr(e ast.Expr) bool {
	_, ok := e.(*ast.BoolLit)
	return ok
}

func isIntOrCharLitExpr(e ast.Expr) bool {
	switch e.(type) {
	case *ast.IntLit, *ast.CharLit:
		return true
	}
	return false
}
