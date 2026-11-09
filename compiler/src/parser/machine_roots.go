package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
)

// buildMachineArmChain folds a state's lowered arms into a single if/elif/else ladder —
// or the bare body when the state has only its irrefutable arm. A state with no irrefutable
// (unguarded-wildcard) arm — legal only for a closed-enum input proven exhaustive by the
// coverage check (docs/125 §9) — gets a defensive `else: break`: dead code under a proven
// total dispatch, but a guarantee the machine can never spin on an unhandled input even if
// a hole ever slipped past the checker.
func buildMachineArmChain(pos lexer.Pos, arms []loweredMachineArm) []ast.Stmt {
	// A state with no arms was already refused ("machine state %q has no arms"); keep the
	// desugar total so the diagnostic is reported instead of an index-out-of-range panic.
	if len(arms) == 0 {
		return []ast.Stmt{&ast.BreakStmt{Position: pos}}
	}
	if len(arms) == 1 && arms[0].cond == nil {
		return arms[0].body
	}
	ifStmt := &ast.IfStmt{Position: arms[0].pos, Cond: arms[0].cond, Then: arms[0].body}
	hasElse := false
	for _, a := range arms[1:] {
		if a.cond == nil {
			ifStmt.Else = a.body
			hasElse = true
			break
		}
		ifStmt.Elifs = append(ifStmt.Elifs, ast.ElifClause{Position: a.pos, Cond: a.cond, Body: a.body})
	}
	if !hasElse {
		ifStmt.Else = []ast.Stmt{&ast.BreakStmt{Position: pos}}
	}
	return []ast.Stmt{ifStmt}
}

// contentNameSuffix builds a program-unique suffix for names synthesized from
// tokens[from:to]: a hash of the file and of the tokens' kinds and spellings, plus
// an occurrence number among identical token runs in that file. Unlike a byte
// offset it does not change when unrelated text before the construct is edited,
// so the retained analysis can splice the enclosing body instead of re-analyzing.
func (p *Parser) contentNameSuffix(pos lexer.Pos, from, to int) string {
	h := fnv.New64a()
	h.Write([]byte(pos.File))
	h.Write([]byte{0})
	if from < 0 {
		from = 0
	}
	if count := p.tokenCount(); to > count {
		to = count
	}
	var kind [8]byte
	for _, tok := range p.tokenSpan(from, to) {
		binary.LittleEndian.PutUint64(kind[:], uint64(tok.Kind))
		h.Write(kind[:])
		h.Write([]byte(tok.Text))
		h.Write([]byte{0})
		h.Write([]byte(tok.SuffixString()))
		h.Write([]byte{0})
	}
	key := fmt.Sprintf("%016x", h.Sum64())
	n := strings.Count(p.contentNames, key+";")
	p.contentNames += key + ";"
	return fmt.Sprintf("%s_n%d", key, n)
}

// collectMachineCallArgRoots adds every driven-resource root that appears inside a call
// in the statement (as receiver or argument) to the mutated set — call-based mutation is
// invisible without types, so any driven root touching a call is conservatively licensed.
func collectMachineCallArgRoots(stmt ast.Stmt, overRoots, out map[string]bool) {
	var visit func(ast.Expr)
	var visitStatements func([]ast.Stmt)
	visitStatements = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.ExprStmt:
				visit(s.Expr)
			case *ast.AssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.AugAssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.AsRefAssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.VarDeclStmt:
				visit(s.Value)
			case *ast.LetDestructureStmt:
				visit(s.Value)
			case *ast.TupleBindStmt:
				visit(s.Value)
			case *ast.MoveBindStmt:
				visit(s.Value)
				visit(s.Store)
			case *ast.DeferStmt:
				visitStatements(s.Body)
			case *ast.ReturnStmt:
				visit(s.Value)
			case *ast.IfStmt:
				visit(s.Cond)
				visitStatements(s.Then)
				for _, clause := range s.Elifs {
					visit(clause.Cond)
					visitStatements(clause.Body)
				}
				visitStatements(s.Else)
			case *ast.WhileStmt:
				visit(s.Cond)
				visitStatements(s.Body)
			case *ast.ForStmt:
				visit(s.Start)
				visit(s.End)
				visit(s.Step)
				visitStatements(s.Body)
			case *ast.IterForStmt:
				visit(s.Source)
				visit(s.WhereFilter)
				visit(s.Filter)
				visitStatements(s.Body)
			case *ast.ParallelForStmt:
				visit(s.Source)
				visitStatements(s.Body)
			case *ast.MatchStmt:
				visit(s.Value)
				visit(s.Store)
				for _, arm := range s.Arms {
					visit(arm.Guard)
					visitStatements(arm.Body)
				}
			case *ast.ExpectPatternStmt:
				visit(s.Value)
			case *ast.InStoreStmt:
				visit(s.Store)
				visitStatements(s.Body)
			case *ast.CanStmt:
				for _, arg := range s.HandlerArgs {
					visit(arg)
				}
				visitStatements(s.Body)
			case *ast.ScopeStmt:
				visit(s.Guard)
				visitStatements(s.Body)
			case *ast.PoolStmt:
				visit(s.Workers)
				visitStatements(s.Body)
			case *ast.LockStmt:
				visit(s.Mutex)
				visitStatements(s.Body)
			case *ast.PanicStmt:
				visit(s.Message)
			case *ast.StaticIfStmt:
				visit(s.Cond)
				visitStatements(s.Then)
				for _, clause := range s.Elifs {
					visit(clause.Cond)
					visitStatements(clause.Body)
				}
				visitStatements(s.Else)
			case *ast.StaticErrorStmt:
				visit(s.Message)
			case *ast.StaticAssertStmt:
				visit(s.Cond)
				visit(s.Message)
			case *ast.AssertByStmt:
				visit(s.Cond)
				visitStatements(s.Proof)
			case *ast.ProofBlockStmt:
				visit(s.Goal)
				visitStatements(s.Proof)
			case *ast.ProofUseStmt:
				for _, citation := range s.Citations {
					visit(citation)
				}
			case *ast.ContractStmt:
				visit(s.Cond)
				visitStatements(s.Proof)
				for _, arg := range s.UsesArgs {
					visit(arg)
				}
			case *ast.StaticBlockStmt:
				visitStatements(s.Body)
			case *ast.DiscardStmt:
				visit(s.Value)
			case *ast.RegionStmt:
				visit(s.Capacity)
				visitStatements(s.Body)
			case *ast.PromoteStmt:
				visit(s.Value)
			case *ast.CheckpointStmt:
				visit(s.Target)
				visitStatements(s.Body)
			case *ast.GroupedCheckpointStmt:
				for _, target := range s.Targets {
					visit(target)
				}
				visitStatements(s.Body)
			}
		}
	}
	visit = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.CallExpr:
			roots := map[string]bool{}
			collectExprRootIdents(n, roots)
			for r := range roots {
				if overRoots[r] {
					out[r] = true
				}
			}
		case *ast.BinaryExpr:
			visit(n.Left)
			visit(n.Right)
		case *ast.UnaryExpr:
			visit(n.Operand)
		case *ast.ParenExpr:
			visit(n.Inner)
		case *ast.FieldExpr:
			visit(n.Object)
		case *ast.IndexExpr:
			visit(n.Object)
			visit(n.Index)
			visit(n.Index2)
			visit(n.Fallback)
		case *ast.SliceExpr:
			visit(n.Object)
			visit(n.Start)
			visit(n.End)
		case *ast.CastExpr:
			visit(n.Operand)
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
			for _, key := range n.Keys {
				visit(key)
			}
			visit(n.Owner)
		case *ast.MembershipRangeExpr:
			visit(n.Start)
			visit(n.End)
		case *ast.ListComprehensionExpr:
			visit(n.Value)
			visit(n.Source)
			visit(n.RangeEnd)
			visit(n.RangeStep)
			visit(n.Filter)
			visit(n.Owner)
			visit(n.Key)
			visit(n.LoweredParallel)
			visitStatements(n.Bindings)
		case *ast.QueryExpr:
			visit(n.Source)
			visit(n.Filter)
			visit(n.Projection)
			visit(n.Owner)
		case *ast.TernaryExpr:
			visit(n.Value)
			visit(n.Cond)
			visit(n.Alt)
		case *ast.AddrOfExpr:
			visit(n.Operand)
		case *ast.SpecializeExpr:
			visit(n.Operand)
		case *ast.StructLitExpr:
			for _, arg := range n.Args {
				visit(arg)
			}
			for _, spread := range n.Spreads {
				visit(spread)
			}
		case *ast.RecordUpdateExpr:
			visit(n.Base)
			for _, arg := range n.Args {
				visit(arg)
			}
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
		case *ast.OptionalBindExpr:
			visit(n.Value)
		case *ast.AllocExpr:
			visit(n.Owner)
			visit(n.Value)
		case *ast.CanExpr:
			visit(n.Expr)
		case *ast.RaiseExpr:
			visit(n.Error)
		case *ast.IsPatternExpr:
			for _, target := range n.Targets {
				visit(target)
			}
		case *ast.IsAliasExpr:
			visit(n.Target)
		case *ast.ExprBlock:
			visitStatements(n.Stmts)
			visit(n.Value)
		case *ast.LambdaExpr:
			visitStatements(n.Body)
			visit(n.BodyExpr)
		case *ast.FoldExpr:
			visit(n.Value)
			for _, arm := range n.Arms {
				visit(arm.Guard)
				visitStatements(arm.Body)
			}
		case *ast.EmitExpr:
			visit(n.Value)
		case *ast.TryExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.GetExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.UnwrapElseExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.CatchExpr:
			visit(n.Value)
			visitStatements(n.Success.Body)
			for _, arm := range n.Arms {
				visitStatements(arm.Body)
			}
		case *ast.MatchExpr:
			visit(n.Value)
			visit(n.Store)
			for _, arm := range n.Arms {
				visit(arm.Guard)
				visitStatements(arm.Body)
			}
		}
	}
	// Enter through the same complete statement dispatcher used for nested bodies. The
	// machine-arm validator now accepts compound and as-ref assignments, and routing only
	// plain assignments here would miss calls in their targets or values when the statement
	// is at arm level.
	visitStatements([]ast.Stmt{stmt})
}

// assignRootIdent walks an lvalue to its root identifier (`lexer.pos` → "lexer").
func assignRootIdent(target ast.Expr) string {
	for {
		switch t := target.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.FieldExpr:
			target = t.Object
		case *ast.IndexExpr:
			target = t.Object
		case *ast.ParenExpr:
			target = t.Inner
		default:
			return ""
		}
	}
}

// collectExprRootIdents gathers every identifier that roots a value chain in an
// expression (`lexer.current_char()` → lexer). Used for the driven-resource whitelist.
func collectExprRootIdents(expr ast.Expr, out map[string]bool) {
	switch e := expr.(type) {
	case nil:
	case *ast.Ident:
		out[e.Name] = true
	case *ast.FieldExpr:
		collectExprRootIdents(e.Object, out)
	case *ast.CallExpr:
		// A plain-ident callee is a FUNCTION name (`at_end(scanner)`), not a value root;
		// a FieldExpr callee's object chain is a method receiver and does root a value.
		if _, plainFn := e.Func.(*ast.Ident); !plainFn {
			collectExprRootIdents(e.Func, out)
		}
		for _, a := range e.Args {
			collectExprRootIdents(a, out)
		}
	case *ast.IndexExpr:
		collectExprRootIdents(e.Object, out)
		collectExprRootIdents(e.Index, out)
		collectExprRootIdents(e.Index2, out)
		collectExprRootIdents(e.Fallback, out)
	case *ast.CastExpr:
		// A postfix value cast/ctor (`box.data[i].char()`) is a CastExpr, not a method
		// call — the driven resource is rooted in its operand (`box`), so descend.
		collectExprRootIdents(e.Operand, out)
	case *ast.BinaryExpr:
		collectExprRootIdents(e.Left, out)
		collectExprRootIdents(e.Right, out)
	case *ast.UnaryExpr:
		collectExprRootIdents(e.Operand, out)
	case *ast.ParenExpr:
		collectExprRootIdents(e.Inner, out)
	case *ast.SliceExpr:
		collectExprRootIdents(e.Object, out)
		collectExprRootIdents(e.Start, out)
		collectExprRootIdents(e.End, out)
	case *ast.ListLitExpr:
		for _, elem := range e.Elems {
			collectExprRootIdents(elem, out)
		}
		for _, key := range e.Keys {
			collectExprRootIdents(key, out)
		}
		collectExprRootIdents(e.Owner, out)
	case *ast.MembershipRangeExpr:
		collectExprRootIdents(e.Start, out)
		collectExprRootIdents(e.End, out)
	case *ast.ListComprehensionExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Source, out)
		collectExprRootIdents(e.RangeEnd, out)
		collectExprRootIdents(e.RangeStep, out)
		collectExprRootIdents(e.Filter, out)
		collectExprRootIdents(e.Owner, out)
		collectExprRootIdents(e.Key, out)
		collectExprRootIdents(e.LoweredParallel, out)
		for _, binding := range e.Bindings {
			collectStmtRootIdents(binding, out)
		}
	case *ast.QueryExpr:
		collectExprRootIdents(e.Source, out)
		collectExprRootIdents(e.Filter, out)
		collectExprRootIdents(e.Projection, out)
		collectExprRootIdents(e.Owner, out)
	case *ast.TernaryExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Cond, out)
		collectExprRootIdents(e.Alt, out)
	case *ast.AddrOfExpr:
		collectExprRootIdents(e.Operand, out)
	case *ast.SpecializeExpr:
		collectExprRootIdents(e.Operand, out)
	case *ast.StructLitExpr:
		for _, arg := range e.Args {
			collectExprRootIdents(arg, out)
		}
		for _, spread := range e.Spreads {
			collectExprRootIdents(spread, out)
		}
	case *ast.RecordUpdateExpr:
		collectExprRootIdents(e.Base, out)
		for _, arg := range e.Args {
			collectExprRootIdents(arg, out)
		}
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			collectExprRootIdents(elem, out)
		}
	case *ast.OptionalBindExpr:
		collectExprRootIdents(e.Value, out)
	case *ast.AllocExpr:
		collectExprRootIdents(e.Owner, out)
		collectExprRootIdents(e.Value, out)
	case *ast.CanExpr:
		collectExprRootIdents(e.Expr, out)
	case *ast.RaiseExpr:
		collectExprRootIdents(e.Error, out)
	case *ast.IsPatternExpr:
		for _, target := range e.Targets {
			collectExprRootIdents(target, out)
		}
	case *ast.IsAliasExpr:
		collectExprRootIdents(e.Target, out)
	case *ast.ExprBlock:
		for _, stmt := range e.Stmts {
			collectStmtRootIdents(stmt, out)
		}
		collectExprRootIdents(e.Value, out)
	case *ast.LambdaExpr:
		for _, stmt := range e.Body {
			collectStmtRootIdents(stmt, out)
		}
		collectExprRootIdents(e.BodyExpr, out)
	case *ast.FoldExpr:
		collectExprRootIdents(e.Value, out)
		for _, arm := range e.Arms {
			collectExprRootIdents(arm.Guard, out)
			for _, stmt := range arm.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.EmitExpr:
		collectExprRootIdents(e.Value, out)
	case *ast.TryExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			collectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.GetExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			collectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.UnwrapElseExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			collectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.CatchExpr:
		collectExprRootIdents(e.Value, out)
		for _, stmt := range e.Success.Body {
			collectStmtRootIdents(stmt, out)
		}
		for _, arm := range e.Arms {
			for _, stmt := range arm.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.MatchExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Store, out)
		for _, arm := range e.Arms {
			collectExprRootIdents(arm.Guard, out)
			for _, stmt := range arm.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	}
}
