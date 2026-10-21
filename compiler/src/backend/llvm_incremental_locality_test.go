//go:build cgo

package backend

import (
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

func nativeLocalityFixture(terms int) string {
	var source strings.Builder
	for i := 0; i < 128; i++ {
		count := 32
		if i == 0 {
			count = terms
		}
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n    return %s\n\n", i, strings.Repeat("(value + 1) + ", count-1)+"(value + 1)")
	}
	source.WriteString("def main() -> i64:\n    return work0(0) + work127(0)\n")
	return source.String()
}

func TestIncrementalObjectEditLocality(t *testing.T) {
	base := nativeLocalityFixture(32)
	extra := "def inserted(value: i64) -> i64:\n    return value + 9\n\n"
	for _, tc := range []struct {
		name, source string
		status       int
	}{
		{"modest-growth", nativeLocalityFixture(64), 96},
		{"large-growth", nativeLocalityFixture(320), 96},
		{"front-insertion", extra + base, 64},
		{"middle-insertion", strings.Replace(base, "def work64", extra+"def work64", 1), 64},
		{"removal", strings.Replace(base, "def work64(value: i64) -> i64:\n    return "+strings.Repeat("(value + 1) + ", 31)+"(value + 1)\n\n", "", 1), 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewIncrementalObjectSession(8, 0)
			out := filepath.Join(t.TempDir(), "main.o")
			if err := session.WriteObject(parseAndAnalyzeBackendTest(t, "locality.elisa", base), out, LLVMObjectEmitOptions{}); err != nil {
				t.Fatal(err)
			}
			before := session.Stats()
			if err := session.WriteObject(parseAndAnalyzeBackendTest(t, "locality.elisa", tc.source), out, LLVMObjectEmitOptions{}); err != nil {
				t.Fatal(err)
			}
			after := session.Stats()
			t.Logf("emitted=%d reused=%d", after.PartitionsEmitted-before.PartitionsEmitted, after.PartitionsReused-before.PartitionsReused)
			if after.PartitionsEmitted-before.PartitionsEmitted != 1 || after.PartitionsReused-before.PartitionsReused != 7 {
				t.Fatal("independent edit invalidated unrelated native shards")
			}
			if got := incrementalObjectNativeStatus(t, out); got != tc.status {
				t.Fatalf("native status %d, want %d", got, tc.status)
			}
		})
	}
}

// Inspect emitted artifacts rather than reimplementing the ownership hash. This
// keeps the pointer-identity regression explicitly cross-shard as policy evolves.
func assertNativeFunctionsInDifferentShards(t *testing.T, session *IncrementalObjectSession, first, second string) {
	t.Helper()
	owners := map[string]int{}
	index := 0
	for _, entry := range session.entries {
		path := filepath.Join(t.TempDir(), "part.o")
		if err := os.WriteFile(path, entry.object, 0600); err != nil {
			t.Fatal(err)
		}
		symbols, err := exec.Command("nm", "-g", path).CombinedOutput()
		if err != nil {
			t.Fatalf("inspect native shard: %v: %s", err, symbols)
		}
		for _, line := range strings.Split(string(symbols), "\n") {
			columns := strings.Fields(line)
			if len(columns) < 2 || (columns[len(columns)-2] != "T" && columns[len(columns)-2] != "t") {
				continue
			}
			name := strings.TrimPrefix(columns[len(columns)-1], "_")
			if name == first || name == second {
				owners[name] = index
			}
		}
		index++
	}
	a, okA := owners[first]
	b, okB := owners[second]
	if !okA || !okB || a == b {
		t.Fatalf("fixture must span native shards: %v", owners)
	}
}

func BenchmarkIncrementalNativeStructuralEdit(b *testing.B) {
	base := nativeLocalityFixture(32)
	analyze := func(source string) *semantic.Result {
		l := lexer.New("structural_bench.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("structural_bench.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid structural edit")
		}
		result := semantic.Analyze(file)
		if len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		return result
	}
	for _, kind := range []string{"growth", "insertion"} {
		b.Run(kind, func(b *testing.B) {
			session := NewIncrementalObjectSession(8, 0)
			out := filepath.Join(b.TempDir(), "main.o")
			if err := session.WriteObject(analyze(base), out, LLVMObjectEmitOptions{}); err != nil {
				b.Fatal(err)
			}
			before := session.Stats()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				source := nativeLocalityFixture(64 + i*16)
				if kind == "insertion" {
					extra := fmt.Sprintf("def inserted%d(value: i64) -> i64:\n    return value + 9\n\n", i)
					source = strings.Replace(base, "def work64", extra+"def work64", 1)
				}
				current := analyze(source)
				b.StartTimer()
				if err := session.WriteObject(current, out, LLVMObjectEmitOptions{}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after := session.Stats()
			for name, elapsed := range map[string]time.Duration{"ir-ms/edit": after.IRTime - before.IRTime, "owner-map-ms/edit": after.OwnerMapTime - before.OwnerMapTime, "partition-ms/edit": after.PartitionTime - before.PartitionTime, "clone-ms/edit": after.CloneTime - before.CloneTime, "bitcode-ms/edit": after.BitcodeTime - before.BitcodeTime, "native-ms/edit": after.NativeTime - before.NativeTime, "merge-ms/edit": after.MergeTime - before.MergeTime} {
				b.ReportMetric(float64(elapsed)/float64(time.Millisecond)/float64(b.N), name)
			}
			b.ReportMetric(float64(after.OwnerMapBuilds-before.OwnerMapBuilds)/float64(b.N), "owner-map-builds/edit")
			b.ReportMetric(float64(after.PartitionsEmitted-before.PartitionsEmitted)/float64(b.N), "emitted-parts/edit")
			b.ReportMetric(float64(after.PartitionsReused-before.PartitionsReused)/float64(b.N), "reused-parts/edit")
		})
	}
}

func TestIncrementalObjectRetainsUsedCalleeABI(t *testing.T) {
	// Pins the module-split path: its owner map places main and work0 apart.
	t.Setenv("ELISACORE_PARALLEL_IR", "0")
	base := incrementalObjectFixture(32)
	base = strings.Replace(base, "work0(value: i64)", "work0(value: i32)", 1)
	base = base[:strings.Index(base, "def main()")] + `def main() -> i64:
    if work0(-1) == 0:
        return 42
    return 1
`
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	out := filepath.Join(t.TempDir(), "main.o")
	for revision, source := range []string{base, strings.Replace(base, "work0(value: i32)", "work0(value: i64)", 1)} {
		before := session.Stats()
		if err := session.WriteObject(parseAndAnalyzeBackendTest(t, "callee_abi.elisa", source), out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		if revision == 0 {
			assertNativeFunctionsInDifferentShards(t, session, "work0", "main")
		}
		// The unchanged caller must change its argument lowering when the callee
		// widens. Replaying its old i32 call can zero-extend -1 into a positive i64.
		if got := incrementalObjectNativeStatus(t, out); got != 42 {
			t.Fatalf("stale referenced ABI at revision %d: %d", revision, got)
		}
		if revision == 1 && session.Stats().PartitionsEmitted == before.PartitionsEmitted {
			t.Fatal("live ABI dependency was pruned")
		}
	}
}
