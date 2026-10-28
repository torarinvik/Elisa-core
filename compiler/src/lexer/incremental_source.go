package lexer

import (
	"strings"
	"unsafe"
)

const sourceCheckpointStride = 256
const sourceCheckpointLimit = 4096
const sourceCheckpointBytes = 16 << 20
const sourceTokenCapacityDivisor = 7

type sourceCheckpoint struct {
	count, required int
	state           Lexer
}

// SourceTokens owns one immutable source revision and token/checkpoint storage.
// Slice consumers get copies unless they explicitly borrow read-only tokens.
// It never retains a previous revision: prefix views and checkpoint state are
// rebound to its source.
type sourceLineEpoch struct{ token, epoch int }

type SourceTokens struct {
	filename        string
	source          []byte
	tokens          []Token
	errors          []string
	checkpoints     []sourceCheckpoint
	reused          int
	checkpointBytes int
	lineEpochs      []sourceLineEpoch
	suffixReused    int
}

func (s *SourceTokens) CopyTokens() []Token     { return append([]Token(nil), s.tokens...) }
func (s *SourceTokens) CopyErrors() []string    { return append([]string(nil), s.errors...) }
func (s *SourceTokens) ReusedTokens() int       { return s.reused }
func (s *SourceTokens) SuffixReusedTokens() int { return s.suffixReused }

// sourceTokenBuilder borrows a prefix only during construction. Materialization
// creates independent backing and rebinds its views before the result escapes.
type sourceTokenBuilder struct {
	out       *SourceTokens
	prefix    []Token
	oldSource []byte
}

func (b *sourceTokenBuilder) count() int { return len(b.prefix) + len(b.out.tokens) }
func (b *sourceTokenBuilder) materialize(capacity int) {
	count := b.count()
	capacity = max(capacity, count)
	if len(b.prefix) == 0 && cap(b.out.tokens) >= capacity {
		return
	}
	tokens := make([]Token, count, capacity)
	for i, token := range b.prefix {
		tokens[i] = rebaseToken(token, b.oldSource, b.out.source)
	}
	copy(tokens[len(b.prefix):], b.out.tokens)
	b.out.tokens = tokens
	b.prefix = nil
	b.oldSource = nil
}

// TokenizeSource resumes only a prefix whose complete observed read range is
// unchanged. A compatible lexer checkpoint inside an identical byte suffix can
// also converge; source-attribution epochs distinguish relative lines from #line resets.
func TokenizeSource(filename string, source []byte, previous *SourceTokens) *SourceTokens {
	result := &SourceTokens{filename: strings.Clone(filename), source: copySourceSlice(source)}
	l := New(result.filename, result.source)
	l.trackReads = true
	// Include-heavy compiler inputs have substantial whitespace and comments.
	// Reserve headroom without pre-zeroing a quarter of every source byte as a
	// Token; append still grows safely for denser inputs.
	capacity := max(16, len(source)/sourceTokenCapacityDivisor)
	if previous == nil || previous.filename != filename {
		return tokenizeFreshSource(result, l, capacity)
	}
	builder := sourceTokenBuilder{out: result}
	var checkpoint *sourceCheckpoint
	if previous != nil && previous.filename == filename {
		common := 0
		for common < len(previous.source) && common < len(source) && previous.source[common] == source[common] {
			common++
		}
		for i := range previous.checkpoints {
			candidate := &previous.checkpoints[i]
			if candidate.required <= common && (checkpoint == nil || candidate.count > checkpoint.count) {
				checkpoint = candidate
			}
		}
	}
	if checkpoint != nil {
		builder.prefix = previous.tokens[:checkpoint.count]
		builder.oldSource = previous.source
		for _, epoch := range previous.lineEpochs {
			if epoch.token >= checkpoint.count {
				break
			}
			result.lineEpochs = append(result.lineEpochs, epoch)
		}
		state := cloneLexerState(checkpoint.state, previous.source, result.source)
		l = &state
		result.reused = checkpoint.count
		for _, old := range previous.checkpoints {
			if old.count > checkpoint.count {
				break
			}
			if !result.addCheckpoint(old.state, previous.source, result.source, old.count, old.required) {
				break
			}
		}
	}
	var convergence *sourceConvergence
	if checkpoint == nil || len(previous.tokens)-checkpoint.count >= sourceCheckpointStride {
		convergence = newSourceConvergence(previous, result)
	}
	// Small revisions and edits without a viable suffix use direct output.
	// Deferring a long dirty tail would otherwise grow a scratch buffer and then
	// copy it again at EOF, adding work to edits that cannot converge.
	deferTokens := convergence != nil && len(previous.tokens) > 8*sourceCheckpointStride
	if checkpoint != nil && deferTokens {
		result.tokens = make([]Token, 0, 2*sourceCheckpointStride)
	}
	if checkpoint != nil && !deferTokens {
		capacity = max(capacity, checkpoint.count+(len(source)-checkpoint.state.pos)/4)
		if checkpoint.count >= len(previous.tokens)/2 {
			capacity = max(capacity, len(previous.tokens)+max(0, len(source)-len(previous.source))*2)
		}
		builder.materialize(capacity)
	}
	if checkpoint == nil {
		if deferTokens {
			capacity = min(capacity, 2*sourceCheckpointStride)
		}
		result.tokens = make([]Token, 0, capacity)
	}
	count := builder.count()
	for {
		token := l.NextToken()
		result.noteLineEpoch(count, l.lineEpoch)
		result.tokens = append(result.tokens, token)
		count++
		if token.Kind == TOKEN_EOF {
			break
		}
		if convergence != nil && convergence.replay(&builder, l) {
			break
		}
		if count%sourceCheckpointStride == 0 && len(result.checkpoints) < sourceCheckpointLimit {
			result.addCheckpoint(*l, result.source, result.source, count, l.readLimit)
		}
	}
	builder.materialize(builder.count())
	if cap(result.tokens) > 2*len(result.tokens) {
		result.tokens = copySourceSlice(result.tokens)
	}
	result.errors = copySourceSlice(l.Errors())
	return result
}

// Initial tokenization has no convergence or borrowed prefix to manage.
func tokenizeFreshSource(result *SourceTokens, l *Lexer, capacity int) *SourceTokens {
	result.tokens = make([]Token, 0, capacity)
	for {
		token := l.NextToken()
		result.noteLineEpoch(len(result.tokens), l.lineEpoch)
		result.tokens = append(result.tokens, token)
		if token.Kind == TOKEN_EOF {
			break
		}
		if len(result.tokens)%sourceCheckpointStride == 0 && len(result.checkpoints) < sourceCheckpointLimit {
			result.addCheckpoint(*l, result.source, result.source, len(result.tokens), l.readLimit)
		}
	}
	if cap(result.tokens) > 2*len(result.tokens) {
		result.tokens = copySourceSlice(result.tokens)
	}
	result.errors = copySourceSlice(l.Errors())
	return result
}

func rebaseSourceString(value string, old, new []byte) string {
	if len(value) == 0 || len(old) == 0 {
		return value
	}
	start := uintptr(unsafe.Pointer(unsafe.SliceData(old)))
	address := uintptr(unsafe.Pointer(unsafe.StringData(value)))
	if address < start || address-start >= uintptr(len(old)) {
		return value
	}
	offset := int(address - start)
	if len(value) > len(old)-offset || offset > len(new) || len(value) > len(new)-offset {
		panic("incremental source view outside common prefix")
	}
	return bytesToStringView(new[offset : offset+len(value)])
}
func rebaseToken(token Token, old, new []byte) Token {
	token.Text = rebaseSourceString(token.Text, old, new)
	token.Pos.File = rebaseSourceString(token.Pos.File, old, new)
	return token
}
func cloneLexerState(state Lexer, old, new []byte) Lexer {
	state.src = new
	state.filename = rebaseSourceString(state.filename, old, new)
	state.indentStack = copySourceSlice(state.indentStack)
	state.pendingPatternBlocks = copySourceSlice(state.pendingPatternBlocks)
	state.groupedBlockContexts = copySourceSlice(state.groupedBlockContexts)
	state.pendingToks = copySourceSlice(state.pendingToks)
	for i := range state.pendingToks {
		state.pendingToks[i] = rebaseToken(state.pendingToks[i], old, new)
	}
	state.errors = copySourceSlice(state.errors)
	for i := range state.errors {
		state.errors[i] = rebaseSourceString(state.errors[i], old, new)
	}
	return state
}

// RetainedBytes conservatively accounts one source owner, tokens and all nested
// checkpoint storage. Shared token text/source or filenames can be charged more
// than once; this is a payload admission bound, not allocator/RSS accounting.
func (s *SourceTokens) RetainedBytes(limit int) (int, bool) {
	size := 0
	add := func(n int) bool {
		if n > limit-size {
			return false
		}
		size += n
		return true
	}
	if !add(int(unsafe.Sizeof(*s))) || !add(len(s.filename)) || !add(cap(s.source)) {
		return 0, false
	}
	tokenBytes := func(tokens []Token) bool {
		if cap(tokens) > (limit-size)/int(unsafe.Sizeof(Token{})) {
			return false
		}
		if !add(cap(tokens) * int(unsafe.Sizeof(Token{}))) {
			return false
		}
		for _, t := range tokens {
			if !add(len(t.Text)) || !add(len(t.Pos.File)) {
				return false
			}
		}
		return true
	}
	errorBytes := func(errors []string) bool {
		if cap(errors) > (limit-size)/int(unsafe.Sizeof("")) || !add(cap(errors)*int(unsafe.Sizeof(""))) {
			return false
		}
		for _, e := range errors {
			if !add(len(e)) {
				return false
			}
		}
		return true
	}
	if cap(s.lineEpochs) > (limit-size)/int(unsafe.Sizeof(sourceLineEpoch{})) || !add(cap(s.lineEpochs)*int(unsafe.Sizeof(sourceLineEpoch{}))) {
		return 0, false
	}
	if !tokenBytes(s.tokens) || !errorBytes(s.errors) {
		return 0, false
	}
	if cap(s.checkpoints) > (limit-size)/int(unsafe.Sizeof(sourceCheckpoint{})) || !add(cap(s.checkpoints)*int(unsafe.Sizeof(sourceCheckpoint{}))) {
		return 0, false
	}
	for _, c := range s.checkpoints {
		if !add(len(c.state.filename)) {
			return 0, false
		}
		if !add(cap(c.state.indentStack)*int(unsafe.Sizeof(int(0)))) || !add(cap(c.state.pendingPatternBlocks)*int(unsafe.Sizeof(groupedPatternHeader{}))) || !add(cap(c.state.groupedBlockContexts)*int(unsafe.Sizeof(groupedBlockContext{}))) {
			return 0, false
		}
		if !tokenBytes(c.state.pendingToks) || !errorBytes(c.state.errors) {
			return 0, false
		}
	}
	return size, true
}

func checkpointStorageBytes(state Lexer, limit int) (int, bool) {
	size := int(unsafe.Sizeof(sourceCheckpoint{}))
	if size > limit {
		return 0, false
	}
	addSlice := func(count, item int) bool {
		if count > (limit-size)/item {
			return false
		}
		size += count * item
		return true
	}
	if !addSlice(len(state.indentStack), int(unsafe.Sizeof(int(0)))) || !addSlice(len(state.pendingToks), int(unsafe.Sizeof(Token{}))) || !addSlice(len(state.pendingPatternBlocks), int(unsafe.Sizeof(groupedPatternHeader{}))) || !addSlice(len(state.groupedBlockContexts), int(unsafe.Sizeof(groupedBlockContext{}))) || !addSlice(len(state.errors), int(unsafe.Sizeof(""))) {
		return 0, false
	}
	addText := func(text string) bool {
		if len(text) > limit-size {
			return false
		}
		size += len(text)
		return true
	}
	if !addText(state.filename) {
		return 0, false
	}
	for _, token := range state.pendingToks {
		if !addText(token.Text) || !addText(token.Pos.File) {
			return 0, false
		}
	}
	for _, err := range state.errors {
		if !addText(err) {
			return 0, false
		}
	}

	return size, true
}

func copySourceSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	result := make([]T, len(value))
	copy(result, value)
	return result
}

func (s *SourceTokens) addCheckpoint(state Lexer, old, new []byte, count, required int) bool {
	if len(s.checkpoints) >= sourceCheckpointLimit {
		return false
	}
	cost, ok := checkpointStorageBytes(state, sourceCheckpointBytes)
	if !ok {
		return false
	}
	header := int(unsafe.Sizeof(sourceCheckpoint{}))
	payload := cost - header
	capacity := cap(s.checkpoints)
	if capacity == len(s.checkpoints) {
		capacity = min(sourceCheckpointLimit, max(4, capacity*2))
	}
	growth := (capacity - cap(s.checkpoints)) * header
	remaining := sourceCheckpointBytes - s.checkpointBytes
	if growth > remaining || payload > remaining-growth {
		return false
	}
	if capacity != cap(s.checkpoints) {
		grown := make([]sourceCheckpoint, len(s.checkpoints), capacity)
		copy(grown, s.checkpoints)
		s.checkpoints = grown
	}
	s.checkpoints = append(s.checkpoints, sourceCheckpoint{count, required, cloneLexerState(state, old, new)})
	s.checkpointBytes += payload + growth
	return true
}

// MatchingTokenPrefix compares syntax and exact diagnostic positions without
// exposing mutable cached tokens. SourceTokens owns every string it retains.
func (s *SourceTokens) MatchingTokenPrefix(tokens []Token) int {
	if s == nil {
		return 0
	}
	count := min(len(s.tokens), len(tokens))
	for i := 0; i < count; i++ {
		if s.tokens[i] != tokens[i] {
			return i
		}
	}
	return count
}

// MatchingTokenSuffix includes exact source positions and EOF. It does not
// relocate cached syntax when edits shift following diagnostic locations.
func (s *SourceTokens) MatchingTokenSuffix(tokens []Token) int {
	if s == nil || len(s.tokens) != len(tokens) {
		return 0
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		if s.tokens[i] != tokens[i] {
			return len(tokens) - 1 - i
		}
	}
	return len(tokens)
}
