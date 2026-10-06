package parser

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
)

// parseForNesting lexes and parses src, returning the parser's errors (lexer errors are not
// expected here; the inputs are lexically plain).
func parseForNesting(t *testing.T, src string) []string {
	t.Helper()
	l := lexer.New("nesting.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := New(tokens)
	p.ParseFile("nesting.elisa")
	return p.Errors()
}

func requireNestingError(t *testing.T, src string) {
	t.Helper()
	errs := parseForNesting(t, src)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %v", len(errs), truncateErrors(errs))
	}
	if !strings.Contains(errs[0], "nesting too deep: exceeds the maximum depth of 1000") {
		t.Fatalf("expected the nesting diagnostic, got %q", errs[0])
	}
}

func requireNoNestingError(t *testing.T, src string) {
	t.Helper()
	if errs := parseForNesting(t, src); len(errs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", truncateErrors(errs))
	}
}

func truncateErrors(errs []string) []string {
	if len(errs) > 5 {
		return append(errs[:5:5], "...")
	}
	return errs
}

func parenProgram(depth int) string {
	return "def main() -> i32:\n    x: i32 = " + strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth) + "\n    return x\n"
}

func chainProgram(terms int) string {
	return "def main() -> i32:\n    x: i32 = " + strings.TrimSuffix(strings.Repeat("1 + ", terms), " + ") + "\n    return x\n"
}

func prefixProgram(depth int) string {
	return "def main() -> i32:\n    x: i32 = " + strings.Repeat("-", depth) + "1\n    return x\n"
}

func postfixProgram(depth int) string {
	return "struct S:\n    v: i32\ndef main() -> i32:\n    s: S = S(v: 1)\n    return s" + strings.Repeat(".v", depth) + "\n"
}

func blockProgram(depth int) string {
	var b strings.Builder
	b.WriteString("def main() -> i32:\n")
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat("    ", i+1))
		b.WriteString("if true:\n")
	}
	b.WriteString(strings.Repeat("    ", depth+1))
	b.WriteString("return 1\n    return 0\n")
	return b.String()
}

func typeProgram(depth int) string {
	return "def main() -> i32:\n    x: " + strings.Repeat("darray[", depth) + "i32" + strings.Repeat("]", depth) + " = new darray[i32]()\n    return 0\n"
}

// Twenty thousand nested parentheses used to overflow the Go stack (a fatal runtime crash);
// the parser now stops at MaxNestingDepth with a single diagnostic.
func TestDeeplyNestedParenthesesReportNestingLimit(t *testing.T) {
	requireNestingError(t, parenProgram(20000))
}

// A flat `1 + 1 + …` chain of 200,000 terms parses iteratively but builds a tree 200,000 levels
// deep, which crashed constant evaluation. The chain depth counts toward the same limit.
func TestLongBinaryChainReportsNestingLimit(t *testing.T) {
	requireNestingError(t, chainProgram(200000))
}

func TestNestingLimitBoundaryShapes(t *testing.T) {
	// Each shape: a depth comfortably below the limit parses; one above it is rejected once.
	requireNoNestingError(t, parenProgram(990))
	requireNestingError(t, parenProgram(1001))
	requireNoNestingError(t, chainProgram(999))
	requireNestingError(t, chainProgram(1001))
	requireNoNestingError(t, prefixProgram(990))
	requireNestingError(t, prefixProgram(1001))
	requireNoNestingError(t, postfixProgram(990))
	requireNestingError(t, postfixProgram(1100))
	requireNoNestingError(t, blockProgram(990))
	requireNestingError(t, blockProgram(1001))
	requireNoNestingError(t, typeProgram(500))
	requireNestingError(t, typeProgram(1100))
}

// The depth counter must unwind with the recursion: many sibling expressions at modest depth
// never accumulate toward the limit.
func TestNestingDepthResetsBetweenSiblings(t *testing.T) {
	var b strings.Builder
	b.WriteString("def main() -> i32:\n")
	for i := 0; i < 3000; i++ {
		b.WriteString("    x: i32 = ((((((1))))))\n")
	}
	b.WriteString("    return 0\n")
	requireNoNestingError(t, b.String())
}
