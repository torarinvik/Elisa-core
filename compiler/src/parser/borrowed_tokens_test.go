package parser

import (
	"elisacore/src/lexer"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestBorrowedTokenParseParityAndRecoveryIsolation(t *testing.T) {
	for _, source := range []string{
		"def value(x: i64) -> i64:\n    return x + 2\n",
		"def value():\n    to <- 1\n    for tail in [1, 2]:\n        pass\n",
		"def value():\n    for x in [1, 2]:\n        pass\ndef following() -> i64:\n    return 3\n",
	} {
		previous := lexer.TokenizeSource("borrowed.elisa", []byte(source), nil)
		pristine := previous.CopyTokens()
		owned := New(previous.CopyTokens())
		expected := owned.ParseFile("borrowed.elisa")
		for _, traced := range []bool{false, true} {
			var p *Parser
			if traced {
				p = NewWithBorrowedTokensAndSyntaxTrace(previous.BorrowReadOnlyTokens())
			} else {
				p = NewWithBorrowedTokens(previous.BorrowReadOnlyTokens())
			}
			actual := p.ParseFile("borrowed.elisa")
			if !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(p.Errors(), owned.Errors()) || !reflect.DeepEqual(p.Notices(), owned.Notices()) {
				t.Fatalf("borrowed parse changed AST/diagnostics: %q", source)
			}
			if !reflect.DeepEqual(pristine, previous.CopyTokens()) {
				t.Fatal("parser rewrote cached token storage")
			}
			if len(p.Errors()) > 0 && traced && p.SyntaxTrace() != nil {
				t.Fatal("recovery trace was reusable")
			}
			if len(p.Errors()) == 0 && !p.borrowedTokens {
				t.Fatal("normal borrowed parse copied the token buffer")
			}
		}
	}
}

func TestBorrowedTokenConcurrentRecovery(t *testing.T) {
	previous := lexer.TokenizeSource("concurrent.elisa", []byte("def work():\n    to <- 1\n    for tail in [1, 2]:\n        pass\n"), nil)
	pristine := previous.CopyTokens()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := NewWithBorrowedTokensAndSyntaxTrace(previous.BorrowReadOnlyTokens())
			p.ParseFile("concurrent.elisa")
			if len(p.Errors()) == 0 || p.borrowedTokens {
				t.Error("fixture did not exercise recovery detachment")
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(pristine, previous.CopyTokens()) {
		t.Fatal("concurrent parsers mutated shared tokens")
	}
}

func BenchmarkTokenViewParse(b *testing.B) {
	for _, count := range []int{16, 256, 2048} {
		var source strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x * 3 + 1\n\n", i)
		}
		previous := lexer.TokenizeSource("parse-view.elisa", []byte(source.String()), nil)
		for _, mode := range []string{"copied", "borrowed"} {
			b.Run(fmt.Sprintf("functions%d/%s", count, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var p *Parser
					if mode == "copied" {
						p = New(previous.CopyTokens())
					} else {
						p = NewWithBorrowedTokens(previous.BorrowReadOnlyTokens())
					}
					p.ParseFile("parse-view.elisa")
					if len(p.Errors()) != 0 {
						b.Fatal(p.Errors())
					}
				}
			})
		}
	}
}
