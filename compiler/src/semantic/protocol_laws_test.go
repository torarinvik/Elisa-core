package semantic

import (
	"strings"
	"testing"
)

const ordProtocolSource = `
protocol Ord:
    def le(self: Self, other: Self) -> bool
    law total(a: Self, b: Self) = a.le(b) or b.le(a)
    law transitive(a: Self, b: Self, c: Self) = (a.le(b) and b.le(c)) implies a.le(c)
`

// Version's le is structural (lexicographic over two integer fields): the laws are linear/boolean over
// the fields, so the SMT tier should PROVE total + transitive with no fuzz obligation.
func TestProtocolLawProvenForStructuralImpl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "law_version.elisa", ordProtocolSource+`
struct Version:
    major: i64
    minor: i64

impl Ord for Version:
    def le(self: Version, other: Version) -> bool:
        return self.major < other.major or (self.major == other.major and self.minor <= other.minor)
`, AnalyzeOptions{EnableSMT: true})

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	for _, ob := range result.LawFuzzObligations {
		if ob.TypeName == "Version" {
			t.Fatalf("Version impl should prove law %q statically, but a fuzz obligation was emitted", ob.LawName)
		}
	}
}

// Rational's le multiplies two field pairs (num*den): a nonlinear product the SMT tier declines, so
// the laws must AUTO-LOWER to @property fuzz obligations rather than erroring or silently passing.
func TestProtocolLawFuzzedForNonlinearImpl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "law_rational.elisa", ordProtocolSource+`
struct Rational:
    num: i64
    den: i64

impl Ord for Rational:
    def le(self: Rational, other: Rational) -> bool:
        return self.num * other.den <= other.num * self.den
`, AnalyzeOptions{EnableSMT: true})

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	got := map[string]bool{}
	for _, ob := range result.LawFuzzObligations {
		if ob.TypeName == "Rational" {
			got[ob.LawName] = true
		}
	}
	// transitive over Rational requires relating three nonlinear cross-products (num*den), which the
	// SMT tier cannot — so it must auto-lower to a @property fuzz harness. (total, by contrast, is the
	// tautology `x<=y or y<=x` over the opaque products and is soundly PROVEN even here.)
	if !got["transitive"] {
		t.Fatalf("Rational impl should fuzz-check the nonlinear law %q, but no obligation was emitted; got %v", "transitive", got)
	}
}

// A genuinely broken impl (le always false ⇒ not total) must NOT pass silently: the total law is
// unprovable, so it is auto-lowered to a @property fuzz obligation whose body is `false or false`. The
// fuzz harness then fails at runtime (verified by the runtime test). Here we assert the obligation is
// emitted (the honesty guarantee) rather than the law being silently assumed.
func TestProtocolLawBrokenImplIsNotSilentlyAssumed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "law_broken.elisa", ordProtocolSource+`
struct Bad:
    v: i64

impl Ord for Bad:
    def le(self: Bad, other: Bad) -> bool:
        return false
`, AnalyzeOptions{EnableSMT: true})

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	got := map[string]bool{}
	for _, ob := range result.LawFuzzObligations {
		if ob.TypeName == "Bad" {
			got[ob.LawName] = true
		}
	}
	if !got["total"] {
		t.Fatalf("broken impl (le always false) must auto-lower the unprovable total law to a fuzz harness, not pass silently; got %v", got)
	}
}

// Generic code bounded on a protocol may ASSUME the protocol's laws (docs/85 P3). Here a generic
// function over `T: Ord` returns x.le(z) given x.le(y) and y.le(z); its `ensure` is provable ONLY via
// the injected transitive law (modeling le as an uninterpreted predicate). This is the "laws as facts
// in generic code" acceptance.
func TestProtocolLawAssumedAsFactInGenericCode(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "law_generic.elisa", ordProtocolSource+`
def chained[T: Ord](x: T, y: T, z: T) -> bool:
    requires x.le(y)
    requires y.le(z)
    ensure result == true
    return x.le(z)
`, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("generic ensure should be provable from the injected transitive law, got: %v", errs)
	}
}

// Soundness guard: WITHOUT a transitive law the same generic ensure is NOT provable (the assumption
// must come from the declared law, not be fabricated). With only `total` declared, the chained ensure
// must fail under -strict.
func TestProtocolLawDoesNotFabricateUnstatedFacts(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "law_generic_nofab.elisa", `
protocol Ord:
    def le(self: Self, other: Self) -> bool
    law total(a: Self, b: Self) = a.le(b) or b.le(a)

def chained[T: Ord](x: T, y: T, z: T) -> bool:
    requires x.le(y)
    requires y.le(z)
    ensure result == true
    return x.le(z)
`, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})

	found := false
	for _, e := range result.Errors() {
		if strings.Contains(e, "ensure postcondition") {
			found = true
		}
	}
	if !found {
		t.Fatalf("without a transitive law the chained ensure must NOT be provable (no fabricated facts); errors: %v", result.Errors())
	}
}
