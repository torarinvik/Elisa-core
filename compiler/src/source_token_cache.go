package main

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"sync"
	"unsafe"
)

const defaultSourceTokenCacheBytes = 512 << 20

type sourceTokenKey struct {
	filename string
	source   [32]byte
}
type sourceTokenEntry struct {
	lexed         *lexer.SourceTokens
	parsed        *ast.SyntaxSnapshot
	trace         *parser.SyntaxTrace
	parseDeclined bool
	parseRequests uint64
	bytes         int
	lastUse       uint64
}
type sourceTokenCache struct {
	mu                          sync.Mutex
	entries                     map[sourceTokenKey]sourceTokenEntry
	maxEntries, maxBytes, bytes int
	clock, hits, misses         uint64
	prefixReusedTokens          uint64
	parseHits                   uint64
	prefixParsedDeclarations    uint64
	suffixParsedDeclarations    uint64
}

func newSourceTokenCache(maxEntries, maxBytes int) *sourceTokenCache {
	if maxEntries <= 0 {
		maxEntries = defaultCompileServerSessionCacheEntries
	}
	if maxBytes <= 0 {
		maxBytes = defaultSourceTokenCacheBytes
	}
	return &sourceTokenCache{entries: map[sourceTokenKey]sourceTokenEntry{}, maxEntries: maxEntries, maxBytes: maxBytes}
}

// Slice callers receive their own token storage. Parsers use immutable views and
// detach on recovery. Cold entries own source bytes because token strings are
// zero-copy views. Lexer.New preserves parity registration on every request.
func (c *sourceTokenCache) lex(filename string, source []byte) ([]lexer.Token, []string) {
	return c.lexWithKey(sourceTokenKey{filename, sha256.Sum256(source)}, source)
}

// The request key names these exact immutable source bytes.
func (c *sourceTokenCache) lexWithKey(key sourceTokenKey, source []byte) ([]lexer.Token, []string) {
	tokens, errors := c.lexBorrowedWithKey(key, source)
	return append([]lexer.Token(nil), tokens...), errors
}

// Cache entries remain immutable. Parsers borrow a read-only slice and detach on the
// exceptional token-kind rewrite; legacy slice callers still receive a copy.
func (c *sourceTokenCache) lexBorrowedWithKey(key sourceTokenKey, source []byte) ([]lexer.Token, []string) {
	filename := key.filename
	c.mu.Lock()
	c.clock++
	if entry, ok := c.entries[key]; ok {
		c.hits++
		entry.lastUse = c.clock
		c.entries[key] = entry
		c.mu.Unlock()
		tokens := entry.lexed.BorrowReadOnlyTokens()
		errors := entry.lexed.CopyErrors()
		_ = lexer.New(filename, source)
		return tokens, errors
	}
	c.misses++
	var previous *lexer.SourceTokens
	var recent uint64
	for candidate, entry := range c.entries {
		if candidate.filename == filename && entry.lastUse > recent {
			previous = entry.lexed
			recent = entry.lastUse
		}
	}
	c.mu.Unlock()
	if len(source) > c.maxBytes {
		l := lexer.New(filename, source)
		return l.Tokenize(), l.Errors()
	}
	lexed := lexer.TokenizeSource(filename, source, previous)
	tokens, errors := lexed.BorrowReadOnlyTokens(), lexed.CopyErrors()
	key.filename = strings.Clone(filename)
	overhead := int(unsafe.Sizeof(sourceTokenEntry{})) + len(filename) + 32
	if overhead > c.maxBytes {
		return tokens, errors
	}
	size, admit := lexed.RetainedBytes(c.maxBytes - overhead)
	if !admit {
		return tokens, errors
	}
	size += overhead
	entry := sourceTokenEntry{lexed: lexed, bytes: size}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prefixReusedTokens += uint64(lexed.ReusedTokens())
	// Concurrent cold requests may compute the same immutable entry. Keep one.
	if _, exists := c.entries[key]; exists {
		return tokens, errors
	}
	for len(c.entries) >= c.maxEntries || c.bytes > c.maxBytes-size {
		var oldest sourceTokenKey
		use := ^uint64(0)
		for candidate, retained := range c.entries {
			if retained.lastUse < use {
				oldest = candidate
				use = retained.lastUse
			}
		}
		c.bytes -= c.entries[oldest].bytes
		delete(c.entries, oldest)
	}
	c.clock++
	entry.lastUse = c.clock
	c.entries[key] = entry
	c.bytes += size
	return tokens, errors
}

// parsedFile replays only exact diagnostic-free parses. Parser construction has
// no options; filename and source bytes determine the syntax and its positions.
// The third result requests tracing only when this retained revision can admit a snapshot.
func (c *sourceTokenCache) parsedFile(filename string, source []byte) (*ast.File, bool, bool) {
	return c.parsedFileWithKey(sourceTokenKey{filename, sha256.Sum256(source)}, source)
}
func (c *sourceTokenCache) parsedFileWithKey(key sourceTokenKey, source []byte) (*ast.File, bool, bool) {
	filename := key.filename
	c.mu.Lock()
	entry, exists := c.entries[key]
	snapshot := entry.parsed
	if exists {
		entry.parseRequests++
		c.entries[key] = entry
	}
	if !exists || snapshot == nil {
		c.mu.Unlock()
		return nil, false, exists && !entry.parseDeclined && snapshot == nil
	}
	c.mu.Unlock()
	file, err := snapshot.CopyFile()
	if err != nil {
		return nil, false, false
	}
	c.mu.Lock()
	c.clock++
	if current, ok := c.entries[key]; ok {
		current.lastUse = c.clock
		c.entries[key] = current
	}
	c.hits++
	c.parseHits++
	c.mu.Unlock()
	_ = lexer.New(filename, source)
	return file, true, false
}

// Capture precedes semantic mutation. Unsupported graph shapes and parser
// notices retain the normal token/parse path. Snapshot cost shares the LRU budget.
func (c *sourceTokenCache) rememberParsed(filename string, source []byte, file *ast.File, trace *parser.SyntaxTrace, diagnosticFree bool) {
	if trace == nil && diagnosticFree {
		return
	}
	c.rememberParsedWithKey(sourceTokenKey{filename, sha256.Sum256(source)}, file, trace, diagnosticFree)
}
func (c *sourceTokenCache) rememberParsedWithKey(key sourceTokenKey, file *ast.File, trace *parser.SyntaxTrace, diagnosticFree bool) {
	if trace == nil && diagnosticFree {
		return
	}
	c.mu.Lock()
	entry, exists := c.entries[key]
	if !exists || entry.parsed != nil || entry.parseDeclined || entry.parseRequests == 0 {
		c.mu.Unlock()
		return
	}
	if !diagnosticFree {
		entry.parseDeclined = true
		c.entries[key] = entry
		c.mu.Unlock()
		return
	}
	traceBytes := trace.RetainedBytes()
	limit := c.maxBytes - entry.bytes - traceBytes
	c.mu.Unlock()
	snapshot, err := ast.NewSyntaxSnapshot(file, limit)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists = c.entries[key]
	if !exists || entry.parsed != nil || entry.parseDeclined {
		return
	}
	if err != nil {
		entry.parseDeclined = true
		c.entries[key] = entry
		return
	}
	size := snapshot.RetainedBytes() + traceBytes
	for c.bytes > c.maxBytes-size {
		var oldest sourceTokenKey
		use := ^uint64(0)
		for candidate, retained := range c.entries {
			if candidate != key && retained.lastUse < use {
				oldest = candidate
				use = retained.lastUse
			}
		}
		if use == ^uint64(0) {
			entry.parseDeclined = true
			c.entries[key] = entry
			return
		}
		c.bytes -= c.entries[oldest].bytes
		delete(c.entries, oldest)
	}
	entry.parsed = snapshot
	entry.trace = trace
	entry.bytes += size
	c.entries[key] = entry
	c.bytes += size
}

// Select before lexing can evict the previous revision. In-flight immutable
// products may outlive LRU eviction; no secondary unbounded history is retained.
func (c *sourceTokenCache) prefixCandidate(filename string) sourceTokenEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var candidate sourceTokenEntry
	for key, entry := range c.entries {
		if key.filename == filename && entry.parsed != nil && entry.trace != nil && entry.lastUse > candidate.lastUse {
			candidate = entry
		}
	}
	return candidate
}
func (c *sourceTokenCache) parsePrefix(candidate sourceTokenEntry, tokens []lexer.Token) (*ast.File, parser.PrefixCheckpoint, bool) {
	if candidate.trace == nil || candidate.parsed == nil || candidate.lexed == nil {
		return nil, parser.PrefixCheckpoint{}, false
	}
	matching := candidate.lexed.MatchingTokenPrefix(tokens)
	checkpoint, ok := candidate.trace.ReusablePrefix(matching, len(tokens))
	if !ok || checkpoint.Declarations() == 0 {
		return nil, parser.PrefixCheckpoint{}, false
	}
	file, err := candidate.parsed.CopyPrefix(checkpoint.Declarations())
	if err != nil {
		return nil, parser.PrefixCheckpoint{}, false
	}
	c.mu.Lock()
	c.prefixParsedDeclarations += uint64(checkpoint.Declarations())
	c.clock++
	for key, entry := range c.entries {
		if entry.parsed == candidate.parsed {
			entry.lastUse = c.clock
			c.entries[key] = entry
			break
		}
	}
	c.mu.Unlock()
	return file, checkpoint, true
}

func (c *sourceTokenCache) configureSuffixReplay(p *parser.Parser, candidate sourceTokenEntry, tokens []lexer.Token) {
	if candidate.trace == nil || candidate.parsed == nil || candidate.lexed == nil {
		return
	}
	matching := candidate.lexed.MatchingTokenSuffix(tokens)
	if matching == 0 {
		return
	}
	p.SetSyntaxSuffixReplay(candidate.trace, len(tokens)-matching,
		func(declarations int) (*ast.File, error) {
			return candidate.parsed.CopySuffix(declarations)
		}, func(declarations int) {
			c.mu.Lock()
			c.suffixParsedDeclarations += uint64(declarations)
			c.clock++
			for key, entry := range c.entries {
				if entry.parsed == candidate.parsed {
					entry.lastUse = c.clock
					c.entries[key] = entry
					break
				}
			}
			c.mu.Unlock()
		})
}
