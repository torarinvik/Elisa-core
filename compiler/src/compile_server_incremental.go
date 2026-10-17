package main

import (
	"path/filepath"
	"strings"
	"sync"

	"elisacore/src/semantic"
)

const defaultCompileServerSessionCacheEntries = 32

type compileServerSessionKey struct {
	filename string
	profile  string
}

type compileServerSessionEntry struct {
	session *semantic.IncrementalAnalysisSession
	lastUse uint64
}

// compileServerSessionCache keeps independent semantic databases for each
// source path and analysis profile. The bound prevents an HTTP server that sees
// many one-off filenames from retaining every session for its entire lifetime.
type compileServerSessionCache struct {
	tokens     *sourceTokenCache
	mu         sync.Mutex
	maxEntries int
	clock      uint64
	entries    map[compileServerSessionKey]compileServerSessionEntry
}

func newCompileServerSessionCache(maxEntries int) *compileServerSessionCache {
	if maxEntries <= 0 {
		maxEntries = defaultCompileServerSessionCacheEntries
	}
	return &compileServerSessionCache{
		maxEntries: maxEntries,
		tokens:     newSourceTokenCache(maxEntries, defaultSourceTokenCacheBytes),
		entries:    make(map[compileServerSessionKey]compileServerSessionEntry),
	}
}

func compileServerModeAnalyzes(mode string) bool {
	switch mode {
	case emitAST, emitDoc, emitSemantic, emitFacts, emitUnsafe, emitProgress, emitIR, emitLLVM, emitInterpret:
		return true
	default:
		return false
	}
}

func compileServerAnalysisProfile(mode string) string {
	switch mode {
	case emitUnsafe, emitProgress:
		return mode
	case emitAST, emitDoc:
		return "inspection"
	default:
		return "default"
	}
}

func canonicalCompileServerFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "request.elisa"
	}
	filename = filepath.Clean(filename)
	if absolute, err := filepath.Abs(filename); err == nil {
		filename = absolute
	}
	return filename
}

func (cache *compileServerSessionCache) session(filename, mode string) *semantic.IncrementalAnalysisSession {
	if cache == nil {
		return nil
	}
	key := compileServerSessionKey{
		filename: canonicalCompileServerFilename(filename),
		profile:  compileServerAnalysisProfile(mode),
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.clock++
	if entry, ok := cache.entries[key]; ok {
		entry.lastUse = cache.clock
		cache.entries[key] = entry
		return entry.session
	}
	if len(cache.entries) >= cache.maxEntries {
		var oldestKey compileServerSessionKey
		oldestUse := ^uint64(0)
		for candidate, entry := range cache.entries {
			if entry.lastUse < oldestUse {
				oldestKey = candidate
				oldestUse = entry.lastUse
			}
		}
		delete(cache.entries, oldestKey)
	}
	entry := compileServerSessionEntry{session: semantic.NewIncrementalAnalysisSession(), lastUse: cache.clock}
	cache.entries[key] = entry
	return entry.session
}
