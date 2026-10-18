package lexer

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"unsafe"
)

func (s *SourceTokens) noteLineEpoch(token, epoch int) {
	last := 0
	if len(s.lineEpochs) != 0 {
		last = s.lineEpochs[len(s.lineEpochs)-1].epoch
	}
	if last != epoch {
		s.lineEpochs = append(s.lineEpochs, sourceLineEpoch{token, epoch})
	}
}
func (s *SourceTokens) epochAt(token int) int {
	index := sort.Search(len(s.lineEpochs), func(i int) bool { return s.lineEpochs[i].token > token })
	if index == 0 {
		return 0
	}
	return s.lineEpochs[index-1].epoch
}

type sourceConvergence struct {
	old                         *SourceTokens
	delta, candidate, tailStart int
}

func newSourceConvergence(old, current *SourceTokens) *sourceConvergence {
	if old == nil || old.filename != current.filename || len(old.errors) != 0 || len(old.checkpoints) == 0 {
		return nil
	}
	common := 0
	for common < len(old.source) && common < len(current.source) && old.source[len(old.source)-1-common] == current.source[len(current.source)-1-common] {
		common++
	}
	if common == 0 {
		return nil
	}
	start := len(old.source) - common
	index := sort.Search(len(old.checkpoints), func(i int) bool { return old.checkpoints[i].state.pos >= start })
	if index == len(old.checkpoints) {
		return nil
	}
	return &sourceConvergence{old: old, delta: len(current.source) - len(old.source), candidate: index, tailStart: start}
}

// Pending layout/pattern work can retain physical source positions before the
// boundary. Converge only after it has drained, with equal indentation and all
// remaining lexical modes. Line counters may differ; columns must already agree.
func compatibleConvergence(old, current *Lexer) bool {
	return old.trackReads && current.trackReads && old.filename == current.filename && old.col == current.col && old.atLineStart == current.atLineStart && old.lastTokenKind == current.lastTokenKind && old.parenDepth == 0 && current.parenDepth == 0 && !old.pendingGroupedBlock && !current.pendingGroupedBlock && len(old.pendingToks) == 0 && len(current.pendingToks) == 0 && len(old.pendingPatternBlocks) == 0 && len(current.pendingPatternBlocks) == 0 && len(old.groupedBlockContexts) == 0 && len(current.groupedBlockContexts) == 0 && len(old.errors) == 0 && len(current.errors) == 0 && slices.Equal(old.indentStack, current.indentStack)
}

func (c *sourceConvergence) replay(out *SourceTokens, current *Lexer) bool {
	for c.candidate < len(c.old.checkpoints) && c.old.checkpoints[c.candidate].state.pos+c.delta < current.pos {
		c.candidate++
	}
	for index := c.candidate; index < len(c.old.checkpoints); index++ {
		cp := c.old.checkpoints[index]
		if cp.state.pos+c.delta != current.pos {
			break
		}
		if len(c.old.tokens)-cp.count < sourceCheckpointStride || !compatibleConvergence(&cp.state, current) {
			continue
		}
		// Later grouped-layout handling can inspect the current physical line's
		// leading indentation. Certify that line too, including suppressed layout
		// after boolean continuations, rather than relying on columns alone.
		oldLine := bytes.LastIndexByte(c.old.source[:cp.state.pos], '\n') + 1
		newLine := bytes.LastIndexByte(out.source[:current.pos], '\n') + 1
		if oldLine < c.tailStart || newLine != oldLine+c.delta {
			continue
		}
		return c.copySuffix(out, current, cp)
	}
	return false
}

type sourceTailCopier struct {
	old, new                                                  []byte
	offsetDelta, lineDelta, epochDelta, baseEpoch, baseOffset int
	filenames                                                 map[string]string
}

func (c *sourceTailCopier) text(value string) (string, bool) {
	if len(value) == 0 || len(c.old) == 0 {
		return value, true
	}
	start := uintptr(unsafe.Pointer(unsafe.SliceData(c.old)))
	address := uintptr(unsafe.Pointer(unsafe.StringData(value)))
	if address < start || address-start >= uintptr(len(c.old)) {
		return value, true
	}
	offset := int(address - start)
	shifted := offset + c.offsetDelta
	if offset < c.baseOffset || shifted < 0 || len(value) > len(c.old)-offset || len(value) > len(c.new)-shifted || shifted > len(c.new) {
		return "", false
	}
	return bytesToStringView(c.new[shifted : shifted+len(value)]), true
}
func (c *sourceTailCopier) filename(value string) string {
	if owned, ok := c.filenames[value]; ok {
		return owned
	}
	owned := strings.Clone(value)
	c.filenames[value] = owned
	return owned
}
func (c *sourceTailCopier) token(token Token, epoch int) (Token, bool) {
	var ok bool
	token.Text, ok = c.text(token.Text)
	if !ok {
		return Token{}, false
	}
	token.Suffix, ok = c.text(token.Suffix)
	if !ok {
		return Token{}, false
	}
	if !token.Pos.IsZero() {
		token.Pos.File = c.filename(token.Pos.File)
		token.Pos.Offset += c.offsetDelta
		if epoch == c.baseEpoch {
			token.Pos.Line += c.lineDelta
		}
		if token.Pos.EndLine != 0 || token.Pos.EndCol != 0 || token.Pos.EndOffset != 0 {
			token.Pos.EndOffset += c.offsetDelta
			if epoch == c.baseEpoch {
				token.Pos.EndLine += c.lineDelta
			}
		}
	}
	return token, true
}
func (c *sourceTailCopier) state(old Lexer, required int) (Lexer, bool) {
	result := old
	result.src = c.new
	result.filename = c.filename(old.filename)
	result.pos += c.offsetDelta
	result.readLimit = max(required, result.readLimit+c.offsetDelta)
	if old.lineEpoch == c.baseEpoch {
		result.line += c.lineDelta
	}
	result.lineEpoch += c.epochDelta
	result.indentStack = copySourceSlice(old.indentStack)
	result.pendingPatternBlocks = copySourceSlice(old.pendingPatternBlocks)
	for i := range result.pendingPatternBlocks {
		if result.pendingPatternBlocks[i].HeadStart < c.baseOffset {
			return Lexer{}, false
		}
		result.pendingPatternBlocks[i].HeadStart += c.offsetDelta
	}
	result.groupedBlockContexts = copySourceSlice(old.groupedBlockContexts)
	result.pendingToks = copySourceSlice(old.pendingToks)
	for i := range result.pendingToks {
		token, ok := c.token(result.pendingToks[i], old.lineEpoch)
		if !ok {
			return Lexer{}, false
		}
		result.pendingToks[i] = token
	}
	result.errors = copySourceSlice(old.errors)
	return result, true
}

func (c *sourceConvergence) copySuffix(out *SourceTokens, current *Lexer, cp sourceCheckpoint) bool {
	copier := sourceTailCopier{old: c.old.source, new: out.source, offsetDelta: c.delta, lineDelta: current.line - cp.state.line, epochDelta: current.lineEpoch - cp.state.lineEpoch, baseEpoch: cp.state.lineEpoch, baseOffset: cp.state.pos, filenames: map[string]string{}}
	start, epochs := len(out.tokens), len(out.lineEpochs)
	total := start + len(c.old.tokens) - cp.count
	if cap(out.tokens) < total {
		grown := make([]Token, start, total)
		copy(grown, out.tokens)
		out.tokens = grown
	}
	epochIndex := sort.Search(len(c.old.lineEpochs), func(i int) bool { return c.old.lineEpochs[i].token > cp.count })
	epoch := c.old.epochAt(cp.count)
	for index := cp.count; index < len(c.old.tokens); index++ {
		for epochIndex < len(c.old.lineEpochs) && c.old.lineEpochs[epochIndex].token <= index {
			epoch = c.old.lineEpochs[epochIndex].epoch
			epochIndex++
		}
		token, ok := copier.token(c.old.tokens[index], epoch)
		if !ok {
			out.tokens = out.tokens[:start]
			out.lineEpochs = out.lineEpochs[:epochs]
			return false
		}
		out.noteLineEpoch(len(out.tokens), epoch+copier.epochDelta)
		out.tokens = append(out.tokens, token)
	}
	// Keep the converged boundary and later rebased checkpoints, so moving the
	// next edit toward the file's end does not lose prefix reuse opportunities.
	out.addCheckpoint(*current, out.source, out.source, start, current.readLimit)
	for _, old := range c.old.checkpoints {
		if old.count <= cp.count {
			continue
		}
		state, ok := copier.state(old.state, current.readLimit)
		if !ok {
			break
		}
		if !out.addCheckpoint(state, out.source, out.source, start+old.count-cp.count, state.readLimit) {
			break
		}
	}
	out.suffixReused = len(c.old.tokens) - cp.count
	return true
}
