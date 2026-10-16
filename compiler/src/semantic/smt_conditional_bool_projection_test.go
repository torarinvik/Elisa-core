//go:build cgo

package semantic

import "testing"

func TestSMTConditionalRecordBoolProjection(t *testing.T) {
	for _, tc := range []struct {
		name, condition, expected string
		reject                    bool
	}{
		{"identity", "flag", "flag", false},
		{"wrong", "flag", "(not flag)", true},
		{"pure_call", "identity(flag)", "flag", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `struct Pair:
    flag: bool
    value: i64
def identity(flag: bool) -> bool:
    ensure result == flag
    return flag
def choose(flag: bool) -> Pair:
    ensure result.flag == ` + tc.expected + `
    return Pair{flag: true, value: 1} if ` + tc.condition + ` else Pair{flag: false, value: 2}
`
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			if got := ensureFailuresFor(result.Errors(), "choose") > 0; got != tc.reject {
				t.Fatalf("rejected %v, want %v: %v", got, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTConditionalProjectionDoesNotReplayMutatedCondition(t *testing.T) {
	for _, expected := range []string{"result.value == (1 if flag else 2)", "result.flag == flag"} {
		for _, condition := range []string{"flag", "read(flag)"} {
			source := `struct Pair:
    flag: bool
    value: i64
def read(flag: bool&) -> bool:
    return flag
def clear(flag: mutable bool&) -> bool:
    flag <- false
    return true
def wrong(flag: mutable bool&) -> Pair:
    ensure ` + expected + `
    return Pair{flag: clear(flag), value: 1} if ` + condition + ` else Pair{flag: false, value: 2}
`
			result := analyzeContractStrict(t, "mutable_condition.elisa", source)
			if ensureFailuresFor(result.Errors(), "wrong") == 0 {
				t.Fatalf("must not re-evaluate %s changed by the selected arm (%s): %v", condition, expected, result.Errors())
			}
		}
	}
}
