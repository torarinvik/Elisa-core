//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestIncrementalParameterReturnRebindsCurrentSymbols(t *testing.T) {
	source := "def work(value: i64, delta: i64) -> i64:\n    return value + delta * 2\n"
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(first.Errors()) != 0 {
		t.Fatal(first.Errors())
	}
	before := session.Stats()
	warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(warm.Errors()) != 0 {
		t.Fatal(warm.Errors())
	}
	after := session.Stats()
	if after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatalf("parameter body not reused: before%+v after%+v", before, after)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), warm)
	if after.ReusedExpressionNodes-before.ReusedExpressionNodes != 3 {
		t.Fatal("fresh parameter reads counted as restored expression nodes")
	}
	if after.PrimaryExprChecks-before.PrimaryExprChecks != 2 {
		t.Fatal("identifier reads must execute against fresh parameter bindings")
	}
}

func TestIncrementalParameterEditsAndFallback(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	sources := []struct {
		source string
		reused uint64
	}{
		{"def work(value: i64) -> i64:\n    return (value + 1) * 2\n", 0},
		{"\n\ndef work(value: i64) -> i64:\n    return (value + 1) * 2\n", 1},
		{"def work(other: i64) -> i64:\n    return (other + 1) * 2\n", 0},
		{"def work(other: i64) -> i64:\n    return (other + 1) * 2\n", 1},
		{"def work(other: i32) -> i64:\n    return (other + 1) * 2\n", 0},
		{"def work(other: i64 = 3) -> i64:\n    return (other + 1) * 2\n", 0},
		{"def work(other: mutable i64) -> i64:\n    return (other + 1) * 2\n", 0},
		{"def work() -> i64:\n    return (other + 1) * 2\n", 0},
		{"def work(other: i64) -> i64:\n    return (other + 1) * 2\n", 0},
	}
	for _, step := range sources {
		before := session.Stats()
		result := session.Analyze(parseIncrementalSessionFile(t, step.source), AnalyzeOptions{})
		cold := Analyze(parseIncrementalSessionFile(t, step.source))
		assertIncrementalResultParity(t, cold, result)
		if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != step.reused {
			t.Fatalf("unexpected reuse for %s: before%+v after%+v", step.source, before, session.Stats())
		}
	}
}

func TestIncrementalParameterBareAndRepeatedReads(t *testing.T) {
	for _, expression := range []string{"value", "(value)", "value + value * (value - 1)"} {
		source := "def work(value: i64) -> i64:\n    return " + expression + "\n"
		session := NewIncrementalAnalysisSession()
		session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		before := session.Stats()
		current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
			t.Fatalf("not reused: %s", expression)
		}
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
	}
}

func TestIncrementalParameterBindingRejectsWrongOwnerAndOrdinal(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	canonical := &BuiltinType{Name: "i64"}
	scope := NewScope(nil)
	symbol := &Symbol{Name: "value", Kind: SymbolParam, Node: fn, ParamIndex: 0, Type: canonical}
	scope.Define(symbol)
	analyzer := &Analyzer{currentScope: scope, namedTypes: map[string]Type{"i64": canonical}}
	if !primitiveCurrentParameterBindings(analyzer, fn) {
		t.Fatal("current owner rejected")
	}
	symbol.Node = parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	if primitiveCurrentParameterBindings(analyzer, fn) {
		t.Fatal("old owner accepted")
	}
	symbol.Node = fn
	symbol.ParamIndex = 1
	if primitiveCurrentParameterBindings(analyzer, fn) {
		t.Fatal("wrong parameter ordinal accepted")
	}
	symbol.ParamIndex = 0
	symbol.Kind = SymbolGlobal
	if primitiveCurrentParameterBindings(analyzer, fn) {
		t.Fatal("global accepted as parameter")
	}
}

func TestIncrementalParameterOverloadOwnersStayIndependent(t *testing.T) {
	source := "def work() -> i64:\n    return 1\n\ndef work(value: i64) -> i64:\n    return value + 1\n"
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(first.Errors()) != 0 {
		t.Fatal(first.Errors())
	}
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 2 {
		t.Fatalf("overload owners collided: before%+v after%+v", before, session.Stats())
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
	edited := strings.Replace(source, "value + 1", "value + 2", 1)
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 || session.Stats().PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 {
		t.Fatal("one overload edit invalidated the other owner")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), current)
}
