package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

// Isolate the post-analysis nested-statement search from parsing and typing.
// A return expression contains no nested statement lists, but its interface
// children must still be checked in case a lambda or block is present.
func BenchmarkIncrementalStatementListWalk(b *testing.B) {
	source := "def work(value: i64) -> i64:\n    return " + strings.Repeat("(value + 1) + ", 255) + "(value + 1)\n"
	l := lexer.New("statement_walk.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("statement_walk.elisa")
	if len(l.Errors())+len(p.Errors()) != 0 {
		b.Fatal("invalid statement-walk fixture")
	}
	stmt := file.Decls[0].(*ast.FuncDecl).Body[0]
	a := &Analyzer{}
	a.descendStmtLists(stmt, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.descendStmtLists(stmt, nil)
	}
}
