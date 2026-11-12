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

// Variant search includes families nested in modules, regardless of path depth.
func TestErrorUnqualifiedVariantInNestedModule(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "nested_error_variant.elisa", `
module Outer:
    public:
        module Inner:
            public:
                error Fault:
                    Expected
                    Other

def origin() -> i64 error[Expected]:
    return 1
`)
	sym, ok := result.GlobalScope.Lookup("origin")
	if !ok {
		t.Fatal("missing origin")
	}
	resultType, ok := sym.Type.(*FuncType).Return.(*ErrorUnionType)
	if !ok {
		t.Fatalf("expected an error-union return, got %T", sym.Type.(*FuncType).Return)
	}
	if len(resultType.Errors.Tags) != 1 || resultType.Errors.Tags[0] != "Outer.Inner.Fault.Expected" {
		t.Fatalf("expected nested error variant, got %v", resultType.Errors.Tags)
	}
}

func TestErrorUnqualifiedVariantNestedAmbiguity(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "nested_error_ambiguous.elisa", `
error RootFault:
    Shared
module Outer:
    public:
        error NestedFault:
            Shared

def origin() -> i64 error[Shared]:
    return 1
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "ambiguous") || !strings.Contains(all, "Outer.NestedFault") || !strings.Contains(all, "RootFault") {
		t.Fatalf("expected root and nested variant ambiguity, got:\n%s", all)
	}
}
