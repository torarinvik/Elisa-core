package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

type parallelForCaptureCollector struct {
	analyzer     *Analyzer
	outerScope   *Scope
	rootLocals   map[string]bool
	captureOrder []string
	captureSeen  map[string]bool
	errors       []string
}

func newParallelForCaptureCollector(analyzer *Analyzer, outerScope *Scope, loopNames ...string) *parallelForCaptureCollector {
	rootLocals := map[string]bool{}
	for _, name := range loopNames {
		if name != "" {
			rootLocals[name] = true
		}
	}
	return &parallelForCaptureCollector{
		analyzer:    analyzer,
		outerScope:  outerScope,
		rootLocals:  rootLocals,
		captureSeen: map[string]bool{},
	}
}

func cloneParallelForLocals(locals map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(locals))
	for name, ok := range locals {
		cloned[name] = ok
	}
	return cloned
}

func (c *parallelForCaptureCollector) addError(msg string) {
	for _, existing := range c.errors {
		if existing == msg {
			return
		}
	}
	c.errors = append(c.errors, msg)
}

func (c *parallelForCaptureCollector) noteCapture(name string) {
	if c.captureSeen[name] {
		return
	}
	c.captureSeen[name] = true
	c.captureOrder = append(c.captureOrder, name)
}

func (c *parallelForCaptureCollector) collectStmts(stmts []ast.Stmt) {
	locals := cloneParallelForLocals(c.rootLocals)
	for _, stmt := range stmts {
		c.collectStmt(stmt, locals)
	}
}

func (c *parallelForCaptureCollector) collectStmt(stmt ast.Stmt, locals map[string]bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			c.collectExpr(n.Value, locals)
		}
		locals[n.Name] = true
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		for _, name := range parallelForMoveBindNames(n.Pattern) {
			locals[name] = true
		}
	case *ast.DeferStmt:
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.AssignStmt:
		c.collectAssignmentTarget(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AugAssignStmt:
		c.collectAssignmentTarget(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AsRefAssignStmt:
		c.collectAssignmentTarget(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.ReturnStmt:
		c.addError("parallel for body cannot return from the enclosing function")
		if n.Value != nil {
			c.collectExpr(n.Value, locals)
		}
	case *ast.IfStmt:
		c.collectExpr(n.Cond, locals)
		thenLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Then {
			c.collectStmt(innerStmt, thenLocals)
		}
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond, locals)
			elifLocals := cloneParallelForLocals(locals)
			for _, innerStmt := range elif.Body {
				c.collectStmt(innerStmt, elifLocals)
			}
		}
		elseLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Else {
			c.collectStmt(innerStmt, elseLocals)
		}
	case *ast.WhileStmt:
		c.collectExpr(n.Cond, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.ForStmt:
		c.collectExpr(n.Start, locals)
		c.collectExpr(n.End, locals)
		if n.Step != nil {
			c.collectExpr(n.Step, locals)
		}
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.IterForStmt:
		c.collectExpr(n.Source, locals)
		if n.WhereFilter != nil {
			c.collectExpr(n.WhereFilter, locals)
		}
		if n.Filter != nil {
			c.collectExpr(n.Filter, locals)
		}
		bodyLocals := cloneParallelForLocals(locals)
		for _, name := range parallelForMoveBindNames(n.Pattern) {
			bodyLocals[name] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.ParallelForStmt:
		c.addError("parallel for body cannot nest another parallel for")
	case *ast.MatchStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.ExpectPatternStmt:
		c.collectExpr(n.Value, locals)
		for _, pattern := range n.Patterns {
			for _, name := range parallelForMatchArmPatternNames(pattern) {
				locals[name] = true
			}
		}
	case *ast.InStoreStmt:
		c.collectExpr(n.Store, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.CanStmt:
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.PoolStmt:
		c.collectExpr(n.Workers, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.LockStmt:
		c.collectExpr(n.Mutex, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.GuardName] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.PanicStmt:
		if n.Message != nil {
			c.collectExpr(n.Message, locals)
		}
	case *ast.ExprStmt:
		c.collectExpr(n.Expr, locals)
	case *ast.StaticIfStmt:
		for _, active := range c.analyzer.activeStmtBranch(n) {
			c.collectStmt(active, cloneParallelForLocals(locals))
		}
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message, locals)
	case *ast.StaticAssertStmt:
		c.collectExpr(n.Cond, locals)
		if n.Message != nil {
			c.collectExpr(n.Message, locals)
		}
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			c.collectExpr(item.Cond, locals)
			if item.Message != nil {
				c.collectExpr(item.Message, locals)
			}
		}
	case *ast.StaticBlockStmt:
		for _, inner := range n.Body {
			c.collectStmt(inner, cloneParallelForLocals(locals))
		}
	case *ast.DiscardStmt:
		c.collectExpr(n.Value, locals)
	case *ast.RegionStmt:
		if n.Capacity != nil {
			c.collectExpr(n.Capacity, locals)
		}
		locals[n.Name] = true
		if n.OwnerName != "" {
			locals[n.OwnerName] = true
		}
		c.collectStmts(n.Body)
	case *ast.DestroyStmt:
		if !locals[n.Name] {
			c.addError(fmt.Sprintf("parallel for body cannot destroy outer region %q", n.Name))
		}
	case *ast.MarkStmt:
		if !locals[n.RegionName] {
			c.addError(fmt.Sprintf("parallel for body cannot mark outer region %q", n.RegionName))
		}
		locals[n.Name] = true
	case *ast.RestoreStmt:
		if !locals[n.RegionName] {
			c.addError(fmt.Sprintf("parallel for body cannot restore outer region %q", n.RegionName))
		}
		if !locals[n.MarkName] {
			c.addError(fmt.Sprintf("parallel for body cannot restore from outer checkpoint %q", n.MarkName))
		}
	case *ast.ResetStmt:
		if !locals[n.Name] {
			c.addError(fmt.Sprintf("parallel for body cannot reset outer region %q", n.Name))
		}
	}
}
