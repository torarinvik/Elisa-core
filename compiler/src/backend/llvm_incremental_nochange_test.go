//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"os"
	"path/filepath"
	"testing"
)

func TestIncrementalMergedObjectReuse(t *testing.T) {
	session := NewIncrementalObjectSession(8, 0)
	result := parseAndAnalyzeBackendTest(t, "nochange.elisa", nativeLocalityFixture(32))
	output := filepath.Join(t.TempDir(), "main.o")
	if err := session.WriteObject(result, output, LLVMObjectEmitOptions{}); err != nil {
		t.Fatal(err)
	}
	before := session.Stats()
	if before.RetainedMergedObjects != 1 {
		t.Fatal("host merged artifact not retained")
	}
	if err := os.WriteFile(output, []byte("stale output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := session.WriteObject(result, output, LLVMObjectEmitOptions{}); err != nil {
		t.Fatal(err)
	}
	after := session.Stats()
	if after.MergedObjectsReused-before.MergedObjectsReused != 1 || after.CloneTime != before.CloneTime || after.BitcodeTime != before.BitcodeTime || after.MergeTime != before.MergeTime || after.PartitionsEmitted != before.PartitionsEmitted {
		t.Fatalf("unchanged module repeated partition work: before=%+v after=%+v", before, after)
	}
	if got := incrementalObjectNativeStatus(t, output); got != 64 {
		t.Fatalf("native status %d", got)
	}
	// A new merger selection must run, even after a full-module cache hit.
	t.Setenv("ELISA_LD", filepath.Join(t.TempDir(), "missing-linker"))
	if err := session.WriteObject(result, output, LLVMObjectEmitOptions{}); err == nil {
		t.Fatal("cached output hid changed merger failure")
	}
	if got := incrementalObjectNativeStatus(t, output); got != 64 {
		t.Fatal("failed merger replaced previous output")
	}
}

func BenchmarkIncrementalNativeUnchanged(b *testing.B) {
	session := NewIncrementalObjectSession(8, 0)
	l := lexer.New("nochange_bench.elisa", []byte(nativeLocalityFixture(32)))
	p := parser.New(l.Tokenize())
	result := semantic.Analyze(p.ParseFile("nochange_bench.elisa"))
	if len(l.Errors())+len(p.Errors())+len(result.Errors()) != 0 {
		b.Fatal("invalid no-change fixture")
	}
	output := filepath.Join(b.TempDir(), "main.o")
	if err := session.WriteObject(result, output, LLVMObjectEmitOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := session.WriteObject(result, output, LLVMObjectEmitOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
