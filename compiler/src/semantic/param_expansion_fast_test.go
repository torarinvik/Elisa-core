package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func BenchmarkExpandedFuncDeclParams(b *testing.B) {
	fn := &ast.FuncDecl{Name: "f", Params: make([]ast.ParamDecl, 12)}
	for i := range fn.Params {
		fn.Params[i].Name = fmt.Sprintf("p%d", i)
	}
	a := &Analyzer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(a.expandedFuncDeclParams(fn)) != len(fn.Params) {
			b.Fatal("lost parameters")
		}
	}
}

func BenchmarkReadOnlyExpandedFuncDeclParams(b *testing.B) {
	fn := &ast.FuncDecl{Name: "f", Params: make([]ast.ParamDecl, 12)}
	for i := range fn.Params {
		fn.Params[i].Name = fmt.Sprintf("p%d", i)
	}
	a := &Analyzer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(a.readOnlyExpandedFuncDeclParams(fn)) != len(fn.Params) {
			b.Fatal("lost parameters")
		}
	}
}

func BenchmarkOrdinaryFunctionSignature(b *testing.B) {
	source := []byte("def f(a: i64, b: i64, c: i64, d: i64) -> i64:\n    return a\n")
	l := lexer.New("signature.elisa", source)
	p := parser.New(l.Tokenize())
	file := p.ParseFile("signature.elisa")
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 || len(result.Errors()) != 0 {
		b.Fatal("invalid fixture")
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	a := result.analyzer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ft := a.funcTypeFromDeclWithFrame(fn.Name, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fn.Params, fn.ReturnType, false, false)
		if len(ft.Params) != len(fn.Params) {
			b.Fatal("lost parameters")
		}
	}
}

func TestExpandedFuncDeclParamsOwnsResult(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x"}, {Name: "y"}}}
	params := (&Analyzer{}).expandedFuncDeclParams(fn)
	params[0].Name = "changed"
	if fn.Params[0].Name != "x" {
		t.Fatal("expanded parameter list aliases declaration")
	}
}

func TestReadOnlyExpandedParamsMatchMalformedFallback(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x"}, {Name: ""}, {Name: "x"}, {Name: "y"}}}
	borrower, original := &Analyzer{}, &Analyzer{}
	got := borrower.readOnlyExpandedFuncDeclParams(fn)
	want := original.expandedFuncDeclParams(fn)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(borrower.diagnostics, original.diagnostics) {
		t.Fatal("read-only expansion changed filtering or diagnostics")
	}
}

func TestOrdinaryFunctionSignatureDoesNotRetainParamSlice(t *testing.T) {
	source := []byte("def f(a: i64, b: i64) -> i64:\n    return a\n")
	l := lexer.New("signature.elisa", source)
	p := parser.New(l.Tokenize())
	file := p.ParseFile("signature.elisa")
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 || len(result.Errors()) != 0 {
		t.Fatal("invalid fixture")
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	ft := result.analyzer.funcTypeFromDeclWithFrame(fn.Name, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fn.Params, fn.ReturnType, false, false)
	for i := range fn.Params {
		if funcTypeExplicitParamHasDefault(ft, i) || funcTypeExplicitParamDefaultExpr(ft, i) != nil {
			t.Fatal("ordinary parameter unexpectedly has a default")
		}
	}
	fn.Params[0].Name = "changed"
	if ft.ExplicitParamNames[0] != "a" || ft.SpecSignature.Params[0].SourceName != "a" {
		t.Fatal("signature retained parameter slice")
	}
}

func BenchmarkEmptyCallContracts(b *testing.B) {
	a := &Analyzer{}
	call := &ast.CallExpr{}
	params := []ast.ParamDecl{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}
	args := []ast.Expr{&ast.Ident{Name: "a"}, &ast.Ident{Name: "b"}, &ast.Ident{Name: "c"}, &ast.Ident{Name: "d"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.checkCalleeRequires(call, "f", nil, params, args, nil)
		a.checkCalleeParamWhereRefinements(call, "f", params, args)
	}
}

func TestFunctionSignatureOwnsGenericParameters(t *testing.T) {
	source := []byte("def f(a: i64) -> i64:\n    return a\n")
	l := lexer.New("signature.elisa", source)
	p := parser.New(l.Tokenize())
	file := p.ParseFile("signature.elisa")
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 || len(result.Errors()) != 0 {
		t.Fatal("invalid fixture")
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	generic := []ast.GenericParam{{Name: "T", Kind: ast.GenericParamType}}
	ft := result.analyzer.funcTypeFromDeclWithFrame(fn.Name, nil, generic, nil, nil, nil, nil, nil, nil, nil, nil, fn.Params, fn.ReturnType, false, false)
	generic[0].Name = "changed"
	if len(ft.GenericParams) != 1 || ft.GenericParams[0].Name != "T" {
		t.Fatal("signature retained declaration parameter slice")
	}
	ft.GenericParams[0].Name = "own"
	if generic[0].Name != "changed" {
		t.Fatal("signature mutated declaration parameter slice")
	}
}
