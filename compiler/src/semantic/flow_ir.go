package semantic

import (
	"strconv"

	"llcontext/src/ast"
)

type FlowInstrKind string

const (
	FlowInstrAlias   FlowInstrKind = "alias"
	FlowInstrConsume FlowInstrKind = "consume"
	FlowInstrMutate  FlowInstrKind = "mutate"
	FlowInstrReturn  FlowInstrKind = "return"
)

type FlowInstr struct {
	Kind     FlowInstrKind
	Location string
	Source   string
	Note     string
}

type FlowEdge struct {
	To    int
	Guard GuardFactSet
}

type CFGBlock struct {
	ID     int
	Nodes  []ast.Node
	Instrs []FlowInstr
	Edges  []FlowEdge
}

type CFG struct {
	Entry          int
	Blocks         []CFGBlock
	ExitBlocks     []int
	ParamLocations []string
}

type cfgBuilder struct {
	cfg *CFG
}

func ConstructCFG(fn *ast.FuncDecl) *CFG {
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
	builder := &cfgBuilder{cfg: cfg}
	exits := builder.buildStmtList([]int{cfg.Entry}, fn.Body)
	cfg.ExitBlocks = dedupeCFGBlockIDs(append(cfg.ExitBlocks, exits...))
	return cfg
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
		case *ast.ForStmt:
			current = b.buildLoopBody(current, n.Body)
		case *ast.ParallelForStmt:
			current = b.buildLoopBody(current, n.Body)
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
		case *ast.OpenStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.ViewStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		case *ast.DeferStmt:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			current = b.buildStmtList(current, n.Body)
		default:
			for _, exit := range current {
				b.appendNode(exit, stmt)
			}
			if flowStmtTerminates(stmt) {
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
		out = append(out, b.buildConditional(exit, stmt.Cond, stmt.Then, stmt.Elifs, stmt.Else)...)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) buildConditional(from int, cond ast.Expr, thenBody []ast.Stmt, elifs []ast.ElifClause, elseBody []ast.Stmt) []int {
	thenEntry := b.newBlock()
	elseEntry := b.newBlock()
	b.addEdge(from, thenEntry, GuardFactsForCondition(cond, true))
	b.addEdge(from, elseEntry, GuardFactsForCondition(cond, false))
	thenExits := b.buildStmtList([]int{thenEntry}, thenBody)
	var elseExits []int
	if len(elifs) != 0 {
		elseExits = b.buildConditional(elseEntry, elifs[0].Cond, elifs[0].Body, elifs[1:], elseBody)
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
		bodyEntry := b.newBlock()
		afterBlock := b.newBlock()
		b.addEdge(condBlock, bodyEntry, GuardFactsForCondition(stmt.Cond, true))
		b.addEdge(condBlock, afterBlock, GuardFactsForCondition(stmt.Cond, false))
		bodyExits := b.buildStmtList([]int{bodyEntry}, stmt.Body)
		for _, bodyExit := range bodyExits {
			b.addEdge(bodyExit, condBlock, GuardFactSet{})
		}
		out = append(out, afterBlock)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) buildLoopBody(exits []int, body []ast.Stmt) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		loopEntry := b.newBlock()
		afterBlock := b.newBlock()
		b.addEdge(exit, loopEntry, GuardFactSet{})
		b.addEdge(exit, afterBlock, GuardFactSet{})
		bodyExits := b.buildStmtList([]int{loopEntry}, body)
		for _, bodyExit := range bodyExits {
			b.addEdge(bodyExit, loopEntry, GuardFactSet{})
		}
		out = append(out, afterBlock)
	}
	return dedupeCFGBlockIDs(out)
}

func (b *cfgBuilder) buildMatch(exits []int, stmt *ast.MatchStmt) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
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

func flowStmtTerminates(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.PanicStmt, *ast.StaticErrorStmt:
		return true
	case *ast.ExprStmt:
		_, ok := n.Expr.(*ast.RaiseExpr)
		return ok
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
