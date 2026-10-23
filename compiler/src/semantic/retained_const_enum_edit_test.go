package semantic

import (
	"strings"
	"testing"
)

const retainedConstEnumBefore = `
const enum Choice of u8:
    First
    Second

def selected() -> Choice:
    return Choice.Second

def check(x: Choice) -> i64:
    match x:
        Choice.First:
            return 1
        Choice.Second:
            return 2

def unrelated() -> i64:
    return 9
`

func TestRetainedConstEnumEditsDiskRoundTrip(t *testing.T) {
	_, state := AnalyzeRetained(retainedParse(t, retainedConstEnumBefore), retainedParse(t, retainedConstEnumBefore), []byte(retainedConstEnumBefore), AnalyzeOptions{})
	reordered := strings.Replace(retainedConstEnumBefore, "    First\n    Second\n", "    Second\n    First\n", 1)
	added := strings.Replace(reordered, "    First\n", "    First\n    Third\n", 1)
	removed := strings.Replace(retainedConstEnumBefore, "    Second\n", "", 1)
	for i, source := range []string{reordered, added, removed, retainedConstEnumBefore} {
		encoded, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(encoded)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
		cold, coldState := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(cold); got != want {
			t.Fatalf("edit %d diagnostics:\n%s\nwant:\n%s", i, got, want)
		}
		got := state.a.namedTypes["Choice"].(*ConstEnumType)
		want := coldState.a.namedTypes["Choice"].(*ConstEnumType)
		if len(got.Members) != len(want.Members) || got.Storage.String() != want.Storage.String() {
			t.Fatalf("edit %d: member/storage mismatch", i)
		}
		for k, m := range got.Members {
			if state.a.constValues["Choice."+m.Name].Int != coldState.a.constValues["Choice."+m.Name].Int || state.a.constValues["Choice."+m.Name].Kind != coldState.a.constValues["Choice."+m.Name].Kind {
				t.Fatalf("edit %d: stale folded member %s", i, m.Name)
			}
			if m.Name != want.Members[k].Name || m.Value != want.Members[k].Value || got.MemberMap[m.Name] != m {
				t.Fatalf("edit %d member %d mismatch", i, k)
			}
		}
		if i == 1 && len(cold.Errors()) == 0 {
			t.Fatal("new member must invalidate exhaustive match")
		}
		if i == 2 && len(cold.Errors()) == 0 {
			t.Fatal("removed member must invalidate use")
		}
		if i == 3 && len(cold.Errors()) != 0 {
			t.Fatalf("restored members should restore validity: %s", retainedDiagnostics(cold))
		}
	}
}

func TestRetainedConstEnumRejectsDeclarationValueDependencies(t *testing.T) {
	for name, extra := range map[string]string{
		"constant": "\nconst saved: Choice = Choice.Second\n",
		"static":   "\nstatic def code() -> u8:\n    return Choice.First.u8()\n\nconst SAVED: u8 = code()\n",
		"contract": "\ndef requires_second(x: Choice) -> Choice:\n    requires x == Choice.Second\n    return x\n",
		"default":  "\ndef defaulted(x: Choice = Choice.Second) -> Choice:\n    return x\n",
	} {
		t.Run(name, func(t *testing.T) {
			source := retainedConstEnumBefore + extra
			_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
			edited := strings.Replace(source, "    First\n    Second\n", "    Second\n    First\n", 1)
			if _, err := state.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited)); err == nil {
				t.Fatal("unsafe declaration-phase dependency accepted")
			}
		})
	}
}

func TestRetainedConstEnumReorderRetractsNumericProof(t *testing.T) {
	before := retainedConstEnumBefore + "\ndef code() -> u8:\n    ensure result == 0\n    return Choice.First.u8()\n"
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	original, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	if len(original.Errors()) != 0 {
		t.Fatalf("fixture: %s", retainedDiagnostics(original))
	}
	after := strings.Replace(before, "    First\n    Second\n", "    Second\n    First\n", 1)
	for _, source := range []string{after, before, after} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), options)
		if source == after && len(want.Errors()) == 0 {
			t.Fatal("reordered member should invalidate the numeric proof")
		}
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("stale proof:\n%s\nwant:\n%s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
	}
}

func TestRetainedConstEnumIgnoresUnrelatedNamesAndComments(t *testing.T) {
	before := strings.ReplaceAll(retainedConstEnumBefore, "First", "Output") + "\nconst enum Other of u8:\n    Input\n    Output\n    # A sentence ends here. Output remains unrelated.\n    Last\n"
	after := strings.Replace(before, "    Output\n    Second\n", "    Second\n    Output\n", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	got, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	want := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if retainedDiagnostics(got) != retainedDiagnostics(want) {
		t.Fatal("unrelated enum changed replay")
	}
}

func TestRetainedConstEnumNewMemberRepairsShorthand(t *testing.T) {
	before := retainedConstEnumBefore + "\ndef take(c: Choice) -> Choice:\n    return c\n\ndef local() -> i64:\n    c: Choice = .Third\n    return 0\n\ndef through_call() -> i64:\n    take(.Third)\n    return 0\n"
	after := strings.Replace(before, "    Second\n", "    Second\n    Third\n", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	got, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	want := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if retainedDiagnostics(got) != retainedDiagnostics(want) {
		t.Fatalf("shorthand replay:\n%s\nwant:\n%s", retainedDiagnostics(got), retainedDiagnostics(want))
	}
}
