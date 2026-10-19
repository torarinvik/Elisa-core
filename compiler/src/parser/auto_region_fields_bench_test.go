package parser

import (
	"elisacore/src/lexer"
	"fmt"
	"strings"
	"testing"
)

// Retain the parser contexts used in the field-plan qualification. Tokenization
// and the first type-plan construction happen before timing; this models a
// persistent compiler's repeated parsing, including AST and auto-region work.
func BenchmarkAutoRegionParseBodies(b *testing.B) {
	for _, count := range []int{256, 2048} {
		for _, fixture := range []struct{ name, body string }{
			{"scalar", "    return x * 3 + 1\n\n"},
			{"container", "    values: mutable darray[i64] = []\n    values.push(x)\n    return x\n\n"},
			{"exprblock", "    value: i64 =\n        x + 1\n    return value\n\n"},
			{"forwarding", "    value: mutable Record = Record(items: [])\n    grow(&value)\n    return x\n\n"},
		} {
			b.Run(fmt.Sprintf("functions%d/%s", count, fixture.name), func(b *testing.B) {
				var source strings.Builder
				for i := 0; i < count; i++ {
					fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n%s", i, fixture.body)
				}
				tokens := lexer.TokenizeSource("fields.elisa", []byte(source.String()), nil)
				if errors := tokens.CopyErrors(); len(errors) != 0 {
					b.Fatal(errors)
				}
				p := NewWithBorrowedTokens(tokens.BorrowReadOnlyTokens())
				p.ParseFile("fields.elisa")
				if len(p.Errors()) != 0 || len(p.Notices()) != 0 {
					b.Fatalf("fixture did not parse cleanly: %v / %v", p.Errors(), p.Notices())
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					p := NewWithBorrowedTokens(tokens.BorrowReadOnlyTokens())
					p.ParseFile("fields.elisa")
					if len(p.Errors()) != 0 {
						b.Fatal(p.Errors())
					}
				}
			})
		}
	}
}
