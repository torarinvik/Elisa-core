package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStoreScanTaintParameterNamesAcrossPairs(t *testing.T) {
	const source = `def probe(first: i64, second: i64, third: i64) -> void:
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "taint_params.elisa", source, AnalyzeOptions{})
	fn := storeScanFindFunc(t, result, "probe")
	a := result.analyzer
	want := map[string]bool{"first": true, "second": true, "third": true}
	for i, param := range fn.Params {
		for _, target := range fn.Params {
			params, header, content, targets, _ := a.storeScanTaint(fn, i, target.Name, map[storeScanKey]bool{})
			if !reflect.DeepEqual(params, want) {
				t.Fatalf("parameter names changed: %#v", params)
			}
			if !header[param.Name] || !content[param.Name] || !targets[target.Name] {
				t.Fatal("query-specific taint seeds changed")
			}
		}
	}
}

func BenchmarkStoreScanTaintParameterPairs(b *testing.B) {
	var source strings.Builder
	source.WriteString("def probe(")
	for i := 0; i < 18; i++ {
		if i != 0 {
			source.WriteString(", ")
		}
		fmt.Fprintf(&source, "p%d: i64", i)
	}
	source.WriteString(") -> void:\n    return\n")
	l := lexer.New("taint_pairs.elisa", []byte(source.String()))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("taint_pairs.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal(l.Errors(), p.Errors())
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	a := &Analyzer{}
	a.storeScanTaint(fn, 0, "p1", map[storeScanKey]bool{})
	seen := map[storeScanKey]bool{}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		a.storeScanTaintCache = nil
		for i := range fn.Params {
			for _, target := range fn.Params {
				a.storeScanTaint(fn, i, target.Name, seen)
			}
		}
	}
}
