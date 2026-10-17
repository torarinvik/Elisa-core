package main

import (
	"crypto/sha256"
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
	lexed   *lexer.SourceTokens
	bytes   int
	lastUse uint64
}
type sourceTokenCache struct {
	mu                          sync.Mutex
	entries                     map[sourceTokenKey]sourceTokenEntry
	maxEntries, maxBytes, bytes int
	clock, hits, misses         uint64
	prefixReusedTokens          uint64
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
// zero-copy views. No AST is retained. Lexer.New runs on every invocation so
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
