package semantic

import "llcontext/src/ast"

type analyzerASTCensus struct {
	exprs        int
	funcDecls    int
	parallelFors int
}

func analyzeASTCensus(file *ast.File) analyzerASTCensus {
	if file == nil {
		return analyzerASTCensus{}
	}
	var census analyzerASTCensus
	census.countDecls(file.Decls)
	return census
}

func (c *analyzerASTCensus) countDecls(decls []ast.Decl) {
	for _, decl := range decls {
		c.countDecl(decl)
	}
}

func (c *analyzerASTCensus) countDecl(decl ast.Decl) {
	switch n := decl.(type) {
	case *ast.ConstDecl:
		c.countExpr(n.Value)
	case *ast.ConstEnumDecl:
		for _, member := range n.Members {
			c.countExpr(member.Value)
		}
	case *ast.ParamsDecl:
		for _, param := range n.Params {
			c.countExpr(param.DefaultValue)
		}
	case *ast.NamespaceDecl:
		c.countDecls(n.Decls)
	case *ast.GlobalDecl:
		c.countExpr(n.Value)
	case *ast.FuncDecl:
		c.funcDecls++
		c.countStmts(n.Body)
	case *ast.AttributeDecl:
		for _, arm := range n.Arms {
			c.countExpr(arm.Guard)
			c.countStmts(arm.Body)
		}
	case *ast.StaticIfDecl:
		c.countExpr(n.Cond)
		c.countDecls(n.Then)
		for _, clause := range n.Elifs {
			c.countExpr(clause.Cond)
			c.countDecls(clause.Body)
		}
		c.countDecls(n.Else)
	}
}

func (c *analyzerASTCensus) countStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		c.countStmt(stmt)
	}
}

func (c *analyzerASTCensus) countStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		c.countExpr(n.Target)
		c.countExpr(n.Value)
	case *ast.AugAssignStmt:
		c.countExpr(n.Target)
		c.countExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.countExpr(n.Target)
		c.countExpr(n.Value)
	case *ast.VarDeclStmt:
		c.countExpr(n.Value)
	case *ast.LocalParamsStmt:
		for _, param := range n.Params {
			c.countExpr(param.DefaultValue)
		}
	case *ast.LetDestructureStmt:
		c.countExpr(n.Value)
	case *ast.TupleBindStmt:
		c.countExpr(n.Value)
	case *ast.MoveBindStmt:
		c.countExpr(n.Value)
		c.countExpr(n.Store)
		c.countMoveBindPattern(n.Pattern)
	case *ast.OpenStmt:
		c.countExpr(n.Value)
		c.countExpr(n.Store)
		c.countMoveBindPattern(n.Pattern)
		c.countStmts(n.Body)
	case *ast.ViewStmt:
		c.countExpr(n.Value)
		c.countExpr(n.Store)
		c.countViewBindPattern(n.Pattern)
		c.countStmts(n.Body)
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			c.countExpr(arg.Value)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				c.countExpr(arg.Value)
			}
		}
		c.countStmts(n.Body)
	case *ast.DeferStmt:
		c.countStmts(n.Body)
	case *ast.ReturnStmt:
		c.countExpr(n.Value)
	case *ast.IfStmt:
		c.countExpr(n.Cond)
		c.countStmts(n.Then)
		for _, clause := range n.Elifs {
			c.countExpr(clause.Cond)
			c.countStmts(clause.Body)
		}
		c.countStmts(n.Else)
	case *ast.WhileStmt:
		c.countExpr(n.Cond)
		c.countStmts(n.Body)
	case *ast.ForStmt:
		c.countExpr(n.Start)
		c.countExpr(n.End)
		c.countExpr(n.Step)
		c.countStmts(n.Body)
	case *ast.IterForStmt:
		c.countExpr(n.Source)
		c.countExpr(n.Filter)
		c.countMoveBindPattern(n.Pattern)
		c.countStmts(n.Body)
	case *ast.ParallelForStmt:
		c.parallelFors++
		c.countExpr(n.Source)
		c.countStmts(n.Body)
	case *ast.MatchStmt:
		c.countExpr(n.Value)
		c.countExpr(n.Store)
		c.countArms(n.Arms)
	case *ast.InStoreStmt:
		c.countExpr(n.Store)
		c.countStmts(n.Body)
	case *ast.CanStmt:
		c.countStmts(n.Body)
	case *ast.CascadeStmt:
		c.countExpr(n.Target)
		c.countStmts(n.Body)
	case *ast.PoolStmt:
		c.countExpr(n.Workers)
		c.countStmts(n.Body)
	case *ast.LockStmt:
		c.countExpr(n.Mutex)
		c.countStmts(n.Body)
	case *ast.PanicStmt:
		c.countExpr(n.Message)
	case *ast.ExprStmt:
		c.countExpr(n.Expr)
	case *ast.StaticIfStmt:
		c.countExpr(n.Cond)
		c.countStmts(n.Then)
		for _, clause := range n.Elifs {
			c.countExpr(clause.Cond)
			c.countStmts(clause.Body)
		}
		c.countStmts(n.Else)
	case *ast.StaticErrorStmt:
		c.countExpr(n.Message)
	case *ast.DiscardStmt:
		c.countExpr(n.Value)
	case *ast.RegionStmt:
		c.countExpr(n.Capacity)
	}
}

func (c *analyzerASTCensus) countExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	c.exprs++
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		c.countExpr(n.Left)
		c.countExpr(n.Right)
	case *ast.UnaryExpr:
		c.countExpr(n.Operand)
	case *ast.MoveExpr:
		c.countExpr(n.Operand)
	case *ast.CallExpr:
		c.countExpr(n.Func)
		for _, arg := range n.Args {
			c.countExpr(arg)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				c.countExpr(arg.Value)
			}
		}
		for _, arg := range n.WithArgs {
			c.countExpr(arg.Value)
		}
		for _, bundle := range n.WithBundles {
			for _, arg := range bundle.Args {
				c.countExpr(arg.Value)
			}
		}
	case *ast.FieldExpr:
		c.countExpr(n.Object)
	case *ast.IndexExpr:
		c.countExpr(n.Object)
		c.countExpr(n.Index)
		c.countExpr(n.Fallback)
	case *ast.SliceExpr:
		c.countExpr(n.Object)
		c.countExpr(n.Start)
		c.countExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.countExpr(elem)
		}
	case *ast.TernaryExpr:
		c.countExpr(n.Value)
		c.countExpr(n.Cond)
		c.countExpr(n.Alt)
	case *ast.AddrOfExpr:
		c.countExpr(n.Operand)
	case *ast.SpecializeExpr:
		c.countExpr(n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.countExpr(arg)
		}
	case *ast.RecordUpdateExpr:
		c.countExpr(n.Base)
		for _, arg := range n.Args {
			c.countExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			c.countExpr(elem)
		}
	case *ast.ParenExpr:
		c.countExpr(n.Inner)
	case *ast.RaiseExpr:
		c.countExpr(n.Error)
	case *ast.TryExpr:
		c.countExpr(n.Value)
		c.countExpr(n.Fallback)
	case *ast.UnwrapElseExpr:
		c.countExpr(n.Value)
		c.countExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		c.countExpr(n.Value)
	case *ast.AllocExpr:
		c.countExpr(n.Owner)
		c.countExpr(n.NodeSpan)
		c.countExpr(n.Value)
	case *ast.CanExpr:
		c.countExpr(n.Expr)
	case *ast.MatchExpr:
		c.countExpr(n.Value)
		c.countExpr(n.Store)
		c.countArms(n.Arms)
	case *ast.VisitExpr:
		c.countExpr(n.Value)
		c.countVisitArms(n.Arms)
	case *ast.FoldExpr:
		c.countExpr(n.Value)
		c.countVisitArms(n.Arms)
	case *ast.VariantTestExpr:
		c.countMatchPattern(n.Pattern)
	case *ast.StructTestExpr:
		c.countMatchPattern(n.Pattern)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			c.countExpr(target)
		}
	}
}

func (c *analyzerASTCensus) countArms(arms []ast.MatchArm) {
	for _, arm := range arms {
		c.countMatchPattern(arm.Pattern)
		c.countStmts(arm.Body)
	}
}

func (c *analyzerASTCensus) countVisitArms(arms []ast.VisitArm) {
	for _, arm := range arms {
		c.countExpr(arm.Guard)
		c.countStmts(arm.Body)
	}
}

func (c *analyzerASTCensus) countMatchPattern(pattern ast.MatchPattern) {
	switch n := pattern.(type) {
	case *ast.MatchLiteralPattern:
		c.countExpr(n.Value)
	case *ast.MatchTuplePattern:
		for _, elem := range n.Elems {
			c.countMatchPattern(elem)
		}
	case *ast.MatchStructPattern:
		for _, arg := range n.Args {
			c.countMatchPattern(arg.Pattern)
		}
	case *ast.MatchVariantPattern:
		for _, arg := range n.Args {
			c.countMatchPattern(arg.Pattern)
		}
	}
}

func (c *analyzerASTCensus) countMoveBindPattern(pattern ast.MoveBindPattern) {
	switch n := pattern.(type) {
	case *ast.MoveBindVariantPattern:
		for _, arg := range n.Args {
			c.countMatchPattern(arg.Pattern)
		}
	}
}

func (c *analyzerASTCensus) countViewBindPattern(pattern *ast.ViewBindPattern) {
	if pattern == nil {
		return
	}
	for _, arg := range pattern.Args {
		c.countMatchPattern(arg.Pattern)
	}
}
