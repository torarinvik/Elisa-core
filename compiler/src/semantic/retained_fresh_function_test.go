//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const retainedFreshBase = `struct P:
    a: i64

def first(value: i64) -> i64:
    return value + 1

module M:
    def inner(value: i64) -> i64:
        return value + 2

def last(value: i64) -> i64:
    return value + 3
`

const retainedFreshPublic = `struct P:
    a: i64

module M:
    public:
        def inner(value: i64) -> i64:
            return value + 2
`

const retainedFreshHelper = "def fresh_helper(a: i64, b: i64) -> i64:\n    c = a + b\n    if c > 3:\n        return (c * 2) - 1\n    return c\n\n"

func retainedFreshInsert(t *testing.T, base, before, text string) string {
	t.Helper()
	if !strings.Contains(base, before) {
		t.Fatalf("missing insertion point %q", before)
	}
	return strings.Replace(base, before, text+before, 1)
}

// The extended baseline must be what the whole-program scratch would have captured.
func assertFreshBaselineMatchesScratch(t *testing.T, state *RetainedAnalysis, source string) {
	t.Helper()
	want := scratchPreBody(retainedParse(t, source), AnalyzeOptions{})
	if state.pre.global != want.global {
		t.Fatalf("global differs%s", firstLineDifference(want.global, state.pre.global))
	}
	for key, value := range want.fingerprint {
		if state.pre.fingerprint[key] != value {
			t.Fatalf("fingerprint of %s differs%s", key, firstFieldDifference(value, state.pre.fingerprint[key]))
		}
		if state.pre.classification[key] != want.classification[key] {
			t.Fatalf("classification of %s: %q want %q", key, state.pre.classification[key], want.classification[key])
		}
		if state.pre.resolvedTypeNames.functions[key] != want.resolvedTypeNames.functions[key] {
			t.Fatalf("resolved type names of %s differ", key)
		}
	}
	if len(state.pre.fingerprint) != len(want.fingerprint) || len(state.pre.diags) != len(want.diags) {
		t.Fatalf("function or diagnostic set differs")
	}
}

func TestRetainedFreshFunctionSkipsScratch(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	indented := "    " + strings.ReplaceAll(strings.TrimSuffix(retainedFreshHelper, "\n"), "\n", "\n    ")
	indented = strings.ReplaceAll(indented, "\n    \n", "\n\n")
	for name, source := range map[string]string{
		"shifts later lines": retainedFreshInsert(t, retainedFreshBase, "def first", retainedFreshHelper),
		"in a module":        retainedFreshInsert(t, retainedFreshBase, "    def inner", indented+"\n"),
		"mid file":           retainedFreshInsert(t, retainedFreshBase, "def last", retainedFreshHelper),
		"calls":              retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper(a: i64) -> i64:\n    return first(a)\n\n"),
		"struct param":       retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper(a: P&) -> i64:\n    return a.a\n\n"),
		"typed local":        retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper(a: i64) -> i64:\n    b: i64 = a\n    return b\n\n"),
		"darray param":       retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper(a: darray[u32]&) -> usize:\n    return a.count\n\n"),
		"grown darray":       retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper(a: mutable darray[u32]&) -> void:\n    a.push(1)\n\n"),
		"allocating":         retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper() -> darray[u32]:\n    out: darray[u32] = [1, 2]\n    return out\n\n"),
		"shadowed scalar":    retainedFreshInsert(t, "module N:\n    struct u8:\n        a: bool\n\n"+retainedFreshBase, "def last", "def fresh_helper(a: u8) -> i64:\n    return 1\n\n"),
		"sview param":        retainedFreshInsert(t, retainedFreshBase, "def last", "def fresh_helper(a: sview) -> sview:\n    return a\n\n"),
	} {
		t.Run(name, func(t *testing.T) {
			base := retainedFreshBase
			if name == "shadowed scalar" {
				base = "module N:\n    struct u8:\n        a: bool\n\n" + retainedFreshBase
			}
			_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
			retainedLineShiftLazyUpdate(t, state, source, 0)
			if !state.LastStats.FastAdded || state.LastStats.Added != 1 {
				t.Fatalf("stats=%+v", state.LastStats)
			}
			assertFreshBaselineMatchesScratch(t, state, source)
			// The extended baseline keeps comparing cleanly with a real scratch.
			next := source + "\nconst LATER_CONST: i64 = 4\n"
			if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err != nil {
				t.Fatalf("later declaration edit: %v", err)
			}
		})
	}
}

func TestRetainedFreshFunctionInPublicSection(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	source := retainedFreshInsert(t, retainedFreshPublic, "        def inner", "        def fresh_helper(a: i64) -> i64:\n            return a\n\n")
	_, state := AnalyzeRetained(retainedParse(t, retainedFreshPublic), retainedParse(t, retainedFreshPublic), []byte(retainedFreshPublic), AnalyzeOptions{})
	retainedLineShiftLazyUpdate(t, state, source, 0)
	if !state.LastStats.FastAdded {
		t.Fatalf("stats=%+v", state.LastStats)
	}
	assertFreshBaselineMatchesScratch(t, state, source)
}

func TestRetainedFreshFunctionInPrivateSection(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	base := strings.Replace(retainedFreshPublic, "public:", "private:", 1)
	source := retainedFreshInsert(t, base, "        def inner", "        def fresh_helper(a: i64) -> i64:\n            return a\n\n")
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	retainedLineShiftLazyUpdate(t, state, source, 0)
	if !state.LastStats.FastAdded {
		t.Fatalf("stats=%+v", state.LastStats)
	}
	assertFreshBaselineMatchesScratch(t, state, source)
}

func TestRetainedFreshFunctionBodyStillDiagnoses(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	source := retainedFreshInsert(t, retainedFreshBase, "def first", "def fresh_helper(a: i64) -> bool:\n    return a + 1\n\n")
	_, state := AnalyzeRetained(retainedParse(t, retainedFreshBase), retainedParse(t, retainedFreshBase), []byte(retainedFreshBase), AnalyzeOptions{})
	warm, err := state.UpdatePrepared(retainedParse(t, source), nil, []byte(source), NewLazyParsedRetainedScratch(nil, AnalyzeOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(cold.Errors()) == 0 || retainedDiagnostics(cold) != retainedDiagnostics(warm) || !state.LastStats.FastAdded {
		t.Fatalf("cold=%s warm=%s stats=%+v", retainedDiagnostics(cold), retainedDiagnostics(warm), state.LastStats)
	}
}

func TestRetainedFreshFunctionDeclines(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	helper := func(text string) string { return retainedFreshInsert(t, retainedFreshBase, "def first", text+"\n\n") }
	for name, tc := range map[string]struct{ base, source string }{
		"appended at end": {retainedFreshBase, retainedFreshBase + "\n" + retainedFreshHelper},
		"default arg":     {retainedFreshBase, helper("def fresh_helper(a: i64 = 1) -> i64:\n    return a")},
		"contract":        {retainedFreshBase, helper("def fresh_helper(a: i64) -> i64:\n    requires a > 0\n    return a")},
		"annotated":       {retainedFreshBase, helper("@inline(always)\ndef fresh_helper(a: i64) -> i64:\n    return a")},
		"comment mention": {retainedFreshBase + "# fresh_helper\n", strings.Replace(retainedFreshBase+"# fresh_helper\n", "def first", "def fresh_helper(a: i64) -> i64:\n    return a\n\ndef first", 1)},
		"unresolved call": {strings.Replace(retainedFreshBase, "return value + 3", "return fresh_helper(value)", 1), strings.Replace(strings.Replace(retainedFreshBase, "return value + 3", "return fresh_helper(value)", 1), "def first", "def fresh_helper(a: i64) -> i64:\n    return a\n\ndef first", 1)},
		"plain program":   {"def first(value: i64) -> i64:\n    return value\n", "def fresh_helper(a: i64) -> i64:\n    return a\n\ndef first(value: i64) -> i64:\n    return value\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, state := AnalyzeRetained(retainedParse(t, tc.base), retainedParse(t, tc.base), []byte(tc.base), AnalyzeOptions{})
			source := tc.source
			scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) { return retainedParse(t, source), nil }, AnalyzeOptions{})
			warm, err := state.UpdatePrepared(retainedParse(t, source), nil, []byte(source), scratch)
			if state.LastStats.FastAdded {
				t.Fatalf("admitted: %+v", state.LastStats)
			}
			if err != nil {
				t.Logf("fell back: %v", err)
				return
			}
			cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
			if retainedDiagnostics(cold) != retainedDiagnostics(warm) {
				t.Fatalf("cold=%s warm=%s", retainedDiagnostics(cold), retainedDiagnostics(warm))
			}
		})
	}
}

func TestRetainedFreshFunctionSurvivesDiskAndLaterEdits(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	_, state := AnalyzeRetained(retainedParse(t, retainedFreshBase), retainedParse(t, retainedFreshBase), []byte(retainedFreshBase), AnalyzeOptions{})
	source := retainedFreshInsert(t, retainedFreshBase, "def first", retainedFreshHelper)
	retainedLineShiftLazyUpdate(t, state, source, 0)
	if !state.LastStats.FastAdded {
		t.Fatalf("stats=%+v", state.LastStats)
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	// Editing the added function and then calling it both follow the scratch path.
	for _, next := range []string{
		strings.Replace(source, "(c * 2) - 1", "(c * 3) - 1", 1),
		strings.Replace(strings.Replace(source, "(c * 2) - 1", "(c * 3) - 1", 1), "return value + 3", "return fresh_helper(value, 1)", 1),
	} {
		warm, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
		if retainedDiagnostics(cold) != retainedDiagnostics(warm) {
			t.Fatalf("cold=%s warm=%s", retainedDiagnostics(cold), retainedDiagnostics(warm))
		}
		assertRetainedCanonicalLiveResultParity(t, cold, warm)
	}
}

func TestRetainedBodyEditNamingUniqueScalarStruct(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const base = "struct Id:\n    value: u32\n\nstruct Holder:\n    items: darray[u32] = []\n\ndef first(v: i64) -> i64:\n    return v\n"
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	source := strings.Replace(base, "    return v\n", "    probe: Id = Id{value: 1}\n    return v\n", 1)
	retainedLineShiftLazyUpdate(t, state, source, 0)
	next := source + "\nconst LATER_CONST: i64 = 4\n"
	if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err != nil {
		t.Fatalf("later scratch comparison: %v", err)
	}

	_, state = AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	declined := strings.Replace(base, "    return v\n", "    probe: Holder = Holder{}\n    return v\n", 1)
	retainedLineShiftLazyUpdate(t, state, declined, 1)
}

func TestRetainedScopedDeclarationEdits(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	base := retainedFreshBase + "\nconst LIMIT: i64 = 3\n\ndef uses_limit(v: i64) -> i64:\n    return v + LIMIT\n"
	for name, source := range map[string]string{
		"new const":   retainedFreshInsert(t, base, "def last", "const FRESH_CONST: i64 = 9\n\n"),
		"new struct":  retainedFreshInsert(t, base, "def last", "struct FreshStruct:\n    a: i64\n    b: i64\n\n"),
		"const value": strings.Replace(base, "LIMIT: i64 = 3", "LIMIT: i64 = 4", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
			retainedLineShiftLazyUpdate(t, state, source, 0)
			if !state.LastStats.FastAdded {
				t.Fatalf("stats=%+v", state.LastStats)
			}
			assertFreshBaselineMatchesScratch(t, state, source)
			next := strings.Replace(source, "return value + 1", "return value + 5", 1)
			if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err != nil {
				t.Fatalf("later edit: %v", err)
			}
		})
	}
}
