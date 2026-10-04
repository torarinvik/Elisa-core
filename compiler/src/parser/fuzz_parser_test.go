package parser

import (
	"strings"
	"testing"
	"time"

	"elisacore/internal/fuzzseed"
	"elisacore/src/lexer"
)

func fuzzParse(src []byte) ([]string, []string) {
	l := lexer.New("fuzz.elisa", src)
	toks := l.Tokenize()
	p := New(toks)
	p.ParseFile("fuzz.elisa")
	return append(l.Errors(), p.Errors()...), p.Notices()
}

// FuzzParser: lexing+parsing any input must terminate, never panic, and give the same
// diagnostics on a second run (the parser has no business depending on map order).
func FuzzParser(f *testing.F) {
	for _, s := range fuzzseed.Seeds() {
		f.Add(s)
	}
	fuzzseed.LimitStack()
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > fuzzseed.MaxInput {
			return
		}
		fuzzseed.NoteInput(src)
		stop := fuzzseed.Watchdog(20*time.Second, "parser", src)
		defer stop()
		errs1, notes1 := fuzzParse(src)
		errs2, notes2 := fuzzParse(src)
		if strings.Join(errs1, "\n") != strings.Join(errs2, "\n") || strings.Join(notes1, "\n") != strings.Join(notes2, "\n") {
			t.Fatalf("nondeterministic parse diagnostics:\n%v\n---\n%v", append(errs1, notes1...), append(errs2, notes2...))
		}
	})
}
