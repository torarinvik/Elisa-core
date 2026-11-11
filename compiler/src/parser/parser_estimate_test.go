package parser

import (
	"strings"
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

func TestBoundedCommaEstimateStillParsesLongCall(t *testing.T) {
	const argumentCount = 256
	source := strings.Repeat("value, ", argumentCount-1) + "value)"
	p := New(lexer.New("long_call.elisa", []byte(source)).Tokenize())

	if got := p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN); got >= argumentCount {
		t.Fatalf("capacity estimate scanned the complete call: got %d", got)
	}
	args, _, _, _, _, _ := p.parseCallArgs()
	if len(args) != argumentCount || p.peek() != lexer.TOKEN_RPAREN || len(p.Errors()) != 0 {
		t.Fatalf("parsed %d arguments before %v, errors: %v", len(args), p.cur(), p.Errors())
	}
}
