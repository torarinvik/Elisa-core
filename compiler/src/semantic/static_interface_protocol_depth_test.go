package semantic

import (
	"strings"
	"testing"
)

// A type that omits a protocol's default method inherits it: the impl's method set is populated with
// a synthesized method even though the impl body only provides the required methods.
func TestProtocolDefaultMethodInherited(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "protocol_default_inherited.elisa", `
struct Num:
    v: i64

protocol Ord:
    def lt(self: Self, other: Self) -> bool
    def le(self: Self, other: Self) -> bool:
        return Self.lt(self, other)

impl Ord for Num:
    def lt(self: Num, other: Num) -> bool:
        return self.v < other.v
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
	impl, ok := LookupStaticImpl(result.StaticImpls, "Ord", result.NamedTypes["Num"])
	if !ok || impl == nil {
		t.Fatal("expected impl for Ord/Num")
	}
	if impl.Methods["lt"] == nil {
		t.Fatal("expected provided impl method lt")
	}
	if impl.Methods["le"] == nil {
		t.Fatal("expected default method le to be inherited into the impl")
	}
}

// A type that provides a method overrides the protocol's default; this must analyze cleanly (the
// explicit method wins, no duplicate-method error).
func TestProtocolDefaultMethodOverridden(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "protocol_default_override.elisa", `
struct Num:
    v: i64

protocol Ord:
    def lt(self: Self, other: Self) -> bool
    def le(self: Self, other: Self) -> bool:
        return Self.lt(self, other)

impl Ord for Num:
    def lt(self: Num, other: Num) -> bool:
        return self.v < other.v
    def le(self: Num, other: Num) -> bool:
        return self.v <= other.v
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
	impl, ok := LookupStaticImpl(result.StaticImpls, "Ord", result.NamedTypes["Num"])
	if !ok || impl == nil {
		t.Fatal("expected impl for Ord/Num")
	}
	if impl.Methods["le"] == nil {
		t.Fatal("expected explicit override method le")
	}
}

// Protocol inheritance: a type conforming to a derived protocol must satisfy the base protocol's
// members. Providing eq (from Eq) + lt (from Ord) is enough; le is inherited as a default.
func TestProtocolInheritanceSatisfiedBySingleImpl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "protocol_inheritance_ok.elisa", `
struct Num:
    v: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

protocol Ord is Eq:
    def lt(self: Self, other: Self) -> bool
    def le(self: Self, other: Self) -> bool:
        return Self.lt(self, other) or Self.eq(self, other)

impl Ord for Num:
    def eq(self: Num, other: Num) -> bool:
        return self.v == other.v
    def lt(self: Num, other: Num) -> bool:
        return self.v < other.v
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
	impl, ok := LookupStaticImpl(result.StaticImpls, "Ord", result.NamedTypes["Num"])
	if !ok || impl == nil {
		t.Fatal("expected impl for Ord/Num")
	}
	for _, name := range []string{"eq", "lt", "le"} {
		if impl.Methods[name] == nil {
			t.Fatalf("expected impl to carry method %q (own, base, or inherited default)", name)
		}
	}
}

// Conforming to a derived protocol but failing to provide a base protocol's required member is an
// error: Ord folds in Eq.eq, so an impl that omits eq (and has no default for it) is incomplete.
func TestProtocolInheritanceRejectsMissingBaseMember(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "protocol_inheritance_missing.elisa", `
struct Num:
    v: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

protocol Ord is Eq:
    def lt(self: Self, other: Self) -> bool

impl Ord for Num:
    def lt(self: Num, other: Num) -> bool:
        return self.v < other.v
`)

	found := false
	for _, err := range result.Errors() {
		if strings.Contains(err, "missing method") && strings.Contains(err, "eq") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a missing-base-method error mentioning eq, got: %v", result.Errors())
	}
}

// Base-protocol methods are callable on a value bound by the derived protocol: with B: Ord, B.eq is
// resolvable because Eq's members are folded into Ord.
func TestProtocolInheritanceBaseMethodCallableOnDerived(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "protocol_inheritance_base_callable.elisa", `
struct Num:
    v: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

protocol Ord is Eq:
    def lt(self: Self, other: Self) -> bool

impl Ord for Num:
    def eq(self: Num, other: Num) -> bool:
        return self.v == other.v
    def lt(self: Num, other: Num) -> bool:
        return self.v < other.v

def same[B: Ord](a: B, b: B) -> bool:
    return B.eq(a, b)
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

// Unknown base protocol is reported.
func TestProtocolInheritanceRejectsUnknownBase(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "protocol_inheritance_unknown_base.elisa", `
protocol Ord is Nonexistent:
    def lt(self: Self, other: Self) -> bool
`)

	if len(result.Errors()) == 0 {
		t.Fatal("expected an error for an unknown base protocol")
	}
}
