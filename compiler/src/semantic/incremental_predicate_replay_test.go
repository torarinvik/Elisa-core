//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestIncrementalPredicateReplayMatchesColdAnalysis(t *testing.T) {
	const source = `def same_bool(value: bool) -> bool:
    return value == value

def negated(value: bool) -> bool:
    return not value

def same_integer(value: i64) -> bool:
    return value == value

def ordered_byte(value: i8) -> bool:
    return value < 10

def logical_chain(left: bool, right: bool) -> bool:
    return left and (right or not left)

def same_character(value: char) -> bool:
    return value == 'x'
`
	shifted := "\n" + source
	session := NewIncrementalAnalysisSession()
	cold := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(cold.Errors()) != 0 {
		t.Fatal(cold.Errors())
	}
	before := session.Stats()
	warm := session.Analyze(parseIncrementalSessionFile(t, shifted), AnalyzeOptions{})
	after := session.Stats()
	if len(warm.Errors()) != 0 {
		t.Fatal(warm.Errors())
	}
	if after.PrimaryBodiesReused-before.PrimaryBodiesReused != 6 {
		t.Fatalf("predicate bodies were not reused: before=%+v after=%+v", before, after)
	}
	if after.PrimaryBodiesChecked != before.PrimaryBodiesChecked || after.PrimaryValueChecks != before.PrimaryValueChecks {
		t.Fatalf("warm predicates repeated primary body/value checks: before=%+v after=%+v", before, after)
	}
	// Parameter identifiers are deliberately resolved on every invocation so
	// lexical dependencies stay current. Composite expression nodes are replayed.
	if after.PrimaryExprChecks-before.PrimaryExprChecks != 10 {
		t.Fatalf("warm predicates did not limit work to current parameter reads: before=%+v after=%+v", before, after)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, shifted)), warm)
}

func TestIncrementalPredicateUnaryMinusDistinguishesIntrinsicFromOverloaded(t *testing.T) {
	t.Run("numeric intrinsic", func(t *testing.T) {
		const source = `def below_zero(value: i32) -> bool:
    return -value < 0
`
		session := NewIncrementalAnalysisSession()
		first := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if len(first.Errors()) != 0 {
			t.Fatal(first.Errors())
		}
		before := session.Stats()
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
			t.Fatal("intrinsic numeric unary minus did not reuse")
		}
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), warm)
	})

	t.Run("char intrinsic", func(t *testing.T) {
		const source = `def negative_char_param(value: char) -> bool:
    return -value == 'x'

def negative_char_literal() -> bool:
    return -'x' == 'x'
`
		session := NewIncrementalAnalysisSession()
		coldFile := parseIncrementalSessionFile(t, source)
		cold := session.Analyze(coldFile, AnalyzeOptions{})
		if len(cold.Errors()) != 0 {
			t.Fatal(cold.Errors())
		}
		for _, decl := range coldFile.Decls {
			fn := decl.(*ast.FuncDecl)
			binary := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
			unary := binary.Left.(*ast.UnaryExpr)
			if unary.LoweredCall != nil {
				t.Fatal("char unary minus unexpectedly lowered to an overload")
			}
		}
		before := session.Stats()
		warmFile := parseIncrementalSessionFile(t, source)
		warm := session.Analyze(warmFile, AnalyzeOptions{})
		if len(warm.Errors()) != 0 || session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 2 {
			t.Fatalf("intrinsic char unary minus did not replay: %v / %+v", warm.Errors(), session.Stats())
		}
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), warm)
	})

	t.Run("bool overload", func(t *testing.T) {
		const source = `protocol Neg:
    def __neg__(value: Self) -> Self

impl Neg for bool:
    def __neg__(value: bool) -> bool:
        return false

def negated_equal(value: bool) -> bool:
    return -value == true
`
		session := NewIncrementalAnalysisSession()
		coldFile := parseIncrementalSessionFile(t, source)
		cold := session.Analyze(coldFile, AnalyzeOptions{})
		if len(cold.Errors()) != 0 {
			t.Fatal(cold.Errors())
		}
		unaryCall := func(file *ast.File) bool {
			fn := file.Decls[len(file.Decls)-1].(*ast.FuncDecl)
			binary := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
			unary := binary.Left.(*ast.UnaryExpr)
			return unary.LoweredCall != nil
		}
		if !unaryCall(coldFile) {
			t.Fatal("cold analysis did not lower bool negation")
		}
		warmFile := parseIncrementalSessionFile(t, source)
		warm := session.Analyze(warmFile, AnalyzeOptions{})
		if len(warm.Errors()) != 0 || session.Stats().PrimaryBodiesReused != 0 || !unaryCall(warmFile) {
			t.Fatalf("overloaded unary minus was reused or lost its lowering: %v / %+v", warm.Errors(), session.Stats())
		}
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), warm)
	})
}

func TestPredicateUnaryMinusDeclinesTypedNilChild(t *testing.T) {
	var nested *ast.UnaryExpr
	expr := &ast.UnaryExpr{Op: lexer.TOKEN_MINUS, Operand: nested}
	if predicateUnaryMinusIsIntrinsic(expr, nil) {
		t.Fatal("typed-nil unary operand was admitted")
	}
}

func TestValidPredicateTypingBoundsAndTags(t *testing.T) {
	for _, value := range []string{"", "n", "i", "?", "b?", "cb", strings.Repeat("b", 4097)} {
		if validPredicateTyping(value) {
			t.Fatalf("accepted invalid predicate type stream %q", value)
		}
	}
	for _, name := range []string{"bool", "char", "int", "i64", "i8", "i16", "i32", "u8", "u16", "u32", "u64", "isize", "usize", "uintptr"} {
		tag := predicateTypeTag(name)
		if tag == 0 || predicateTypeName(tag) != name {
			t.Fatalf("predicate type tag did not round trip for %q", name)
		}
	}
}

func TestIncrementalPredicateComparisonImplChangeBlocksStaleReplay(t *testing.T) {
	const body = `def same_bool(value: bool) -> bool:
    return value == value
`
	const implementation = `protocol BoolEquality:
    def __eq__(left: Self, right: Self) -> bool

impl BoolEquality for bool:
    def __eq__(left: bool, right: bool) -> bool:
        return true

`
	session := NewIncrementalAnalysisSession()
	initial := session.Analyze(parseIncrementalSessionFile(t, body), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	before := session.Stats()
	withImpl := session.Analyze(parseIncrementalSessionFile(t, implementation+body), AnalyzeOptions{})
	afterAdd := session.Stats()
	if len(withImpl.Errors()) != 0 {
		t.Fatal(withImpl.Errors())
	}
	if afterAdd.PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatalf("added comparison implementation reused a stale predicate: before=%+v after=%+v", before, afterAdd)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, implementation+body)), withImpl)

	beforeRemove := session.Stats()
	withoutImpl := session.Analyze(parseIncrementalSessionFile(t, body), AnalyzeOptions{})
	afterRemove := session.Stats()
	if len(withoutImpl.Errors()) != 0 {
		t.Fatal(withoutImpl.Errors())
	}
	if afterRemove.PrimaryBodiesChecked-beforeRemove.PrimaryBodiesChecked != 1 || afterRemove.PrimaryBodiesReused != beforeRemove.PrimaryBodiesReused {
		t.Fatalf("removing comparison implementation should cold-compile the predicate before publishing a safe product: before=%+v after=%+v", beforeRemove, afterRemove)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, body)), withoutImpl)

	beforeWarm := session.Stats()
	warmAgain := session.Analyze(parseIncrementalSessionFile(t, body), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-beforeWarm.PrimaryBodiesReused != 1 {
		t.Fatal("comparison-implementation removal did not restore predicate reuse after a cold refresh")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, body)), warmAgain)
}

func TestIncrementalPredicateNestedComparisonImplChangeBlocksStaleReplay(t *testing.T) {
	const body = `def same_nested(left: i64, right: i64, other_left: i64, other_right: i64) -> bool:
    return (left < right) == (other_left < other_right)
`
	const implementation = `protocol BoolEquality:
    def __eq__(left: Self, right: Self) -> bool

impl BoolEquality for bool:
    def __eq__(left: bool, right: bool) -> bool:
        return false

`
	session := NewIncrementalAnalysisSession()
	initial := session.Analyze(parseIncrementalSessionFile(t, body), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	beforeWarm := session.Stats()
	warm := session.Analyze(parseIncrementalSessionFile(t, body), AnalyzeOptions{})
	if len(warm.Errors()) != 0 || session.Stats().PrimaryBodiesReused-beforeWarm.PrimaryBodiesReused != 1 {
		t.Fatalf("nested comparison predicate did not warm-reuse before the implementation appeared: errors=%v stats=%+v", warm.Errors(), session.Stats())
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, body)), warm)

	beforeImpl := session.Stats()
	withImplFile := parseIncrementalSessionFile(t, implementation+body)
	withImpl := session.Analyze(withImplFile, AnalyzeOptions{})
	afterImpl := session.Stats()
	if len(withImpl.Errors()) != 0 {
		t.Fatal(withImpl.Errors())
	}
	if afterImpl.PrimaryBodiesReused != beforeImpl.PrimaryBodiesReused || afterImpl.PrimaryBodiesChecked <= beforeImpl.PrimaryBodiesChecked {
		t.Fatalf("nested bool comparison replayed after BoolEquality appeared: before=%+v after=%+v", beforeImpl, afterImpl)
	}
	var fn *ast.FuncDecl
	for _, decl := range withImplFile.Decls {
		candidate, ok := decl.(*ast.FuncDecl)
		if ok && candidate.Name == "same_nested" {
			fn = candidate
			break
		}
	}
	if fn == nil || len(fn.Body) != 1 {
		t.Fatal("nested predicate declaration was not preserved")
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatal("nested predicate return was not preserved")
	}
	outer, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || outer.LoweredCall == nil {
		t.Fatal("cold analysis after BoolEquality did not lower the outer bool comparison")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, implementation+body)), withImpl)
}
