package semantic

import (
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

const storeScanParameterLoopSource = `global region source

struct Table:
    source_names: mutable darray[sview @source] = []
    names: mutable darray[sview] = []
    buffers: mutable darray[dstr] = []

def copy_source_names(table: mutable Table&, names: darray[sview @source]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for name in names |table|:
            table.source_names.push(name)

def copy_plain_names(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for name in names |table|:
            table.names.push(name)

def copy_buffers(table: mutable Table&, buffers: darray[dstr]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for buffer in buffers |table|:
            table.buffers.push(buffer)
`

func TestStoreScanUntypedParameterLoopElements(t *testing.T) {
	for _, tc := range []struct {
		name  string
		empty bool
	}{
		{"copy_source_names", true},
		{"copy_plain_names", false},
		{"copy_buffers", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_loop.elisa", storeScanParameterLoopSource, AnalyzeOptions{})
			storeScanUntyped(result)
			a := result.analyzer
			a.currentScope = a.globalScope
			fn := storeScanFindFunc(t, result, tc.name)
			if got := a.calleeStoreModeInto(fn, 1, 0); (got == storeNone) != tc.empty {
				t.Fatalf("store mode %v, want empty=%v", got, tc.empty)
			}
		})
	}
}

func BenchmarkStoreScanParameterLoopElements(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := lexer.New("param_loop_bench.elisa", []byte(storeScanParameterLoopSource))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("param_loop_bench.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid benchmark fixture")
		}
		result := AnalyzeWithOptions(file, AnalyzeOptions{})
		storeScanUntyped(result)
		a := result.analyzer
		a.currentScope = a.globalScope
		for fn := range a.funcDeclSymbols {
			_ = a.calleeStoreModeInto(fn, 1, 0)
		}
	}
}
