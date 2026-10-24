package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestRetainedAlignmentDiskRoundTrip(t *testing.T) {
	const base = `struct Pair:
    value: i64

type Alias = Pair
struct Outer:
    pair: Alias

def echo(value: Pair) -> Pair:
    return value

def layout() -> usize:
    return size_of(Alias) + align_of(Pair) + size_of(Outer)
`
	for _, prefix := range []string{"", "def before() -> i64:\n    return 1\n\n"} {
		old := prefix + base
		_, state := AnalyzeRetained(retainedParse(t, old), retainedParse(t, old), []byte(old), AnalyzeOptions{})
		for _, alignment := range []string{"64", "128", "", "16", ""} {
			source := prefix + base
			if alignment != "" {
				source = prefix + "@align(" + alignment + ")\n" + base
			}
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			identity := state.a.namedTypes["Pair"]
			got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			want, cold := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
			if retainedDiagnostics(got) != retainedDiagnostics(want) || len(got.Errors()) != 0 {
				t.Fatalf("diagnostics: %v", got.Errors())
			}
			current := state.a.namedTypes["Pair"].(*StructType)
			expected := cold.a.namedTypes["Pair"].(*StructType)
			if current != identity || current.Alignment != expected.Alignment || current.HasAlignment != expected.HasAlignment {
				t.Fatalf("stale alignment after %q: %+v", alignment, current)
			}
		}
	}
}

func TestRetainedAlignmentEligibilityGuards(t *testing.T) {
	const base = "struct Pair:\n    value: i64\n"
	old := retainedParse(t, base).Decls[0].(*ast.StructDecl)
	for _, annotation := range []string{"@align(24)", "@align(64)\n@align(64)", "@align(64)\n@fixed_layout", "@append_only", "@fixed_layout", "@align()"} {
		current := retainedParse(t, annotation+"\n"+base).Decls[0].(*ast.StructDecl)
		if retainedStructFieldEditAllowed(old, current) {
			t.Fatalf("accepted unqualified annotation %s", annotation)
		}
	}
}
