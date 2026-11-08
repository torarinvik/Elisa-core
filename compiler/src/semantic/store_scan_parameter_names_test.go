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

func BenchmarkStoreScanTaintManyCallArgs(b *testing.B) {
	var source strings.Builder
	source.WriteString("def keep(")
	for i := 0; i < 64; i++ {
		if i != 0 {
			source.WriteString(", ")
		}
		fmt.Fprintf(&source, "p%d: darray[u32]&", i)
	}
	source.WriteString(") -> darray[u32]&:\n    return p0\ndef probe(source: darray[u32]&):\n    local = keep(")
	for i := 0; i < 64; i++ {
		if i != 0 {
			source.WriteString(", ")
		}
		source.WriteString("source")
	}
	source.WriteString(")\n")
	l := lexer.New("taint_args.elisa", []byte(source.String()))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("taint_args.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid fixture")
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	a := result.analyzer
	var fn *ast.FuncDecl
	for decl := range a.funcDeclSymbols {
		if decl.Name == "probe" {
			fn = decl
		}
	}
	if fn == nil {
		b.Fatal("missing probe")
	}
	seen := map[storeScanKey]bool{}
	_, header, _, _, _ := a.storeScanTaint(fn, 0, "", seen)
	if !header["local"] {
		b.Fatalf("fixture did not taint local: %v", header)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.storeScanTaintCache = nil
		a.storeScanTaint(fn, 0, "", seen)
	}
}

func BenchmarkStoreScanViewLocalsCold(b *testing.B) {
	fields := make(map[string]Field, 32)
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("field%d", i)
		fields[name] = Field{Name: name, Type: &BuiltinType{Name: "u32"}}
	}
	fn := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "source"}}, Body: []ast.Stmt{&ast.ReturnStmt{}}}
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Type: &FuncType{Params: []Type{&StructType{Name: "source", Fields: fields}}}}}}
	if a.storeScanViewLocalsCached(fn, 0) == nil {
		b.Fatal("source not proven byte-free")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.storeScanViewCache = nil
		_ = a.storeScanViewLocalsCached(fn, 0)
	}
}
