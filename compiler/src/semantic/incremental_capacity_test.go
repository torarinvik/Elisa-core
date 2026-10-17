//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
	"testing"
)

func TestIncrementalCapacityRetainsOwnersAcrossInsertionAndRemoval(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 1
	retained := "def retained() -> i64:\n    return 21\n"
	added := "def added() -> i64:\n    return 42\n"
	session.Analyze(parseIncrementalSessionFile(t, retained), AnalyzeOptions{})
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, added+retained), AnalyzeOptions{})
	after := session.Stats()
	if after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 || after.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 || after.ColdFallbackAnalyses != before.ColdFallbackAnalyses {
		t.Fatalf("insertion displaced retained owner: before%+v after%+v", before, after)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, added+retained)), current)
	// Removing the retained owner frees admission for the formerly cold owner.
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, added), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 {
		t.Fatal("freed slot did not admit new owner")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, added)), current)
	before = session.Stats()
	session.Analyze(parseIncrementalSessionFile(t, added), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 || len(session.previous) != 1 {
		t.Fatal("newly admitted owner did not warm")
	}
}

func TestIncrementalCapacityRetainsLocalAcrossInsertion(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 2
	source := "def work(value: i64) -> i64:\n    retained: i64 = value + 1\n    return retained\n"
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	inserted := strings.Replace(source, "    retained:", "    added: i64 = 7\n    retained:", 1)
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, inserted), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 || len(session.previous) != 2 {
		t.Fatal("new local displaced retained product or exceeded budget")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, inserted)), current)
}

func TestIncrementalCapacityLargeFileKeepsActualHits(t *testing.T) {
	var source strings.Builder
	for i := 0; i < primitiveSessionMaxTrackedBodies+1; i++ {
		fmt.Fprintf(&source, "def work%d() -> i64:\n    return 1\n\n", i)
	}
	text := source.String()
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(parseIncrementalSessionFile(t, text), AnalyzeOptions{})
	if len(first.Errors()) != 0 {
		t.Fatal(first.Errors())
	}
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, text), AnalyzeOptions{})
	if len(current.Errors()) != 0 {
		t.Fatal(current.Errors())
	}
	after := session.Stats()
	if after.PrimaryBodiesReused-before.PrimaryBodiesReused == 0 || after.PrimaryBodiesChecked-before.PrimaryBodiesChecked == 0 || after.ColdFallbackAnalyses != before.ColdFallbackAnalyses {
		t.Fatalf("large file lost partial reuse: before%+v after%+v", before, after)
	}
	if len(session.previous) > session.trackedBodyLimit() || after.RetainedProductBytes > uint64(session.productByteLimit()) {
		t.Fatal("large request exceeded retention budget")
	}
}

func TestIncrementalCapacityNewLocalDoesNotDisplaceAnotherRetainedOwner(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 3
	source := "def first(value: i64) -> i64:\n    retained: i64 = value + 1\n    return retained\n\ndef second() -> i64:\n    return 42\n"
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	inserted := strings.Replace(source, "    retained:", "    added: i64 = 7\n    retained:", 1)
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, inserted), AnalyzeOptions{})
	after := session.Stats()
	if after.LocalInitializersReused-before.LocalInitializersReused != 1 || after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 || len(session.previous) != 3 {
		t.Fatalf("new local displaced retained owner: before%+v after%+v", before, after)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, inserted)), current)
}

func TestIncrementalCapacityUnsupportedCallerCannotStarveReusableBody(t *testing.T) {
	source := "def caller() -> i64:\n    return hot()\n\ndef hot() -> i64:\n    return 42\n"
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 1
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("unsupported call owner monopolized the query budget")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
}

func TestIncrementalCapacityManyRetainedOwnersGainLocals(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 16
	var initial, edited strings.Builder
	for fn := 0; fn < 8; fn++ {
		fmt.Fprintf(&initial, "def work%d() -> i64:\n    local0: i64 = 0\n    return local0\n\n", fn)
		fmt.Fprintf(&edited, "def work%d() -> i64:\n", fn)
		for local := 0; local < 32; local++ {
			fmt.Fprintf(&edited, "    local%d: i64 = %d\n", local, local)
		}
		edited.WriteString("    return local31\n\n")
	}
	session.Analyze(parseIncrementalSessionFile(t, initial.String()), AnalyzeOptions{})
	result := session.Analyze(parseIncrementalSessionFile(t, edited.String()), AnalyzeOptions{})
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited.String())), result)
	before := session.Stats()
	result = session.Analyze(parseIncrementalSessionFile(t, edited.String()), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 8 || len(session.previous) != 16 {
		t.Fatal("retained owner growth lost bounded local hits")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited.String())), result)
}

func TestIncrementalCapacityMaximumLocalProductReuses(t *testing.T) {
	makeFile := func(extra int) *ast.File {
		file := parseIncrementalSessionFile(t, "def work() -> i64:\n    local: i64 = 1\n    return local\n")
		var tree func(int) ast.Expr
		tree = func(depth int) ast.Expr {
			if depth == 0 {
				return &ast.IntLit{Value: "1"}
			}
			return &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: tree(depth - 1), Right: tree(depth - 1)}
		}
		value := tree(11) // 4095 nodes before unary wrappers; depth stays bounded.
		for range extra {
			value = &ast.UnaryExpr{Op: lexer.TOKEN_MINUS, Operand: value}
		}
		file.Decls[0].(*ast.FuncDecl).Body[0].(*ast.VarDeclStmt).Value = value
		return file
	}
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(makeFile(1), AnalyzeOptions{})
	if len(first.Errors()) != 0 {
		t.Fatal(first.Errors())
	}
	before := session.Stats()
	current := session.Analyze(makeFile(1), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 {
		t.Fatal("maximum-size local product could not reuse its extra result tag")
	}
	assertIncrementalResultParity(t, Analyze(makeFile(1)), current)
	before = session.Stats()
	session.Analyze(makeFile(2), AnalyzeOptions{})
	current = session.Analyze(makeFile(2), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused != before.LocalInitializersReused {
		t.Fatal("over-limit local expression was admitted")
	}
	assertIncrementalResultParity(t, Analyze(makeFile(2)), current)
}

func TestIncrementalCapacityOwnerContextAloneCannotStarveBody(t *testing.T) {
	source := "def local_work() -> i64:\n    local: i64 = 20\n    return local\n\ndef hot() -> i64:\n    return 42\n"
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 1
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("owner context consumed a slot without a reusable query")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
}
