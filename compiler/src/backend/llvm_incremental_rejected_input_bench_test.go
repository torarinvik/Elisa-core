//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"path/filepath"
	"testing"
)

// A nonretaining budget isolates object generation when exact bitcode snapshots
// cannot be admitted. Parsing and analysis are outside the timed region.
func BenchmarkIncrementalObjectRejectedInput(b *testing.B) {
	l := lexer.New("rejected_input.elisa", []byte(incrementalObjectFixture(128)))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("rejected_input.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid fixture")
	}
	result := semantic.Analyze(file)
	if len(result.Errors()) != 0 {
		b.Fatal(result.Errors())
	}
	session := NewIncrementalObjectSession(4, 1)
	session.minWeight = 0
	output := filepath.Join(b.TempDir(), "main.o")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := session.WriteObject(result, output, LLVMObjectEmitOptions{}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if stats := session.Stats(); stats.RetainedBytes != 0 || stats.RetainedPartitions != 0 || stats.PartitionsReused != 0 || stats.SerialFallbacks != 0 {
		b.Fatalf("unexpected cache state: %+v", stats)
	}
}
