package semantic

import (
	"reflect"
	"strings"
	"testing"
)

func TestRetainedShiftedFunctionFactReportsMatchCold(t *testing.T) {
	const before = "struct Pair:\n    value: u32\n\ndef identity[T](value: T) -> T:\n    return value\n\ndef branch(value: i64) -> i64:\n    if value > 0:\n        return value\n    return 0\n"
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for _, fields := range []string{"    extra: u64 = 0\n", "    extra: u64 = 0\n    other: u8 = 0\n"} {
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
		if state.LastStats.Changed != 0 {
			t.Fatalf("unchanged bodies reanalyzed: %+v", state.LastStats)
		}
		cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
		byName := map[string]*FunctionAnalysis{}
		for fn, analysis := range cold.FunctionAnalyses {
			byName[fn.Name] = analysis
		}
		count := 0
		for fn, got := range warm.FunctionAnalyses {
			if fn.Name != "identity" && fn.Name != "branch" {
				continue
			}
			count++
			want := byName[fn.Name]
			if want == nil {
				t.Fatal("missing cold function")
			}
			if !reflect.DeepEqual(got.FactExitSummary, want.FactExitSummary) {
				t.Fatalf("%s exit report stale: got=%+v want=%+v", fn.Name, got.FactExitSummary, want.FactExitSummary)
			}
			compare := func(g, w []FactTransform) {
				t.Helper()
				if len(g) != len(w) {
					t.Fatal("fact count")
				}
				for i := range g {
					gp, wp := g[i].SourcePos, w[i].SourcePos
					gp.Offset, gp.EndOffset = 0, 0
					wp.Offset, wp.EndOffset = 0, 0
					if gp != wp {
						t.Fatalf("%s fact position stale: got=%+v want=%+v", fn.Name, gp, wp)
					}
				}
			}
			compare(got.FactTransforms, want.FactTransforms)
			if len(got.BlockFactTransforms) != len(want.BlockFactTransforms) {
				t.Fatal("block fact count")
			}
			for i := range got.BlockFactTransforms {
				compare(got.BlockFactTransforms[i].Transforms, want.BlockFactTransforms[i].Transforms)
			}
			if len(got.CFG.Blocks) != len(want.CFG.Blocks) {
				t.Fatal("CFG block count")
			}
			for i := range got.CFG.Blocks {
				compare(got.CFG.Blocks[i].FactTransforms, want.CFG.Blocks[i].FactTransforms)
			}
		}
		if count != 2 {
			t.Fatalf("expected two function reports, got %d", count)
		}
	}
}
