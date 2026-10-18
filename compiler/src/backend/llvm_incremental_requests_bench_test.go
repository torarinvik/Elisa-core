//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Includes lowering, partition preparation, cloning, exact bitcode
// keys, native generation and merging. Parsing and semantic analysis are untimed.
func nativeRequestFixture(revision uint64) string {
	// Vary one fixed-width literal, not the number of operations in work0.
	// Revision zero is the prewarm; every timed revision remains novel.
	return strings.Replace(nativeLocalityFixture(32), "(value + 1)", fmt.Sprintf("(value + 0x%016x)", revision+1), 1)
}

func BenchmarkIncrementalNativeRequests(b *testing.B) {
	base := nativeRequestFixture(0)
	analyze := func(source string) *semantic.Result {
		l := lexer.New("clone_plan_bench.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("clone_plan_bench.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("parse")
		}
		result := semantic.Analyze(file)
		if len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		return result
	}
	for _, edited := range []bool{false, true} {
		name := "unchanged"
		if edited {
			name = "body-edit"
		}
		b.Run(name, func(b *testing.B) {
			session := NewIncrementalObjectSession(8, 0)
			out := filepath.Join(b.TempDir(), "main.o")
			options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}
			if err := session.WriteObject(analyze(base), out, options); err != nil {
				b.Fatal(err)
			}
			before := session.Stats()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				source := base
				if edited {
					source = nativeRequestFixture(uint64(i) + 1)
				}
				result := analyze(source)
				b.StartTimer()
				if err := session.WriteObject(result, out, options); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after := session.Stats()
			for name, elapsed := range map[string]time.Duration{"ir-ms/edit": after.IRTime - before.IRTime, "owner-map-ms/edit": after.OwnerMapTime - before.OwnerMapTime, "clone-ms/edit": after.CloneTime - before.CloneTime, "bitcode-ms/edit": after.BitcodeTime - before.BitcodeTime, "partition-ms/edit": after.PartitionTime - before.PartitionTime, "native-ms/edit": after.NativeTime - before.NativeTime, "merge-ms/edit": after.MergeTime - before.MergeTime} {
				b.ReportMetric(float64(elapsed)/float64(time.Millisecond)/float64(b.N), name)
			}
			emitted := after.PartitionsEmitted - before.PartitionsEmitted
			reused := after.PartitionsReused - before.PartitionsReused
			want := uint64(0)
			if edited {
				want = uint64(b.N)
			}
			if emitted != want || reused != uint64(8*b.N)-want {
				b.Fatalf("cache locality changed: emitted=%d reused=%d", emitted, reused)
			}
			b.ReportMetric(float64(emitted)/float64(b.N), "emitted-parts/edit")
			b.ReportMetric(float64(reused)/float64(b.N), "reused-parts/edit")
		})
	}
}
