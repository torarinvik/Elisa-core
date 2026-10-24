package semantic

import (
	"strings"
	"testing"
)

// Standalone regression for a pre-existing retained source-position defect.
// Intentionally separate from inline-group support. Byte offsets are NOT
// asserted: retainedShiftDecl explicitly preserves declaration-local offsets.
func TestRetainedStructEditMovesFollowingNonFunctionLines(t *testing.T) {
	const before = "struct Pair:\n    value: u32\n\ntype Alias = Pair\nstruct Outer:\n    pair: Alias\n"
	after := strings.Replace(before, "    value: u32\n", "    value: u32\n    extra: u64 = 0\n", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
		t.Fatal("diagnostic parity")
	}
	for i := 1; i < len(cold.File.Decls); i++ {
		want, got := cold.File.Decls[i].Pos(), warm.File.Decls[i].Pos()
		if want.File != got.File || want.Line != got.Line || want.Col != got.Col || want.EndLine != got.EndLine || want.EndCol != got.EndCol {
			t.Fatalf("declaration %d source position stale: cold=%+v retained=%+v", i, want, got)
		}
	}
}

func TestRetainedRecordInsertionShiftsNestedDeclarationsOnce(t *testing.T) {
	const before = "struct Pair:\n    value: u32\n\nmodule Shapes:\n    struct Box:\n        value: i64\n\n    def read(b: Box&) -> i64:\n        return b.value\n"
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	original := retainedWalkDecls(state.result.File)
	offsets := make([]int, len(original))
	for i, d := range original {
		offsets[i] = d.Pos().Offset
	}
	for _, fields := range []string{"    extra: u64 = 0\n", "    extra: u64 = 0\n    extra2: u8 = 0\n"} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		after := strings.Replace(before, "    value: u32\n", "    value: u32\n"+fields, 1)
		warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
		if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
			t.Fatal("diagnostic parity")
		}
		got, want := retainedWalkDecls(warm.File), retainedWalkDecls(cold.File)
		if len(got) != len(want) {
			t.Fatal("declaration count")
		}
		for i, d := range got {
			if i == 0 {
				continue
			} // Pair is edited; only unchanged declarations retain offsets.
			g, w := d.Pos(), want[i].Pos()
			if g.File != w.File || g.Line != w.Line || g.Col != w.Col || g.EndLine != w.EndLine || g.EndCol != w.EndCol {
				t.Fatalf("declaration %d shifted incorrectly: got=%+v want=%+v", i, g, w)
			}
			if g.Offset != offsets[i] {
				t.Fatalf("declaration %d lost stable offset: got=%d want=%d", i, g.Offset, offsets[i])
			}
		}
	}
}
