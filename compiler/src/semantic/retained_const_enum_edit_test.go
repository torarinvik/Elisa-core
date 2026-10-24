package semantic

import (
	"fmt"
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

func TestRetainedConstEnumStorageWidthDiskRoundTrip(t *testing.T) {
	before := retainedConstEnumBefore + "\ndef width() -> usize:\n    return size_of(Choice)\n"
	original, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if len(original.Errors()) != 0 {
		t.Fatal(retainedDiagnostics(original))
	}
	for _, storage := range []string{"u16", "u32", "i16", "u64", "u8"} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		oldType := state.a.namedTypes["Choice"]
		source := strings.Replace(before, "Choice of u8:", "Choice of "+storage+":", 1)
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("storage %s: %v", storage, err)
		}
		cold, coldState := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(cold); got != want {
			t.Fatalf("storage %s diagnostics: %s want %s", storage, got, want)
		}
		got, want := state.a.namedTypes["Choice"].(*ConstEnumType), coldState.a.namedTypes["Choice"].(*ConstEnumType)
		if got != oldType || got.Storage.String() != want.Storage.String() {
			t.Fatalf("storage %s: identity or storage mismatch", storage)
		}
		if state.LastStats.Changed != 3 {
			t.Fatalf("storage %s: expected the two enum users and size query, got %+v", storage, state.LastStats)
		}
	}
}

func TestRetainedConstEnumStorageContainingLayouts(t *testing.T) {
	before := retainedConstEnumBefore + `
type Alias = Choice
struct Holder layout(c):
    pad: u8
    choice: Alias
    payload: u32
struct Outer:
    held: Holder

def echo_holder(value: Holder) -> Holder:
    return value

def outer_size() -> usize:
    return size_of(Outer)

def layout() -> usize:
    return size_of(Holder) + align_of(Holder) + offset_of(Holder, choice)
`
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for _, storage := range []string{"u16", "u64", "u8"} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		source := strings.Replace(before, "Choice of u8:", "Choice of "+storage+":", 1)
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("%s: %v", storage, err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("%s: diagnostics differ", storage)
		}
		if state.LastStats.Changed != 5 {
			t.Fatalf("%s: containing layouts were not invalidated: %+v", storage, state.LastStats)
		}
	}
}

func TestRetainedConstEnumStorageHeaderDiagnostics(t *testing.T) {
	cases := map[string]string{
		"requires": `def limited() -> i64:
    requires size_of(Choice) == 1
    return 7

def caller() -> i64:
    return limited()
`,
		"default": `def defaulted(value: usize = size_of(Choice)) -> usize:
    ensure result == value
    return value

def caller() -> usize:
    ensure result == 1
    return defaulted()
`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			before := retainedConstEnumBefore + "\n" + extra
			options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
			_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			after := strings.Replace(before, "Choice of u8:", "Choice of u16:", 1)
			got, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
			want := AnalyzeWithOptions(retainedParse(t, after), options)

			if err != nil {
				t.Fatalf("supported storage/header replay rejected: %v", err)
			}
			if retainedDiagnostics(got) != retainedDiagnostics(want) {
				t.Fatalf("stale header proof: %s\nwant %s", retainedDiagnostics(got), retainedDiagnostics(want))
			}
		})
	}
}

func TestRetainedConstEnumWideningRetractsRangeProof(t *testing.T) {
	before := retainedConstEnumBefore + `
def bounded() -> u8:
    ensure result == 0
    return Choice.First.u8()
`
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	original, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	if len(original.Errors()) != 0 {
		t.Fatalf("fixture: %s", retainedDiagnostics(original))
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	members := ""
	for i := 1; i <= 298; i++ {
		members += fmt.Sprintf("    Extra%d\n", i)
	}
	after := strings.Replace(before, "Choice of u8:", "Choice of u16:", 1)
	after = strings.Replace(after, "    First\n", members+"    First\n", 1)
	got, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	want := AnalyzeWithOptions(retainedParse(t, after), options)
	if len(want.Errors()) == 0 {
		t.Fatal("new values did not invalidate range proof")
	}
	if retainedDiagnostics(got) != retainedDiagnostics(want) {
		t.Fatalf("stale range proof: %s\nwant %s", retainedDiagnostics(got), retainedDiagnostics(want))
	}
}

func TestRetainedConstEnumExplicitValuesDiskRoundTrip(t *testing.T) {
	before := retainedConstEnumBefore + "\ndef code() -> u8:\n    ensure result == 0\n    return Choice.First.u8()\n"
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	initial, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	if len(initial.Errors()) != 0 {
		t.Fatalf("fixture: %s", retainedDiagnostics(initial))
	}
	for _, expression := range []string{"64", "(8 << 3)", "64 if true else 128", "0", ""} {
		source := before
		if expression != "" {
			source = strings.Replace(source, "    First\n", "    First = "+expression+"\n", 1)
		}
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
			t.Fatalf("%s: %v", expression, err)
		}
		cold, want := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), options)
		if retainedDiagnostics(got) != retainedDiagnostics(cold) {
			t.Fatalf("%s: stale explicit-member proof\ngot %s\nwant %s", expression, retainedDiagnostics(got), retainedDiagnostics(cold))
		}
		if expression != "" && expression != "0" && len(cold.Errors()) == 0 {
			t.Fatal("changed explicit value did not retract proof")
		}
		members := state.a.namedTypes["Choice"].(*ConstEnumType).Members
		wanted := want.a.namedTypes["Choice"].(*ConstEnumType).Members
		for i, member := range members {
			if member.Value != wanted[i].Value || state.a.constValues["Choice."+member.Name].Int != want.a.constValues["Choice."+member.Name].Int || state.a.constValues["Choice."+member.Name].Kind != want.a.constValues["Choice."+member.Name].Kind {
				t.Fatalf("%s: stale ordinal/folded value", expression)
			}
		}
	}
}

func TestRetainedConstEnumExplicitExternalDependenciesStayGuarded(t *testing.T) {
	for _, expression := range []string{"BASE", "BASE + 1", "seed()", "size_of(u8)"} {
		before := "const BASE: i64 = 64\n\nstatic def seed() -> i64:\n    return 64\n" + retainedConstEnumBefore
		_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
		source := strings.Replace(before, "    First\n", "    First = "+expression+"\n", 1)
		if _, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source)); err == nil {
			t.Fatalf("accepted nonclosed expression %s", expression)
		}
	}
}
