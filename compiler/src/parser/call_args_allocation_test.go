package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestMixedCallArgumentMetadataPreservesOrder(t *testing.T) {
	source := "def f():\n    consume(a, second: b, c)\n"
	l := lexer.New("mixed.elisa", []byte(source))
	p := New(l.Tokenize())
	file := p.ParseFile("mixed.elisa")
	recordParserParityCase(t, source, len(p.Errors()), len(p.Notices()))
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		t.Fatal(l.Errors(), p.Errors())
	}
	call := file.Decls[0].(*ast.FuncDecl).Body[0].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	if len(call.ArgNames) != 3 || call.ArgNames[0] != "" || call.ArgNames[1] != "second" || call.ArgNames[2] != "" || len(call.ArgShorthand) != 3 || len(call.ArgItemOrder) != 3 {
		t.Fatal("mixed argument metadata changed")
	}
	for i, item := range call.ArgItemOrder {
		if item.ArgIndex != i || item.Position != call.Args[i].Pos() {
			t.Fatal("argument order or position changed")
		}
	}
}

func BenchmarkParsePositionalCalls(b *testing.B) {
	source := "def f():\n" + strings.Repeat("    consume(a, b, c)\n", 1000)
	l := lexer.New("calls.elisa", []byte(source))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		b.Fatal(l.Errors())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := New(tokens)
		p.ParseFile("calls.elisa")
		if len(p.Errors()) != 0 {
			b.Fatal(p.Errors())
		}
	}
}

func BenchmarkParseExplicitStructFields(b *testing.B) {
	source := "def f():\n" + strings.Repeat("    value = Point{x: a, y: b}\n", 1000)
	l := lexer.New("fields.elisa", []byte(source))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		b.Fatal(l.Errors())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := New(tokens)
		p.ParseFile("fields.elisa")
		if len(p.Errors()) != 0 {
			b.Fatal(p.Errors())
		}
	}
}
