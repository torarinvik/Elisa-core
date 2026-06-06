package semantic

import (
	"strings"
	"testing"
)

// docs/76: the canonical inline `common(...)` shared-field form parses and populates the enum's
// common fields, equivalently to the legacy `common:` block.
func TestEnumCommonInlineSingleField(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ci1.elisa", `packed enum Expr:
    common(span: int)
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("common(...) must parse cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if _, ok := et.Common["span"]; !ok {
		t.Fatalf("expected common field 'span', got common=%v", et.Common)
	}
}

func TestEnumCommonInlineMultipleFields(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ci2.elisa", `packed enum Expr:
    common(span: int, cost: int)
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("multi-field common(...) must parse cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	for _, f := range []string{"span", "cost"} {
		if _, ok := et.Common[f]; !ok {
			t.Fatalf("expected common field %q, got common=%v", f, et.Common)
		}
	}
}

// Multi-line inline form (newlines + trailing comma inside the parens) parses.
func TestEnumCommonInlineMultiline(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ci3.elisa", `packed enum Expr:
    common(
        span: int,
        cost: int,
    )
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("multi-line common(...) must parse cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if _, ok := et.Common["cost"]; !ok {
		t.Fatalf("expected common field 'cost' from multi-line form, got common=%v", et.Common)
	}
}

// The legacy `common:` block still works (back-compat) and yields the same field.
func TestEnumCommonBlockStillWorks(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ci4.elisa", `packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("legacy common: block must still work, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if _, ok := et.Common["span"]; !ok {
		t.Fatalf("expected common field 'span' from block form, got common=%v", et.Common)
	}
}
