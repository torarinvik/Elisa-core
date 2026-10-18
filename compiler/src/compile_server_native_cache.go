package main

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"elisacore/src/backend"
)

const serverNativeCacheBytes = 128 << 20
const serverNativeEntryOverhead = 512

type serverNativeIdentity struct {
	digest         [32]byte
	source, config string
}
type serverNativeEntry struct {
	identity serverNativeIdentity
	object   string
	bytes    int
}
type serverNativeStats struct {
	Hits, Misses, Evictions uint64
	Bytes, Entries          int
}

// Only completed native bytes are retained. Semantic results and LLVM handles
// never enter this cache; analysis and diagnostics run before every lookup.
type compileServerNativeCache struct {
	mu                          sync.Mutex
	owner                       *backend.IncrementalObjectSession
	buckets                     map[[32]byte][]*list.Element
	lru                         list.List
	maxEntries, maxBytes, bytes int
	stats                       serverNativeStats
}

func newCompileServerNativeCache(owner *backend.IncrementalObjectSession, entries, bytes int) *compileServerNativeCache {
	return &compileServerNativeCache{owner: owner, maxEntries: entries, maxBytes: bytes, buckets: make(map[[32]byte][]*list.Element)}
}
func (c *compileServerNativeCache) lookup(id serverNativeIdentity) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, element := range c.buckets[id.digest] {
		entry := element.Value.(*serverNativeEntry)
		if entry.identity.config == id.config && entry.identity.source == id.source {
			c.lru.MoveToFront(element)
			c.stats.Hits++
			return entry.object, true
		}
	}
	c.stats.Misses++
	return "", false
}
func (c *compileServerNativeCache) remove(element *list.Element) {
	entry := element.Value.(*serverNativeEntry)
	bucket := c.buckets[entry.identity.digest]
	for i, e := range bucket {
		if e == element {
			copy(bucket[i:], bucket[i+1:])
			bucket[len(bucket)-1] = nil
			bucket = bucket[:len(bucket)-1]
			break
		}
	}
	if len(bucket) == 0 {
		delete(c.buckets, entry.identity.digest)
	} else {
		c.buckets[entry.identity.digest] = bucket
	}
	c.bytes -= entry.bytes
	c.lru.Remove(element)
	c.stats.Evictions++
}
func (c *compileServerNativeCache) retain(id serverNativeIdentity, object string) bool {
	remaining := c.maxBytes - serverNativeEntryOverhead
	if c.maxEntries <= 0 || remaining < 0 || object == "" {
		return false
	}
	for _, s := range []string{id.source, id.config, object} {
		if len(s) > remaining {
			return false
		}
		remaining -= len(s)
	}
	size := c.maxBytes - remaining
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, element := range c.buckets[id.digest] {
		entry := element.Value.(*serverNativeEntry)
		if entry.identity.config == id.config && entry.identity.source == id.source {
			c.lru.MoveToFront(element)
			return true
		}
	}
	for c.lru.Len() >= c.maxEntries || c.bytes > c.maxBytes-size {
		c.remove(c.lru.Back())
	}
	entry := &serverNativeEntry{identity: serverNativeIdentity{digest: id.digest, source: strings.Clone(id.source), config: strings.Clone(id.config)}, object: strings.Clone(object), bytes: size}
	element := c.lru.PushFront(entry)
	c.buckets[id.digest] = append(c.buckets[id.digest], element)
	c.bytes += size
	return true
}
func (c *compileServerNativeCache) Stats() serverNativeStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats
	s.Bytes = c.bytes
	s.Entries = c.lru.Len()
	return s
}

// Start with default, closed process configuration. Unknown compiler switches,
// per-call observers and custom/injected toolchains must retain the normal path.
// Merely adding a future ELISA/LLVM switch cannot silently enable stale reuse.
func serverNativeEnvironmentAllowed(environment []string) bool {
	for _, entry := range environment {
		if !utf8.ValidString(entry) {
			return false
		}
		name, _, _ := strings.Cut(entry, "=")
		// This audited frontend-only policy is recomputed before every lookup and
		// also remains in the exact environment identity below.
		if name == "ELISACORE_SUPPRESS_DEPRECATED_WARNINGS" {
			continue
		}
		if strings.HasPrefix(name, "ELISA") || strings.HasPrefix(name, "LLVM_") || strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") {
			return false
		}
		switch name {
		case "SDKROOT", "DEVELOPER_DIR", "TOOLCHAINS":
			return false
		}
	}
	return true
}

func serverNativeObjectIdentity(req compileServerRequest, program *loadedProgram, sessions *compileServerSessionCache) (serverNativeIdentity, bool) {
	if sessions == nil || sessions.native == nil || sessions.objects == nil || sessions.objects != sessions.native.owner || !req.Incremental || strings.TrimSpace(req.Filename) == "" || program == nil || program.file != nil || len(program.easm) != 0 || len(program.sourceFiles) != 0 || strings.TrimSpace(req.Source) == "" || req.IR != "" || string(program.source) != req.Source {
		return serverNativeIdentity{}, false
	}
	environment := os.Environ()
	if !serverNativeEnvironmentAllowed(environment) {
		return serverNativeIdentity{}, false
	}
	// Only the ordinary system merger is in this first supported tool closure.
	// Explicit overrides and PATH-provided tools continue executing per request.
	selected, err := exec.LookPath("ld")
	if err != nil || selected != "/usr/bin/ld" {
		return serverNativeIdentity{}, false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return serverNativeIdentity{}, false
	}
	artifact, ok := buildCacheObjectArtifactForProgram(cliOptions{emit: emitObject, filename: program.filename, optLevel: backend.OptimizationLevel0, hasOptLevel: true}, program)
	if !ok {
		return serverNativeIdentity{}, false
	}
	command, executable, err := sessions.objects.ObjectMergerCacheInputs()
	if err != nil {
		return serverNativeIdentity{}, false
	}
	if runtime.GOOS == "darwin" && executable == "/usr/bin/ld" {
		return serverNativeIdentity{}, false
	}
	mergerStamp, err := toolchainContentStamp(executable)
	if err != nil {
		return serverNativeIdentity{}, false
	}
	absolute, err := filepath.Abs(program.filename)
	if err != nil {
		return serverNativeIdentity{}, false
	}
	// JSON must preserve every configuration byte; non-text process/path inputs
	// keep the ordinary path rather than normalizing malformed UTF-8 in the key.
	for _, value := range append([]string{cwd, absolute, executable, req.Filename, req.Mode, req.Filter}, command...) {
		if !utf8.ValidString(value) {
			return serverNativeIdentity{}, false
		}
	}
	sort.Strings(environment)
	// The complete environment affects admission/identity, but credentials do
	// not need to remain copied in every retained configuration string.
	environmentBytes, err := json.Marshal(environment)
	if err != nil {
		return serverNativeIdentity{}, false
	}
	environmentStamp := sha256.Sum256(environmentBytes)
	fields := req
	fields.Source = ""
	fields.IR = ""
	config, err := json.Marshal(struct {
		Schema, Build, Cwd, Unit, MergerStamp, MergerExecutable string
		Environment                                             [32]byte
		Merger                                                  []string
		Request                                                 compileServerRequest
	}{"server-native-noop-v2", artifact.key, cwd, absolute, mergerStamp, executable, environmentStamp, command, fields})
	if err != nil {
		return serverNativeIdentity{}, false
	}
	// The build key hashes the exact source; retain and compare those bytes too.
	return serverNativeIdentity{digest: sha256.Sum256(config), source: req.Source, config: string(config)}, true
}
