package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrAlias, Location: n.Name, Source: loc, Position: n.Pos(), Note: "var init alias"})
		}
		appendProduceFlowInstrForExpr(block, n.Name, n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.TupleBindStmt:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.LetDestructureStmt:
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), n.Pos(), "assign")
		appendProduceFlowInstrForExpr(block, flowLocationForExpr(n.Target), n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AsRefAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), n.Pos(), "assign-as-ref")
		appendProduceFlowInstrForExpr(block, flowLocationForExpr(n.Target), n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.AugAssignStmt:
		appendMutationFlowInstr(block, flowLocationForExpr(n.Target), n.Pos(), "aug-assign")
		appendBasicFlowExprInstrs(block, n.Value)
	case *ast.ReturnStmt:
		appendProduceFlowInstrForExpr(block, "<return>", n.Value)
		appendBasicFlowExprInstrs(block, n.Value)
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrReturn, Position: n.Pos(), Note: "return"})
	case *ast.ExprStmt:
		appendBasicFlowExprInstrs(block, n.Expr)
	case *ast.RegionStmt:
		appendBasicFlowExprInstrs(block, n.Capacity)
		for _, stmt := range n.Body {
			appendBasicFlowInstrsForNode(block, stmt)
		}
	case *ast.MarkStmt:
	case *ast.RestoreStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.RegionName, Source: n.MarkName, Position: n.Pos(), Note: "restore region checkpoint"})
	case *ast.ResetStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.Name, Position: n.Pos(), Note: "reset region"})
	case *ast.DestroyStmt:
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrInvalidate, Location: n.Name, Position: n.Pos(), Note: "destroy region"})
	case *ast.PoolStmt:
		appendBasicFlowExprInstrs(block, n.Workers)
	case *ast.LockStmt:
		appendBasicFlowExprInstrs(block, n.Mutex)
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
		appendBasicFlowExprInstrs(block, n.WhereFilter)
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
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: loc, Position: n.Pos(), Note: "explicit move"})
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
	case *ast.RaiseExpr:
		appendBasicFlowExprInstrs(block, n.Error)
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrErrorExit, Location: "<error>", Source: flowLocationForExpr(n.Error), Position: n.Pos(), Note: "raise error path"})
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
		note := "try fallback handles error path"
		if n.Fallback == nil {
			note = "try propagates error path"
		}
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrErrorExit, Location: "<error>", Source: flowLocationForExpr(n.Value), Position: n.Pos(), Note: note})
	case *ast.UnwrapElseExpr:
		appendBasicFlowExprInstrs(block, n.Value)
		appendBasicFlowExprInstrs(block, n.Fallback)
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrErrorExit, Location: "<error>", Source: flowLocationForExpr(n.Value), Position: n.Pos(), Note: "else fallback handles nullable path"})
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
	case *ast.IsAliasExpr:
		appendBasicFlowExprInstrs(block, n.Target)
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
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrProduce, Location: target, Source: flowLocationForExpr(n.Owner), Position: n.Pos(), Note: allocProduceFlowNote(n)})
	case *ast.CallExpr:
		if callIdentName(n) == "freeze" {
			source := "freeze"
			if len(n.Args) == 1 {
				source = flowLocationForExpr(n.Args[0])
			}
			appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrProduce, Location: target, Source: source, Position: n.Pos(), Note: "freeze produces frozen store"})
		}
	}
}

func appendRebaseFlowInstrForCall(block *CFGBlock, call *ast.CallExpr) {
	if block == nil || call == nil || callIdentName(call) != "freeze" || len(call.Args) != 1 {
		return
	}
	if target := flowLocationForExpr(call.Args[0]); target != "" {
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrRebase, Location: target, Source: "freeze", Position: call.Pos(), Note: "freeze rebases store provenance"})
	}
}

func allocProduceFlowNote(expr *ast.AllocExpr) string {
	if expr != nil && expr.NodeSugar {
		if expr.NodeSpan != nil {
			return "node construction with span"
		}
		return "node construction"
	}
	return "allocation produces value"
}

func appendMutationFlowInstr(block *CFGBlock, location string, pos lexer.Pos, note string) {
	if block == nil || location == "" {
		return
	}
	for _, candidate := range mutationLocationPrefixes(location) {
		appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrMutate, Location: candidate, Position: pos, Note: note})
	}
}

func appendFlowInstrUnique(block *CFGBlock, instr FlowInstr) {
	if block == nil {
		return
	}
	for _, existing := range block.Instrs {
		if existing.Kind == instr.Kind && existing.Location == instr.Location && existing.Source == instr.Source && existing.Position == instr.Position && existing.Note == instr.Note {
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
