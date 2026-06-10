package semantic

import (
	"strings"
	"testing"
)

const unqualifiedVariantDecls = `error LexError:
    IndentError
    IllegalCharError
error ParseError:
    IllegalExpression
    LexerError(LexError)
`

// A bare variant name in error[...] resolves across all sets (family-qualified
// internally), and variants from different sets can be mixed.
func TestErrorUnqualifiedVariantMixAcrossSets(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "err_unqual_mix.elisa", unqualifiedVariantDecls+`
def parse(bad: bool) -> i64 error[IndentError, LexerError]:
    if bad:
        raise LexError.IndentError
    return 1
`)
	sym, ok := result.GlobalScope.Lookup("parse")
	if !ok {
		t.Fatal("expected parse symbol")
	}
	fnType := sym.Type.(*FuncType)
	got := fnType.Return.String()
	if !strings.Contains(got, "LexError.IndentError") || !strings.Contains(got, "ParseError.LexerError") {
		t.Fatalf("expected unqualified variants to resolve to qualified set.variant, got: %s", got)
	}
}

// `*Set` is an explicit whole-family spread; a bare set name also works.
func TestErrorStarFamilySpread(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "err_star_family.elisa", unqualifiedVariantDecls+`
def a() -> i64 error[*LexError]:
    return 1

def b() -> i64 error[LexError]:
    return 1
`)
}

// A bare variant name present in two sets is ambiguous and must be qualified.
func TestErrorUnqualifiedVariantAmbiguous(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "err_ambig.elisa", `
error LexError:
    Shared
error ParseError:
    Shared
def f() -> i64 error[Shared]:
    return 1
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "ambiguous") || !strings.Contains(all, "LexError.Shared") {
		t.Fatalf("expected ambiguity error pointing at LexError.Shared, got:\n%s", all)
	}
}

// An error variant may carry a bare (positional) type payload, e.g.
// `LexerError(LexError)`.
func TestErrorBareTypePayload(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "err_bare_payload.elisa", unqualifiedVariantDecls+`
def parse(bad: bool) -> i64 error[ParseError]:
    if bad:
        raise ParseError.IllegalExpression
    return 1
`)
}
