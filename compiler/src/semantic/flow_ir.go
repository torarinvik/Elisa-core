package semantic

import (
	"fmt"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type FlowInstrKind string

const (
	FlowInstrAlias      FlowInstrKind = "alias"
	FlowInstrConsume    FlowInstrKind = "consume"
	FlowInstrInvalidate FlowInstrKind = "invalidate"
	FlowInstrMutate     FlowInstrKind = "mutate"
	FlowInstrProduce    FlowInstrKind = "produce"
	FlowInstrRebase     FlowInstrKind = "rebase"
	FlowInstrErrorExit  FlowInstrKind = "error-exit"
	FlowInstrReturn     FlowInstrKind = "return"
)

type FlowInstr struct {
	Kind     FlowInstrKind
	Location string
	Source   string
	Position lexer.Pos
	Note     string
}

type FlowEdge struct {
	To    int
	Guard GuardFactSet
}

// CFGTerminatorKind records why a block has no more ordinary sequential code.
// It distinguishes function exits from branches and loop transfers, and gives
// the CFG verifier enough information to reject malformed successor lists.
type CFGTerminatorKind string

const (
	CFGTerminatorUnset           CFGTerminatorKind = "unset"
	CFGTerminatorFallthrough     CFGTerminatorKind = "fallthrough"
	CFGTerminatorJump            CFGTerminatorKind = "jump"
	CFGTerminatorBranch          CFGTerminatorKind = "branch"
	CFGTerminatorLoopBranch      CFGTerminatorKind = "loop-branch"
	CFGTerminatorMatch           CFGTerminatorKind = "match"
	CFGTerminatorReturn          CFGTerminatorKind = "return"
	CFGTerminatorErrorExit       CFGTerminatorKind = "error-exit"
	CFGTerminatorPanic           CFGTerminatorKind = "panic"
	CFGTerminatorStaticError     CFGTerminatorKind = "static-error"
	CFGTerminatorBreak           CFGTerminatorKind = "break"
	CFGTerminatorContinue        CFGTerminatorKind = "continue"
	CFGTerminatorInvalidTransfer CFGTerminatorKind = "invalid-transfer"
	CFGTerminatorUnsupported     CFGTerminatorKind = "unsupported"
)

type CFGTerminator struct {
	Kind     CFGTerminatorKind
	Position lexer.Pos
	Reason   string
}

type CFGBlock struct {
	ID             int
	Nodes          []ast.Node
	Instrs         []FlowInstr
	FactTransforms []FactTransform
	Edges          []FlowEdge
	Terminator     CFGTerminator
}

type CFG struct {
	Entry          int
	Blocks         []CFGBlock
	ExitBlocks     []int
	ParamLocations []string
}

type cfgBuilder struct {
	cfg         *CFG
	guardFacts  func(ast.Expr, bool) GuardFactSet
	loopTargets []cfgLoopTargets
}

type cfgLoopTargets struct {
	breakTarget    int
	continueTarget int
}

func ConstructCFG(fn *ast.FuncDecl) *CFG {
	return constructCFG(fn, nil)
}

func constructCFG(fn *ast.FuncDecl, guardFacts func(ast.Expr, bool) GuardFactSet) *CFG {
	cfg := &CFG{}
	if fn == nil {
		return cfg
	}
	cfg.Entry = 0
	cfg.Blocks = append(cfg.Blocks, CFGBlock{ID: 0})
	cfg.ParamLocations = make([]string, 0, len(fn.Params))
	for _, param := range fn.Params {
		cfg.ParamLocations = append(cfg.ParamLocations, param.Name)
	}
	builder := &cfgBuilder{cfg: cfg, guardFacts: guardFacts}
	exits := builder.buildStmtList([]int{cfg.Entry}, fn.Body)
	for _, exit := range exits {
		builder.setTerminator(exit, CFGTerminator{Kind: CFGTerminatorFallthrough})
	}
	cfg.ExitBlocks = dedupeCFGBlockIDs(append(cfg.ExitBlocks, exits...))
	return cfg
}

// VerifyCFG checks structural invariants required by semantic analyses. It is
// independent of typing so it can also validate test-built graphs.
func VerifyCFG(cfg *CFG) error {
	if cfg == nil {
		return fmt.Errorf("nil CFG")
	}
	if len(cfg.Blocks) == 0 {
		return fmt.Errorf("CFG has no blocks")
	}
	if cfg.Entry < 0 || cfg.Entry >= len(cfg.Blocks) {
		return fmt.Errorf("CFG entry %d is out of range", cfg.Entry)
	}
	exits := make(map[int]bool, len(cfg.ExitBlocks))
	for _, id := range cfg.ExitBlocks {
		if id < 0 || id >= len(cfg.Blocks) {
			return fmt.Errorf("CFG exit block %d is out of range", id)
		}
		if exits[id] {
			return fmt.Errorf("CFG exit block %d is repeated", id)
		}
		exits[id] = true
	}
	for i := range cfg.Blocks {
		block := &cfg.Blocks[i]
		if block.ID != i {
			return fmt.Errorf("CFG block at index %d has id %d", i, block.ID)
		}
		for _, edge := range block.Edges {
			if edge.To < 0 || edge.To >= len(cfg.Blocks) {
				return fmt.Errorf("CFG block %d targets out-of-range block %d", block.ID, edge.To)
			}
		}
		edges := len(block.Edges)
		switch block.Terminator.Kind {
		case CFGTerminatorFallthrough, CFGTerminatorReturn, CFGTerminatorErrorExit,
			CFGTerminatorPanic, CFGTerminatorStaticError, CFGTerminatorInvalidTransfer:
			if edges != 0 {
				return fmt.Errorf("terminal CFG block %d has %d successors", block.ID, edges)
			}
			if !exits[block.ID] {
				return fmt.Errorf("terminal CFG block %d is not listed as an exit", block.ID)
			}
		case CFGTerminatorJump, CFGTerminatorBreak, CFGTerminatorContinue:
			if edges != 1 {
				return fmt.Errorf("CFG block %d terminator %q needs one successor, got %d", block.ID, block.Terminator.Kind, edges)
			}
		case CFGTerminatorBranch, CFGTerminatorLoopBranch:
			if edges != 2 {
				return fmt.Errorf("CFG block %d terminator %q needs two successors, got %d", block.ID, block.Terminator.Kind, edges)
			}
		case CFGTerminatorMatch:
			if edges == 0 {
				return fmt.Errorf("CFG block %d match terminator has no successors", block.ID)
			}
		case CFGTerminatorUnset:
			return fmt.Errorf("CFG block %d has no terminator", block.ID)
		case CFGTerminatorUnsupported:
			if edges != 0 {
				return fmt.Errorf("unsupported CFG block %d unexpectedly has successors", block.ID)
			}
			if !exits[block.ID] {
				return fmt.Errorf("unsupported CFG block %d is not closed as an exit", block.ID)
			}
			return fmt.Errorf("CFG block %d contains unsupported statement %s", block.ID, block.Terminator.Reason)
		default:
			return fmt.Errorf("CFG block %d has unknown terminator %q", block.ID, block.Terminator.Kind)
		}
	}
	for id := range exits {
		kind := cfg.Blocks[id].Terminator.Kind
		switch kind {
		case CFGTerminatorFallthrough, CFGTerminatorReturn, CFGTerminatorErrorExit,
			CFGTerminatorPanic, CFGTerminatorStaticError, CFGTerminatorInvalidTransfer:
		default:
			return fmt.Errorf("CFG exit block %d has nonterminal terminator %q", id, kind)
		}
	}
	return nil
}

func (b *cfgBuilder) conditionGuardFacts(cond ast.Expr, truthy bool) GuardFactSet {
	if b != nil && b.guardFacts != nil {
		return b.guardFacts(cond, truthy)
	}
	return GuardFactsForCondition(cond, truthy)
}

func (b *cfgBuilder) buildStmtList(exits []int, stmts []ast.Stmt) []int {
	current := dedupeCFGBlockIDs(exits)
	for _, stmt := range stmts {
		if len(current) == 0 {
			break
		}
		switch n := stmt.(type) {
		case *ast.IfStmt:
			current = b.buildIf(current, n)
		case *ast.WhileStmt:
			current = b.buildWhile(current, n)
		case *ast.MatchStmt:
			current = b.buildMatch(current, n)
		case *ast.StaticIfStmt:
			current = b.buildStaticIf(current, n)
		case *ast.ForStmt:
			current = b.buildLoopBody(current, n, n.Body)
		case *ast.IterForStmt:
			current = b.buildIterLoopBody(current, n, combineIterForFilters(n.WhereFilter, n.Filter), n.Body)
		case *ast.ParallelForStmt:
			current = b.buildLoopBody(current, n, n.Body)
		case *ast.PoolStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.LockStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.InStoreStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.CanStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.DeferStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.ScopeStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.RegionStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.CheckpointStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.GroupedCheckpointStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.StaticBlockStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.BreakStmt:
			current = b.buildLoopTransfer(current, stmt, true)
		case *ast.ContinueStmt:
			current = b.buildLoopTransfer(current, stmt, false)
		default:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			if kind, ok := flowStmtTerminator(stmt); ok {
				for _, exit := range current {
					b.setTerminator(exit, CFGTerminator{Kind: kind, Position: stmt.Pos()})
				}
				b.cfg.ExitBlocks = append(b.cfg.ExitBlocks, current...)
				current = nil
			} else if !flowStmtIsExplicitlyLinear(stmt) {
				for _, exit := range current {
					b.setTerminator(exit, CFGTerminator{Kind: CFGTerminatorUnsupported, Position: stmtPosition(stmt), Reason: fmt.Sprintf("%T", stmt)})
				}
				b.cfg.ExitBlocks = append(b.cfg.ExitBlocks, current...)
				current = nil
			}
		}
	}
	return current
}

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
