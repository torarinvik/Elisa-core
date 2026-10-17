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
	tokens  []lexer.Token
	errors  []string
	bytes   int
	lastUse uint64
}
type sourceTokenCache struct {
	mu                          sync.Mutex
	entries                     map[sourceTokenKey]sourceTokenEntry
	maxEntries, maxBytes, bytes int
	clock, hits, misses         uint64
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
		tokens := append([]lexer.Token(nil), entry.tokens...)
		errors := append([]string(nil), entry.errors...)
		_ = lexer.New(filename, source)
		return tokens, errors
	}
	c.misses++
	c.mu.Unlock()
	// Oversized inputs cannot be retained; preserve the ordinary cold path.
	owned := source
	if len(source) <= c.maxBytes {
		owned = append([]byte(nil), source...)
	}
	filename = strings.Clone(filename)
	key.filename = filename
	l := lexer.New(filename, owned)
	tokens := l.Tokenize()
	errors := l.Errors()
	size, admit := c.entryBytes(filename, len(owned), tokens, errors)
	if !admit {
		return tokens, errors
	}
	entry := sourceTokenEntry{tokens: append([]lexer.Token(nil), tokens...), errors: append([]string(nil), errors...), bytes: size}
	c.mu.Lock()
	defer c.mu.Unlock()
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

func (c *sourceTokenCache) entryBytes(filename string, sourceBytes int, tokens []lexer.Token, errors []string) (int, bool) {
	size := int(unsafe.Sizeof(sourceTokenEntry{})) + len(filename) + 32
	if sourceBytes > c.maxBytes-size {
		return 0, false
	}
	size += sourceBytes
	tokenSize := int(unsafe.Sizeof(lexer.Token{}))
	if size > c.maxBytes || len(tokens) > (c.maxBytes-size)/tokenSize {
		return 0, false
	}
	size += len(tokens) * tokenSize
	add := func(n int) bool {
		if n > c.maxBytes-size {
			return false
		}
		size += n
		return true
	}
	for _, token := range tokens {
		if !add(len(token.Text)) || !add(len(token.Suffix)) || !add(len(token.Pos.File)) {
			return 0, false
		}
	}
	for _, err := range errors {
		if !add(int(unsafe.Sizeof(""))) || !add(len(err)) {
			return 0, false
		}
	}
	return size, true
}
