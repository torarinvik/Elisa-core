package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkStoreScanBodyWithoutWrites(b *testing.B) {
	source := "struct Table:\n    names: mutable darray[sview] = []\ndef scan(table: mutable Table&, names: darray[sview]&) -> i64:\n" + strings.Repeat("    if true:\n        pass\n", 128) + "    return 0\n"
	l := lexer.New("no_writes.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("no_writes.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid benchmark fixture")
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	a := result.analyzer
	var fn *ast.FuncDecl
	for decl := range a.funcDeclSymbols {
		if decl.Name == "scan" {
			fn = decl
		}
	}
	if fn == nil {
		b.Fatal("missing scan declaration")
	}
	if a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}) != storeNone {
		b.Fatal("unexpected store")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}) != storeNone {
			b.Fatal("unexpected store")
		}
	}
}

func BenchmarkStoreScanCallsWithoutTarget(b *testing.B) {
	source := "struct Table:\n    names: mutable darray[sview] = []\ndef observe(names: darray[sview]&):\n    pass\ndef scan(table: mutable Table&, names: darray[sview]&):\n" + strings.Repeat("    observe(names)\n", 128)
	l := lexer.New("untargeted_calls.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("untargeted_calls.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid benchmark fixture")
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	storeScanUntyped(result)
	a := result.analyzer
	a.currentScope = a.globalScope
	var fn *ast.FuncDecl
	for decl := range a.funcDeclSymbols {
		if decl.Name == "scan" {
			fn = decl
		}
	}
	if fn == nil {
		b.Fatal("missing scan declaration")
	}
	// Warm declaration indexes and taint facts: measure the directed node scan,
	// which is repeated for different source/target queries in recursive graphs.
	if mode := a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}); mode != storeNone {
		b.Fatal(mode)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if mode := a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}); mode != storeNone {
			b.Fatal(mode)
		}
	}
}

func BenchmarkStoreScanTypedAggregateArguments(b *testing.B) {
	source := "struct Table:\n    names: mutable darray[sview] = []\nenum Payload:\n"
	for i := 0; i < 64; i++ {
		source += fmt.Sprintf("    Names%d(values: darray[sview])\n", i)
	}
	source += "def observe(table: mutable Table&, value: Payload&):\n    pass\ndef scan(table: mutable Table&, value: Payload&):\n" + strings.Repeat("    observe(table, value)\n", 128)
	l := lexer.New("typed_store_calls.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("typed_store_calls.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid benchmark fixture")
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	var fn *ast.FuncDecl
	for decl := range a.funcDeclSymbols {
		if decl.Name == "scan" {
			fn = decl
		}
	}
	if fn == nil {
		b.Fatal("missing scan declaration")
	}
	if mode := a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}); mode != storeNone {
		b.Fatal(mode)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if mode := a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}); mode != storeNone {
			b.Fatal(mode)
		}
	}
}

func BenchmarkStoreScanAggregateHeader(b *testing.B) {
	args := make([]ast.Expr, 128)
	for i := range args {
		args[i] = &ast.Ident{Name: "source"}
	}
	fn := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "target"}, {Name: "source"}}, Body: []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "target"}, Value: &ast.StructLitExpr{Args: args}}}}
	a := &Analyzer{}
	seen := map[storeScanKey]bool{}
	if mode := a.calleeStoreModeIntoBody(fn, 1, 0, seen); mode != storeHeader {
		b.Fatalf("got %v", mode)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if a.calleeStoreModeIntoBody(fn, 1, 0, seen) != storeHeader {
			b.Fatal("incorrect mode")
		}
	}
}

func BenchmarkStoreScanGrowthHeader(b *testing.B) {
	receiver := &ast.Ident{Name: "source"}
	args := make([]ast.Expr, 128)
	for i := range args {
		args[i] = &ast.Ident{Name: "source"}
	}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: receiver, Field: "push"}, Args: args}
	fn := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "target"}, {Name: "source"}}, Body: []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "target"}, Value: call}}}
	a := &Analyzer{exprTypes: map[ast.Expr]Type{receiver: &DArrayType{Elem: &BuiltinType{Name: "u32"}}}}
	seen := map[storeScanKey]bool{}
	if mode := a.calleeStoreModeIntoBody(fn, 1, 0, seen); mode != storeHeader {
		b.Fatalf("got %v", mode)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if a.calleeStoreModeIntoBody(fn, 1, 0, seen) != storeHeader {
			b.Fatal("incorrect mode")
		}
	}
}

func BenchmarkStoreScanManyReturns(b *testing.B) {
	fn := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "source"}}}
	for i := 0; i < 256; i++ {
		fn.Body = append(fn.Body, &ast.ReturnStmt{Value: &ast.Ident{Name: "source"}})
	}
	a := &Analyzer{}
	seen := map[storeScanKey]bool{}
	if mode := a.calleeReturnModeOf(fn, 0, seen); mode != storeHeader {
		b.Fatalf("got %v", mode)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		delete(a.storeScanCache, storeScanKey{fn, 0, -1})
		if a.calleeReturnModeOf(fn, 0, seen) != storeHeader {
			b.Fatal("incorrect mode")
		}
	}
}
