package semantic

import (
	"elisacore/src/ast"
)

type deferCaptureCollector struct {
	analyzer     *Analyzer
	outerScope   *Scope
	rootLocals   map[string]bool
	captureOrder []string
	captureSeen  map[string]bool
}

func newDeferCaptureCollector(analyzer *Analyzer, outerScope *Scope) *deferCaptureCollector {
	return &deferCaptureCollector{
		analyzer:    analyzer,
		outerScope:  outerScope,
		rootLocals:  map[string]bool{},
		captureSeen: map[string]bool{},
	}
}

func (c *deferCaptureCollector) noteCapture(name string) {
	if c.captureSeen[name] {
		return
	}
	c.captureSeen[name] = true
	c.captureOrder = append(c.captureOrder, name)
}

func (c *deferCaptureCollector) collectStmts(stmts []ast.Stmt) {
	locals := cloneParallelForLocals(c.rootLocals)
	for _, stmt := range stmts {
		c.collectStmt(stmt, locals)
	}
}

func (c *deferCaptureCollector) collectStmt(stmt ast.Stmt, locals map[string]bool) {
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
		c.collectExpr(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AugAssignStmt:
		c.collectExpr(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AsRefAssignStmt:
		c.collectExpr(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.ReturnStmt:
		c.collectExpr(n.Value, locals)
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
		c.collectExpr(n.Step, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.IterForStmt:
		c.collectExpr(n.Source, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, name := range parallelForMoveBindNames(n.Pattern) {
			bodyLocals[name] = true
		}
		for _, name := range parallelForMatchArmPatternNames(n.PatternFilter) {
			bodyLocals[name] = true
		}
		c.collectExpr(n.WhereFilter, bodyLocals)
		c.collectExpr(n.Filter, bodyLocals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.ParallelForStmt:
		c.collectExpr(n.Source, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		if n.IndexName != "" {
			bodyLocals[n.IndexName] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.MatchStmt:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Store, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
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
		c.collectExpr(n.Message, locals)
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
	case *ast.StaticBlockStmt:
		for _, inner := range n.Body {
			c.collectStmt(inner, cloneParallelForLocals(locals))
		}
	case *ast.DiscardStmt:
		c.collectExpr(n.Value, locals)
	case *ast.RegionStmt:
		c.collectExpr(n.Capacity, locals)
		locals[n.Name] = true
		if n.OwnerName != "" {
			locals[n.OwnerName] = true
		}
		c.collectStmts(n.Body)
	case *ast.MarkStmt:
		locals[n.Name] = true
	case *ast.RestoreStmt, *ast.ResetStmt, *ast.DestroyStmt, *ast.PassStmt, *ast.SignalStmt:
		return
	}
}

func (c *deferCaptureCollector) collectExpr(expr ast.Expr, locals map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if locals[n.Name] {
			return
		}
		sym, ok := c.outerScope.Lookup(n.Name)
		if ok && parallelForCapturableSymbolKind(sym.Kind) {
			c.noteCapture(n.Name)
		}
	case *ast.BinaryExpr:
		c.collectExpr(n.Left, locals)
		c.collectExpr(n.Right, locals)
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.CallExpr:
		c.collectExpr(n.Func, locals)
		c.collectExpr(n.SafeReceiver, locals)
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object, locals)
	case *ast.IndexExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Index, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.SliceExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Start, locals)
		c.collectExpr(n.End, locals)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem, locals)
		}
		c.collectExpr(n.Owner, locals)
	case *ast.CastExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.TernaryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Cond, locals)
		c.collectExpr(n.Alt, locals)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.ParenExpr:
		c.collectExpr(n.Inner, locals)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error, locals)
	case *ast.TryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.CatchExpr:
		c.collectExpr(n.Value, locals)
		successLocals := cloneParallelForLocals(locals)
		successLocals[n.Success.Name] = true
		for _, innerStmt := range n.Success.Body {
			c.collectStmt(innerStmt, successLocals)
		}
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.OptionalBindExpr:
		c.collectExpr(n.Value, locals)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner, locals)
		c.collectExpr(n.Value, locals)
	case *ast.CanExpr:
		c.collectExpr(n.Expr, locals)
	case *ast.MatchExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Store, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.VisitExpr:
		c.collectExpr(n.Value, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			appendVisitArmLocals(armLocals, arm)
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.FoldExpr:
		c.collectExpr(n.Value, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			appendVisitArmLocals(armLocals, arm)
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.LambdaExpr:
		lambdaLocals := cloneParallelForLocals(locals)
		for _, param := range n.Params {
			if param.Name != "" {
				lambdaLocals[param.Name] = true
			}
		}
		if n.BodyExpr != nil {
			c.collectExpr(n.BodyExpr, lambdaLocals)
		} else {
			for _, innerStmt := range n.Body {
				c.collectStmt(innerStmt, lambdaLocals)
			}
		}
	}
}
