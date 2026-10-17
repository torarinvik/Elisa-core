package main

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/lexer"
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

// Every parser owns its token slice: contextual keyword handling rewrites token
// kinds. A cold entry lexes an owned source copy because token strings are
// zero-copy views. Pristine AST snapshots may also be retained. Lexer.New runs on every invocation so
// parity source registration and failure cleanup retain their normal lifecycle.
func (c *sourceTokenCache) lex(filename string, source []byte) ([]lexer.Token, []string) {
	key := sourceTokenKey{filename, sha256.Sum256(source)}
	c.mu.Lock()
	c.clock++
	if entry, ok := c.entries[key]; ok {
		c.hits++
		entry.lastUse = c.clock
		c.entries[key] = entry
		c.mu.Unlock()
		tokens := entry.lexed.CopyTokens()
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
	tokens, errors := lexed.CopyTokens(), lexed.CopyErrors()
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
func (c *sourceTokenCache) parsedFile(filename string, source []byte) (*ast.File, bool) {
	key := sourceTokenKey{filename, sha256.Sum256(source)}
	c.mu.Lock()
	entry, exists := c.entries[key]
	snapshot := entry.parsed
	if exists {
		entry.parseRequests++
		c.entries[key] = entry
	}
	if !exists || snapshot == nil {
		c.mu.Unlock()
		return nil, false
	}
	c.mu.Unlock()
	file, err := snapshot.CopyFile()
	if err != nil {
		return nil, false
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
	return file, true
}

// Capture precedes semantic mutation. Unsupported graph shapes and parser
// notices retain the normal token/parse path. Snapshot cost shares the LRU budget.
func (c *sourceTokenCache) rememberParsed(filename string, source []byte, file *ast.File, diagnosticFree bool) {
	key := sourceTokenKey{filename, sha256.Sum256(source)}
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
	limit := c.maxBytes - entry.bytes
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
	size := snapshot.RetainedBytes()
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
	entry.bytes += size
	c.entries[key] = entry
	c.bytes += size
}
