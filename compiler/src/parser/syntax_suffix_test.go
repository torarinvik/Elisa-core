package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"strings"
	"testing"
)

func TestSyntaxSuffixRejectsStateChangesAndBackwardReads(t *testing.T) {
	source := "def first() -> i64:\n    return 1\n\ndef second() -> i64:\n    return 2\n\ndef third() -> i64:\n    return 3\n"
	for _, mode := range []string{"state", "backward", "positions"} {
		t.Run(mode, func(t *testing.T) {
			old := lexer.TokenizeSource("certificate.elisa", []byte(source), nil)
			p := NewWithSyntaxTrace(old.CopyTokens())
			file := p.ParseFile("certificate.elisa")
			trace := p.SyntaxTrace()
			if trace == nil {
				t.Fatal("trace")
			}
			snapshot, err := ast.NewSyntaxSnapshot(file, 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(source, "return 1", "return 4", 1)
			tokens := lexer.TokenizeSource("certificate.elisa", []byte(changed), old).CopyTokens()
			start := len(tokens) - old.MatchingTokenSuffix(tokens)
			if mode == "state" {
				for i := range trace.checkpoints {
					trace.checkpoints[i].rebindCounter++
				}
			}
			if mode == "backward" {
				for i := range trace.checkpoints {
					trace.checkpoints[i].readFrom = 0
				}
			}
			if mode == "positions" {
				start = len(tokens)
			}
			calls := 0
			replay := NewWithSyntaxTrace(tokens)
			replay.SetSyntaxSuffixReplay(trace, start, func(n int) (*ast.File, error) { calls++; return snapshot.CopySuffix(n) }, nil)
			got := replay.ParseFile("certificate.elisa")
			fresh := New(lexer.New("certificate.elisa", []byte(changed)).Tokenize())
			want := fresh.ParseFile("certificate.elisa")
			if calls != 0 || !reflect.DeepEqual(got, want) || len(replay.Errors()) != 0 {
				t.Fatalf("replayed uncertified suffix: calls=%d errors=%v", calls, replay.Errors())
			}
		})
	}
}
