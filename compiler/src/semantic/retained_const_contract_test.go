package semantic

import (
	"strings"
	"testing"
)

func TestRetainedConstValueContractsInvalidateCallersFromDisk(t *testing.T) {
	cases := map[string]string{
		"postcondition": `const LIMIT: i64 = 1

def answer() -> i64:
    ensure result == LIMIT
    return LIMIT

def forward() -> i64:
    ensure result == LIMIT
    return answer()

def client() -> i64:
    ensure result == 1
    return forward()
`,
		"precondition": `const LIMIT: i64 = 1

def accept(value: i64) -> i64:
    requires value == LIMIT
    return value

def bridge() -> i64:
    return accept(1)

def client() -> i64:
    return bridge()
`,
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
			initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), options)
			if len(initial.Errors()) != 0 {
				t.Fatalf("fixture: %s", retainedDiagnostics(initial))
			}
			for _, value := range []string{"2", "1", "3", "1"} {
				data, err := state.MarshalState()
				if err != nil {
					t.Fatal(err)
				}
				state, err = RestoreAnalysisState(data)
				if err != nil {
					t.Fatal(err)
				}
				source := strings.Replace(base, "LIMIT: i64 = 1", "LIMIT: i64 = "+value, 1)
				got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
				if err != nil {
					t.Fatal(err)
				}
				want := AnalyzeWithOptions(retainedParse(t, source), options)
				if value != "1" && len(want.Errors()) == 0 {
					t.Fatal("fixture did not retract a proof")
				}
				if retainedDiagnostics(got) != retainedDiagnostics(want) {
					t.Fatalf("value %s: stale contract/caller proof\ngot %s\nwant %s", value, retainedDiagnostics(got), retainedDiagnostics(want))
				}
				if state.LastStats.ConstUsers != 3 {
					t.Fatalf("missing caller closure: %+v", state.LastStats)
				}
			}
		})
	}
}

func TestRetainedConstContractKeepsDeclarationDependenciesGuarded(t *testing.T) {
	for name, extra := range map[string]string{
		"array":   "def value(input: i64[LIMIT]) -> i64:\n    return 0\n",
		"default": "def value(input: i64 = LIMIT) -> i64:\n    return input\n",
		"global":  "global value: i64 = LIMIT\n",
	} {
		t.Run(name, func(t *testing.T) {
			base := "const LIMIT: i64 = 1\n\n" + extra
			_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
			source := strings.Replace(base, "LIMIT: i64 = 1", "LIMIT: i64 = 2", 1)
			if _, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source)); err == nil {
				t.Fatal("declaration-phase dependency accepted")
			}
		})
	}
}

func TestRetainedConstContractReadsEditedCallerSyntax(t *testing.T) {
	const base = `const LIMIT: i64 = 1

def accept(value: i64) -> i64:
    requires value == LIMIT
    return value

def bridge() -> i64:
    return 1

def client() -> i64:
    return bridge()
`
	for _, cached := range []bool{false, true} {
		options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
		_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), options)
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		if !cached {
			state.bodyUses = nil
		}
		source := strings.Replace(base, "LIMIT: i64 = 1", "LIMIT: i64 = 2", 1)
		source = strings.Replace(source, "    return 1\n", "    return accept(1)\n", 1)
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), options)
		if len(want.Errors()) == 0 || retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("cached=%v: stale edited caller: %s\nwant %s", cached, retainedDiagnostics(got), retainedDiagnostics(want))
		}
		if state.LastStats.ConstUsers != 3 {
			t.Fatalf("cached=%v: omitted transitive caller: %+v", cached, state.LastStats)
		}
	}
}
