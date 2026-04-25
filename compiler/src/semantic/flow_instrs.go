package semantic

import "llcontext/src/ast"

func populateBasicFlowInstrs(cfg *CFG) {
	if cfg == nil {
		return
	}
	for i := range cfg.Blocks {
		block := &cfg.Blocks[i]
		for _, node := range block.Nodes {
			appendBasicFlowInstrsForNode(block, node)
		}
	}
}

func appendBasicFlowInstrsForNode(block *CFGBlock, node ast.Node) {
	if block == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.VarDeclStmt:
		if loc := flowLocationForExpr(n.Value); loc != "" {
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrAlias, Location: n.Name, Source: loc, Note: "var init alias"})
		}
		appendProduceFlowInstrForExpr(block, n.Name, n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.TupleBindStmt:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.LetDestructureStmt:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "assign")
		appendProduceFlowInstrForExpr(block, flowLocationForExpr(n.Target), n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AsRefAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "assign-as-ref")
		appendProduceFlowInstrForExpr(block, flowLocationForExpr(n.Target), n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AugAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), "aug-assign")
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.ReturnStmt:
		appendProduceFlowInstrForExpr(block, "<return>", n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrReturn, Note: "return"})
	case *ast.ExprStmt:
		appendBasicFlowExprInstrs(block, n.Expr)
	case *ast.RegionStmt:
		appendBasicFlowExprInstrs(block, n.Capacity)
	case *ast.MarkStmt:
	case *ast.RestoreStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.RegionName, Source: n.MarkName, Note: "restore region checkpoint"})
	case *ast.ResetStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.Name, Note: "reset region"})
	case *ast.DestroyStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.Name, Note: "destroy region"})
	case *ast.PoolStmt:
		appendBasicFlowExprInstrs(block, n.Workers)
	case *ast.LockStmt:
		appendBasicFlowExprInstrs(block, n.Mutex)
	case *ast.OpenStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.ViewStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg.Value)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				appendBasicFlowExprInstrs(block, arg.Value)
			}
		}
	case *ast.MoveBindStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.InStoreStmt:
		appendBasicFlowExprInstrs(block, n.Store)
	case *ast.CanStmt:
	case *ast.ForStmt:
		appendBasicFlowExprInstrs(block, n.Start)
		appendBasicFlowExprInstrs(block, n.End)
		appendBasicFlowExprInstrs(block, n.Step)
	case *ast.IterForStmt:
		appendBasicFlowExprInstrs(block, n.Source)
		appendBasicFlowExprInstrs(block, n.Filter)
	case *ast.ParallelForStmt:
		appendBasicFlowExprInstrs(block, n.Source)
	case *ast.IfStmt:
		appendBasicFlowExprInstrs(block, n.Cond)
	case *ast.WhileStmt:
		appendBasicFlowExprInstrs(block, n.Cond)
	case *ast.MatchStmt:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
	}
}

func appendBasicFlowExprInstrs(block *CFGBlock, expr ast.Expr) {
	if block == nil || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		appendBasicFlowExprInstrs(block, n.Inner)
	case *ast.CastExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.CanExpr:
		appendBasicFlowExprInstrs(block, n.Expr)
	case *ast.MoveExpr:
		if loc := flowLocationForExpr(n.Operand); loc != "" {
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: loc, Note: "explicit move"})
		}
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.AllocExpr:
		appendBasicFlowExprInstrs(block, n.Owner)
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.NodeSpan)
	case *ast.AddrOfExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.BinaryExpr:
		appendBasicFlowExprInstrs(block, n.Left)
		appendBasicFlowExprInstrs(block, n.Right)
	case *ast.UnaryExpr:
		appendBasicFlowExprInstrs(block, n.Operand)
	case *ast.CallExpr:
		appendRebaseFlowInstrForCall(block, n)
		appendBasicFlowExprInstrs(block, n.Func)
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.FieldExpr:
		appendBasicFlowExprInstrs(block, n.Object)
	case *ast.IndexExpr:
		appendBasicFlowExprInstrs(block, n.Object)
		appendBasicFlowExprInstrs(block, n.Index)
		appendBasicFlowExprInstrs(block, n.Fallback)
	case *ast.SliceExpr:
		appendBasicFlowExprInstrs(block, n.Object)
		appendBasicFlowExprInstrs(block, n.Start)
		appendBasicFlowExprInstrs(block, n.End)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.RecordUpdateExpr:
		appendBasicFlowExprInstrs(block, n.Base)
		for _, arg := range n.Args {
			appendBasicFlowExprInstrs(block, arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			appendBasicFlowExprInstrs(block, elem)
		}
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			appendBasicFlowExprInstrs(block, elem)
		}
	case *ast.TryExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Fallback)
	case *ast.UnwrapElseExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Fallback)
	case *ast.OptionalBindExpr:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.TernaryExpr:
		appendBasicFlowExprInstrs(block, n.Cond)
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Alt)
	case *ast.MatchExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Store)
		for _, arm := range n.Arms {
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.VisitExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		for _, arm := range n.Arms {
			appendBasicFlowExprInstrs(block, arm.Guard)
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.FoldExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		for _, arm := range n.Arms {
			appendBasicFlowExprInstrs(block, arm.Guard)
			for _, stmt := range arm.Body {
				appendBasicFlowInstrsForNode(block, stmt)
			}
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			appendBasicFlowExprInstrs(block, target)
		}
	}
}

func appendProduceFlowInstrForExpr(block *CFGBlock, target string, expr ast.Expr) {
	if block == nil || target == "" || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		appendProduceFlowInstrForExpr(block, target, n.Inner)
	case *ast.CastExpr:
		appendProduceFlowInstrForExpr(block, target, n.Operand)
	case *ast.CanExpr:
		appendProduceFlowInstrForExpr(block, target, n.Expr)
	case *ast.AllocExpr:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrProduce, Location: target, Source: flowLocationForExpr(n.Owner), Note: allocProduceFlowNote(n)})
	case *ast.CallExpr:
		if callIdentName(n) == "freeze" {
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrProduce, Location: target, Note: "freeze produces frozen store"})
		}
	}
}

func appendRebaseFlowInstrForCall(block *CFGBlock, call *ast.CallExpr) {
	if block == nil || call == nil || callIdentName(call) != "freeze" || len(call.Args) != 1 {
		return
	}
	if target := flowLocationForExpr(call.Args[0]); target != "" {
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrRebase, Location: target, Note: "freeze rebases store provenance"})
	}
}

func allocProduceFlowNote(expr *ast.AllocExpr) string {
	if expr != nil && expr.NodeSugar {
		return "node construction"
	}
	return "allocation produces value"
}

func appendMutationFlowInstr(block *CFGBlock, location string, note string) {
	if block == nil || location == "" {
		return
	}
	for _, candidate := range mutationLocationPrefixes(location) {
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrMutate, Location: candidate, Note: note})
	}
}

func appendFlowInstrUnique(block *CFGBlock, instr FlowInstr) {
	if block == nil {
		return
	}
	for _, existing := range block.Instrs {
		if existing.Kind == instr.Kind && existing.Location == instr.Location && existing.Source == instr.Source && existing.Note == instr.Note {
			return
		}
	}
	block.Instrs = append(block.Instrs, instr)
}

func mutationLocationPrefixes(location string) []string {
	if location == "" {
		return nil
	}
	out := []string{location}
	root := flowLocationRoot(location)
	if root != "" && root != location {
		out = append(out, root)
	}
	return dedupeStringSlice(out)
}
