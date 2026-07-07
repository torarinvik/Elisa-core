package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Flow-checked loops (docs/121). Messy scanner/parser loops hide a state machine: untyped
// flag bindings (`depth == 0`, `in_string`), cursor mutation scattered across branches, and
// divergent exit shapes. The pilot (`read_fstring`) proved the clean shape — a typed mode
// enum in the loop header + one top-level `match` — needs no new syntax and is zero-overhead,
// so this is a LINT tier, not a construct. Every rule is a structural check on the AST; none
// touch codegen. The common denominator the rules enforce: decide (pure reads) then act (one
// mutation, tail position).
//
// This file holds the shared machinery — the graduation lever, the ComplexFlow escape hatch,
// the loop walk, and the per-loop `loopFlowInfo` pre-pass. The individual rules live in
// analyzer_flow_complexity_r*.go and consume a `loopFlowInfo`.

// FlowLintMode is the graduated-strictness lever for the flow lints, mirroring the -Wperf
// model: off (default in the staged rollout), warn (`-Wflow`), or strict (the eventual
// default, downgraded to warn by `-permissive`).
type FlowLintMode int

const (
	FlowLintOff FlowLintMode = iota
	FlowLintWarn
	FlowLintStrict
)

// flowLint emits a flow-complexity diagnostic at the configured severity: nothing when the
// lints are off, a warning under `-Wflow`, a hard error under strict mode. All six rules emit
// only through here so the graduation is uniform.
func (a *Analyzer) flowLint(pos lexer.Pos, format string, args ...interface{}) {
	switch a.flowLintMode {
	case FlowLintStrict:
		a.errorf(pos, format, args...)
	case FlowLintWarn:
		a.warnf(pos, format, args...)
	}
}

// refsGrantComplexFlow reports whether a `can …:` grant acknowledges complex flow — the
// escape hatch that silences the flow lints for the loop(s) it lexically covers.
func refsGrantComplexFlow(refs []ast.PermissionRef) bool {
	return permissionRefsContain(refs, "ComplexFlow", "")
}

// checkFlowComplexity is the per-function entry, called from the analysis tail alongside the
// perf lints. It is a no-op unless the flow lints are enabled, so functions pay nothing in the
// default configuration.
func (a *Analyzer) checkFlowComplexity(fn *ast.FuncDecl) {
	if a == nil || fn == nil || a.flowLintMode == FlowLintOff {
		return
	}
	a.walkFlowLoops(fn.Body, false)
}

// walkFlowLoops descends the statement tree. At each loop it builds a loopFlowInfo and runs
// the rules (unless lexically inside a `can ComplexFlow:` grant), then recurses into the loop
// body so nested loops are analyzed as their own jurisdictions. A value-form loop header
// desugars to `ExprStmt{ExprBlock{decls…, loop}}` and a statement-form one to `IfStmt{true,
// {decls…, loop}}` (parser/loop_header.go), so the walk must pass through ExprBlock/IfStmt to
// reach the loop — which it does.
func (a *Analyzer) walkFlowLoops(stmts []ast.Stmt, inGrant bool) {
	for _, stmt := range stmts {
		a.walkFlowLoopStmt(stmt, inGrant)
	}
}

func (a *Analyzer) walkFlowLoopStmt(stmt ast.Stmt, inGrant bool) {
	switch n := stmt.(type) {
	case *ast.WhileStmt:
		if !inGrant {
			a.runFlowRules(a.buildLoopFlowInfo(loopWhile, n.Position, n.Cond, n.Body, n.Captures))
		}
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.ForStmt:
		if !inGrant {
			a.runFlowRules(a.buildLoopFlowInfo(loopFor, n.Position, nil, n.Body, nil))
		}
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.IterForStmt:
		if !inGrant {
			a.runFlowRules(a.buildLoopFlowInfo(loopIter, n.Position, nil, n.Body, nil))
		}
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.IfStmt:
		a.walkFlowLoops(n.Then, inGrant)
		for _, elif := range n.Elifs {
			a.walkFlowLoops(elif.Body, inGrant)
		}
		a.walkFlowLoops(n.Else, inGrant)
	case *ast.MatchStmt:
		for _, arm := range n.Arms {
			a.walkFlowLoops(arm.Body, inGrant)
		}
	case *ast.ScopeStmt:
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.InStoreStmt:
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.RegionStmt:
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.PoolStmt:
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.LockStmt:
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.DeferStmt:
		a.walkFlowLoops(n.Body, inGrant)
	case *ast.CanStmt:
		a.walkFlowLoops(n.Body, inGrant || refsGrantComplexFlow(n.Permissions))
	case *ast.ExprStmt:
		a.walkFlowLoopExpr(n.Expr, inGrant)
	case *ast.AssignStmt:
		a.walkFlowLoopExpr(n.Value, inGrant)
	case *ast.VarDeclStmt:
		a.walkFlowLoopExpr(n.Value, inGrant)
	}
}

// walkFlowLoopExpr reaches loops that sit in value position — the loop-expression desugar
// wraps them in an ExprBlock, which can be the value of an ExprStmt, an assignment, or a decl.
func (a *Analyzer) walkFlowLoopExpr(expr ast.Expr, inGrant bool) {
	block, ok := expr.(*ast.ExprBlock)
	if !ok || block == nil {
		return
	}
	a.walkFlowLoops(block.Stmts, inGrant)
}

type loopKind int

const (
	loopWhile loopKind = iota
	loopFor
	loopIter
)

// loopFlowInfo is the shared pre-pass every flow rule consumes. It classifies the loop body
// once — carried bindings, branch arms, and per-binding mutation sites — so the rules are thin
// queries over it rather than independent walks.
type loopFlowInfo struct {
	kind     loopKind
	pos      lexer.Pos
	cond     ast.Expr // while condition; nil for for/iter
	body     []ast.Stmt
	captures []string

	// carried names the loop-carried bindings: mutated in the body but NOT declared inside it
	// (so header accumulators — declared in a sibling decl before the loop — and outer locals
	// both qualify, while per-iteration scratch declared in the body does not).
	carried map[string]bool

	// arms is the flat list of branch-arm bodies in the loop body (each if-then, elif, else,
	// and match-arm, at any nesting). A rule that counts "distinct branches mutating b" counts
	// arms whose direct statements assign b.
	arms []flowArm
}

type flowArm struct {
	pos  lexer.Pos
	body []ast.Stmt // the arm's direct statements
}

func (a *Analyzer) buildLoopFlowInfo(kind loopKind, pos lexer.Pos, cond ast.Expr, body []ast.Stmt, captures []string) *loopFlowInfo {
	info := &loopFlowInfo{
		kind:     kind,
		pos:      pos,
		cond:     cond,
		body:     body,
		captures: captures,
		carried:  map[string]bool{},
	}
	bodyLocal := map[string]bool{}
	collectBodyLocalDecls(body, bodyLocal)
	mutated := map[string]bool{}
	collectMutatedNames(body, mutated)
	for name := range mutated {
		if !bodyLocal[name] {
			info.carried[name] = true
		}
	}
	for _, name := range captures {
		info.carried[name] = true
	}
	collectFlowArms(body, &info.arms)
	return info
}

// collectBodyLocalDecls records names declared inside the loop body. A name declared in the
// body does not persist across iterations, so it is scratch, never carried state. Descends
// through branch/scope statements but NOT into nested loops (a nested loop's decls belong to
// that loop's jurisdiction, and its body is analyzed separately).
func collectBodyLocalDecls(stmts []ast.Stmt, out map[string]bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if n.Name != "" {
				out[n.Name] = true
			}
		case *ast.TupleBindStmt:
			if n.Declare {
				for _, name := range n.Names {
					out[name.Name] = true
				}
			}
		case *ast.IfStmt:
			collectBodyLocalDecls(n.Then, out)
			for _, elif := range n.Elifs {
				collectBodyLocalDecls(elif.Body, out)
			}
			collectBodyLocalDecls(n.Else, out)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				collectBodyLocalDecls(arm.Body, out)
			}
		case *ast.ScopeStmt:
			collectBodyLocalDecls(n.Body, out)
		case *ast.CanStmt:
			collectBodyLocalDecls(n.Body, out)
		case *ast.InStoreStmt:
			collectBodyLocalDecls(n.Body, out)
		case *ast.RegionStmt:
			collectBodyLocalDecls(n.Body, out)
		}
	}
}

// collectMutatedNames records the root binding names assigned anywhere in the loop body. Only
// simple `name <- …` / `name += …` targets count (a state flag or cursor is a bare binding);
// field/index writes are not loop-carried scalar state. Does not descend into nested loops.
func collectMutatedNames(stmts []ast.Stmt, out map[string]bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if name := identNameOf(n.Target); name != "" {
				out[name] = true
			}
		case *ast.AugAssignStmt:
			if name := identNameOf(n.Target); name != "" {
				out[name] = true
			}
		case *ast.IfStmt:
			collectMutatedNames(n.Then, out)
			for _, elif := range n.Elifs {
				collectMutatedNames(elif.Body, out)
			}
			collectMutatedNames(n.Else, out)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				collectMutatedNames(arm.Body, out)
			}
		case *ast.ScopeStmt:
			collectMutatedNames(n.Body, out)
		case *ast.CanStmt:
			collectMutatedNames(n.Body, out)
		case *ast.InStoreStmt:
			collectMutatedNames(n.Body, out)
		case *ast.RegionStmt:
			collectMutatedNames(n.Body, out)
		}
	}
}

// collectFlowArms flattens every branch-arm body in the loop into a list. Each if-then, elif,
// else, and match-arm becomes one arm (recursively, so nested branches contribute their own
// arms). Does not descend into nested loops.
func collectFlowArms(stmts []ast.Stmt, out *[]flowArm) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			*out = append(*out, flowArm{pos: n.Position, body: n.Then})
			collectFlowArms(n.Then, out)
			for _, elif := range n.Elifs {
				*out = append(*out, flowArm{pos: elif.Position, body: elif.Body})
				collectFlowArms(elif.Body, out)
			}
			if len(n.Else) > 0 {
				*out = append(*out, flowArm{pos: n.Position, body: n.Else})
				collectFlowArms(n.Else, out)
			}
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				*out = append(*out, flowArm{pos: arm.Position, body: arm.Body})
				collectFlowArms(arm.Body, out)
			}
		case *ast.ScopeStmt:
			collectFlowArms(n.Body, out)
		case *ast.CanStmt:
			collectFlowArms(n.Body, out)
		case *ast.InStoreStmt:
			collectFlowArms(n.Body, out)
		case *ast.RegionStmt:
			collectFlowArms(n.Body, out)
		}
	}
}

// armDirectlyAssigns reports whether an arm's own direct statements assign the binding — used
// to count "distinct branches that mutate b" without double-counting a parent arm that only
// contains the assignment transitively through a child arm.
func armDirectlyAssigns(body []ast.Stmt, name string) bool {
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if identNameOf(n.Target) == name {
				return true
			}
		case *ast.AugAssignStmt:
			if identNameOf(n.Target) == name {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) runFlowRules(info *loopFlowInfo) {
	if info == nil {
		return
	}
	a.checkFlowStateFlag(info) // R2
	a.checkFlowDuplicatedAdvance(info) // R3
}
