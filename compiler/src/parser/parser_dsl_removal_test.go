package parser

import (
	"strings"
	"testing"
)

func TestGrammarAndLexerDSLDeclarationsAreRejected(t *testing.T) {
	tests := map[string]string{
		"grammar":    "grammar G:\n    pass\n",
		"grammarenv": "grammarenv E:\n    pass\n",
		"lexer":      "lexer L:\n    pass\n",
		"tokenset":   "tokenset T = []\n",
		"charset":    "charset C = 'a'\n",
		"keywordmap": "keywordmap K: cstr -> i64:\n    _ => 0\n",
		"extend":     "extend grammar G:\n    pass\n",
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			file, errs := parseSourceFile(t, src)
			if len(errs) != 1 {
				t.Fatalf("expected one directed removal diagnostic, got %d: %v", len(errs), errs)
			}
			if !strings.Contains(errs[0], "grammar and lexer DSL declarations have been removed") {
				t.Fatalf("unexpected diagnostic: %v", errs)
			}
			if len(file.Decls) != 0 {
				t.Fatalf("removed declaration must not enter the AST, got %T", file.Decls[0])
			}
		})
	}
}
