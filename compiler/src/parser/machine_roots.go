package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/machineroots"
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
	machineroots.CollectMachineCallArgRoots(stmt, overRoots, out)
}
func assignRootIdent(expr ast.Expr) string { return machineroots.AssignRootIdent(expr) }
func collectExprRootIdents(expr ast.Expr, out map[string]bool) {
	machineroots.CollectExprRootIdents(expr, out)
}
