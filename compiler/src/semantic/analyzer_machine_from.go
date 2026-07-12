package semantic

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §5 — `machine from START:` state-machine expression.
//
// Unlike `machine over` (a pure parser desugar), the result TYPE of `machine from` is only
// known here, from the binding/return context (`expected`) or the join of its `done`
// values. So the analyzer owns the lowering: it validates the state graph, determines the
// result type, and builds the loop/mode/match desugar into `expr.Lowered` (the LoweredCall
// pattern) — the backend then emits `Lowered`, reusing existing codegen entirely.
//
// The states are the variants of an existing enum, so the `mode` local is that enum and no
// enum is synthesized. `next State` is `mode <- Enum.State`; `done VALUE` is
// `result <- VALUE; done_flag <- true`; the whole form is a value-block yielding `result`.

func (a *Analyzer) analyzeMachineFromExpr(expr *ast.MachineFromExpr, expected Type) Type {
	if expr.Lowered != nil {
		// Already lowered (a second analysis pass): re-analyze the built form.
		return a.analyzeValueExpr(expr.Lowered, expected)
	}

	pos := expr.Position
	if expr.StartEnum == "" {
		a.errorf(pos, "`machine from` needs a qualified start state `Enum.State` so the state enum is known")
		return invalidType
	}
	variants, variantCount, ok := a.machineStateVariants(pos, expr.StartEnum)
	if !ok {
		a.errorf(pos, "`machine from %s.%s`: %q is not an enum", expr.StartEnum, expr.StartState, expr.StartEnum)
		return invalidType
	}

	// Validate the state graph (R2/R4) and that arm/transition states are real variants.
	if !a.checkMachineFromGraph(expr, variants) {
		return invalidType
	}

	// The result type: the binding/return context if present, else the join of `done`
	// value types. Context is the common case (docs canonical: `kind: TokenKind = …`).
	resultType := expected
	if resultType == nil {
		resultType = a.machineFromDoneJoin(expr)
	}
	if resultType == nil || IsInvalidType(resultType) {
		a.errorf(pos, "cannot infer the result type of `machine from`; bind it to a typed local or return it (`kind: T = machine from …`)")
		return invalidType
	}

	expr.Lowered = a.buildMachineFromLowering(expr, variantCount, resultType)
	result := a.analyzeValueExpr(expr.Lowered, expected)
	a.recordAnalyzedExprType(expr, result)
	return result
}

// machineStateVariants resolves the state enum (regular or const) to its variant-name set
// and count. States are variants of an existing enum, so both enum kinds are accepted.
func (a *Analyzer) machineStateVariants(pos lexer.Pos, name string) (map[string]bool, int, bool) {
	resolved := a.resolveType(&ast.NamedType{Position: pos, Name: name})
	names := map[string]bool{}
	switch t := resolved.(type) {
	case *EnumType:
		for _, v := range t.Variants {
			names[v.Name] = true
		}
		return names, len(t.Variants), true
	case *ConstEnumType:
		for _, m := range t.Members {
			names[m.Name] = true
		}
		return names, len(t.Members), true
	}
	return nil, 0, false
}

// machineFromDoneJoin analyzes the `done` value expressions (in a throwaway pass) and joins
// their types — the fallback result type when there is no binding/return context.
func (a *Analyzer) machineFromDoneJoin(expr *ast.MachineFromExpr) Type {
	var joined Type
	for _, arm := range expr.Arms {
		for _, term := range arm.Terminators {
			if !term.IsDone || term.Value == nil {
				continue
			}
			t := a.analyzeExpr(term.Value)
			if joined == nil {
				joined = t
			} else {
				joined = MergeTypes(joined, t)
			}
		}
	}
	return joined
}

// checkMachineFromGraph enforces the state-graph refusals: every arm names a real variant
// with no duplicate, every arm resolves (R2: ends in a terminator, the last unguarded),
// every `next` target is a real variant, and every state is reachable from the start (R4).
func (a *Analyzer) checkMachineFromGraph(expr *ast.MachineFromExpr, valid map[string]bool) bool {
	if !valid[expr.StartState] {
		a.errorf(expr.Position, "`machine from` start state %q is not a variant of %s", expr.StartState, expr.StartEnum)
		return false
	}

	ok := true
	armByState := map[string]bool{}
	for i := range expr.Arms {
		arm := &expr.Arms[i]
		if !valid[arm.State] {
			a.errorf(arm.Position, "machine arm state %q is not a variant of %s", arm.State, expr.StartEnum)
			ok = false
			continue
		}
		if armByState[arm.State] {
			a.errorf(arm.Position, "machine state %q has more than one arm", arm.State)
			ok = false
		}
		armByState[arm.State] = true

		// R2 — every arm resolves: it must end in a terminator, and the last terminator
		// must be unguarded (otherwise a path falls off the arm without deciding).
		if len(arm.Terminators) == 0 {
			a.errorf(arm.Position, "machine arm %q makes no decision — end it with `next State` or `done VALUE` (docs/125 §5)", arm.State)
			ok = false
		} else if last := arm.Terminators[len(arm.Terminators)-1]; last.Guard != nil {
			a.errorf(last.Position, "machine arm %q can complete without a transition — its final `next`/`done` must be unguarded (docs/125 §5)", arm.State)
			ok = false
		}
		for _, term := range arm.Terminators {
			if !term.IsDone && !valid[term.Target] {
				a.errorf(term.Position, "machine transition `next %s` names a non-variant of %s", term.Target, expr.StartEnum)
				ok = false
			}
		}

		// R5 — declared out-edges (`State -> {A, B}:`). When an arm declares its
		// successor set, every `next` it takes must be listed, and every listed target
		// must be a real variant. The declared table becomes the contract graph checks
		// query; a control-flow change that adds a transition now has to change the
		// declaration too, so the intended graph and the code cannot silently diverge.
		if arm.HasDeclaredOut {
			declared := map[string]bool{}
			for _, target := range arm.DeclaredOut {
				if !valid[target] {
					a.errorf(arm.Position, "machine arm %q declares out-edge %q which is not a variant of %s", arm.State, target, expr.StartEnum)
					ok = false
					continue
				}
				declared[target] = true
			}
			for _, term := range arm.Terminators {
				if !term.IsDone && valid[term.Target] && !declared[term.Target] {
					a.errorf(term.Position, "machine arm %q transitions `next %s`, but its declared out-edges are {%s} — add %s to the arm's `-> {…}` set or remove the transition (docs/125 §5 R5)", arm.State, term.Target, machineDeclaredOutList(arm.DeclaredOut), term.Target)
					ok = false
				}
			}
		}
	}
	if !ok {
		return false
	}

	// R4 — dead states: every state with an arm must be reachable from the start via
	// `next` edges. A state the graph can never enter means the code and the intended
	// machine diverged.
	reachable := map[string]bool{expr.StartState: true}
	changed := true
	for changed {
		changed = false
		for i := range expr.Arms {
			arm := &expr.Arms[i]
			if !reachable[arm.State] {
				continue
			}
			for _, term := range arm.Terminators {
				if !term.IsDone && !reachable[term.Target] {
					reachable[term.Target] = true
					changed = true
				}
			}
		}
	}
	for i := range expr.Arms {
		if arm := &expr.Arms[i]; !reachable[arm.State] {
			a.errorf(arm.Position, "machine state %q is unreachable from the start state %q (docs/125 §5)", arm.State, expr.StartState)
			ok = false
		}
	}

	// R3 — a cyclic transition graph must carry a `decreases` measure, or the machine can
	// loop forever (the classic forgot-to-advance lexer hang). Acyclic ⇒ terminates for
	// free. Discharging the measure is deferred to the docs/118 prover; here we only demand
	// its presence when the `next` graph has a cycle.
	if cycle := machineFromCycle(expr); cycle != "" && expr.Decreases == nil {
		a.errorf(expr.Position, "machine transition cycle through state %q has no `decreases` measure — add `machine from %s.%s decreases <measure>:` or the loop may never terminate (docs/125 §5)", cycle, expr.StartEnum, expr.StartState)
		ok = false
	}
	return ok
}

// machineDeclaredOutList renders an arm's declared out-edge set for a diagnostic (`A, B`),
// or `∅` when the arm declared an empty set (a terminal arm that promises no transitions).
func machineDeclaredOutList(targets []string) string {
	if len(targets) == 0 {
		return "∅"
	}
	out := targets[0]
	for _, t := range targets[1:] {
		out += ", " + t
	}
	return out
}

// machineFromCycle returns a state name on some transition cycle, or "" if the `next` graph
// is acyclic (an iterative-deepening DFS with a recursion/visited stack).
func machineFromCycle(expr *ast.MachineFromExpr) string {
	targets := map[string][]string{}
	for i := range expr.Arms {
		arm := &expr.Arms[i]
		for _, term := range arm.Terminators {
			if !term.IsDone {
				targets[arm.State] = append(targets[arm.State], term.Target)
			}
		}
	}
	color := map[string]int{} // 0 unvisited, 1 in-stack, 2 done
	var found string
	var dfs func(string)
	dfs = func(state string) {
		if found != "" {
			return
		}
		color[state] = 1
		for _, next := range targets[state] {
			if color[next] == 1 {
				found = next
				return
			}
			if color[next] == 0 {
				dfs(next)
			}
		}
		color[state] = 2
	}
	dfs(expr.StartState)
	return found
}

// buildMachineFromLowering constructs the ExprBlock desugar: a typed `result`, a `done`
// flag, a `mode` enum local, and a `while not done: match mode: …` loop whose arm bodies
// run then resolve via a terminator if/elif/else chain. Yields `result`.
func (a *Analyzer) buildMachineFromLowering(expr *ast.MachineFromExpr, variantCount int, resultType Type) ast.Expr {
	pos := expr.Position
	suffix := fmt.Sprintf("%d", pos.Offset)
	resultVar := "__mf_result_" + suffix
	doneVar := "__mf_done_" + suffix
	modeVar := "__mf_mode_" + suffix

	// stateValue builds `Enum.State` — or `Enum.State(args)` when the target carries a
	// payload (docs/125 §5). A payload-free state stays a bare member; a payload state is
	// an ordinary enum construction, reusing existing call/construct analysis and codegen.
	stateValue := func(state string, args []ast.Expr, at lexer.Pos) ast.Expr {
		member := &ast.FieldExpr{Position: at, Object: &ast.Ident{Position: at, Name: expr.StartEnum}, Field: state}
		if len(args) == 0 {
			return member
		}
		return &ast.CallExpr{Position: at, Func: member, Args: args}
	}
	enumMember := func(state string, args []ast.Expr) ast.Expr {
		return stateValue(state, args, pos)
	}
	ident := func(name string) ast.Expr { return &ast.Ident{Position: pos, Name: name} }

	// Per-arm mode/done rebinds share these action builders.
	setMode := func(state string, args []ast.Expr, at lexer.Pos) ast.Stmt {
		return &ast.AssignStmt{Position: at, Target: &ast.Ident{Position: at, Name: modeVar}, Value: stateValue(state, args, at)}
	}
	setDone := func(value ast.Expr, at lexer.Pos) []ast.Stmt {
		return []ast.Stmt{
			&ast.AssignStmt{Position: at, Target: &ast.Ident{Position: at, Name: resultVar}, Value: value},
			&ast.AssignStmt{Position: at, Target: &ast.Ident{Position: at, Name: doneVar}, Value: &ast.BoolLit{Position: at, Value: true}},
		}
	}
	termAction := func(term ast.MachineFromTerminator) []ast.Stmt {
		if term.IsDone {
			return setDone(term.Value, term.Position)
		}
		return []ast.Stmt{setMode(term.Target, term.Args, term.Position)}
	}

	// Collect mutated roots across arm bodies so the loop captures license the mutation.
	// Header-pipe accumulators (`|r = 0|`) are machine-private locals declared in this same
	// ExprBlock, so they are licensed by their own declaration, not by an outer capture —
	// exclude them from the capture list and keep only genuine outer mutables (bare-name
	// threads from the pipe, plus roots mutated but not declared here).
	headerLocal := map[string]bool{}
	for _, decl := range expr.HeaderDecls {
		if v, ok := decl.(*ast.VarDeclStmt); ok {
			headerLocal[v.Name] = true
		}
	}
	captures := make([]string, 0)
	for _, root := range machineFromCaptureRoots(expr) {
		if !headerLocal[root] {
			captures = append(captures, root)
		}
	}
	captures = append(captures, expr.HeaderCaptures...)

	matchArms := make([]ast.MatchArm, 0, len(expr.Arms)+1)
	covered := map[string]bool{}
	for i := range expr.Arms {
		arm := &expr.Arms[i]
		covered[arm.State] = true
		body := append([]ast.Stmt(nil), arm.Body...)
		body = append(body, buildMachineFromTerminatorChain(arm.Terminators, termAction)...)
		// A payload arm binds its fields by destructuring the `mode` variant: each binding
		// becomes a positional variant-pattern arg (`Num.Exponent(was_fraction)`), so the
		// binding is an ordinary typed local the body reads — refinement facts seed per-arm.
		var patternArgs []ast.MatchPatternArg
		for _, name := range arm.Bindings {
			patternArgs = append(patternArgs, ast.MatchPatternArg{
				Position: arm.Position,
				Pattern:  &ast.MatchBindPattern{Position: arm.Position, Name: name},
			})
		}
		matchArms = append(matchArms, ast.MatchArm{
			Position: arm.Position,
			Pattern:  &ast.MatchVariantPattern{Position: arm.Position, EnumName: expr.StartEnum, Variant: arm.State, Args: patternArgs},
			Body:     body,
		})
	}
	// A defensive `_` exit only when the enum has variants without an arm (mode never
	// holds one, but the match must stay exhaustive). Adding it under full coverage would
	// be a redundant-wildcard error.
	if len(covered) < variantCount {
		matchArms = append(matchArms, ast.MatchArm{
			Position: pos,
			Pattern:  &ast.MatchWildcardPattern{Position: pos},
			Body:     []ast.Stmt{&ast.AssignStmt{Position: pos, Target: ident(doneVar), Value: &ast.BoolLit{Position: pos, Value: true}}},
		})
	}

	// A machine-level `decreases M` (docs/125 §5 R3) lowers to a leading loop-body
	// `decreases` clause so the EXISTING loop-termination prover (checkLoopTermination,
	// analyzer_flow.go) discharges it on the same footing as a hand-written `while`: type
	// the measure as an integer, prove strict-decrease + bounded-below where the body is
	// straight-line, else record ProofRuntime and lean on the runtime progress backstop /
	// `can Unsafe.AssumeProgress`. The R3 pre-check already demanded the measure's PRESENCE
	// for a cyclic transition graph; this is what makes the measure actually mean something.
	loopBody := make([]ast.Stmt, 0, 2)
	if expr.Decreases != nil {
		loopBody = append(loopBody, &ast.ContractStmt{Position: expr.Decreases.Pos(), Kind: ast.ContractDecreases, Cond: expr.Decreases})
	}
	loopBody = append(loopBody, &ast.MatchStmt{Position: pos, Value: ident(modeVar), Arms: matchArms})

	loop := &ast.WhileStmt{
		Position:                pos,
		Cond:                    &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: ident(doneVar)},
		Body:                    loopBody,
		Captures:                captures,
		RuntimeProgressBackstop: expr.Decreases != nil,
	}

	// Header-pipe accumulators are declared first so they are in scope for the whole
	// machine (the `decreases` measure inside the loop body and every arm).
	stmts := make([]ast.Stmt, 0, len(expr.HeaderDecls)+4)
	stmts = append(stmts, expr.HeaderDecls...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: pos, Name: resultVar, Mutable: true, Type: astTypeExprForBuiltinMethodRewrite(pos, resultType), Value: &ast.ZeroedLit{Position: pos}},
		&ast.VarDeclStmt{Position: pos, Name: doneVar, Mutable: true, Type: &ast.NamedType{Position: pos, Name: "bool"}, Value: &ast.BoolLit{Position: pos, Value: false}},
		&ast.VarDeclStmt{Position: pos, Name: modeVar, Mutable: true, Value: enumMember(expr.StartState, expr.StartArgs)},
		loop,
	)
	return &ast.ExprBlock{Position: pos, Stmts: stmts, Value: ident(resultVar), Captures: captures}
}

// buildMachineFromTerminatorChain lowers a trailing run of terminators to an if/elif/else:
// guarded terminators become the conditions, the final unguarded one becomes the else. A
// single unguarded terminator lowers to its bare action (no branch).
func buildMachineFromTerminatorChain(terms []ast.MachineFromTerminator, action func(ast.MachineFromTerminator) []ast.Stmt) []ast.Stmt {
	if len(terms) == 0 {
		return nil
	}
	if len(terms) == 1 && terms[0].Guard == nil {
		return action(terms[0])
	}
	ifStmt := &ast.IfStmt{Position: terms[0].Position, Cond: terms[0].Guard, Then: action(terms[0])}
	for _, term := range terms[1:] {
		if term.Guard == nil {
			ifStmt.Else = action(term)
			break
		}
		ifStmt.Elifs = append(ifStmt.Elifs, ast.ElifClause{Position: term.Position, Cond: term.Guard, Body: action(term)})
	}
	return []ast.Stmt{ifStmt}
}

// machineFromCaptureRoots collects the root identifiers assigned (`x <- …`) anywhere in the
// arm bodies, so the desugared loop's captures license those mutations (docs/120).
func machineFromCaptureRoots(expr *ast.MachineFromExpr) []string {
	seen := map[string]bool{}
	var roots []string
	var walk func([]ast.Stmt)
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			roots = append(roots, name)
		}
	}
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.AssignStmt:
				add(machineFromRootIdent(s.Target))
			case *ast.IfStmt:
				walk(s.Then)
				for _, e := range s.Elifs {
					walk(e.Body)
				}
				walk(s.Else)
			case *ast.WhileStmt:
				walk(s.Body)
			case *ast.ForStmt:
				walk(s.Body)
			case *ast.IterForStmt:
				walk(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walk(arm.Body)
				}
			case *ast.CanStmt:
				walk(s.Body)
			}
		}
	}
	for i := range expr.Arms {
		walk(expr.Arms[i].Body)
	}
	return roots
}

func machineFromRootIdent(target ast.Expr) string {
	switch t := target.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.FieldExpr:
		return machineFromRootIdent(t.Object)
	case *ast.IndexExpr:
		return machineFromRootIdent(t.Object)
	case *ast.ParenExpr:
		return machineFromRootIdent(t.Inner)
	}
	return ""
}
