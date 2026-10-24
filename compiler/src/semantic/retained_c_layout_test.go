package semantic

import (
	"strings"
	"testing"
)

func TestRetainedCLayoutModesAndFieldsDiskRoundTrip(t *testing.T) {
	const base = `struct Pair:
    tag: u8
    value: i64

type Alias = Pair
struct Outer:
    pair: Alias

def echo(value: Pair) -> Pair:
    return value

def layout() -> usize:
    return size_of(Alias) + align_of(Pair) + offset_of(Pair, value)

def nested() -> usize:
    return size_of(Outer)

def other() -> i64:
    return 7
`
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	c := strings.Replace(base, "struct Pair:", "struct Pair layout(c):", 1)
	added := strings.Replace(c, "    value: i64\n", "    value: i64\n    extra: i64 = 0\n", 1)
	for _, source := range []string{c, added, c, base} {
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
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("cold diagnostics differ: %s\nwant %s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
		current := state.a.namedTypes["Pair"].(*StructType)
		expected := cold.a.namedTypes["Pair"].(*StructType)
		if current != identity || current.ReprC != expected.ReprC || current.Layout != expected.Layout || current.PackedLayout != expected.PackedLayout {
			t.Fatal("stale layout metadata or replaced identity")
		}
		if state.LastStats.StructUsers != 3 {
			t.Fatalf("missing direct/alias/containing layout users: %+v", state.LastStats)
		}
	}
}

func TestRetainedNameTextPrefilterParity(t *testing.T) {
	for _, names := range []map[string]bool{{"Pair": true, "Outer": true}, {"名": true, "N.Record": true}, {"a+b": true, "_": true}} {
		pattern := retainedNamesPattern(names)
		pieces := []string{"", "Pair", "PairExtra", " Pair ", "Outer", "_Pair", "N.Record", "名", "a+b", "_", "\n", ".", "()", "x", "é", "# Pair", "\"Outer\""}
		for _, a := range pieces {
			for _, b := range pieces {
				for _, c := range pieces {
					source := a + b + c
					if retainedNameTextMatch(source, names, pattern) != pattern.MatchString(source) {
						t.Fatalf("prefilter differs for %q", source)
					}
				}
			}
		}
	}
}
