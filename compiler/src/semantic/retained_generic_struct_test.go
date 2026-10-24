package semantic

import (
	"elisacore/src/ast"
	"slices"
	"strings"
	"testing"
)

func TestRetainedGenericStructFieldsDiskRoundTrip(t *testing.T) {
	const base = `struct Box[T]:
    value: T

type Alias = Box[i64]
type Small = Box[u8]
struct Outer:
    box: Alias

def echo[T](value: Box[T]) -> Box[T]:
    return value

def construct() -> Alias:
    return Alias{value: 7}

def relay() -> Alias:
    return echo[i64](construct())

def read(value: Alias&) -> i64:
    return value.value

def layout() -> usize:
    return size_of(Alias) + size_of(Outer) + offset_of(Alias, value)

def small_layout() -> usize:
    return size_of(Small) + offset_of(Small, value)

def unrelated() -> i64:
    return 9
`
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	added := strings.Replace(base, "    value: T\n", "    value: T\n    probe: u64 = 0\n", 1)
	reordered := strings.Replace(base, "    value: T\n", "    probe: u64 = 0\n    value: T\n", 1)
	invalid := strings.Replace(base, "    value: T\n", "    value: bool\n", 1)
	for _, source := range []string{added, reordered, base, invalid, base} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		identity := state.a.namedTypes["Box"]
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if source == invalid && len(want.Errors()) == 0 {
			t.Fatal("invalid generic field retype must diagnose constructor/read")
		}
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("cold diagnostics differ\ngot %s\nwant %s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
		if identity != state.a.namedTypes["Box"] {
			t.Fatal("replaced generic base identity")
		}
		if state.LastStats.StructUsers != 6 {
			t.Fatalf("missing generic/alias/containing users: %+v", state.LastStats)
		}
	}
}

func TestRetainedGenericStructFixedBoundsAndBinderGuards(t *testing.T) {
	const base = `protocol Mark:
    type Item
impl Mark for i64:
    type Item = i64

struct Box[T: Mark]:
    value: T

def read() -> i64:
    value: Box[i64] = Box[i64]{value: 7}
    return value.value
`
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	added := strings.Replace(base, "    value: T\n", "    value: T\n    probe: u64 = 0\n", 1)
	got, err := state.Update(retainedParse(t, added), retainedParse(t, added), []byte(added))
	if err != nil {
		t.Fatal(err)
	}
	want := AnalyzeWithOptions(retainedParse(t, added), AnalyzeOptions{})
	if retainedDiagnostics(got) != retainedDiagnostics(want) || len(got.Errors()) != 0 {
		t.Fatalf("fixed-bound diagnostics: %v", got.Errors())
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.ReplaceAll(added, "T", "U")
	got, err = state.Update(retainedParse(t, renamed), retainedParse(t, renamed), []byte(renamed))
	if err != nil {
		t.Fatal(err)
	}
	want = AnalyzeWithOptions(retainedParse(t, renamed), AnalyzeOptions{})
	if retainedDiagnostics(got) != retainedDiagnostics(want) || len(got.Errors()) != 0 {
		t.Fatalf("bounded rename diagnostics: %v", got.Errors())
	}
	for _, source := range []string{strings.Replace(added, "Box[T: Mark]", "Box[T]", 1), strings.Replace(added, "Box[T: Mark]", "Box[T: Mark, U]", 1)} {
		old := retainedParse(t, added)
		current := retainedParse(t, source)
		oldStruct := old.Decls[2].(*ast.StructDecl)
		newStruct := current.Decls[2].(*ast.StructDecl)
		if retainedStructFieldEditAllowed(oldStruct, newStruct) {
			t.Fatal("accepted changed generic binder protocol")
		}
	}
}

func TestRetainedEditedRecordThroughGenericArgumentLayoutUsers(t *testing.T) {
	const base = `struct EditedRecord:
    value: i64
struct Wrapper[T]:
    value: T
struct ReferenceWrapper[T]:
    value: T&
type Alias = Wrapper[EditedRecord]
type Nested = Wrapper[Wrapper[EditedRecord]]
type ReferenceAlias = ReferenceWrapper[EditedRecord]
struct Outer:
    wrapper: Alias
struct ReferenceOuter:
    wrapper: ReferenceAlias

def layout_only() -> usize:
    return size_of(Outer) + offset_of(Outer, wrapper)
def nested_only() -> usize:
    return size_of(Nested)
def reference_only() -> usize:
    return size_of(ReferenceOuter)
`
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	added := strings.Replace(base, "    value: i64\n", "    value: i64\n    extra: u64 = 0\n", 1)
	for _, source := range []string{added, base, added} {
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
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(cold) || len(got.Errors()) != 0 {
			t.Fatalf("diagnostics: %v", got.Errors())
		}
		if state.LastStats.StructUsers != 2 {
			t.Fatalf("expected inline wrapper/containing queries only: %+v", state.LastStats)
		}
	}
}

func TestRetainedGenericStructParameterNamesDiskRoundTrip(t *testing.T) {
	const base = `struct Pair[A, B]:
    left: A
    right: B
type Alias = Pair[i64, u8]

def first[T](value: Pair[T, u8]) -> T:
    return value.left

def read() -> i64:
    value: Alias = Alias{left: 7, right: 9}
    return first[i64](value) + value.right.i64()

def layout() -> usize:
    return size_of(Alias) + offset_of(Alias, right)
`
	renamed := strings.Replace(base, "Pair[A, B]:\n    left: A\n    right: B", "Pair[Left, Right]:\n    left: Left\n    right: Right", 1)
	swapped := strings.Replace(base, "Pair[A, B]:\n    left: A\n    right: B", "Pair[B, A]:\n    left: B\n    right: A", 1)
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, source := range []string{renamed, base, swapped, renamed, base} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		canonical := state.a.namedTypes["Pair"].(*StructType)
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(cold) || len(got.Errors()) != 0 {
			t.Fatalf("cold mismatch: %v", got.Errors())
		}
		want := cold.NamedTypes["Pair"].(*StructType)
		if got.NamedTypes["Pair"] != canonical || !slices.Equal(canonical.TypeParams, want.TypeParams) {
			t.Fatal("canonical binder metadata stale")
		}
		for i, p := range want.GenericParams {
			if canonical.GenericParams[i].Name != p.Name {
				t.Fatal("generic binder metadata stale")
			}
		}
		if state.LastStats.StructUsers != 3 {
			t.Fatalf("expected all instance users: %+v", state.LastStats)
		}
	}
}
