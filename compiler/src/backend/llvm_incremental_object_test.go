//go:build cgo

package backend

import (
	"context"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func incrementalObjectFixture(functions int) string {
	var source strings.Builder
	for i := 0; i < functions; i++ {
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n    return value + %d\n\n", i, i+1)
	}
	fmt.Fprintf(&source, "def main() -> i64:\n    return work0(20) + work%d(1)\n", functions-1)
	return source.String()
}

func incrementalObjectNativeStatus(t *testing.T, object string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal("native qualification requires clang:", err)
	}
	executable := object + ".exe"
	if output, err := exec.CommandContext(ctx, clang, object, "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("native link: %v\n%s", err, output)
	}
	err = exec.CommandContext(ctx, executable).Run()
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	t.Fatal(err)
	return -1
}

func TestIncrementalObjectPartialReuseAndNativeParity(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	base := incrementalObjectFixture(32)
	edited := strings.Replace(base, "return value + 1\n", "return value + 2\n", 1)
	options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}
	dir := t.TempDir()
	for index, src := range []string{base, base, edited, "\n\n" + edited, base} {
		result := parseAndAnalyzeBackendTest(t, "native_edit.elisa", src)
		before := session.Stats()
		object := filepath.Join(dir, "retained.o")
		if err := session.WriteObject(result, object, options); err != nil {
			t.Fatal(err)
		}
		after := session.Stats()
		wantMiss := uint64(1)
		if index == 0 {
			wantMiss = 4
		}
		if index == 1 || index == 3 || index == 4 {
			wantMiss = 0
		}
		if got := after.PartitionsEmitted - before.PartitionsEmitted; got != wantMiss {
			t.Fatalf("revision %d emitted %d partitions, want %d; stats=%+v", index, got, wantMiss, after)
		}
		if got := after.PartitionsReused - before.PartitionsReused; got != 4-wantMiss {
			t.Fatalf("revision %d reused %d partitions", index, got)
		}
		fresh := filepath.Join(dir, "fresh.o")
		if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
			t.Fatal(err)
		}
		got, want := incrementalObjectNativeStatus(t, object), incrementalObjectNativeStatus(t, fresh)
		expected := 54
		if index == 2 || index == 3 {
			expected = 55
		}
		if got != want || got != expected {
			t.Fatalf("native status retained=%d fresh=%d expected=%d", got, want, expected)
		}
		if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
			t.Fatalf("unexpected fallback or memory overflow: %+v", after)
		}
	}
}

func TestIncrementalObjectGlobalAndTargetChanges(t *testing.T) {
	t.Setenv(TargetCPUEnv, "native")
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	base := "global mutable extra: i64 = 1\n" + incrementalObjectFixture(16)
	base = strings.Replace(base, "return value + 1\n", "can Global{Read}:\n        return value + extra\n", 1)
	base = strings.Replace(base, "    return work0", "    can Global{Read}:\n        return work0", 1)
	for index, src := range []string{base, strings.Replace(base, "extra: i64 = 1", "extra: i64 = 2", 1), base} {
		result := parseAndAnalyzeBackendTest(t, "global_edit.elisa", src)
		out := filepath.Join(t.TempDir(), "main.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		want := 38
		if index == 1 {
			want = 39
		}
		if got := incrementalObjectNativeStatus(t, out); got != want {
			t.Fatalf("global edit status %d, want %d", got, want)
		}
	}
	before := session.Stats()
	t.Setenv(TargetCPUEnv, "generic")
	result := parseAndAnalyzeBackendTest(t, "global_edit.elisa", base)
	if err := session.WriteObject(result, filepath.Join(t.TempDir(), "generic.o"), LLVMObjectEmitOptions{}); err != nil {
		t.Fatal(err)
	}
	if session.Stats().PartitionsEmitted == before.PartitionsEmitted {
		t.Fatal("changed target CPU reused every native partition")
	}
}

func TestIncrementalObjectBoundedRetentionAndFailedMerge(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1)
	session.minWeight = 0
	result := parseAndAnalyzeBackendTest(t, "bounded_object.elisa", incrementalObjectFixture(8))
	out := filepath.Join(t.TempDir(), "main.o")
	for i := 0; i < 2; i++ {
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if stats := session.Stats(); stats.RetainedBytes != 0 || stats.PartitionsReused != 0 {
		t.Fatalf("budget bypassed: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, out); got != 30 {
		t.Fatalf("nonretaining object status %d, want 30", got)
	}
	previous, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELISA_LD", filepath.Join(t.TempDir(), "missing-linker"))
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err == nil {
		t.Fatal("missing linker succeeded")
	}
	current, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(previous) {
		t.Fatal("failed merge replaced prior object")
	}
}

func BenchmarkIncrementalNativeObjectBodyEdit(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		term := fmt.Sprintf("(value%d * 2 + delta%d * 3)", i, i)
		fmt.Fprintf(&source, "def work%d(value%d: int, delta%d: int) -> int:\n    return %s\n\n", i, i, i, strings.Repeat(term+" + ", 15)+term)
	}
	base := source.String()
	// Only lowering/native generation is timed. Every edit is novel, so the
	// benchmark cannot hit a cache of alternating complete prior revisions.
	analyze := func(src string) *semantic.Result {
		l := lexer.New("object_bench.elisa", []byte(src))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("object_bench.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid benchmark source")
		}
		result := semantic.Analyze(f)
		if len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		return result
	}
	first := analyze(base)
	for _, retained := range []bool{false, true} {
		name := "fresh"
		if retained {
			name = "retained"
		}
		b.Run(name, func(b *testing.B) {
			session := NewIncrementalObjectSession(8, 0)
			path := filepath.Join(b.TempDir(), "main.o")
			options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}
			if retained {
				if err := session.WriteObject(first, path, options); err != nil {
					b.Fatal(err)
				}
			}
			before := session.Stats()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				result := analyze(strings.Replace(base, "value0 * 2", fmt.Sprintf("value0 * %d", i+4), 1))
				b.StartTimer()
				var err error
				if retained {
					err = session.WriteObject(result, path, options)
				} else {
					err = WriteLLVMObjectFileWithOptions(result, path, options)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after := session.Stats()
			if retained {
				for name, elapsed := range map[string]time.Duration{"ir-ms/edit": after.IRTime - before.IRTime, "partition-ms/edit": after.PartitionTime - before.PartitionTime, "native-ms/edit": after.NativeTime - before.NativeTime, "merge-ms/edit": after.MergeTime - before.MergeTime} {
					b.ReportMetric(float64(elapsed)/float64(time.Millisecond)/float64(b.N), name)
				}
				b.ReportMetric(float64(after.PartitionsEmitted-before.PartitionsEmitted)/float64(b.N), "emitted-parts/edit")
				b.ReportMetric(float64(after.PartitionsReused-before.PartitionsReused)/float64(b.N), "reused-parts/edit")
			}
		})
	}
}

func TestIncrementalObjectLanguageAndOptimizationParity(t *testing.T) {
	cases := []struct {
		name, source, old, new string
		before, after          int
	}{
		{"struct-loop", `struct Pair:
    left: i64
    right: i64

global pair: Pair = zeroed

def work(value: i64) -> i64:
    total: mutable i64 = 0
    for i in 0..<value:
        total <- total + i
    return total + 1

def main() -> i64:
    return work(3) + pair.right
`, "total + 1", "total + 2", 4, 5},
		{"string-match", `def choose(text: sview) -> i64:
    match text:
        "alpha":
            return 20
        _:
            return 1

def main() -> i64:
    return choose("alpha") + choose("other")
`, "return 20", "return 22", 21, 23},
		{"generic", `def identity[T](value: T) -> T:
    return value

def main() -> i64:
    return identity[i64](20) + 1
`, "(20) + 1", "(20) + 2", 21, 22},
		{"recursion", `def sum(value: i64) -> i64:
    if value == 0:
        return 0
    return value + sum(value - 1)

def main() -> i64:
    return sum(5) + 1
`, "sum(5)", "sum(6)", 16, 22},
	}
	for _, tc := range cases {
		for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel2, OptimizationLevel3} {
			t.Run(fmt.Sprintf("%s/O%d", tc.name, level), func(t *testing.T) {
				session := NewIncrementalObjectSession(4, 1<<20)
				session.minWeight = 0
				for revision, src := range []string{tc.source, strings.Replace(tc.source, tc.old, tc.new, 1), tc.source} {
					result := parseAndAnalyzeBackendTest(t, "language_edit.elisa", src)
					dir := t.TempDir()
					retained := filepath.Join(dir, "retained.o")
					fresh := filepath.Join(dir, "fresh.o")
					options := LLVMObjectEmitOptions{OptLevel: level}
					before := session.Stats()
					if err := session.WriteObject(result, retained, options); err != nil {
						t.Fatal(err)
					}
					if revision == 2 && session.Stats().PartitionsReused == before.PartitionsReused {
						t.Fatal("reverting to the original source did not exercise native cache reuse")
					}
					if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
						t.Fatal(err)
					}
					want := tc.before
					if revision == 1 {
						want = tc.after
					}
					if got := incrementalObjectNativeStatus(t, retained); got != want {
						t.Fatalf("retained status %d, want %d", got, want)
					}
					if got := incrementalObjectNativeStatus(t, fresh); got != want {
						t.Fatalf("fresh status %d, want %d", got, want)
					}
				}
			})
		}
	}
}

func TestIncrementalObjectSignatureAndCrossTarget(t *testing.T) {
	// Pins the module-split path, whose partitions all declare every function;
	// partitioned IR prunes unused declarations, so an unrelated partition may
	// correctly be reused (TestPartitionedIR* cover that path).
	t.Setenv("ELISACORE_PARALLEL_IR", "0")
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	source := incrementalObjectFixture(32)
	for _, src := range []string{source, strings.Replace(source, "work0(value: i64)", "work0(value: i32)", 1)} {
		result := parseAndAnalyzeBackendTest(t, "signature_edit.elisa", src)
		out := filepath.Join(t.TempDir(), "main.o")
		before := session.Stats()
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, out); got != 54 {
			t.Fatalf("signature edit status %d", got)
		}
		if session.Stats().PartitionsReused != before.PartitionsReused {
			t.Fatal("changed ABI reused a partition with stale declarations")
		}
	}
	native, err := ResolveTargetIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	cross := "x86_64-unknown-linux-gnu"
	if native.Triple == cross {
		cross = "aarch64-unknown-linux-gnu"
	}
	result := parseAndAnalyzeBackendTest(t, "cross_edit.elisa", source)
	out := filepath.Join(t.TempDir(), "cross.o")
	before := session.Stats()
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{TargetTriple: cross}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || string(data[:4]) != "\x7fELF" {
		t.Fatal("cross-target fallback did not produce ELF")
	}
	if after := session.Stats(); after.SerialFallbacks-before.SerialFallbacks != 1 || after.PartitionsReused != before.PartitionsReused {
		t.Fatalf("cross-target object entered host merger/cache: %+v", after)
	}
}

func TestIncrementalObjectDebugLocationsInvalidate(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	base := incrementalObjectFixture(16)
	for revision, source := range []string{base, base, "\n\n" + base} {
		result := parseAndAnalyzeBackendTest(t, "debug_edit.elisa", source)
		out := filepath.Join(t.TempDir(), "main.o")
		before := session.Stats()
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{DebugInfo: true}); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, out); got != 38 {
			t.Fatalf("debug native status %d", got)
		}
		after := session.Stats()
		if after.SerialFallbacks != 0 {
			t.Fatal("debug partition verification declined")
		}
		if revision == 1 && after.PartitionsReused-before.PartitionsReused != 4 {
			t.Fatal("unchanged debug output was not reused")
		}
		if revision == 2 && after.PartitionsEmitted == before.PartitionsEmitted {
			t.Fatal("relocated debug locations reused every stale partition")
		}
	}
}

func TestIncrementalObjectSmallModuleUsesDirectEmission(t *testing.T) {
	session := NewIncrementalObjectSession(0, 0)
	result := parseAndAnalyzeBackendTest(t, "small_native.elisa", "def main() -> i64:\n    return 42\n")
	out := filepath.Join(t.TempDir(), "small.o")
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, out); got != 42 {
		t.Fatalf("direct native status %d", got)
	}
	if stats := session.Stats(); stats.SerialFallbacks != 1 || stats.RetainedBytes != 0 || stats.PartitionsEmitted != 0 {
		t.Fatalf("tiny module paid for partitioning: %+v", stats)
	}
}

func BenchmarkIncrementalNativeObjectCold(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		term := fmt.Sprintf("(value%d * 2 + delta%d * 3)", i, i)
		fmt.Fprintf(&source, "def work%d(value%d: int, delta%d: int) -> int:\n    return %s\n\n", i, i, i, strings.Repeat(term+" + ", 15)+term)
	}
	l := lexer.New("cold_object.elisa", []byte(source.String()))
	p := parser.New(l.Tokenize())
	f := p.ParseFile("cold_object.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid cold workload")
	}
	result := semantic.Analyze(f)
	if len(result.Errors()) != 0 {
		b.Fatal(result.Errors())
	}
	out := filepath.Join(b.TempDir(), "cold.o")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := NewIncrementalObjectSession(8, 0)
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			b.Fatal(err)
		}
		if stats := session.Stats(); stats.PartitionsEmitted != 8 || stats.PartitionsReused != 0 {
			b.Fatalf("cold sample reused native code: %+v", stats)
		}
	}
}

func TestIncrementalObjectPrivateHelpersAcrossTranslationUnits(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	dir := t.TempDir()
	objects := make([]string, 2)
	for index, name := range []string{"left", "right"} {
		src := fmt.Sprintf(`@inline(never)
def helper(value: i64) -> i64:
    return value + %d

def %s(value: i64) -> i64:
    return helper(value)
export fn %s(value: i64) -> i64 = %s
`, index+1, name, name, name)
		result := parseAndAnalyzeBackendTest(t, filepath.Join(dir, name, "unit.elisa"), src)
		objects[index] = filepath.Join(dir, name+".o")
		if err := session.WriteObject(result, objects[index], LLVMObjectEmitOptions{OptLevel: OptimizationLevel2}); err != nil {
			t.Fatal(err)
		}
	}
	harness := filepath.Join(dir, "main.c")
	if err := os.WriteFile(harness, []byte("#include <stdint.h>\nextern int64_t left(int64_t), right(int64_t);\nint main(void) { return left(20) + right(10); }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "main")
	if out, err := exec.Command(clang, harness, objects[0], objects[1], "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("independent units collided: %v\n%s", err, out)
	}
	err = exec.Command(executable).Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 33 {
		t.Fatalf("private helper behavior crossed units: %v", err)
	}
}

func TestIncrementalObjectConcurrentMissesAndHits(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	source := incrementalObjectFixture(16)
	const workers = 4
	results := make([]*semantic.Result, workers)
	paths := make([]string, workers)
	for i := range results {
		results[i] = parseAndAnalyzeBackendTest(t, fmt.Sprintf("concurrent%d.elisa", i), source)
		paths[i] = filepath.Join(t.TempDir(), "main.o")
	}
	for round := 0; round < 2; round++ {
		before := session.Stats()
		errors := make(chan error, workers)
		for i := range results {
			go func(i int) { errors <- session.WriteObject(results[i], paths[i], LLVMObjectEmitOptions{}) }(i)
		}
		for range results {
			if err := <-errors; err != nil {
				t.Fatal(err)
			}
		}
		after := session.Stats()
		if round == 0 && after.PartitionsEmitted-before.PartitionsEmitted != workers*4 {
			t.Fatalf("distinct cold units conflated: %+v", after)
		}
		if round == 1 && (after.PartitionsEmitted != before.PartitionsEmitted || after.PartitionsReused-before.PartitionsReused != workers*4) {
			t.Fatalf("concurrent warm cache mismatch: %+v", after)
		}
		for _, path := range paths {
			if got := incrementalObjectNativeStatus(t, path); got != 38 {
				t.Fatalf("concurrent native status %d", got)
			}
		}
	}
	// Simultaneous identical misses may duplicate emission, but retention/accounting
	// must converge to one entry per exact partition without returning stale code.
	identical := NewIncrementalObjectSession(4, 1<<20)
	identical.minWeight = 0
	results = results[:2]
	for i := range results {
		results[i] = parseAndAnalyzeBackendTest(t, "same_concurrent.elisa", source)
	}
	errors := make(chan error, 2)
	for i := range results {
		go func(i int) { errors <- identical.WriteObject(results[i], paths[i], LLVMObjectEmitOptions{}) }(i)
	}
	for range results {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	stats := identical.Stats()
	if stats.PartitionsEmitted+stats.PartitionsReused != 8 || stats.RetainedPartitions != 4 || stats.RetainedBytes > 1<<20 {
		t.Fatalf("concurrent duplicate admission broke ownership: %+v", stats)
	}
}

func TestIncrementalObjectVirtualUnitsUseDirectEmission(t *testing.T) {
	for _, filename := range []string{"", "-", "<memory>"} {
		session := NewIncrementalObjectSession(4, 1<<20)
		session.minWeight = 0
		result := parseAndAnalyzeBackendTest(t, filename, incrementalObjectFixture(16))
		out := filepath.Join(t.TempDir(), "main.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		if stats := session.Stats(); stats.SerialFallbacks != 1 || stats.RetainedBytes != 0 {
			t.Fatalf("virtual unit acquired a private-symbol namespace: %+v", stats)
		}
		if got := incrementalObjectNativeStatus(t, out); got != 38 {
			t.Fatalf("virtual unit status %d", got)
		}
	}
}

func TestIncrementalObjectPreservesPrivateConstantAddress(t *testing.T) {
	source := `const XS = [10, 20, 30]

def left(index: usize) -> usize:
    return (&XS[index]).cast[usize]

def right(index: usize) -> usize:
    return (&XS[index]).cast[usize]

def main() -> i64:
    if left(1) == right(1):
        return 42
    return 1
`
	var spacers strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&spacers, "def spacer%d() -> i64:\n    return %d\n\n", i, i)
	}
	source = strings.Replace(source, "def second", spacers.String()+"def second", 1)
	result := parseAndAnalyzeBackendTest(t, "constant_address.elisa", source)
	session := NewIncrementalObjectSession(2, 1<<20)
	session.minWeight = 0
	out := filepath.Join(t.TempDir(), "main.o")
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
		t.Fatal(err)
	}
	assertNativeFunctionsInDifferentShards(t, session, "left", "right")
	if got := incrementalObjectNativeStatus(t, out); got != 42 {
		t.Fatalf("one private constant acquired different partition addresses: %d", got)
	}
	if session.Stats().SerialFallbacks != 0 || session.Stats().PartitionsEmitted != 2 {
		t.Fatal("address-bearing global did not partition")
	}
}
