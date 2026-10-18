package parser

import (
	"elisacore/src/ast"
	"sort"
)

type syntaxSuffixReplay struct {
	previous *SyntaxTrace
	first    int
	copy     func(int) (*ast.File, error)
	accepted func(int)
}

// SetSyntaxSuffixReplay accepts a certificate that tokens from matchingStart to
// EOF match the old immutable lexical stream, including exact source positions.
// Token counts must match. Inspected backward reads and parser-state changes
// reject replay, as do incomplete/recovery traces. copySuffix returns private
// pristine declarations after the supplied old declaration count. onAccepted,
// when non-nil, runs after a copied suffix passes validation and is appended.
func (p *Parser) SetSyntaxSuffixReplay(previous *SyntaxTrace, matchingStart int, copySuffix func(int) (*ast.File, error), onAccepted func(int)) {
	if previous == nil || !previous.complete || previous.invalid || previous.tokenCount != len(p.tokens) || len(previous.checkpoints) == 0 || matchingStart < 0 || matchingStart >= len(p.tokens) || copySuffix == nil {
		return
	}
	last := previous.checkpoints[len(previous.checkpoints)-1]
	if last.pos != len(p.tokens)-1 {
		return
	}
	// Eligible boundaries form a suffix: moving backward can only lower the
	// inspected-token floor. Keep one index instead of a per-boundary map.
	first := -1
	readFloor := len(p.tokens)
	for i := len(previous.checkpoints) - 2; i >= 0; i-- {
		next := previous.checkpoints[i+1]
		readFloor = min(readFloor, next.readFrom)
		cp := previous.checkpoints[i]
		if cp.pos < matchingStart || readFloor < matchingStart {
			break
		}
		if cp.decls < last.decls {
			first = i
		}
	}
	if first < 0 {
		return
	}
	p.suffixReplay = &syntaxSuffixReplay{previous: previous, first: first, copy: copySuffix, accepted: onAccepted}
	if p.trace == nil {
		p.trace = &SyntaxTrace{tokenCount: len(p.tokens)}
		p.observeToken(p.pos)
	}
}

func (p *Parser) trySyntaxSuffix(file *ast.File) bool {
	replay := p.suffixReplay
	if replay == nil || p.trace == nil || p.trace.invalid || len(p.errors) != 0 || len(p.notices) != 0 || !p.syntaxBoundary() {
		return false
	}
	points := replay.previous.checkpoints
	index := replay.first + sort.Search(len(points)-replay.first, func(i int) bool { return points[i+replay.first].pos >= p.pos })
	if index >= len(points)-1 {
		return false
	}
	cp := points[index]
	if cp.pos != p.pos || p.rebindCounter != cp.rebindCounter || p.nurseryCounter != cp.nurseryCounter || p.byParSeq != cp.byParSeq || p.foreignEnumErrorSetEmitted != cp.foreignEnumErrorSetEmitted || p.currentVisibility != cp.visibility {
		return false
	}
	last := replay.previous.checkpoints[len(replay.previous.checkpoints)-1]
	if cp.decls >= last.decls || len(p.trace.checkpoints) == 0 || p.trace.checkpoints[len(p.trace.checkpoints)-1].pos != p.pos || len(p.trace.checkpoints)+len(points)-cp.index-1 > maxSyntaxCheckpoints {
		return false
	}
	suffix, err := replay.copy(cp.decls)
	if err != nil || suffix == nil || suffix.Filename != file.Filename || len(suffix.Decls) != last.decls-cp.decls {
		return false
	}
	declarationDelta := len(file.Decls) - cp.decls
	file.Decls = append(file.Decls, suffix.Decls...)
	if len(suffix.DeclVisibility) != 0 {
		if file.DeclVisibility == nil {
			file.DeclVisibility = map[ast.Decl]string{}
		}
		for decl, visibility := range suffix.DeclVisibility {
			file.DeclVisibility[decl] = visibility
		}
	}
	p.declVisibility = file.DeclVisibility
	for _, old := range replay.previous.checkpoints[cp.index+1:] {
		old.index = len(p.trace.checkpoints)
		old.decls += declarationDelta
		old.readThrough = max(old.readThrough, p.readThrough)
		old.readLength = old.readLength || p.readLength
		p.trace.checkpoints = append(p.trace.checkpoints, old)
	}
	p.pos = last.pos
	p.rebindCounter = last.rebindCounter
	p.nurseryCounter = last.nurseryCounter
	p.byParSeq = last.byParSeq
	p.foreignEnumErrorSetEmitted = last.foreignEnumErrorSetEmitted
	p.currentVisibility = last.visibility
	p.readThrough = max(p.readThrough, last.readThrough)
	p.readLength = p.readLength || last.readLength
	p.readFrom = last.pos
	if replay.accepted != nil {
		replay.accepted(len(suffix.Decls))
	}
	return true
}
