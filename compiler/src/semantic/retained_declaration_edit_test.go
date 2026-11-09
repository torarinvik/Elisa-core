package semantic

import (
	"fmt"
	"strings"
	"testing"
)

// A helper added inside an `extend` block's private section updates in place:
// it lands in the module's namespace with its section's visibility, so a use
// from outside the module is rejected exactly as a fresh analysis rejects it.
func TestRetainedAnalysisAcceptsAddedModuleMember(t *testing.T) {
	added := strings.Replace(retainedModuleBefore, "            return x + x\n",
		"            return x + x\n        def thrice(x: i64) -> i64:\n            v: mutable i64 = x\n            base: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n            return x + x + x\n", 1)
	used := strings.Replace(added, "return twice(base(x))", "return thrice(twice(base(x)))", 1)
	leaked := strings.Replace(used, "return Tool::run(x)", "return Tool::thrice(x)", 1)
	if added == retainedModuleBefore || used == added || leaked == used {
		t.Fatal("fixture edit did not apply")
	}
	options := AnalyzeOptions{}
	before := AnalyzeWithOptions(retainedParse(t, retainedModuleBefore), options)
	if d := retainedDiagnostics(before); strings.Contains(d, "error") {
		t.Fatalf("fixture does not analyze cleanly:\n%s", d)
	}
	for _, step := range []string{added, used, leaked} {
		_, retained := AnalyzeRetained(retainedParse(t, retainedModuleBefore), retainedParse(t, retainedModuleBefore), []byte(retainedModuleBefore), options)
		if retained == nil {
			t.Fatal("analysis was not retained")
		}
		updated, err := retained.Update(retainedParse(t, step), retainedParse(t, step), []byte(step))
		if err != nil {
			t.Fatalf("adding a module member fell back: %v", err)
		}
		if retained.LastStats.Added != 1 {
			t.Fatalf("stats = %+v, want 1 added", retained.LastStats)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, step), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("retained update disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", got, want)
		}
	}
	freshLeak := retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, leaked), options))
	if !strings.Contains(freshLeak, "thrice") {
		t.Fatalf("the private-use fixture is not rejected:\n%s", freshLeak)
	}
}

// A member's signature edit replaces it inside its block: callers are re-analyzed
// against the new signature, exactly as a fresh analysis sees them.
func TestRetainedAnalysisAcceptsModuleMemberSignatureEdits(t *testing.T) {
	steps := []string{
		strings.Replace(retainedModuleBefore, "def twice(x: i64) -> i64:\n            return x + x", "def twice(n: i64) -> i64:\n            return n + n", 1),
		strings.Replace(retainedModuleBefore, "def run(x: i64) -> i64:", "def run(x: i32) -> i64:", 1),
		strings.Replace(retainedModuleBefore, "def twice(x: i64) -> i64:", "def twice(x: bool) -> i64:", 1),
	}
	options := AnalyzeOptions{}
	for i, src := range steps {
		if src == retainedModuleBefore {
			t.Fatalf("fixture edit %d did not apply", i)
		}
		_, retained := AnalyzeRetained(retainedParse(t, retainedModuleBefore), retainedParse(t, retainedModuleBefore), []byte(retainedModuleBefore), options)
		if retained == nil {
			t.Fatal("analysis was not retained")
		}
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("member signature edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("member signature edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
		if i == 2 && len(fresh.Errors()) == 0 {
			t.Fatal("fixture: the member's new parameter type must reject its caller")
		}
		// And back: the block keeps exactly one declaration of the member.
		reverted, err := retained.Update(retainedParse(t, retainedModuleBefore), retainedParse(t, retainedModuleBefore), []byte(retainedModuleBefore))
		if err != nil {
			t.Fatalf("revert of member edit %d fell back: %v", i, err)
		}
		if got, want := retainedDiagnostics(reverted), retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, retainedModuleBefore), options)); got != want {
			t.Fatalf("revert of member edit %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}

const retainedStructBefore = `
struct Point:
    x: i64
    y: i64 = 0

def norm(p: Point) -> i64:
    return p.x + p.y

def make(v: i64) -> Point:
    return Point{x: v}

def unrelated(n: i64) -> i64:
    return n * 2
`

// An edit to a plain struct re-analyzes its users against the new fields and leaves the rest.
func TestRetainedAnalysisAcceptsPlainStructEdits(t *testing.T) {
	steps := []string{
		strings.Replace(retainedStructBefore, "    y: i64 = 0\n", "    y: i64 = 0\n    z: i64 = 1\n", 1),
		strings.Replace(retainedStructBefore, "    y: i64 = 0\n", "    y: bool = false\n", 1),
		strings.Replace(retainedStructBefore, "    x: i64\n", "", 1),
	}
	options := AnalyzeOptions{}
	for i, src := range steps {
		if src == retainedStructBefore {
			t.Fatalf("fixture edit %d did not apply", i)
		}
		_, retained := AnalyzeRetained(retainedParse(t, retainedStructBefore), retainedParse(t, retainedStructBefore), []byte(retainedStructBefore), options)
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("struct edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("struct edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
		if i > 0 && len(fresh.Errors()) == 0 {
			t.Fatalf("fixture: struct edit %d must break a user", i)
		}
		// An added defaulted scalar field is invisible to the reader `norm`.
		want := 2
		if i == 0 {
			want = 1
		}
		if users := retained.LastStats.StructUsers; users != want {
			t.Fatalf("struct edit %d re-analyzed %d functions, want %d", i, users, want)
		}
	}
}

// A struct edit and a signature edit in one update: the re-declared function uses the
// struct, and the struct-user pass must not splice its new body under its old header.
func TestRetainedStructEditSkipsSignatureReplacedUser(t *testing.T) {
	before := `
module Sem:
    struct Spans:
        count: mutable usize = 0

    def fill(offsets: darray[u32]&, spans: mutable Spans&) -> void:
        can Memory.Allocate, Abort.Panic:
            for row in 0..<offsets.count |spans|:
                spans.count <- spans.count + 1

def main() -> i32:
    can Memory.Allocate, Abort.Panic:
        offsets: darray[u32] = [1, 2]
        spans: mutable Sem::Spans = Sem::Spans{}
        Sem::fill(offsets, spans)
        return 0
`
	after := `
module Sem:
    struct Spans:
        count: mutable usize = 0
        hits: mutable usize = 0

    def fill(regions: darray[u32]&, offsets: darray[u32]&, spans: mutable Spans&) -> void:
        can Memory.Allocate, Abort.Panic:
            if regions.count == offsets.count:
                for row in 0..<regions.count |spans, regions, offsets|:
                    spans.hits <- spans.hits + 1 if regions[row] == offsets[row]
            for row in 0..<offsets.count |spans|:
                spans.count <- spans.count + 1

def main() -> i32:
    can Memory.Allocate, Abort.Panic:
        offsets: darray[u32] = [1, 2]
        spans: mutable Sem::Spans = Sem::Spans{}
        Sem::fill(offsets, offsets, spans)
        return 0
`
	broken := strings.Replace(after, "spans.hits <- spans.hits", "spans.nope <- spans.hits", 1)
	options := AnalyzeOptions{}
	for i, src := range []string{after, broken} {
		_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
		if (i == 1) != (len(fresh.Errors()) != 0) {
			t.Fatalf("fixture: edit %d has unexpected errors:\n%s", i, retainedDiagnostics(fresh))
		}
	}
}

// A kept diagnostic names body-local gensyms after the byte offset of their expression; a
// body that moved takes the names a fresh analysis gives it, and only those names.
func TestRetainedShiftLocalGensyms(t *testing.T) {
	in := "known: __quantifier_100 <- false; __auto_7 and __MachineMode_ab_100 stay"
	want := "known: __quantifier_112 <- false; __auto_19 and __MachineMode_ab_100 stay"
	if got := retainedShiftLocalGensyms(in, 12); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := retainedShiftLocalGensyms(in, 0); got != in {
		t.Fatalf("a zero shift changed %q to %q", in, got)
	}
}

// A callee's contract is part of its signature: a caller's proof reads it, so editing it
// re-checks the caller.
func TestRetainedContractEditRechecksCallers(t *testing.T) {
	before := `
def pick(x: u8, y: u8) -> u8:
    ensure result == x
    r: mutable u8 = x
    for i in 0 ..< 3:
        r = x
    return r

def caller(x: u8, y: u8) -> u8:
    ensure result == x
    return pick(x, y)
`
	after := strings.Replace(before, "    ensure result == x\n    r:", "    ensure result == x or result == y\n    r:", 1)
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	for i, src := range []string{after, before, after} {
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if src == after && len(fresh.Errors()) == 0 {
			t.Fatalf("fixture: the weaker callee contract should leave caller's ensure unproven:\n%s", retainedDiagnostics(fresh))
		}
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			continue // a fallback is a fresh analysis
		}
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}

func TestRetainedRemovesModuleMemberAndRechecksCallers(t *testing.T) {
	before := `module Lib:
    def helper() -> i64:
        return 42

    def kept() -> i64:
        return 1

def main() -> i64:
    return Lib::helper()
`
	after := strings.Replace(before, "    def helper() -> i64:\n        return 42\n\n", "", 1)
	_, r := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for _, src := range []string{after, before} {
		got, err := r.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("module member removal/revert fell back: %v", err)
		}
		want := AnalyzeWithOptions(retainedParse(t, src), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("module member update differs:\n%s\nwant:\n%s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
	}
}

// Removing a function that returns caller-arena storage changes one declaration
// table entry. Surviving region summaries must still agree with cold analysis,
// including after a disk round trip and a later caller edit.
func TestRetainedRemovesCallerArenaFunctionAcrossDiskState(t *testing.T) {
	const removed = "def unused_list(n: i64) -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(n)\n    return out\n\n"
	const survivor = "def make_list(n: i64) -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(n)\n    return out\n\ndef use_list() -> i64:\n    xs: darray[i64] = make_list(3)\n    return xs.count.i64()\n"
	before := removed + survivor
	options := AnalyzeOptions{}
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	summaries := func(state *RetainedAnalysis) map[string]bool {
		out := map[string]bool{}
		for fn, sym := range state.a.funcDeclSymbols {
			if ft, ok := sym.Type.(*FuncType); ok {
				out[fn.Name] = state.a.regionPolyReturnsCallerArena[ft]
			}
		}
		return out
	}
	if !summaries(state)["unused_list"] || !summaries(state)["make_list"] {
		t.Fatal("fixture did not classify caller-arena returns")
	}
	for step, source := range []string{survivor, before, survivor, strings.Replace(survivor, "return xs.count.i64()", "return xs.count.i64() + 1", 1)} {
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
			t.Fatalf("step %d fell back: %v", step, err)
		}
		fresh, cold := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diagnostics diverged\ngot %s\nwant %s", step, got, want)
		}
		got, want := summaries(state), summaries(cold)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("step %d region summaries diverged: %v versus %v", step, got, want)
		}
		for ft := range state.a.regionPolyReturnsCallerArena {
			if ft.Name == "unused_list" && !strings.Contains(source, "def unused_list") {
				t.Fatalf("step %d retains removed region summary", step)
			}
		}
	}
}

func TestRetainedRemovalDoesNotHideSurvivingRegionSummaryChange(t *testing.T) {
	const removed = "def unused_scalar(x: i64) -> i64:\n    return x\n\n"
	const maker = "def make_list() -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(1)\n    return out\n"
	before := "global backing: darray[i64] = []\n\n" + removed + maker
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	after := strings.Replace(before, removed, "", 1)
	after = strings.Replace(after, maker, "def make_list() -> darray[i64]:\n    return backing\n", 1)
	_, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err == nil || !strings.Contains(err.Error(), "declaration results of ::make_list#0 changed") {
		t.Fatalf("expected surviving declaration mismatch, got %v", err)
	}
}

func TestRetainedOverloadEditsPreserveOtherNamespaces(t *testing.T) {
	const prefix = "# namespace probe\n"
	const top = "def pick(x: i64) -> i64:\n    return x + 1\n\n"
	const member = "        def pick(x: i64) -> i64:\n            return x + 2\n\n"
	const module = "module Outer:\n    module Lib:\n" + member + "        def kept() -> i64:\n            return 1\n\ndef caller() -> i64:\n    return Outer::Lib::pick(40)\n"
	const extra = "def pick(x: bool) -> i64:\n    return 3\n\n"
	before := prefix + top + module
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	cases := []struct{ source, keep string }{
		{prefix + module, "Outer.Lib.pick"}, {before, "Outer.Lib.pick"},
		{prefix + top + extra + module, "Outer.Lib.pick"}, {before, "Outer.Lib.pick"},
		{prefix + top + strings.Replace(module, member, "", 1), "pick"}, {before, "pick"},
		{prefix + module, "Outer.Lib.pick"},
	}
	for step, tc := range cases {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		symbol, ok := state.a.globalScope.Lookup(tc.keep)
		if !ok {
			t.Fatalf("fixture lost %s", tc.keep)
		}
		prior := symbol.Type
		updated, err := state.Update(retainedParse(t, tc.source), retainedParse(t, tc.source), []byte(tc.source))
		if err != nil {
			t.Fatalf("step %d fell back: %v", step, err)
		}
		symbol, ok = state.a.globalScope.Lookup(tc.keep)
		if !ok || symbol.Type != prior {
			t.Fatalf("step %d re-declared unrelated %s", step, tc.keep)
		}
		if state.a.functionTypes[tc.keep] != prior {
			t.Fatalf("step %d removed surviving function type %s", step, tc.keep)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, tc.source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\ngot %s\nwant %s", step, got, want)
		}
	}
}

func TestRetainedRemovalRechecksSameNamedNamespaceCaller(t *testing.T) {
	const removed = "    def shared() -> i64:\n        return 1\n\n"
	before := "# namespace caller probe\nmodule Gone:\n" + removed + "    def kept() -> i64:\n        return 0\n\nmodule Kept:\n    def shared() -> i64:\n        return Gone::shared()\n"
	after := strings.Replace(before, removed, "", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for step, source := range []string{after, before, after} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if source == after && len(fresh.Errors()) == 0 {
			t.Fatal("fixture did not lose removed callee")
		}
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\ngot %s\nwant %s", step, got, want)
		}
	}
}
