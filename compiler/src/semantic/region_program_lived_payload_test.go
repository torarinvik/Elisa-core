package semantic

import (
	"strings"
	"testing"
)

// docs/128 rule 3 applies to every slot typed with a program-lived region, not only to
// fields of structs that take region parameters: an enum payload (plain or hierarchy), a
// plain struct literal field, and a field assignment. A value of a shorter region is
// rejected; a program-lived value is accepted.
func TestProgramLivedRegionPayloadStores(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_lived_payload.elisa", `global region source

enum Tok:
    Ident(name: sview @source)
    Num(value: i64)

enum Node layout(handle: u32):
    pass

enum Expr is Node:
    Name(name: sview @source)
    Pair(left: Expr, right: Expr)

struct Plain:
    name: mutable sview @source

def tok_bad[@a](text: sview @a) -> Tok:
    return Tok.Ident(text)

def expr_bad[@a](text: sview @a) -> Expr:
    return Expr.Name(text)

def plain_bad[@a](text: sview @a) -> Plain:
    return Plain{name: text}

def assign_bad[@a](p: mutable Plain&, text: sview @a) -> void:
    p.name <- text

def tok_good(text: sview @source) -> Tok:
    return Tok.Ident(text)

def expr_good(text: sview @source) -> Expr:
    return Expr.Name(text)

def plain_good(text: sview @source) -> Plain:
    return Plain{name: text}

def assign_good(p: mutable Plain&, text: sview @source) -> void:
    p.name <- text

def literal_good() -> Tok:
    return Tok.Ident("x")
`)
	errs := result.Errors()
	want := []string{":18:", ":21:", ":24:", ":27:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 18, 21, 24, 27 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 18, 21, 24, 27 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
