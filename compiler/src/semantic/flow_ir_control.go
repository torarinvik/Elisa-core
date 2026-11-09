package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

func (b *cfgBuilder) buildIf(exits []int, stmt *ast.IfStmt) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		out = append(out, b.buildConditional(exit, stmt.Cond, stmt, stmt.Then, stmt.Elifs, stmt.Else)...)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) buildStaticIf(exits []int, stmt *ast.StaticIfStmt) []int {
	elifs := make([]ast.ElifClause, 0, len(stmt.Elifs))
	for _, elif := range stmt.Elifs {
		elifs = append(elifs, ast.ElifClause{Position: elif.Position, Cond: elif.Cond, Body: elif.Body})
	}
	lowered := &ast.IfStmt{Position: stmt.Position, Cond: stmt.Cond, Then: stmt.Then, Elifs: elifs, Else: stmt.Else}
	return b.buildIf(exits, lowered)
}

func (b *cfgBuilder) buildConditional(from int, cond ast.Expr, conditionNode ast.Node, thenBody []ast.Stmt, elifs []ast.ElifClause, elseBody []ast.Stmt) []int {
	b.appendNode(from, conditionNode)
	thenEntry := b.newBlock()
	elseEntry := b.newBlock()
	b.addEdge(from, thenEntry, b.conditionGuardFacts(cond, true))
	b.addEdge(from, elseEntry, b.conditionGuardFacts(cond, false))
	b.setTerminator(from, CFGTerminator{Kind: CFGTerminatorBranch, Position: cond.Pos()})
	thenExits := b.buildStmtList([]int{thenEntry}, thenBody)
	var elseExits []int
	if len(elifs) != 0 {
		elif := elifs[0]
		elifNode := &ast.IfStmt{Position: elif.Position, Cond: elif.Cond}
		elseExits = b.buildConditional(elseEntry, elif.Cond, elifNode, elif.Body, elifs[1:], elseBody)
	} else {
		elseExits = b.buildStmtList([]int{elseEntry}, elseBody)
	}
	return b.joinConditionalExits(thenExits, elseExits)
}

func (b *cfgBuilder) buildWhile(exits []int, stmt *ast.WhileStmt) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		condBlock := b.newBlock()
		b.addEdge(exit, condBlock, GuardFactSet{})
		b.setTerminator(exit, CFGTerminator{Kind: CFGTerminatorJump, Position: stmt.Pos()})
		b.appendNode(condBlock, stmt)
		bodyEntry := b.newBlock()
		afterBlock := b.newBlock()
		b.addEdge(condBlock, bodyEntry, b.conditionGuardFacts(stmt.Cond, true))
		b.addEdge(condBlock, afterBlock, b.conditionGuardFacts(stmt.Cond, false))
		b.setTerminator(condBlock, CFGTerminator{Kind: CFGTerminatorBranch, Position: stmt.Cond.Pos()})
		b.loopTargets = append(b.loopTargets, cfgLoopTargets{breakTarget: afterBlock, continueTarget: condBlock})
		bodyExits := b.buildStmtList([]int{bodyEntry}, stmt.Body)
		b.loopTargets = b.loopTargets[:len(b.loopTargets)-1]
		for _, bodyExit := range bodyExits {
			b.addEdge(bodyExit, condBlock, GuardFactSet{})
			b.setTerminator(bodyExit, CFGTerminator{Kind: CFGTerminatorJump, Position: stmt.Pos()})
		}
		out = append(out, afterBlock)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) buildLoopBody(exits []int, loopNode ast.Node, body []ast.Stmt) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		b.appendLoopHeaderNode(exit, loopNode)
		bodyEntry := b.newBlock()
		afterBlock := b.newBlock()
		b.addEdge(exit, bodyEntry, GuardFactSet{})
		b.addEdge(exit, afterBlock, GuardFactSet{})
		b.setTerminator(exit, CFGTerminator{Kind: CFGTerminatorLoopBranch, Position: loopNode.Pos()})
		b.loopTargets = append(b.loopTargets, cfgLoopTargets{breakTarget: afterBlock, continueTarget: exit})
		bodyExits := b.buildStmtList([]int{bodyEntry}, body)
		b.loopTargets = b.loopTargets[:len(b.loopTargets)-1]
		for _, bodyExit := range bodyExits {
			b.addEdge(bodyExit, exit, GuardFactSet{})
			b.setTerminator(bodyExit, CFGTerminator{Kind: CFGTerminatorJump, Position: loopNode.Pos()})
		}
		out = append(out, afterBlock)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) buildIterLoopBody(exits []int, loopNode ast.Node, filter ast.Expr, body []ast.Stmt) []int {
	if filter == nil {
		return b.buildLoopBody(exits, loopNode, body)
	}
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		b.appendLoopHeaderNode(exit, loopNode)
		bodyEntry := b.newBlock()
		iterationEntry := b.newBlock()
		skipBody := b.newBlock()
		afterBlock := b.newBlock()
		b.addEdge(exit, iterationEntry, GuardFactSet{})
		b.addEdge(exit, afterBlock, GuardFactSet{})
		b.setTerminator(exit, CFGTerminator{Kind: CFGTerminatorLoopBranch, Position: loopNode.Pos()})
		b.addEdge(iterationEntry, bodyEntry, b.conditionGuardFacts(filter, true))
		b.addEdge(iterationEntry, skipBody, b.conditionGuardFacts(filter, false))
		b.setTerminator(iterationEntry, CFGTerminator{Kind: CFGTerminatorBranch, Position: filter.Pos()})
		b.appendNode(iterationEntry, &ast.IfStmt{Position: filter.Pos(), Cond: filter})
		b.loopTargets = append(b.loopTargets, cfgLoopTargets{breakTarget: afterBlock, continueTarget: exit})
		bodyExits := b.buildStmtList([]int{bodyEntry}, body)
		b.loopTargets = b.loopTargets[:len(b.loopTargets)-1]
		for _, bodyExit := range bodyExits {
			b.addEdge(bodyExit, exit, GuardFactSet{})
			b.setTerminator(bodyExit, CFGTerminator{Kind: CFGTerminatorJump, Position: loopNode.Pos()})
		}
		b.addEdge(skipBody, exit, GuardFactSet{})
		b.setTerminator(skipBody, CFGTerminator{Kind: CFGTerminatorJump, Position: filter.Pos()})
		out = append(out, afterBlock)
	}
	return dedupeCFGBlockIDs(out)
}

func combineIterForFilters(whereFilter ast.Expr, filter ast.Expr) ast.Expr {
	if whereFilter == nil {
		return filter
	}
	if filter == nil {
		return whereFilter
	}
	return &ast.BinaryExpr{Position: whereFilter.Pos(), Op: lexer.TOKEN_AND, Left: whereFilter, Right: filter}
}

func (b *cfgBuilder) buildMatch(exits []int, stmt *ast.MatchStmt) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		b.appendNode(exit, stmt)
		armExits := make([]int, 0, len(stmt.Arms))
		for _, arm := range stmt.Arms {
			armEntry := b.newBlock()
			b.addEdge(exit, armEntry, GuardFactSet{})
			armExits = append(armExits, b.buildStmtList([]int{armEntry}, arm.Body)...)
		}
		if len(stmt.Arms) == 0 {
			out = append(out, exit)
			continue
		}
		b.setTerminator(exit, CFGTerminator{Kind: CFGTerminatorMatch, Position: stmt.Pos()})
		out = append(out, b.joinConditionalExits(armExits, nil)...)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) joinConditionalExits(left []int, right []int) []int {
	joined := dedupeCFGBlockIDs(append(append([]int(nil), left...), right...))
	if len(joined) <= 1 {
		return joined
	}
	joinBlock := b.newBlock()
	for _, block := range joined {
		b.addEdge(block, joinBlock, GuardFactSet{})
		b.setTerminator(block, CFGTerminator{Kind: CFGTerminatorJump})
	}
	return []int{joinBlock}
}

func (b *cfgBuilder) newBlock() int {
	id := len(b.cfg.Blocks)
	b.cfg.Blocks = append(b.cfg.Blocks, CFGBlock{ID: id})
	return id
}

func (b *cfgBuilder) appendNode(blockID int, node ast.Node) {
	if b == nil || node == nil || blockID < 0 || blockID >= len(b.cfg.Blocks) {
		return
	}
	b.cfg.Blocks[blockID].Nodes = append(b.cfg.Blocks[blockID].Nodes, node)
}

func (b *cfgBuilder) addEdge(from int, to int, guard GuardFactSet) {
	if b == nil || from < 0 || from >= len(b.cfg.Blocks) || to < 0 || to >= len(b.cfg.Blocks) {
		return
	}
	b.cfg.Blocks[from].Edges = append(b.cfg.Blocks[from].Edges, FlowEdge{To: to, Guard: guard.Clone()})
}

func (b *cfgBuilder) setTerminator(blockID int, terminator CFGTerminator) {
	if b == nil || blockID < 0 || blockID >= len(b.cfg.Blocks) {
		return
	}
	b.cfg.Blocks[blockID].Terminator = terminator
}

func (b *cfgBuilder) appendLoopHeaderNode(blockID int, loopNode ast.Node) {
	if loop, ok := loopNode.(*ast.IterForStmt); ok {
		// The iterable expression is evaluated once before iteration. Per-item
		// filters are represented by their own branch block below.
		b.appendNode(blockID, loop.Source)
		return
	}
	b.appendNode(blockID, loopNode)
}

func (b *cfgBuilder) buildLoopTransfer(blocks []int, stmt ast.Stmt, isBreak bool) []int {
	for _, block := range blocks {
		b.appendNode(block, stmt)
		if len(b.loopTargets) == 0 {
			// The semantic checker diagnoses this source error. Keep the recovery
			// CFG closed so no later statement is analyzed as reachable.
			b.setTerminator(block, CFGTerminator{Kind: CFGTerminatorInvalidTransfer, Position: stmt.Pos()})
			b.cfg.ExitBlocks = append(b.cfg.ExitBlocks, block)
			continue
		}
		targets := b.loopTargets[len(b.loopTargets)-1]
		target := targets.continueTarget
		kind := CFGTerminatorContinue
		if isBreak {
			target = targets.breakTarget
			kind = CFGTerminatorBreak
		}
		b.addEdge(block, target, GuardFactSet{})
		b.setTerminator(block, CFGTerminator{Kind: kind, Position: stmt.Pos()})
	}
	return nil
}

func flowStmtTerminator(stmt ast.Stmt) (CFGTerminatorKind, bool) {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return CFGTerminatorReturn, true
	case *ast.PanicStmt:
		return CFGTerminatorPanic, true
	case *ast.StaticErrorStmt:
		return CFGTerminatorStaticError, true
	case *ast.ExprStmt:
		_, ok := n.Expr.(*ast.RaiseExpr)
		if ok {
			return CFGTerminatorErrorExit, true
		}
		return CFGTerminatorUnset, false
	default:
		return CFGTerminatorUnset, false
	}
}

func stmtPosition(stmt ast.Stmt) lexer.Pos {
	if stmt == nil {
		return lexer.Pos{}
	}
	return stmt.Pos()
}

// flowStmtIsExplicitlyLinear is the allowlist for statements whose execution
// does not add control-flow edges. New AST statement kinds must be handled by
// the CFG builder or deliberately added here; unknown kinds fail closed.
func flowStmtIsExplicitlyLinear(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.AssignStmt, *ast.AugAssignStmt, *ast.AsRefAssignStmt,
		*ast.VarDeclStmt, *ast.LetDestructureStmt, *ast.TupleBindStmt, *ast.MoveBindStmt, *ast.ExprStmt,
		*ast.PassStmt, *ast.SignalStmt, *ast.MachineCoverageStmt, *ast.ExpectPatternStmt,
		*ast.StaticAssertStmt, *ast.AssertByStmt, *ast.ProofBlockStmt, *ast.AssertHoleStmt,
		*ast.ProofUseStmt, *ast.ContractStmt, *ast.StaticAssertBlockStmt, *ast.DiscardStmt,
		*ast.DestroyStmt, *ast.PromoteStmt, *ast.AdoptStmt, *ast.LeakStmt, *ast.MarkStmt,
		*ast.RestoreStmt, *ast.RestoreCheckpointStmt, *ast.ResetStmt:
		return true
	default:
		return false
	}
}

func dedupeCFGBlockIDs(ids []int) []int {
	if len(ids) <= 1 {
		return ids
	}
	// Branch joins normally have only two exits. Avoid constructing a map for
	// this common case while keeping the returned slice independent of ids.
	if len(ids) == 2 {
		if ids[0] == ids[1] {
			return []int{ids[0]}
		}
		return []int{ids[0], ids[1]}
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func flowLocationForExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return flowLocationForExpr(n.Inner)
	case *ast.CastExpr:
		return flowLocationForExpr(n.Operand)
	case *ast.CanExpr:
		return flowLocationForExpr(n.Expr)
	case *ast.MoveExpr:
		return flowLocationForExpr(n.Operand)
	case *ast.AddrOfExpr:
		return flowLocationForExpr(n.Operand)
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		base := flowLocationForExpr(n.Object)
		if base == "" {
			return ""
		}
		return base + "." + n.Field
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return ""
		}
		base := flowLocationForExpr(n.Object)
		if base == "" {
			return ""
		}
		return base + flowIndexSuffix(n.Index)
	default:
		return ""
	}
}

func flowLocationRoot(location string) string {
	for i := 0; i < len(location); i++ {
		switch location[i] {
		case '.', '[':
			return location[:i]
		}
	}
	return location
}

func flowIndexSuffix(index ast.Expr) string {
	if index == nil {
		return "[*]"
	}
	switch n := index.(type) {
	case *ast.ParenExpr:
		return flowIndexSuffix(n.Inner)
	case *ast.CastExpr:
		return flowIndexSuffix(n.Operand)
	case *ast.IntLit:
		value := n.Value
		if value == "" {
			return "[*]"
		}
		if _, err := strconv.ParseInt(value, 0, 64); err == nil {
			return "[" + value + "]"
		}
	}
	return "[*]"
}
