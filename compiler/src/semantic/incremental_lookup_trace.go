package semantic

import (
	"elisacore/src/ast"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// LookupDependencyTrace is an owned audit of function-scoped global/type/permission
// lookup decisions. Direct Scope reads, type/callee/flow inputs and complete
// function identity are separate domains: this trace cannot authorize body reuse.
// Contexts and Records contain JSON strings, never compiler pointers. A record's
// Context is a one-based index. Truncation is explicit; partial reads are dropped.
type LookupDependencyTrace struct {
	Version           int
	Contexts, Records []string
	Reads, Dropped    uint64
	Truncated         bool
}

const lookupTraceMaxEntries = 65536
const lookupTraceMaxCandidates = 4096
const lookupTraceMaxBytes = 16 << 20

// A sorted context ID can grow from one to five decimal digits.
const lookupTraceContextIDSlack = 4

type lookupCandidateRead struct {
	Name             string
	Present, Visible bool
}

// Origin labels the root AST file, not an imported declaration's authoritative
// identity. Function labels likewise do not encode impl/specialization identity.
type lookupDependencyContext struct {
	Namespace, Function, Origin, Phase string
	Usings                             []string
}
type lookupDependencyRecord struct {
	Context       uint32
	Kind, Request string
	LocalShadowed bool
	Candidates    []lookupCandidateRead
}
type lookupDependencyRecorder struct {
	contexts       []string
	contextIDs     map[string]uint32
	records        map[string]struct{}
	bytes          int
	reads, dropped uint64
}

func (r *lookupDependencyRecorder) record(a *Analyzer, kind, name string, candidates []lookupCandidateRead, shadowed bool) {
	if a.currentFuncDecl == nil && a.lookupTraceOwner == "" {
		return
	}
	r.reads++
	function := a.lookupTraceOwner
	phase := "effects"
	if function == "" {
		function = a.currentFuncDecl.Name
		phase = "function"
	}
	if a.lookupTraceOwner == "" && a.currentFuncType != nil && a.currentFuncType.Name != "" {
		function = a.currentFuncType.Name
	}
	origin := ""
	if a.file != nil {
		origin = a.file.Filename
	}
	context := lookupDependencyContext{a.currentNamespace, function, origin, phase, a.currentUsings}
	if len(candidates) > lookupTraceMaxCandidates || len(context.Usings) > lookupTraceMaxEntries {
		r.dropped++
		return
	}
	// JSON can expand each input byte sixfold. Bound before encoding, including
	// invalid UTF-8 which JSON would otherwise replace and conflate.
	size := 0
	add := func(text string) bool {
		if !utf8.ValidString(text) || len(text) > lookupTraceMaxBytes/6-size {
			return false
		}
		size += len(text)
		return true
	}
	for _, text := range []string{kind, name, context.Namespace, function, origin, phase} {
		if !add(text) {
			r.dropped++
			return
		}
	}
	for _, using := range context.Usings {
		if !add(using) {
			r.dropped++
			return
		}
	}
	for _, candidate := range candidates {
		if !add(candidate.Name) {
			r.dropped++
			return
		}
	}
	encodedContext, err := json.Marshal(context)
	if err != nil {
		r.dropped++
		return
	}
	contextKey := string(encodedContext)
	id, exists := r.contextIDs[contextKey]
	contextBytes := 0
	if !exists {
		id = uint32(len(r.contexts) + 1)
		contextBytes = len(contextKey)
	}
	encoded, err := json.Marshal(lookupDependencyRecord{id, kind, name, shadowed, candidates})
	if err != nil {
		r.dropped++
		return
	}
	key := string(encoded)
	if _, exists := r.records[key]; exists {
		return
	}
	needed := 1
	if !exists {
		needed++
	}
	if len(r.records)+len(r.contexts)+needed > lookupTraceMaxEntries || contextBytes > lookupTraceMaxBytes-r.bytes || len(key)+lookupTraceContextIDSlack > lookupTraceMaxBytes-r.bytes-contextBytes {
		r.dropped++
		return
	}
	if r.contextIDs == nil {
		r.contextIDs = map[string]uint32{}
	}
	if r.records == nil {
		r.records = map[string]struct{}{}
	}
	if !exists {
		r.contextIDs[contextKey] = id
		r.contexts = append(r.contexts, contextKey)
	}
	r.records[key] = struct{}{}
	r.bytes += len(key) + contextBytes + lookupTraceContextIDSlack
}
func (r *lookupDependencyRecorder) snapshot() LookupDependencyTrace {
	result := LookupDependencyTrace{Version: 2, Reads: r.reads, Dropped: r.dropped, Truncated: r.dropped != 0}
	for _, context := range r.contexts {
		result.Contexts = append(result.Contexts, strings.Clone(context))
	}
	sort.Strings(result.Contexts)
	ids := map[string]uint32{}
	for i, context := range result.Contexts {
		ids[context] = uint32(i + 1)
	}
	// Canonicalize context numbering independently of first-observation order.
	for encoded := range r.records {
		var record lookupDependencyRecord
		if err := json.Unmarshal([]byte(encoded), &record); err != nil || record.Context == 0 || int(record.Context) > len(r.contexts) {
			result.Truncated = true
			result.Dropped++
			continue
		}
		record.Context = ids[r.contexts[record.Context-1]]
		data, err := json.Marshal(record)
		if err != nil {
			result.Truncated = true
			result.Dropped++
			continue
		}
		result.Records = append(result.Records, string(data))
	}
	sort.Strings(result.Records)
	return result
}

// AnalyzeWithLookupTrace performs full fresh analysis and records this audited
// dependency domain. Replaying function products needs the remaining domains
// and full Result/live-analyzer hydration independently of these observations.
func AnalyzeWithLookupTrace(file *ast.File, options AnalyzeOptions) (*Result, LookupDependencyTrace) {
	recorder := &lookupDependencyRecorder{}
	result := analyzeWithLookupRecorder(file, options, nil, nil, recorder)
	return result, recorder.snapshot()
}

// Keep only a bounded prefix plus one overflow marker. record rejects the
// entire incomplete observation instead of presenting a prefix as complete.
func appendLookupCandidate(reads []lookupCandidateRead, read lookupCandidateRead) []lookupCandidateRead {
	if len(reads) > lookupTraceMaxCandidates {
		return reads
	}
	return append(reads, read)
}
