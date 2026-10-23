package main

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestAnalysisBuildCheckpointGeneratesFreshIR(t *testing.T) {
	source := retainedCodegenBase
	build, err := semantic.BeginAnalysisBuild(retainedCodegenParse(t, source), retainedCodegenParse(t, source), []byte(source), semantic.AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var state *semantic.RetainedAnalysis
	for step := 0; step < 100; step++ {
		data, err := build.MarshalState()
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		build, err = semantic.RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
		result, finished, err := build.Advance(2, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			continue
		}
		state = finished
		if got, want := retainedIR(t, result), retainedIR(t, semantic.AnalyzeWithOptions(retainedCodegenParse(t, source), semantic.AnalyzeOptions{})); got != want {
			t.Fatal(firstLineDiff(got, want))
		}
		break
	}
	if state == nil {
		t.Fatal("build did not finish")
	}
	for _, edit := range []string{strings.Replace(source, "return p.a + p.b", "return p.b + p.a", 1), source} {
		result, err := state.Update(retainedCodegenParse(t, edit), retainedCodegenParse(t, edit), []byte(edit))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := retainedIR(t, result), retainedIR(t, semantic.AnalyzeWithOptions(retainedCodegenParse(t, edit), semantic.AnalyzeOptions{})); got != want {
			t.Fatal(firstLineDiff(got, want))
		}
	}
}

func TestAnalysisBuildCheckpointPreservesGeneratedIR(t *testing.T) {
	source := `enum Choice:
    One(value: i64)
    Two(value: i64)
static generate:
    for variant in variants(Choice):
        emit def is_${variant.name}(choice: Choice) -> bool:
            return choice is Choice.${variant.name}
def main() -> i32:
    choice: Choice = Choice.One(4)
    return 7 if is_One(choice) else 9
`
	build, err := semantic.BeginAnalysisBuild(retainedCodegenParse(t, source), retainedCodegenParse(t, source), []byte(source), semantic.AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 100; step++ {
		data, err := build.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		build, err = semantic.RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
		result, _, err := build.Advance(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			continue
		}
		if result.File == result.LoweredFile {
			t.Fatal("fixture omitted generated declarations")
		}
		if got, want := retainedIR(t, result), retainedIR(t, semantic.AnalyzeWithOptions(retainedCodegenParse(t, source), semantic.AnalyzeOptions{})); got != want {
			t.Fatal(firstLineDiff(got, want))
		}
		return
	}
	t.Fatal("build did not finish")
}
