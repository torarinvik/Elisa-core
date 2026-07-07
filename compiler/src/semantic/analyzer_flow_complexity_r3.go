package semantic

import (
	"elisacore/src/ast"
)

// R3 — unguarded repeated advance (docs/121 §3-R3). Advancing the same loop-carried cursor twice
// in one block with no guard or exit between the two advances is the double-skip bug: the cursor
// jumps two positions where the author almost certainly meant one, silently dropping input.
//
// Calibration note (docs/121 §5): the rule deliberately does NOT fire on advance-per-arm — a
// typed-mode scanner (the `read_fstring` rewrite R2 pushes toward) advances once in each of many
// match arms, which is correct and clean, not scattered. Nor does it fire on a threaded-state
// accumulator (`table <- table.add_symbol(…)`) unless two such updates sit back-to-back with no
// intervening control flow. The ONLY shape it flags is two advances of the same binding reachable
// in sequence within one block without a guard between them — where a guard is any `if`/`match`/
// `return`/`break`/`continue` that could divert or end the path. That guard is what makes the
// escape-consumes-two idiom (`advance; if at_end: return; advance`) legitimate and unflagged.
func (a *Analyzer) checkFlowDuplicatedAdvance(info *loopFlowInfo) {
	if info == nil {
		return
	}
	for _, key := range info.advanceKeys() {
		if info.hasUnguardedRepeatedAdvance(key) {
			a.flowLint(info.pos, "%s", flowDuplicatedAdvanceMessage(key))
		}
	}
}

func flowDuplicatedAdvanceMessage(key advanceKey) string {
	return "flow warning [-Wflow]: loop cursor `" + key.binding + "` is advanced twice via `" +
		key.call + "` in one block with no guard between the advances — a double-skip that drops " +
		"input silently. Decide the step first (pure lookahead), then advance once by a computed " +
		"width:\n" +
		"    width: usize = 2 if <two-wide case> else 1\n" +
		"    " + key.binding + " <- " + key.binding + ".advance_chars(width)\n" +
		"  If the second advance is intentional, separate it with the guard that makes it so, or " +
		"wrap the loop in `can ComplexFlow:`."
}

// advanceKey identifies a cursor-advancing mutation: `binding <- binding.call(...)`.
type advanceKey struct {
	binding string
	call    string
}

// advanceKeys returns the distinct (binding, call) pairs where a carried binding is rebound to a
// method-style call on itself — the cursor-advance shape. Only carried bindings qualify.
func (info *loopFlowInfo) advanceKeys() []advanceKey {
	seen := map[advanceKey]bool{}
	var keys []advanceKey
	visitAdvances(info.body, func(binding, call string) {
		if !info.carried[binding] {
			return
		}
		key := advanceKey{binding: binding, call: call}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	})
	return keys
}

// hasUnguardedRepeatedAdvance reports whether any block in the loop advances the cursor, then
// advances it again before any guard (`if`/`match`/`return`/`break`/`continue`) intervenes.
func (info *loopFlowInfo) hasUnguardedRepeatedAdvance(key advanceKey) bool {
	return blockHasUnguardedRepeat(info.body, key)
}

// blockHasUnguardedRepeat scans a block's direct statements in order. Two advances of the key with
// nothing but plain (non-control-flow) statements between them is a double-skip. A guard resets the
// "saw an advance" state — after it, a following advance starts a fresh, intentional step. Recurses
// into arm bodies so a repeat inside a nested arm is caught, but each block is scanned on its own.
func blockHasUnguardedRepeat(body []ast.Stmt, key advanceKey) bool {
	pendingAdvance := false
	for _, stmt := range body {
		if isAdvanceStmt(stmt, key) {
			if pendingAdvance {
				return true
			}
			pendingAdvance = true
			continue
		}
		if isFlowGuardStmt(stmt) {
			pendingAdvance = false
		}
		if blockHasUnguardedRepeatNested(stmt, key) {
			return true
		}
	}
	return false
}

// blockHasUnguardedRepeatNested descends into the sub-blocks of a branch/scope statement, scanning
// each as its own block (a guard on the outer path does not merge with an inner arm's advances).
func blockHasUnguardedRepeatNested(stmt ast.Stmt, key advanceKey) bool {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		if blockHasUnguardedRepeat(n.Then, key) {
			return true
		}
		for _, elif := range n.Elifs {
			if blockHasUnguardedRepeat(elif.Body, key) {
				return true
			}
		}
		return blockHasUnguardedRepeat(n.Else, key)
	case *ast.MatchStmt:
		for _, arm := range n.Arms {
			if blockHasUnguardedRepeat(arm.Body, key) {
				return true
			}
		}
	case *ast.ScopeStmt:
		return blockHasUnguardedRepeat(n.Body, key)
	case *ast.CanStmt:
		return blockHasUnguardedRepeat(n.Body, key)
	case *ast.InStoreStmt:
		return blockHasUnguardedRepeat(n.Body, key)
	case *ast.RegionStmt:
		return blockHasUnguardedRepeat(n.Body, key)
	}
	return false
}

func isAdvanceStmt(stmt ast.Stmt, key advanceKey) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok {
		return false
	}
	binding, call, ok := advanceOfAssign(assign)
	return ok && binding == key.binding && call == key.call
}

// isFlowGuardStmt reports whether a statement can divert or end the current path — after one, a
// subsequent advance is a fresh intentional step, not a continuation of the previous.
func isFlowGuardStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.IfStmt, *ast.MatchStmt, *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	}
	return false
}

// visitAdvances walks the loop body and reports each `binding <- binding.call(...)` advance. Does
// not descend into nested loops.
func visitAdvances(stmts []ast.Stmt, visit func(binding, call string)) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if binding, call, ok := advanceOfAssign(n); ok {
				visit(binding, call)
			}
		case *ast.IfStmt:
			visitAdvances(n.Then, visit)
			for _, elif := range n.Elifs {
				visitAdvances(elif.Body, visit)
			}
			visitAdvances(n.Else, visit)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				visitAdvances(arm.Body, visit)
			}
		case *ast.ScopeStmt:
			visitAdvances(n.Body, visit)
		case *ast.CanStmt:
			visitAdvances(n.Body, visit)
		case *ast.InStoreStmt:
			visitAdvances(n.Body, visit)
		case *ast.RegionStmt:
			visitAdvances(n.Body, visit)
		}
	}
}

// advanceOfAssign recognizes `binding <- binding.call(...)` (a rebind of a binding to a
// method-style call on itself) and returns (binding, call name). The `read_fstring` cursor idiom
// `lexer <- lexer.advance_char()` is the canonical shape; the receiver is required to be the same
// binding so an unrelated `x <- y.f()` is not counted as advancing x.
func advanceOfAssign(assign *ast.AssignStmt) (string, string, bool) {
	binding := identNameOf(assign.Target)
	if binding == "" {
		return "", "", false
	}
	call, ok := assign.Value.(*ast.CallExpr)
	if !ok || call == nil {
		return "", "", false
	}
	name, receiver := callNameAndReceiver(call)
	if name == "" {
		return "", "", false
	}
	// The binding must be the call's RECEIVER (the thing being advanced), not an incidental
	// argument. This is what separates a cursor advance `lexer <- lexer.advance_char()` from a
	// threaded-state fold `table <- walk_expression(entry.key, table, …)`, where `table` rides as
	// a non-first argument — the latter is accumulator threading, never a double-skip.
	if receiver != binding {
		return "", "", false
	}
	return binding, name, true
}

// callNameAndReceiver extracts a call's method name and, if it is a method/UFCS call on a bare
// binding, that receiver name. Handles both `recv.method()` (FieldExpr callee) and the
// UFCS/lowered `method(recv)` form (Ident callee with a SafeReceiver or leading Ident arg).
func callNameAndReceiver(call *ast.CallExpr) (string, string) {
	switch fn := call.Func.(type) {
	case *ast.FieldExpr:
		return fn.Field, identNameOf(fn.Object)
	case *ast.Ident:
		receiver := identNameOf(call.SafeReceiver)
		if receiver == "" && len(call.Args) > 0 {
			receiver = identNameOf(call.Args[0])
		}
		return fn.Name, receiver
	}
	return "", ""
}
