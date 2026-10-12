package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// docs/125 §4 — `when` decision tables: desugar shape and the R1/R3 refusals.

const whenTableSrc = `def literal_fits(value: i64, negated: bool, type_name: sview) -> bool:
    return when type_name, negated:
        "u8", false -> value <= 255
        _ -> true
        "u8" | "u16", true -> false
`

func TestWhenDesugarShape(t *testing.T) {
	file, errs := parseSourceFile(t, whenTableSrc)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return, got %T", fn.Body[0])
	}
	match, ok := ret.Value.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("when must desugar to MatchExpr, got %T", ret.Value)
	}
	if _, ok := match.Value.(*ast.TupleExpr); !ok {
		t.Fatalf("multi-scrutinee when must desugar to a tuple scrutinee, got %T", match.Value)
	}
	if len(match.Arms) != 3 {
		t.Fatalf("arms = %d, want 3", len(match.Arms))
	}
	// Order independence: the `_` default row (written mid-table) is emitted LAST so it
	// cannot swallow later arms under match's first-wins ordering.
	if _, ok := match.Arms[2].Pattern.(*ast.MatchWildcardPattern); !ok {
		t.Fatalf("default row must be moved last, got %T", match.Arms[2].Pattern)
	}
	first, ok := match.Arms[0].Pattern.(*ast.MatchTuplePattern)
	if !ok || len(first.Elems) != 2 {
		t.Fatalf("arm 0 must be a 2-column tuple pattern, got %#v", match.Arms[0].Pattern)
	}
	// `|` binds WITHIN a column (unlike match's top-level fan-out): `"u8" | "u16", true`
	// is one row whose first column is an or-group.
	second, ok := match.Arms[1].Pattern.(*ast.MatchTuplePattern)
	if !ok {
		t.Fatalf("arm 1 must be a tuple pattern, got %T", match.Arms[1].Pattern)
	}
	if orPat, ok := second.Elems[0].(*ast.MatchOrPattern); !ok || len(orPat.Options) != 2 {
		t.Fatalf("arm 1 column 0 must be a 2-option or-group, got %#v", second.Elems[0])
	}
	// Arms carry no guards (R3 rejects them before desugar).
	for i, arm := range match.Arms {
		if arm.Guard != nil {
			t.Fatalf("arm %d has a guard; when arms are guard-free", i)
		}
	}
}

func TestWhenSingleColumnOrFansOut(t *testing.T) {
	src := "def f(n: i64) -> i64:\n" +
		"    return when n:\n" +
		"        1 | 2 -> 10\n" +
		"        _ -> 20\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	match := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.MatchExpr)
	// Single-column or-groups fan out to sibling literal arms, matching match's own
	// top-level `A | B:` lowering (which coverage/codegen are tuned for).
	if len(match.Arms) != 3 {
		t.Fatalf("arms = %d, want 3 (two fanned literals + default)", len(match.Arms))
	}
	if _, ok := match.Arms[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("fanned arm 0 = %T", match.Arms[0].Pattern)
	}
}

func TestWhenStatementFormDesugarsToMatchStmt(t *testing.T) {
	src := "def f(n: i64) -> void:\n" +
		"    when n:\n" +
		"        0 -> report_zero()\n" +
		"        _ ->\n" +
		"            report_other(n)\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	match, ok := fn.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("statement-position when must desugar to MatchStmt, got %T", fn.Body[0])
	}
	if len(match.Arms) != 2 {
		t.Fatalf("arms = %d, want 2", len(match.Arms))
	}
}

func expectWhenError(t *testing.T, src string, wantSubstr string) {
	t.Helper()
	_, errs := parseSourceFile(t, src)
	for _, err := range errs {
		if strings.Contains(err, wantSubstr) {
			return
		}
	}
	t.Fatalf("expected error containing %q, got %v", wantSubstr, errs)
}

func TestWhenRefusalOverlapLiteralVsWildcardColumn(t *testing.T) {
	expectWhenError(t, `def f(name: sview, neg: bool) -> i64:
    return when name, neg:
        "u8", false -> 1
        "u8", _ -> 2
        _ -> 3
`, "arm (\"u8\", _) overlaps arm (\"u8\", false) at line 3; `when` arms must be disjoint")
}

func TestWhenRefusalOverlapRangeVsLiteral(t *testing.T) {
	expectWhenError(t, `def f(n: i64) -> i64:
    return when n:
        0..=10 -> 1
        5 -> 2
        _ -> 3
`, "arm (5) overlaps arm (0..=10)")
}

func TestWhenRefusalOverlapRanges(t *testing.T) {
	expectWhenError(t, `def f(n: i64) -> i64:
    return when n:
        0..<10 -> 1
        9..=20 -> 2
        _ -> 3
`, "overlaps arm (0..<10)")
}

func TestWhenAdjacentRangesAreDisjoint(t *testing.T) {
	_, errs := parseSourceFile(t, `def f(n: i64) -> i64:
    return when n:
        0..<10 -> 1
        10..=20 -> 2
        _ -> 3
`)
	if len(errs) != 0 {
		t.Fatalf("adjacent half-open/closed ranges must be accepted, got %v", errs)
	}
}

func TestWhenRefusalOverlapEnumTags(t *testing.T) {
	expectWhenError(t, `const enum Color of u8:
    Red
    Green

def f(c: Color) -> i64:
    return when c:
        Color.Red -> 1
        Color.Red | Color.Green -> 2
`, "overlaps arm (Color.Red)")
}

func TestWhenRefusalDuplicateDefaultRow(t *testing.T) {
	expectWhenError(t, `def f(n: i64) -> i64:
    return when n:
        0 -> 1
        _ -> 2
        _ -> 3
`, "duplicate `_` default row")
}

func TestWhenRefusalGuard(t *testing.T) {
	expectWhenError(t, `def f(n: i64) -> i64:
    return when n:
        0 if n > 3 -> 1
        _ -> 2
`, "computed guards need `match`")
}

func TestWhenRefusalBinding(t *testing.T) {
	expectWhenError(t, `def f(n: i64) -> i64:
    return when n:
        x -> 1
        _ -> 2
`, "bindings and computed guards need `match`")
}

func TestWhenRefusalPayloadDestructure(t *testing.T) {
	expectWhenError(t, `enum Shape:
    Dot
    Line(len: i64)

def f(s: Shape) -> i64:
    return when s:
        Shape.Dot -> 1
        Shape.Line(n) -> 2
`, "cannot destructure payloads")
}

func TestWhenRefusalColumnCountMismatch(t *testing.T) {
	expectWhenError(t, `def f(a: i64, b: i64) -> i64:
    return when a, b:
        0 -> 1
        _ -> 2
`, "has 1 column(s) but the scrutinee has 2")
}

func TestWhenRefusalPinnedValue(t *testing.T) {
	expectWhenError(t, `def f(n: i64, k: i64) -> i64:
    return when n:
        ^k -> 1
        _ -> 2
`, "pinned `^` value is computed")
}

// `when` stays a legal identifier: the construct is gated on the following token, so
// existing code (and the derive-state/catch contextual uses) is untouched.
func TestWhenRemainsAnIdentifier(t *testing.T) {
	_, errs := parseSourceFile(t, `def g(when: i64) -> i64:
    x: i64 = when + 1
    return when
`)
	if len(errs) != 0 {
		t.Fatalf("`when` as identifier must still parse, got %v", errs)
	}
}

func TestWhenExpressionInsideParenthesizedContract(t *testing.T) {
	source := `def classify(value: u32) -> bool:
    ensure result == (
        when value:
            1 | 2 -> true
            _ -> false
    )
    return when value:
        1 | 2 -> true
        _ -> false
`
	file, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("grouped when parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if len(fn.EnsureValues) != 1 {
		t.Fatalf("ensures = %d", len(fn.EnsureValues))
	}
	if len(fn.Body) != 1 {
		t.Fatalf("body = %d", len(fn.Body))
	}
}

func TestGroupedPatternExpressionHeaders(t *testing.T) {
	for _, header := range []string{"when value", "when choose(value)", "when M::choose(value)", "match value"} {
		t.Run(header, func(t *testing.T) {
			separator := " -> "
			if header == "match value" {
				separator = ": "
			}
			source := "def classify(value: u32) -> bool:\n    ensure result == (\n        " + header + ":\n            1" + separator + "true\n            _" + separator + "false\n    )\n    return (\n        true\n    )\n"
			file, errs := parseSourceFile(t, source)
			if len(errs) != 0 {
				t.Fatalf("grouped header parse errors: %v", errs)
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			if len(fn.EnsureValues) != 1 || len(fn.Body) != 1 {
				t.Fatalf("lost contract or return: %#v", fn)
			}
		})
	}
}

func TestGroupedWhenArmNestedOrdinaryParentheses(t *testing.T) {
	source := `def classify(value: u32) -> bool:
    ensure result == (
        when value:
            1 -> (
                true
            )
            _ -> false
    )
    return false
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("nested arm parentheses parse errors: %v", errs)
	}
}

func TestGroupedWhenNestedScrutinee(t *testing.T) {
	source := `def classify(value: u32) -> bool:
    ensure result == (
        when choose(
            when value:
                1 -> true
                _ -> false
        ):
            true -> true
            _ -> false
    )
    return false
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("nested scrutinee parse errors: %v", errs)
	}
}

func TestGroupedWhenNamedParameterRemainsOrdinaryGrouping(t *testing.T) {
	source := `def g(
    when: i64,
    value: i64
) -> i64:
    return value
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("when parameter parse errors: %v", errs)
	}
}

func TestGroupedWhenIdentifierCallAndRecordArgument(t *testing.T) {
	source := `def g(when: i64) -> i64:
    return choose(
        when,
        Record{value: 1}
    )
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("when value/record parse errors: %v", errs)
	}
}

func TestGroupedWhenLeadingDotFloatScrutinee(t *testing.T) {
	source := `def classify() -> bool:
    ensure result == (
        when .5:
            .5 -> true
            _ -> false
    )
    return true
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("leading-dot scrutinee parse errors: %v", errs)
	}
}

func TestGroupedWhenRecordScrutinee(t *testing.T) {
	source := `def classify() -> bool:
    ensure result == (
        when Pair{left: 1}:
            _ -> true
    )
    return true
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("record scrutinee parse errors: %v", errs)
	}
}

func TestGroupedWhenNestedExplicitDoBlock(t *testing.T) {
	source := `def classify(value: u32) -> bool:
    ensure result == (
        when value:
            1 -> (do:
                true
            )
            _ -> false
    )
    return false
`
	_, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("nested do block parse errors: %v", errs)
	}
}
