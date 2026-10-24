//go:build cgo

package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

const retainedDisjointCachePartitions = 8
const retainedDisjointCacheIdentity = "retained-disjoint-noalias-cache-v1"

var retainedDisjointGensym = regexp.MustCompile("__([a-z_]+)_[0-9]+")

func parseRetainedDisjointCacheSource(t *testing.T, filename, source string) *ast.File {
	t.Helper()
	l := lexer.New(filename, []byte(source))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("lex %s: %v", filename, errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse %s: %v", filename, errs)
	}
	return file
}

func retainedDisjointRoundTrip(t *testing.T, state *semantic.RetainedAnalysis, source []byte) (*semantic.RetainedAnalysis, *semantic.Result) {
	t.Helper()
	data, err := state.MarshalState()
	if err != nil {
		t.Fatalf("marshal retained state: %v", err)
	}
	restored, err := semantic.RestoreAnalysisState(data)
	if err != nil {
		t.Fatalf("restore retained state: %v", err)
	}
	result, ok := restored.ResultForSource(source)
	if !ok || result == nil {
		t.Fatal("restored state did not publish the current source result")
	}
	return restored, result
}

func newRetainedDisjointState(t *testing.T, filename, source string) (*semantic.RetainedAnalysis, *semantic.Result) {
	t.Helper()
	src := []byte(source)
	result, state := semantic.AnalyzeRetained(
		parseRetainedDisjointCacheSource(t, filename, source),
		parseRetainedDisjointCacheSource(t, filename, source),
		src,
		semantic.AnalyzeOptions{},
	)
	if state == nil || result == nil {
		t.Fatal("initial retained analysis was not created")
	}
	return retainedDisjointRoundTrip(t, state, src)
}

func updateRetainedDisjointState(t *testing.T, filename, source string, state *semantic.RetainedAnalysis) (*semantic.RetainedAnalysis, *semantic.Result) {
	t.Helper()
	src := []byte(source)
	result, err := state.Update(
		parseRetainedDisjointCacheSource(t, filename, source),
		parseRetainedDisjointCacheSource(t, filename, source),
		src,
	)
	if err != nil {
		t.Fatalf("retained update: %v", err)
	}
	if result == nil {
		t.Fatal("retained update returned no result")
	}
	return retainedDisjointRoundTrip(t, state, src)
}

func normalizeRetainedDisjointIR(ir string) string {
	return retainedDisjointGensym.ReplaceAllStringFunc(ir, func(match string) string {
		parts := retainedDisjointGensym.FindStringSubmatch(match)
		return "__" + parts[1] + "_N"
	})
}

func retainedDisjointIR(t *testing.T, result *semantic.Result) string {
	t.Helper()
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("analysis errors: %v", errs)
	}
	ir, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(
		result,
		OptimizationLevel0,
		DefaultPackedLoweringProfile(),
	)
	if err != nil {
		t.Fatalf("generate LLVM IR: %v", err)
	}
	return ir
}

func assertRetainedDisjointColdIRParity(t *testing.T, filename, source string, retained *semantic.Result) string {
	t.Helper()
	retainedIR := retainedDisjointIR(t, retained)
	cold := semantic.AnalyzeWithOptions(
		parseRetainedDisjointCacheSource(t, filename, source),
		semantic.AnalyzeOptions{},
	)
	coldIR := retainedDisjointIR(t, cold)
	if got, want := normalizeRetainedDisjointIR(retainedIR), normalizeRetainedDisjointIR(coldIR); got != want {
		t.Fatalf("retained LLVM IR differs from a cold analysis\nretained:\n%s\ncold:\n%s", got, want)
	}
	return retainedIR
}

func hasRetainedDisjointNoaliasStamp(ir string) bool {
	return strings.Contains(ir, "elisa.disjoint.axpy.aa") &&
		strings.Contains(ir, `"elt.p0"`) &&
		strings.Contains(ir, `"elt.p1"`)
}

func findRetainedDisjointFunctionName(parts int, occupied map[int]bool, prefix string) string {
	for i := 0; i < 10000; i++ {
		name := fmt.Sprintf("%s%d", prefix, i)
		if !occupied[partitionOwner(name, parts)] {
			return name
		}
	}
	return ""
}

func retainedDisjointCacheSource(callerName, unrelatedName, secondArgument string) string {
	return fmt.Sprintf(`@inline(never)
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

@inline(never)
def %s() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    for i in 0..<8:
        a.push(i.f64())
        b.push(i.f64())
    axpy(&a, %s)

@inline(never)
def %s() -> i64:
    return 17
`, callerName, secondArgument, unrelatedName)
}

func retainedDisjointPartitionMembers(t *testing.T, session *IncrementalObjectSession) [][]string {
	t.Helper()
	if session == nil || session.disk == nil {
		t.Fatal("disk object cache was not enabled")
	}
	paths, err := filepath.Glob(filepath.Join(session.disk.root, "*.schedule"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one persisted partition schedule, got %v (%v)", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || len(data) < 32 {
		t.Fatalf("read partition schedule: %v", err)
	}
	var members [][]string
	if err := json.Unmarshal(data[32:], &members); err != nil {
		t.Fatalf("decode partition schedule: %v", err)
	}
	if len(members) != retainedDisjointCachePartitions {
		t.Fatalf("partition schedule has %d rows, want %d", len(members), retainedDisjointCachePartitions)
	}
	return members
}

func retainedDisjointPartitionContains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func writeRetainedDisjointObject(t *testing.T, cacheRoot, outputRoot, revision string, result *semantic.Result) (*IncrementalObjectSession, IncrementalObjectStats) {
	t.Helper()
	session := NewIncrementalObjectSession(retainedDisjointCachePartitions, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(cacheRoot, retainedDisjointCacheIdentity, 128<<20)
	output := filepath.Join(outputRoot, revision+".o")
	if err := session.WriteObject(result, output, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatalf("%s WriteObject: %v", revision, err)
	}
	stats := session.Stats()
	if stats.SerialFallbacks != 0 {
		t.Fatalf("%s unexpectedly used serial object emission: %+v", revision, stats)
	}
	if got := stats.PartitionsEmitted + stats.PartitionsReused; got != uint64(retainedDisjointCachePartitions) {
		t.Fatalf("%s did not process all partition requests: %+v", revision, stats)
	}
	return session, stats
}

// A caller-only source edit changes the whole-program disjointness proof for axpy,
// although axpy's source body is byte-for-byte unchanged. The restored retained
// Result must remove and restore the noalias stamp; partition cache misses must be
// limited to axpy's and the caller's owners, with unrelated partitions reused.
func TestRetainedDisjointProofChangesNoaliasPartitionCache(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	t.Setenv("ELISACORE_PARALLEL_IR", "1")

	const filename = "retained-disjoint-noalias-cache.elisa"
	parts := retainedDisjointCachePartitions
	axpyPart := partitionOwner("axpy", parts)
	callerName := findRetainedDisjointFunctionName(parts, map[int]bool{axpyPart: true}, "caller")
	if callerName == "" {
		t.Fatal("could not choose a caller in a separate partition")
	}
	callerPart := partitionOwner(callerName, parts)
	occupied := map[int]bool{axpyPart: true, callerPart: true}
	unrelatedName := findRetainedDisjointFunctionName(parts, occupied, "unrelated")
	if unrelatedName == "" {
		t.Fatal("could not choose an unrelated function in a third partition")
	}
	unrelatedPart := partitionOwner(unrelatedName, parts)

	baseSource := retainedDisjointCacheSource(callerName, unrelatedName, "&b")
	aliasedSource := retainedDisjointCacheSource(callerName, unrelatedName, "&a")
	callerMarker := fmt.Sprintf("@inline(never)\ndef %s", callerName)
	if !strings.Contains(baseSource, callerMarker) || !strings.Contains(aliasedSource, callerMarker) {
		t.Fatal("generated caller declaration is missing")
	}
	if strings.SplitN(baseSource, callerMarker, 2)[0] != strings.SplitN(aliasedSource, callerMarker, 2)[0] {
		t.Fatal("the callee axpy source changed between revisions")
	}

	state, baseResult := newRetainedDisjointState(t, filename, baseSource)
	baseIR := assertRetainedDisjointColdIRParity(t, filename, baseSource, baseResult)
	if !hasRetainedDisjointNoaliasStamp(baseIR) {
		t.Fatal("distinct private-fresh caller buffers should stamp axpy's element accesses")
	}
	cacheRoot, outputRoot := t.TempDir(), t.TempDir()
	initialSession, initial := writeRetainedDisjointObject(t, cacheRoot, outputRoot, "base-initial", baseResult)
	members := retainedDisjointPartitionMembers(t, initialSession)
	if !retainedDisjointPartitionContains(members[axpyPart], "axpy") ||
		!retainedDisjointPartitionContains(members[callerPart], callerName) ||
		!retainedDisjointPartitionContains(members[unrelatedPart], unrelatedName) {
		t.Fatalf("test declarations do not occupy the intended partitions: axpy=%d caller=%d unrelated=%d members=%v", axpyPart, callerPart, unrelatedPart, members)
	}
	// Identical empty partitions may reuse a sibling published earlier in this
	// same cold build; the cache root itself was freshly created above.
	if initial.PartitionsEmitted == 0 {
		t.Fatalf("initial cache population did not emit cold partitions: %+v", initial)
	}

	_, repeat := writeRetainedDisjointObject(t, cacheRoot, outputRoot, "base-repeat", baseResult)
	if repeat.PartitionsEmitted != 0 || repeat.PartitionsReused != uint64(retainedDisjointCachePartitions) || repeat.DiskPartitionsReused == 0 {
		t.Fatalf("unchanged fresh session did not reuse cached partitions: %+v", repeat)
	}

	state, aliasResult := updateRetainedDisjointState(t, filename, aliasedSource, state)
	aliasIR := assertRetainedDisjointColdIRParity(t, filename, aliasedSource, aliasResult)
	if hasRetainedDisjointNoaliasStamp(aliasIR) {
		t.Fatal("a caller passing the same buffer twice must remove axpy's noalias stamp")
	}
	_, aliasStats := writeRetainedDisjointObject(t, cacheRoot, outputRoot, "aliased", aliasResult)
	if aliasStats.PartitionsEmitted != 2 || aliasStats.PartitionsReused != uint64(retainedDisjointCachePartitions-2) || aliasStats.DiskPartitionsReused == 0 {
		t.Fatalf("proof removal should miss only axpy/caller partitions and reuse unrelated ones: %+v (owners axpy=%d caller=%d unrelated=%d)", aliasStats, axpyPart, callerPart, unrelatedPart)
	}

	state, revertedResult := updateRetainedDisjointState(t, filename, baseSource, state)
	revertedIR := assertRetainedDisjointColdIRParity(t, filename, baseSource, revertedResult)
	if !hasRetainedDisjointNoaliasStamp(revertedIR) {
		t.Fatal("restoring the distinct-buffer caller should restore axpy's noalias stamp")
	}
	_, reverted := writeRetainedDisjointObject(t, cacheRoot, outputRoot, "base-reverted", revertedResult)
	if reverted.PartitionsEmitted != 0 || reverted.PartitionsReused != uint64(retainedDisjointCachePartitions) || reverted.DiskPartitionsReused == 0 {
		t.Fatalf("reverted proof did not reuse the original partition variants: %+v", reverted)
	}
}
