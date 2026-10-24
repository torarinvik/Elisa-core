package semantic

import (
	"strings"
	"testing"
)

func TestRetainedAdditiveScalarFieldSkipsReaders(t *testing.T) {
	const base = `struct Tok:
    kind: u8
    start: i64

struct Holder:
    tok: Tok

def read(t: Tok) -> i64:
    return t.start + 1

def forward(t: Tok) -> Tok:
    return t

def make() -> Tok:
    return Tok{kind: 1, start: 2}

def positional() -> Tok:
    return Tok(1, 2)

def same(a: Tok, b: Tok) -> bool:
    return a == b

def blank() -> Tok:
    return zeroed

def width() -> usize:
    return size_of(Holder)

def nested(h: Holder) -> i64:
    return h.tok.start

def other() -> i64:
    return 7
`
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	added := strings.Replace(base, "    start: i64\n", "    start: i64\n    probe: u64 = 0\n", 1)
	middle := strings.Replace(base, "    kind: u8\n", "    kind: u8\n    flag: bool = false\n", 1)
	for _, source := range []string{added, base, middle, base} {
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
		want, _ := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("cold diagnostics differ: %s\nwant %s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
		if source != base && state.LastStats.StructUnobserved != 3 {
			t.Fatalf("readers read, forward and nested should be kept: %+v", state.LastStats)
		}
	}
}
