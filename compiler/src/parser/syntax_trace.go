package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"unsafe"
)

const maxSyntaxCheckpoints = 4096

// SyntaxTrace is immutable after ParseFile returns. Its checkpoints describe
// cumulative inspected tokens, including speculative probes, and outgoing state.
// It contains no AST or token pointers. Only diagnostic-free traces can replay.
type SyntaxTrace struct {
	checkpoints       []PrefixCheckpoint
	tokenCount        int
	complete, invalid bool
}
type PrefixCheckpoint struct {
	readFrom                                int
	index, pos, decls, readThrough          int
	readLength                              bool
	rebindCounter, nurseryCounter, byParSeq int
	foreignEnumErrorSetEmitted              bool
	visibility                              string
}

func NewWithSyntaxTrace(tokens []lexer.Token) *Parser {
	p := New(tokens)
	p.trace = &SyntaxTrace{tokenCount: len(tokens)}
	p.observeToken(p.pos)
	return p
}
func (p *Parser) SyntaxTrace() *SyntaxTrace {
	if p.trace == nil || !p.trace.complete || p.trace.invalid {
		return nil
	}
	trace := *p.trace
	return &trace
}
func (t *SyntaxTrace) RetainedBytes() int {
	if t == nil {
		return 0
	}
	size := int(unsafe.Sizeof(*t)) + cap(t.checkpoints)*int(unsafe.Sizeof(PrefixCheckpoint{}))
	for _, cp := range t.checkpoints {
		size += len(cp.visibility)
	}
	return size
}
func (cp PrefixCheckpoint) Declarations() int { return cp.decls }
func (t *SyntaxTrace) ReusablePrefix(matchingTokens, tokenCount int) (PrefixCheckpoint, bool) {
	if t == nil || !t.complete || t.invalid {
		return PrefixCheckpoint{}, false
	}
	var best PrefixCheckpoint
	found := false
	for _, cp := range t.checkpoints {
		if cp.readThrough > matchingTokens || cp.readLength && tokenCount != t.tokenCount {
			break
		}
		best = cp
		found = true
	}
	return best, found
}
func (p *Parser) syntaxBoundary() bool {
	return p.exprBlockDepth == 0 && p.nestDepth == 0 && len(p.poolScopes) == 0 && len(p.nurseryGroupByPool) == 0 && p.allowInMembership && p.allowTernary && p.allowWhereExpr && !p.allowQuantifiers && !p.disallowIsComparison && p.staticFunctionDepth == 0 && len(p.pendingStmts) == 0 && len(p.pendingDecls) == 0 && len(p.pendingStateDecls) == 0 && !p.stmtGuardArmed && p.stmtGuardCond == nil && !p.stmtGuardBlockEnd && len(p.loopBreakTargets) == 0
}
func (p *Parser) recordSyntaxBoundary(file *ast.File) {
	if p.trace == nil || p.trace.invalid || len(p.errors) > 0 || len(p.notices) > 0 || !p.syntaxBoundary() || len(p.trace.checkpoints) >= maxSyntaxCheckpoints {
		return
	}
	points := p.trace.checkpoints
	if len(points) > 0 && points[len(points)-1].pos >= p.pos {
		return
	}
	if len(points) == cap(points) {
		capacity := min(maxSyntaxCheckpoints, max(16, 2*cap(points)))
		grown := make([]PrefixCheckpoint, len(points), capacity)
		copy(grown, points)
		points = grown
	}
	p.trace.checkpoints = append(points, PrefixCheckpoint{index: len(points), pos: p.pos, decls: len(file.Decls), readThrough: p.readThrough, readFrom: p.readFrom, readLength: p.readLength, rebindCounter: p.rebindCounter, nurseryCounter: p.nurseryCounter, byParSeq: p.byParSeq, foreignEnumErrorSetEmitted: p.foreignEnumErrorSetEmitted, visibility: strings.Clone(p.currentVisibility)})
	p.readFrom = p.pos
}

// ParseFileFromPrefix requires a private pristine prefix and a checkpoint chosen
// by ReusablePrefix after comparing exact token values/positions against the
// previous revision. The caller owns that immutable token comparison certificate.
func (p *Parser) ParseFileFromPrefix(filename string, prefix *ast.File, previous *SyntaxTrace, cp PrefixCheckpoint) *ast.File {
	if previous == nil || !previous.complete || previous.invalid || cp.index < 0 || cp.index >= len(previous.checkpoints) || previous.checkpoints[cp.index] != cp || prefix == nil || prefix.Filename != filename || len(prefix.Decls) != cp.decls || cp.pos < 0 || cp.pos > len(p.tokens) {
		return p.ParseFile(filename)
	}
	p.pos = cp.pos
	p.rebindCounter = cp.rebindCounter
	p.nurseryCounter = cp.nurseryCounter
	p.byParSeq = cp.byParSeq
	p.foreignEnumErrorSetEmitted = cp.foreignEnumErrorSetEmitted
	p.currentVisibility = cp.visibility
	p.declVisibility = prefix.DeclVisibility
	if p.declVisibility == nil {
		p.declVisibility = map[ast.Decl]string{}
		prefix.DeclVisibility = p.declVisibility
	}
	p.readFrom = cp.pos
	p.readThrough = cp.readThrough
	p.readLength = cp.readLength
	if p.trace != nil {
		p.trace.checkpoints = append([]PrefixCheckpoint(nil), previous.checkpoints[:cp.index+1]...)
	}
	return p.parseFile(filename, prefix)
}
