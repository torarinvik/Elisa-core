package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"maps"
	"slices"
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
	blockCapacity := estimateCFGBlockCapacity(fn.Body)
	cfg.Entry = 0
	cfg.Blocks = make([]CFGBlock, 1, blockCapacity)
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

// Reserve branch blocks before construction to avoid repeatedly copying the
// growing block slice. Bound both the estimate and the traversal work.
func estimateCFGBlockCapacity(body []ast.Stmt) int {
	capacity, budget := 1, 256
	var scan func([]ast.Stmt)
	scan = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if budget == 0 || capacity >= 128 {
				return
			}
			budget--
			switch branch := stmt.(type) {
			case *ast.IfStmt:
				if branch == nil {
					continue
				}
				capacity += 3 + 2*len(branch.Elifs)
				scan(branch.Then)
				for _, elif := range branch.Elifs {
					scan(elif.Body)
				}
				scan(branch.Else)
			case *ast.StaticIfStmt:
				if branch == nil {
					continue
				}
				capacity += 3 + 2*len(branch.Elifs)
				scan(branch.Then)
				for _, elif := range branch.Elifs {
					scan(elif.Body)
				}
				scan(branch.Else)
			case *ast.MatchStmt:
				if branch != nil {
					capacity += len(branch.Arms) + 1
					for _, arm := range branch.Arms {
						scan(arm.Body)
					}
				}
			case *ast.WhileStmt:
				if branch != nil {
					capacity += 3
					scan(branch.Body)
				}
			case *ast.ForStmt:
				if branch != nil {
					capacity += 2
					scan(branch.Body)
				}
			case *ast.IterForStmt:
				if branch != nil {
					capacity += 2
					if branch.WhereFilter != nil || branch.Filter != nil {
						capacity += 2
					}
					scan(branch.Body)
				}
			case *ast.ParallelForStmt:
				if branch != nil {
					capacity += 2
					scan(branch.Body)
				}
			case *ast.InStoreStmt:
				if branch != nil {
					scan(branch.Body)
				}
			case *ast.CanStmt:
				if branch != nil {
					scan(branch.Body)
				}
			case *ast.ScopeStmt:
				if branch != nil {
					scan(branch.Body)
				}
			case *ast.RegionStmt:
				if branch != nil {
					scan(branch.Body)
				}
			}
		}
	}
	scan(body)
	return min(capacity, 128)
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
	for _, id := range slices.Sorted(maps.Keys(exits)) { // map: sort so the reported block is stable
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
