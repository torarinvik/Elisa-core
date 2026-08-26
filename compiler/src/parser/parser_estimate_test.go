package parser

import (
	"testing"

	"elisacore/src/lexer"
)

func TestEstimateCommaSeparatedCountStopsAtColon(t *testing.T) {
	tokens := lexer.New("estimate.elisa", []byte("value => Ready: trailing, commas, must, not, count")).Tokenize()
	p := New(tokens)

	if got := p.estimateCommaSeparatedCount(lexer.TOKEN_COLON); got != 1 {
		t.Fatalf("estimate before a colon counted trailing commas: got %d, want 1", got)
	}
}

func TestUnbracketedPermissionRefsDoNotEstimateToEOF(t *testing.T) {
	tokens := lexer.New("permission.elisa", []byte("Abort.Panic: trailing, commas, are, unrelated")).Tokenize()
	p := New(tokens)
	refs := p.parsePermissionRefs(false)

	if len(refs) != 1 || refs[0].Name != "Abort" || refs[0].Member != "Panic" {
		t.Fatalf("unexpected unbracketed permission refs: %#v", refs)
	}
	if cap(refs) > 1 {
		t.Fatalf("unbracketed permission refs retained capacity for trailing commas: cap=%d", cap(refs))
	}
	if p.peek() != lexer.TOKEN_COLON {
		t.Fatalf("unbracketed permission parser consumed the body delimiter: got %s", p.cur())
	}
}
