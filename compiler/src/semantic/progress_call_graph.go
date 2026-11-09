package semantic

import (
	"elisacore/src/ast"
)

func collectProgressDirectCalls(stmts []ast.Stmt) []string {
	collector := progressCallCollector{}
	collector.collectStmts(stmts)
	return collector.calls
}

type progressCallCollector struct {
	calls []string
}

func (c *progressCallCollector) collectStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		c.collectStmt(stmt)
	}
}

func (c *progressCallCollector) collectStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Owner)
	case *ast.LetDestructureStmt:
		c.collectExpr(n.Value)
	case *ast.TupleBindStmt:
		c.collectExpr(n.Value)
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
	case *ast.AssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AugAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.DeferStmt:
		c.collectStmts(n.Body)
	case *ast.ReturnStmt:
		c.collectExpr(n.Value)
	case *ast.IfStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Then)
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond)
			c.collectStmts(elif.Body)
		}
		c.collectStmts(n.Else)
	case *ast.MatchStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.ExpectPatternStmt:
		c.collectExpr(n.Value)
	case *ast.InStoreStmt:
		c.collectExpr(n.Store)
		c.collectStmts(n.Body)
	case *ast.CanStmt:
		c.collectStmts(n.Body)
	case *ast.PoolStmt:
		c.collectExpr(n.Workers)
		c.collectStmts(n.Body)
	case *ast.LockStmt:
		c.collectExpr(n.Mutex)
		c.collectStmts(n.Body)
	case *ast.WhileStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Body)
	case *ast.ForStmt:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
		c.collectExpr(n.Step)
		c.collectStmts(n.Body)
	case *ast.IterForStmt:
		c.collectExpr(n.Source)
		c.collectExpr(n.WhereFilter)
		c.collectExpr(n.Filter)
		c.collectStmts(n.Body)
	case *ast.ParallelForStmt:
		c.collectExpr(n.Source)
		c.collectStmts(n.Body)
	case *ast.PanicStmt:
		c.collectExpr(n.Message)
	case *ast.ExprStmt:
		c.collectExpr(n.Expr)
	case *ast.StaticIfStmt:
		c.collectStmts(n.Then)
		for _, elif := range n.Elifs {
			c.collectStmts(elif.Body)
		}
		c.collectStmts(n.Else)
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message)
	case *ast.StaticAssertStmt:
		c.collectExpr(n.Cond)
		c.collectExpr(n.Message)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			c.collectExpr(item.Cond)
			c.collectExpr(item.Message)
		}
	case *ast.StaticBlockStmt:
		c.collectStmts(n.Body)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value)
	case *ast.RegionStmt:
		c.collectExpr(n.Capacity)
		c.collectStmts(n.Body)
	case *ast.DestroyStmt:
	case *ast.MarkStmt:
	case *ast.CheckpointStmt:
		c.collectExpr(n.Target)
		c.collectStmts(n.Body)
	case *ast.GroupedCheckpointStmt:
		for _, target := range n.Targets {
			c.collectExpr(target)
		}
		c.collectStmts(n.Body)
	case *ast.RestoreStmt:
	case *ast.RestoreCheckpointStmt:
	case *ast.ResetStmt:
	}
}

func (c *progressCallCollector) collectExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ExprBlock:
		c.collectStmts(n.Stmts)
		c.collectExpr(n.Value)
	case *ast.BinaryExpr:
		c.collectExpr(n.Left)
		c.collectExpr(n.Right)
		if n.LoweredCall != nil {
			c.collectExpr(n.LoweredCall)
		}
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand)
		if n.LoweredCall != nil {
			c.collectExpr(n.LoweredCall)
		}
	case *ast.MoveExpr:
		c.collectExpr(n.Operand)
	case *ast.CallExpr:
		if ident, ok := n.Func.(*ast.Ident); ok {
			c.calls = append(c.calls, ident.Name)
		}
		c.collectExpr(n.Func)
		c.collectExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object)
	case *ast.IndexExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
	case *ast.SliceExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
		c.collectExpr(n.Owner)
	case *ast.MembershipRangeExpr:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ListComprehensionExpr:
		for _, b := range n.Bindings {
			if vd, ok := b.(*ast.VarDeclStmt); ok {
				c.collectExpr(vd.Value)
			}
		}
		c.collectExpr(n.Key)
		c.collectExpr(n.Value)
		c.collectExpr(n.Source)
		c.collectExpr(n.RangeEnd)
		c.collectExpr(n.RangeStep)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Owner)
	case *ast.QueryExpr:
		c.collectExpr(n.Source)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Projection)
		c.collectExpr(n.Owner)
	case *ast.CastExpr:
		c.collectExpr(n.Operand)
	case *ast.TernaryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Cond)
		c.collectExpr(n.Alt)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
		for _, spread := range n.Spreads {
			c.collectExpr(spread)
		}
	case *ast.RecordUpdateExpr:
		c.collectExpr(n.Base)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
	case *ast.MatchExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.LambdaExpr:
		c.collectStmts(n.Body)
		c.collectExpr(n.BodyExpr)
	case *ast.TryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.CatchExpr:
		c.collectExpr(n.Value)
		c.collectStmts(n.Success.Body)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.GetExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.OptionalBindExpr:
		c.collectExpr(n.Value)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error)
	case *ast.ParenExpr:
		c.collectExpr(n.Inner)
	case *ast.IsAliasExpr:
		c.collectExpr(n.Target)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			c.collectExpr(target)
		}
	case *ast.AllocExpr:
		c.collectExpr(n.Owner)
		c.collectExpr(n.Value)
	case *ast.FoldExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.EmitExpr:
		c.collectExpr(n.Value)
	}
}

func progressTarjan(names map[*ast.FuncDecl]string, edges map[*ast.FuncDecl]map[*ast.FuncDecl]bool) [][]*ast.FuncDecl {
	index := 0
	stack := []*ast.FuncDecl{}
	onStack := map[*ast.FuncDecl]bool{}
	indices := map[*ast.FuncDecl]int{}
	lowlink := map[*ast.FuncDecl]int{}
	var components [][]*ast.FuncDecl

	var strongConnect func(*ast.FuncDecl)
	strongConnect = func(v *ast.FuncDecl) {
		index++
		indices[v] = index
		lowlink[v] = index
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range sortedFuncDeclKeys(edges[v]) { // map: source order so component membership order is stable
			if _, ok := names[w]; !ok {
				continue
			}
			if indices[w] == 0 {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && indices[w] < lowlink[v] {
				lowlink[v] = indices[w]
			}
		}

		if lowlink[v] != indices[v] {
			return
		}
		var component []*ast.FuncDecl
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		components = append(components, component)
	}

	for _, fn := range sortedFuncDeclKeys(names) { // map: source order so component order is stable
		if indices[fn] == 0 {
			strongConnect(fn)
		}
	}
	return components
}
