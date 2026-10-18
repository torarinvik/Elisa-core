package main

import (
	"bytes"
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
	"elisacore/src/lexer"
)

const serverNativeCacheBytes = 128 << 20
const serverNativeEntryOverhead = 512

type serverNativeIdentity struct {
	digest         [32]byte
	unitHint       [32]byte // Replacement hint only; never authorizes a cache hit.
	source, config string
}
type serverNativeEntry struct {
	identity serverNativeIdentity
	object   string
	bytes    int
	response *serverCachedResponse
}
type serverNativeStats struct {
	Hits, Misses, Evictions      uint64
	ResponseHits, ResponseMisses uint64
	Bytes, Entries               int
}

type serverCachedResponse struct {
	response compileServerResponse
	status   int
}

func (c *compileServerNativeCache) lookupResponse(id serverNativeIdentity) (compileServerResponse, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, element := range c.buckets[id.digest] {
		entry := element.Value.(*serverNativeEntry)
		if entry.identity.config == id.config && entry.identity.source == id.source && entry.response != nil {
			c.lru.MoveToFront(element)
			c.stats.ResponseHits++
			return entry.response.response, entry.response.status, true
		}
	}
	c.stats.ResponseMisses++
	return compileServerResponse{}, 0, false
}

// Share the object string and the existing LRU/byte budget with artifact reuse.
// Only finalized successful responses can authorize skipping frontend analysis.
func (c *compileServerNativeCache) retainResponse(id serverNativeIdentity, response compileServerResponse, status int) bool {
	if status != 200 || !response.OK || response.Object == "" {
		return false
	}
	extra := 0
	for _, value := range []string{response.Output, response.IR, response.Value, response.Stderr, response.Error, response.ErrorCode} {
		if len(value) > c.maxBytes-extra {
			return false
		}
		extra += len(value)
	}
	if !c.retain(id, response.Object) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, element := range c.buckets[id.digest] {
		entry := element.Value.(*serverNativeEntry)
		if entry.identity.config != id.config || entry.identity.source != id.source {
			continue
		}
		if entry.response != nil {
			return true
		}
		if entry.object != response.Object {
			return false // Never silently substitute bytes in a finalized response.
		}
		if extra > c.maxBytes-entry.bytes {
			return false // Preserve the object-only fallback when diagnostics are too large.
		}
		c.lru.MoveToFront(element)
		for c.bytes > c.maxBytes-extra {
			victim := c.evictionCandidate([32]byte{}, element)
			if victim == nil {
				return false
			}
			c.remove(victim)
		}
		response.Object = entry.object
		response.Output = strings.Clone(response.Output)
		response.IR = strings.Clone(response.IR)
		response.Value = strings.Clone(response.Value)
		response.Stderr = strings.Clone(response.Stderr)
		response.Error = strings.Clone(response.Error)
		response.ErrorCode = strings.Clone(response.ErrorCode)
		entry.response = &serverCachedResponse{response: response, status: status}
		entry.bytes += extra
		c.bytes += extra
		return true
	}
	return false // Another request may have evicted the object between locks.
}

// Completed native bytes and optionally their finalized successful response are
// retained. Semantic results and LLVM handles never enter this cache. An exact
// response hit skips analysis; object-only hits still validate and emit current
// diagnostics before looking up the artifact.
type compileServerNativeCache struct {
	mu                          sync.Mutex
	owner                       *backend.IncrementalObjectSession
	buckets                     map[[32]byte][]*list.Element
	unitCounts                  map[[32]byte]int
	historyEntries              int
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
	if unit := entry.identity.unitHint; unit != ([32]byte{}) {
		if c.unitCounts[unit] <= 1 {
			delete(c.unitCounts, unit)
		} else {
			c.historyEntries--
			c.unitCounts[unit]--
		}
	}
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

// Prefer LRU history over another unit's sole response. An incoming revision
// makes its prior revision history too. Unknown hints keep ordinary LRU behavior;
// collisions can affect replacement quality, never exact reuse authority.
// protected is the object whose finalized response is being attached.
func (c *compileServerNativeCache) evictionCandidate(incoming [32]byte, protected *list.Element) *list.Element {
	last := c.lru.Back()
	if last == protected && last != nil {
		last = last.Prev()
	}
	// Distinct-unit streaming remains O(1) when no history can be preferred.
	if c.historyEntries == 0 && (incoming == ([32]byte{}) || c.unitCounts[incoming] == 0) {
		return last
	}
	for element := c.lru.Back(); element != nil; element = element.Prev() {
		if element == protected {
			continue
		}
		unit := element.Value.(*serverNativeEntry).identity.unitHint
		if unit != ([32]byte{}) && (unit == incoming || c.unitCounts[unit] > 1) {
			return element
		}
	}
	return last
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
		c.remove(c.evictionCandidate(id.unitHint, nil))
	}
	entry := &serverNativeEntry{identity: serverNativeIdentity{digest: id.digest, unitHint: id.unitHint, source: strings.Clone(id.source), config: strings.Clone(id.config)}, object: strings.Clone(object), bytes: size}
	element := c.lru.PushFront(entry)
	if id.unitHint != ([32]byte{}) {
		if c.unitCounts == nil {
			c.unitCounts = make(map[[32]byte]int)
		}
		if c.unitCounts[id.unitHint] > 0 {
			c.historyEntries++
		}
		c.unitCounts[id.unitHint]++
	}
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
		// also remains in the fingerprinted environment identity below.
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
	diagnosticPaths := serverDiagnosticPathInputs(program.filename, program.source)
	fields := req
	fields.Source = ""
	fields.IR = ""
	config, err := json.Marshal(struct {
		Schema, Build, Cwd, Unit, MergerStamp, MergerExecutable string
		Environment                                             [32]byte
		DiagnosticPaths                                         []serverDiagnosticPathState
		Merger                                                  []string
		Request                                                 compileServerRequest
	}{"server-native-noop-v2", artifact.key, cwd, absolute, mergerStamp, executable, environmentStamp, diagnosticPaths, command, fields})
	if err != nil {
		return serverNativeIdentity{}, false
	}
	// The build key hashes the exact source; retain and compare those bytes too.
	return serverNativeIdentity{digest: sha256.Sum256(config), unitHint: sha256.Sum256([]byte(absolute)), source: req.Source, config: string(config)}, true
}

// Runtime-carrier diagnostics distinguish synthetic bare names from existing
// source paths. #line attribution can select names other than the request name.
type serverDiagnosticPathState struct {
	Path   string
	Exists bool
}

func serverDiagnosticPathInputs(filename string, source []byte) []serverDiagnosticPathState {
	names := map[string]bool{}
	add := func(name string) {
		if name != "" && name != "<unknown>" && !strings.Contains(filepath.ToSlash(name), "/") {
			names[name] = true
		}
	}
	add(filename)
	// Ordinary source needs no tokenization for response admission. For possible
	// attribution directives use the actual lexer, keeping comment/string syntax
	// and directive parsing identical to the compiler rather than duplicating it.
	if bytes.Contains(source, []byte("#line")) {
		scanner := lexer.New(filename, source)
		for {
			token := scanner.NextToken()
			add(token.Pos.File)
			if token.Kind == lexer.TOKEN_EOF {
				break
			}
		}
	}
	paths := make([]string, 0, len(names))
	for name := range names {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	states := make([]serverDiagnosticPathState, 0, len(paths))
	for _, path := range paths {
		_, err := os.Stat(path)
		states = append(states, serverDiagnosticPathState{Path: path, Exists: err == nil})
	}
	return states
}
