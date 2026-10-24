package semantic

import (
	"strings"
	"testing"
)

const retainedRegionStructBefore = `struct Box[@r]:
    value: i64
    bytes: darray[u8] @r

struct Outer[@r]:
    held: Box[r] @r

def echo[@r](value: Box[r] @r) -> Box[r] @r:
    return value

def relay[@r](value: Box[r] @r) -> Box[r] @r:
    return echo[r](value)

def layout[@r](value: Box[r]&) -> usize:
    return size_of(Box[r]) + align_of(Box[r]) + offset_of(Box[r], bytes)

def nested[@r](value: Outer[r]&) -> usize:
    return size_of(Outer[r])

def first[@r](value: Box[r]&) -> u8:
    return value.bytes[0]

def unrelated() -> i64:
    return 9
`

func TestRetainedRegionStructFieldsDiskRoundTrip(t *testing.T) {
	before := retainedRegionStructBefore
	initial, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatalf("fixture: %s", retainedDiagnostics(initial))
	}
	added := strings.Replace(before, "    value: i64\n", "    value: i64\n    probe: u64 = 0\n", 1)
	narrowed := strings.Replace(before, "    value: i64\n", "    value: u8\n", 1)
	borrowed := strings.Replace(before, "bytes: darray[u8] @r", "bytes: darray[u8&] @r", 1)
	removed := strings.Replace(before, "    value: i64\n", "", 1)
	for _, source := range []string{added, narrowed, borrowed, removed, before} {
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
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if source == borrowed && len(want.Errors()) == 0 {
			t.Fatal("borrow-bearing field retype must invalidate scalar getter")
		}
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("stale region-record analysis\ngot %s\nwant %s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
		if state.LastStats.StructUsers != 5 {
			t.Fatalf("missing direct or containing users: %+v", state.LastStats)
		}
	}
}

func TestRetainedRegionStructBinderChangesStillFallBack(t *testing.T) {
	base := `struct Box[@r]:
    bytes: darray[u8] @r

def unrelated() -> i64:
    return 9
`
	for _, source := range []string{
		strings.ReplaceAll(base, "@r", "@s"),
		strings.Replace(base, "Box[@r]", "Box[T, @r]", 1),
		strings.Replace(base, "Box[@r]", "Box", 1),
	} {
		_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
		if _, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source)); err == nil {
			t.Fatal("changed generic/region binding protocol accepted")
		}
	}
}
