package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) inferFuncSinkParamsForExpr(expr ast.Expr, fnType *FuncType) {
	if fnType == nil || fnType.SinkParamsKnown || expr == nil {
		return
	}
	decl, resolvedType, ok := a.resolveSinkFuncDecl(expr)
	if !ok {
		if len(fnType.SinkParams) != len(fnType.Params) {
			fnType.SinkParams = make([]bool, len(fnType.Params))
		}
		fnType.SinkParamsKnown = true
		return
	}
	if resolvedType != nil && resolvedType != fnType {
		fnType = resolvedType
	}
	a.inferFuncSinkParams(decl, fnType)
}

func (a *Analyzer) inferFuncSinkParams(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.SinkParamsKnown {
		return
	}
	if a.sinkParamInferenceInProgress[fn] {
		return
	}
	a.sinkParamInferenceInProgress[fn] = true
	defer delete(a.sinkParamInferenceInProgress, fn)

	cfg := a.constructCFG(fn)
	configureCFGParamLocations(cfg, fnType)
	populateBasicFlowInstrs(cfg)
	a.addImplicitSinkFlowInstrs(cfg)
	fnType.SinkParams = inferSinkParamsFromCFG(cfg)
	fnType.SinkParamsKnown = true
	analysis := a.functionAnalyses[fn]
	if analysis == nil {
		analysis = &FunctionAnalysis{}
	}
	analysis.CFG = cfg
	analysis.SinkParams = append([]bool(nil), fnType.SinkParams...)
	a.functionAnalyses[fn] = analysis
}

func (a *Analyzer) addImplicitSinkFlowInstrs(cfg *CFG) {
	if a == nil || cfg == nil {
		return
	}
	for i := range cfg.Blocks {
		block := &cfg.Blocks[i]
		for _, node := range block.Nodes {
			a.appendImplicitSinkFlowInstrsForNode(block, node)
		}
	}
}

func (a *Analyzer) appendImplicitSinkFlowInstrsForNode(block *CFGBlock, node ast.Node) {
	if a == nil || block == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.VarDeclStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.TupleBindStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.AssignStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.AsRefAssignStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.AugAssignStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.ReturnStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.ExprStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Expr)
	case *ast.RegionStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Capacity)
		a.appendImplicitSinkFlowInstrsForStmts(block, n.Body)
	case *ast.PoolStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Workers)
	case *ast.LockStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Mutex)
	case *ast.MoveBindStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.LetDestructureStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.InStoreStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	case *ast.ForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Start)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.End)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Step)
	case *ast.IterForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Source)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.WhereFilter)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Filter)
	case *ast.ParallelForStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Source)
	case *ast.IfStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Cond)
	case *ast.WhileStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Cond)
	case *ast.MatchStmt:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
	}
}

func (a *Analyzer) appendImplicitSinkFlowInstrsForStmts(block *CFGBlock, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		a.appendImplicitSinkFlowInstrsForNode(block, stmt)
	}
}

func (a *Analyzer) appendImplicitSinkFlowInstrsForExpr(block *CFGBlock, expr ast.Expr) {
	if a == nil || block == nil || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Inner)
	case *ast.CastExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.CanExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Expr)
	case *ast.MoveExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.AddrOfExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.BinaryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Left)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Right)
	case *ast.UnaryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Operand)
	case *ast.CallExpr:
		a.appendImplicitSinkCallInstrs(block, n)
	case *ast.FieldExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
	case *ast.IndexExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Index)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.SliceExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Object)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Start)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.End)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			a.appendImplicitSinkFlowInstrsForExpr(block, arg)
		}
	case *ast.RecordUpdateExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Base)
		for _, arg := range n.Args {
			a.appendImplicitSinkFlowInstrsForExpr(block, arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.appendImplicitSinkFlowInstrsForExpr(block, elem)
		}
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.appendImplicitSinkFlowInstrsForExpr(block, elem)
		}
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Owner)
	case *ast.TryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.UnwrapElseExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.GetExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Fallback)
	case *ast.OptionalBindExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
	case *ast.TernaryExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Cond)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Alt)
	case *ast.MatchExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Store)
		for _, arm := range n.Arms {
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.FoldExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Value)
		for _, arm := range n.Arms {
			a.appendImplicitSinkFlowInstrsForExpr(block, arm.Guard)
			for _, stmt := range arm.Body {
				a.appendImplicitSinkFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.appendImplicitSinkFlowInstrsForExpr(block, target)
		}
	case *ast.IsAliasExpr:
		a.appendImplicitSinkFlowInstrsForExpr(block, n.Target)
	}
}

func (a *Analyzer) appendImplicitSinkCallInstrs(block *CFGBlock, call *ast.CallExpr) {
	if a == nil || block == nil || call == nil {
		return
	}
	fnType, decl, ok := a.resolveSinkFuncType(call.Func)
	if ok && fnType != nil {
		if !fnType.SinkParamsKnown {
			if decl != nil {
				a.inferFuncSinkParams(decl, fnType)
			} else {
				fnType.SinkParams = make([]bool, len(fnType.Params))
				fnType.SinkParamsKnown = true
			}
		}
		for i, arg := range call.Args {
			if i >= len(fnType.SinkParams) || !fnType.SinkParams[i] {
				continue
			}
			if _, moved := explicitMoveOperand(arg); moved {
				continue
			}
			if location := flowLocationForExpr(arg); location != "" {
				appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: location, Position: call.Pos(), Note: "sink arg to " + fnType.Name})
			}
		}
	}
	a.appendImplicitSinkFlowInstrsForExpr(block, call.Func)
	for _, arg := range call.Args {
		a.appendImplicitSinkFlowInstrsForExpr(block, arg)
	}
}

func (a *Analyzer) resolveSinkFuncDecl(expr ast.Expr) (*ast.FuncDecl, *FuncType, bool) {
	if a == nil || expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.resolveSinkFuncDecl(n.Inner)
	case *ast.SpecializeExpr:
		return a.resolveSinkFuncDecl(n.Operand)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				fnType, _ := sym.Type.(*FuncType)
				if decl, ok := sym.Node.(*ast.FuncDecl); ok {
					return decl, fnType, true
				}
				if fnType != nil {
					return nil, fnType, true
				}
			}
		}
		if a.globalScope != nil {
			if sym, ok := a.globalScope.Lookup(n.Name); ok {
				fnType, _ := sym.Type.(*FuncType)
				if decl, ok := sym.Node.(*ast.FuncDecl); ok {
					return decl, fnType, true
				}
				if fnType != nil {
					return nil, fnType, true
				}
			}
		}
	}
	if fnType, ok := a.exprTypes[expr].(*FuncType); ok {
		return nil, fnType, true
	}
	return nil, nil, false
}

func (a *Analyzer) resolveSinkFuncType(expr ast.Expr) (*FuncType, *ast.FuncDecl, bool) {
	decl, fnType, ok := a.resolveSinkFuncDecl(expr)
	return fnType, decl, ok
}
