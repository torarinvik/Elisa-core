package semantic

import "elisacore/src/ast"

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
	case *ast.TokenSetDecl:
		c.countExpr(n.Value)
	case *ast.KeywordMapDecl:
		c.countExpr(n.Fallback)
		for _, entry := range n.Entries {
			c.countExpr(entry.Value)
		}
	case *ast.ConstEnumDecl:
		for _, member := range n.Members {
			c.countExpr(member.Value)
		}
	case *ast.ParamsDecl:
		for _, param := range n.Params {
			c.countExpr(param.DefaultValue)
		}
	case *ast.StructDecl:
		for _, field := range n.Fields {
			c.countExpr(field.DefaultValue)
		}
		for _, state := range n.DerivedStates {
			c.countExpr(state.Condition)
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
	case *ast.StaticAssertDecl:
		c.countExpr(n.Cond)
		if n.Message != nil {
			c.countExpr(n.Message)
		}
	case *ast.StaticAssertBlockDecl:
		for _, item := range n.Assertions {
			c.countExpr(item.Cond)
			if item.Message != nil {
				c.countExpr(item.Message)
			}
		}
	case *ast.StaticGenerateDecl:
		c.countStaticGenerateStmts(n.Body)
	}
}

func (c *analyzerASTCensus) countStaticGenerateStmts(stmts []ast.StaticGenerateStmt) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.StaticGenerateForDecl:
			c.countExpr(n.Source)
			if n.Filter != nil {
				c.countExpr(n.Filter)
			}
			c.countStaticGenerateStmts(n.Body)
		case *ast.StaticGenerateIfDecl:
			c.countExpr(n.Cond)
			c.countStaticGenerateStmts(n.Then)
			for _, elif := range n.Elifs {
				c.countExpr(elif.Cond)
				c.countStaticGenerateStmts(elif.Body)
			}
			c.countStaticGenerateStmts(n.Else)
		}
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
		c.countMatchPattern(n.PatternFilter)
		c.countExpr(n.WhereFilter)
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
	case *ast.ExpectPatternStmt:
		c.countExpr(n.Value)
		for _, pattern := range n.Patterns {
			c.countMatchPattern(pattern)
		}
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
	case *ast.StaticAssertStmt:
		c.countExpr(n.Cond)
		if n.Message != nil {
			c.countExpr(n.Message)
		}
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			c.countExpr(item.Cond)
			if item.Message != nil {
				c.countExpr(item.Message)
			}
		}
	case *ast.StaticBlockStmt:
		c.countStmts(n.Body)
	case *ast.DiscardStmt:
		c.countExpr(n.Value)
	case *ast.RegionStmt:
		c.countExpr(n.Capacity)
		c.countStmts(n.Body)
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
		c.countExpr(n.SafeReceiver)
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
		c.countExpr(n.Owner)
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
	case *ast.QueryExpr:
		c.countExpr(n.Source)
		c.countExpr(n.Filter)
		c.countMatchPattern(n.PatternFilter)
		c.countMoveBindPattern(n.Pattern)
		c.countExpr(n.Projection)
		c.countExpr(n.Owner)
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
	case *ast.IsAliasExpr:
		c.countExpr(n.Target)
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
	case *ast.MatchListPattern:
		for _, elem := range n.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
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
