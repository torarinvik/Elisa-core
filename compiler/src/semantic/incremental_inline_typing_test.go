//go:build cgo

package semantic

import "testing"

func TestIncrementalTypingPreservesCurrentInlineMode(t *testing.T) {
	const body = "def step(value: int) -> int:\n    return value + 1\n"
	session := NewIncrementalAnalysisSession()
	cases := []struct {
		prefix string
		mode   FuncInlineMode
		reused bool
		bad    bool
	}{
		{"", FuncInlineModeDefault, false, false},
		{"@inline(always)\n", FuncInlineModeAlways, true, false},
		{"@inline(never)\n", FuncInlineModeNever, true, false},
		{"@inline(force)\n", FuncInlineModeAlways, true, false},
		{"@inline(unknown)\n", FuncInlineModeDefault, false, true},
		{"@inline()\n", FuncInlineModeDefault, false, true},
	}
	for _, tc := range cases {
		source := tc.prefix + body
		cold := Analyze(parseIncrementalSessionFile(t, source))
		before := session.Stats()
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		assertIncrementalResultParity(t, cold, warm)
		if tc.bad && len(warm.Errors()) == 0 {
			t.Fatal("invalid inline annotation was hidden")
		}
		symbol, ok := warm.GlobalScope.Lookup("step")
		if !ok {
			t.Fatal("missing step symbol")
		}
		signature, ok := symbol.Type.(*FuncType)
		if !ok || signature.InlineMode != tc.mode {
			t.Fatalf("stale inline signature for %q: %+v", tc.prefix, signature)
		}
		after := session.Stats()
		if tc.reused && after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
			t.Fatalf("inline metadata blocked unchanged typing: %q", tc.prefix)
		}
		if tc.bad && after.PrimaryBodiesReused != before.PrimaryBodiesReused {
			t.Fatal("malformed annotation entered optional typing cache")
		}
	}
}
