package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"reflect"
	"strings"
	"testing"
)

func TestParallelPristineSnapshotsRebuildRecursivePatternResolution(t *testing.T) {
	source := []byte(`enum Payload:
    Text(value: sview)
    Empty

def left(item: Payload, depth: i64) -> sview:
    match item:
        Payload.Text(value):
            return value
        Payload.Empty:
            return right(item, depth - 1)

def right(item: Payload, depth: i64) -> sview:
    if depth == 0:
        return ""
    return left(item, depth - 1)

def caller(item: Payload) -> sview:
    return left(item, 3)
`)
	analyze := func(workers string) ([]ast.Decl, string) {
		t.Setenv("ELISA_PARALLEL_BODIES", workers)
		lex := lexer.New("recursive_patterns.elisa", source)
		tokens := lex.Tokenize()
		p := parser.New(tokens)
		file := p.ParseFile("recursive_patterns.elisa")
		if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
			t.Fatalf("lex %v; parse %v", lex.Errors(), p.Errors())
		}
		result := AnalyzeWithOptions(file, AnalyzeOptions{})
		if len(result.Errors()) != 0 {
			t.Fatalf("workers %s: %v", workers, result.Errors())
		}
		fn := file.Decls[1].(*ast.FuncDecl)
		pattern := fn.Body[0].(*ast.MatchStmt).Arms[0].Pattern.(*ast.MatchVariantPattern)
		if len(pattern.ResolvedArgs) != 1 || pattern.ResolvedArgs[0] != &pattern.Args[0] {
			t.Fatal("resolved pattern arguments lost their owning source element")
		}
		return file.Decls, strings.Join(result.Notices(), "\n")
	}
	want, notices := analyze("1")
	for _, chunk := range []string{"1", "4", "8"} {
		t.Setenv("ELISA_PARALLEL_SCC_CHUNK", chunk)
		for _, workers := range []string{"2", "3", "4"} {
			for i := 0; i < 5; i++ {
				got, actualNotices := analyze(workers)
				if !reflect.DeepEqual(want, got) || notices != actualNotices {
					t.Fatalf("workers %s, chunk %s: lowered AST or notices differ from serial", workers, chunk)
				}
			}
		}
	}
}
