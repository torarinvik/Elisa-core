//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"strings"
	"testing"
)

const shortCircuitRequiresHelper = `def needs_nonnegative(n: i64) -> bool:
    requires n >= 0
    return true
`

func TestShortCircuitCallRequires(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"and_true_path", "return n >= 0 and needs_nonnegative(n)", false},
		{"or_false_path", "return n < 0 or needs_nonnegative(n)", false},
		{"and_insufficient_guard", "return n <= 10 and needs_nonnegative(n)", true},
		{"or_wrong_path", "return n >= 0 or needs_nonnegative(n)", true},
		{"no_guard_leak", "selected: bool = n >= 0 and needs_nonnegative(n)\n    return needs_nonnegative(n)", true},
		{"statement_guard", "if n >= 0:\n        return needs_nonnegative(n)\n    return false", false},
		{"or_chain_false_path", "return n < 0 or n > 10 or needs_nonnegative(n)", false},
		{"mixed_outer_and", "return (n >= 0 or n >= 10) and needs_nonnegative(n)", false},
		{"mixed_inner_or", "return n >= 0 and (n <= 10 or needs_nonnegative(n))", false},
		{"mixed_insufficient_guard", "return (n <= 10 or n >= 10) and needs_nonnegative(n)", true},
		{"or_guard_no_leak", "selected: bool = n < 0 or needs_nonnegative(n)\n    return needs_nonnegative(n)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, "short_circuit_"+tc.name+".elisa", shortCircuitRequiresHelper+"def caller(n: i64) -> bool:\n    "+tc.body+"\n")
			if tc.reject {
				if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
					t.Fatalf("unproven RHS call accepted: %s", allDiagnostics(result))
				}
			} else if len(result.Errors()) != 0 {
				t.Fatalf("guarded RHS call rejected: %s", allDiagnostics(result))
			}
		})
	}
}

func TestShortCircuitCallRequiresTwoArguments(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_two_arguments.elisa", `def decode(first: i64, second: i64) -> bool:
    requires first >= 192 and first <= 223
    requires second >= 128 and second <= 191
    return true
def caller(first: i64, second: i64) -> bool:
    return first >= 192 and first <= 223 and second >= 128 and second <= 191 and decode(first, second)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("guarded decoder rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitPureHelperGuard(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_helper.elisa", shortCircuitRequiresHelper+`def nonnegative(n: i64) -> bool:
    return n >= 0
def caller(n: i64) -> bool:
    return nonnegative(n) and needs_nonnegative(n)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("pure helper guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitLongGuardChain(t *testing.T) {
	guards := strings.Repeat("n >= 0 and ", 128)
	result := analyzeContractStrict(t, "short_circuit_long_chain.elisa", shortCircuitRequiresHelper+"def caller(n: i64) -> bool:\n    return "+guards+"needs_nonnegative(n)\n")
	if len(result.Errors()) != 0 {
		t.Fatalf("long guard chain rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitImpurePrefixDoesNotRetainOldValue(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_mutating_prefix.elisa", shortCircuitRequiresHelper+`def mutate(value: mutable i64&) -> bool:
    value <- -1
    return true
def caller(n: mutable i64) -> bool:
    return n >= 0 and mutate(&n) and needs_nonnegative(n)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("mutation retained a stale guard: %s", allDiagnostics(result))
	}
}

func TestShortCircuitMutableGlobalIsNotValueSnapshot(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_shared_global.elisa", shortCircuitRequiresHelper+`global mutable counter: i64 = 0
def caller() -> bool:
    can Global.Read:
        return counter >= 0 and needs_nonnegative(counter)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("shared storage read treated as a value snapshot: %s", allDiagnostics(result))
	}
}

func TestShortCircuitStaticReferenceIsNotValueSnapshot(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_static_reference.elisa", shortCircuitRequiresHelper+`def caller(value: static i64&) -> bool:
    return value >= 0 and needs_nonnegative(value)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("shared static reference treated as a value snapshot: %s", allDiagnostics(result))
	}
}

func TestShortCircuitImmutableAliasOfMutableStaticReferenceIsNotValueSnapshot(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_static_reference_alias.elisa", shortCircuitRequiresHelper+`def caller(value: mutable static i64&) -> bool:
    alias: mutable static i64& = value
    return alias >= 0 and needs_nonnegative(alias)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("immutable alias of shared mutable referent treated as a value snapshot: %s", allDiagnostics(result))
	}
}

func TestShortCircuitNumericConversionGuard(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_numeric_conversion.elisa", `def decode(first: i64) -> bool:
    requires first >= 192 and first <= 223
    return true
def caller(first: u8) -> bool:
    return first.i64() >= 192 and first.i64() <= 223 and decode(first.i64())
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("numeric conversion guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitNumericConversionHelperGuard(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_numeric_conversion_helper.elisa", `def continuation(byte: u8) -> bool:
    return byte.i64() >= 128 and byte.i64() <= 191
def decode(first: i64, second: i64) -> bool:
    requires first >= 192 and first <= 223
    requires second >= 128 and second <= 191
    return true
def caller(first: u8, second: u8) -> bool:
    return first.i64() >= 192 and first.i64() <= 223 and second.continuation() and decode(first.i64(), second.i64())
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("numeric conversion helper guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitNumericConversionConstantHelperGuard(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_numeric_conversion_constant_helper.elisa", `const MIN: i64 = 128
const MAX: i64 = 191
def continuation(byte: u8) -> bool:
    return byte.i64() >= MIN and byte.i64() <= MAX
def decode(first: i64, second: i64) -> bool:
    requires first >= 192 and first <= 223
    requires second >= 128 and second <= 191
    return true
def caller(first: u8, second: u8) -> bool:
    return first.i64() >= 192 and first.i64() <= 223 and second.continuation() and decode(first.i64(), second.i64())
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("numeric conversion helper guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitConstantHelperCallerShadowing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bound  int
		reject bool
	}{{"constant_128", 128, false}, {"constant_0", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`const MIN: i64 = %d
def safe(value: i64) -> bool:
    return value >= MIN
def needs_128(value: i64) -> bool:
    requires value >= 128
    return true
def caller(value: i64) -> bool:
    MIN: i64 = 128
    return safe(value) and needs_128(value)
`, tc.bound)
			result := analyzeContractStrict(t, "short_circuit_shadow_"+tc.name+".elisa", source)
			if tc.reject {
				if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
					t.Fatalf("caller local substituted for callee constant: %s", allDiagnostics(result))
				}
			} else if len(result.Errors()) != 0 {
				t.Fatalf("callee constant guard rejected: %s", allDiagnostics(result))
			}
		})
	}
}

func TestShortCircuitConstantNamedResult(t *testing.T) {
	for _, tc := range []struct {
		bound  int
		reject bool
	}{{128, false}, {0, true}} {
		source := fmt.Sprintf(`const result: i64 = %d
def fixed() -> i64:
    return result
def needs_128(value: i64) -> bool:
    requires value >= 128
    return true
def caller() -> bool:
    return needs_128(fixed())
`, tc.bound)
		result := analyzeContractStrict(t, "constant_named_result.elisa", source)
		if tc.reject {
			if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
				t.Fatalf("synthetic result binding replaced callee constant: %s", allDiagnostics(result))
			}
		} else if len(result.Errors()) != 0 {
			t.Fatalf("callee result-named constant rejected: %s", allDiagnostics(result))
		}
	}
}

func TestShortCircuitConstantHelperDefiningModule(t *testing.T) {
	result := analyzeContractStrict(t, "constant_helper_defining_module.elisa", `def needs_128(value: i64) -> bool:
    requires value >= 128
    return true
module Bounds:
    const MIN: i64 = 128
    def safe(value: i64) -> bool:
        return value >= MIN
module Caller:
    const MIN: i64 = 0
    def caller(value: i64) -> bool:
        return Bounds::safe(value) and needs_128(value)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("callee defining module lost: %s", allDiagnostics(result))
	}
}

func TestShortCircuitNarrowingCastDoesNotProveOriginalRange(t *testing.T) {
	result := analyzeContractStrict(t, "constant_helper_narrowing.elisa", shortCircuitRequiresHelper+`const MIN: i64 = 128
def wrapped(value: i64) -> bool:
    return value.u8().i64() >= MIN
def caller(value: i64) -> bool:
    return wrapped(value) and needs_nonnegative(value)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("narrowing conversion treated as identity: %s", allDiagnostics(result))
	}
}

func TestShortCircuitForwardConstantHelperShadowing(t *testing.T) {
	result := analyzeContractStrict(t, "forward_constant_helper_shadow.elisa", `const MIN: i64 = 0
def needs_128(value: i64) -> bool:
    requires value >= 128
    return true
def caller(value: i64) -> bool:
    MIN: i64 = 128
    return safe(value) and needs_128(value)
def safe(value: i64) -> bool:
    return value >= MIN
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("forward helper captured caller local: %s", allDiagnostics(result))
	}
}

func TestShortCircuitFormalNamedResult(t *testing.T) {
	result := analyzeContractStrict(t, "formal_named_result.elisa", `def identity(result: i64) -> i64:
    return result
def needs_128(value: i64) -> bool:
    requires value >= 128
    return true
def caller() -> bool:
    return needs_128(identity(128))
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("formal result captured by synthetic return: %s", allDiagnostics(result))
	}
}

func TestShortCircuitForwardConstantNamedResult(t *testing.T) {
	result := analyzeContractStrict(t, "forward_constant_named_result.elisa", `const result: i64 = 0
def needs_128(value: i64) -> bool:
    requires value >= 128
    return true
def caller() -> bool:
    return needs_128(fixed())
def fixed() -> i64:
    return result
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("forward result constant acquired a synthetic binding: %s", allDiagnostics(result))
	}
}

func TestRefinementBracketDoesNotReplaceSubject(t *testing.T) {
	result := analyzeContractStrict(t, "refinement_bracket_subject.elisa", `law AtMost(self: i64, n: i64) = self <= n
def needs_zero(value: i64 is AtMost[0]) -> bool:
    return true
def caller(n: i64) -> bool:
    requires n >= 0
    requires n <= 10
    return needs_zero(n * n)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("law bracket replaced the same-named subject: %s", allDiagnostics(result))
	}
	for _, proof := range result.ProofReport {
		if proof.Predicate == "AtMost" && proof.Outcome == ProofProvenSMT {
			t.Fatalf("false refinement acquired an SMT proof: %+v", proof)
		}
	}
}

func TestRefinementBracketDoesNotReplaceCallerParameter(t *testing.T) {
	result := analyzeContractStrict(t, "refinement_bracket_caller.elisa", `law AtLeast(self: i64, lo: i64) = self >= lo
def bad(lo: i64) -> i64 is AtLeast[10]:
    return lo
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("law static argument replaced caller parameter: %s", allDiagnostics(result))
	}
	for _, proof := range result.ProofReport {
		if proof.Predicate == "AtLeast" && proof.Outcome == ProofProvenSMT {
			t.Fatalf("false refinement acquired an SMT proof: %+v", proof)
		}
	}
}

func TestRefinementBracketDoesNotReplaceRecursiveFormal(t *testing.T) {
	result := analyzeContractStrict(t, "refinement_recursive_bracket.elisa", `def count(n: i64) -> i64:
    requires n >= 0
    ensure result == n
    decreases n
    if n == 0:
        return 0
    return count(n - 1) + 1
law AtLeast(self: i64, n: i64) = self >= n
def bad(n: i64) -> i64 is AtLeast[10]:
    requires n >= 0
    requires n <= 5
    return count(n)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("law bracket replaced recursive formal: %s", allDiagnostics(result))
	}
	for _, proof := range result.ProofReport {
		if proof.Predicate == "AtLeast" && proof.Outcome == ProofProvenSMT {
			t.Fatalf("false refinement acquired an SMT proof: %+v", proof)
		}
	}
}

func TestRecursiveNestedKeyDoesNotProveFalseBound(t *testing.T) {
	result := analyzeContractStrict(t, "recursive_nested_key_false_bound.elisa", `def bounded(n: i64) -> i64:
    requires n >= 0
    ensure result <= 12
    decreases n
    if n <= 2:
        return 10
    return bounded(n - 1) + 1
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "postcondition") {
		t.Fatalf("nested recursive keys proved false bound: %s", allDiagnostics(result))
	}
	for _, proof := range result.ProofReport {
		if proof.Subject == "ensure bounded" && proof.Pos.Line == 7 && proof.Outcome == ProofProvenSMT {
			t.Fatalf("false recursive return acquired an SMT proof: %+v", proof)
		}
	}
}

func TestRecursiveAffineOverflowBoundRejected(t *testing.T) {
	result := analyzeContractStrict(t, "recursive_affine_overflow_bound.elisa", `def add_const(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 4
    ensure result >= 4
    decreases n
    if n == 0:
        return 4
    return add_const(n - 1) + 1
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "postcondition") {
		t.Fatalf("unbounded affine recurrence hid overflow: %s", allDiagnostics(result))
	}
}

func TestShortCircuitParenthesizedGuardChain(t *testing.T) {
	guard := "n >= 0"
	for i := 0; i < 256; i++ {
		guard = "(" + guard + " and n >= 0)"
	}
	source := shortCircuitRequiresHelper + "def caller(n: i64) -> bool:\n    return " + guard + " and needs_nonnegative(n)\n"
	result := analyzeContractStrict(t, "parenthesized_guard_chain.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatalf("parenthesized guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitParenthesizedMutationInvalidatesFacts(t *testing.T) {
	result := analyzeContractStrict(t, "parenthesized_mutation.elisa", shortCircuitRequiresHelper+`def mutate(value: mutable i64&) -> bool:
    value <- -1
    return true
def caller(n: mutable i64) -> bool:
    return ((n >= 0 and n >= 0) and mutate(&n)) and needs_nonnegative(n)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("cached stability retained a stale value fact: %s", allDiagnostics(result))
	}
}

func TestShortCircuitStabilityMemoSeparatesScopes(t *testing.T) {
	ident := &ast.Ident{Name: "value"}
	first, second := NewScope(nil), NewScope(nil)
	first.Define(&Symbol{Name: "value", Kind: SymbolLocal})
	second.Define(&Symbol{Name: "value", Kind: SymbolLocal})
	analyzer := &Analyzer{
		currentScope:          first,
		exprTypes:             map[ast.Expr]Type{ident: &RefType{Storage: RefStorageStatic}},
		shortCircuitStability: map[shortCircuitStabilityKey]bool{},
	}
	if analyzer.shortCircuitStablePredicate(ident) {
		t.Fatal("shared reference became stable")
	}
	analyzer.currentScope = second
	analyzer.exprTypes[ident] = &BuiltinType{Name: "bool"}
	if !analyzer.shortCircuitStablePredicate(ident) {
		t.Fatal("classification leaked across lexical scopes")
	}
	analyzer.suppressDiagnostics = true
	analyzer.exprTypes[ident] = &RefType{Storage: RefStorageStatic}
	if analyzer.shortCircuitStablePredicate(ident) {
		t.Fatal("speculative analysis reused a cached classification")
	}
	if len(analyzer.shortCircuitStability) != 0 {
		t.Fatal("speculative reanalysis retained earlier classifications")
	}
}

func TestShortCircuitUnaryWrappedGuardChain(t *testing.T) {
	guard := "n >= 0"
	for i := 0; i < 128; i++ {
		guard = "not not (" + guard + " and n >= 0)"
	}
	source := shortCircuitRequiresHelper + "def caller(n: i64) -> bool:\n    return " + guard + " and needs_nonnegative(n)\n"
	result := analyzeContractStrict(t, "unary_wrapped_guard_chain.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatalf("unary-wrapped guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitPureCallWrappedGuardChain(t *testing.T) {
	guard := "n >= 0"
	for i := 0; i < 128; i++ {
		guard = "ident((" + guard + " and n >= 0))"
	}
	source := shortCircuitRequiresHelper + "def ident(value: bool) -> bool:\n    return value\ndef caller(n: i64) -> bool:\n    return " + guard + " and needs_nonnegative(n)\n"
	result := analyzeContractStrict(t, "pure_call_wrapped_guard_chain.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatalf("pure-call-wrapped guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitStabilityMemoBindingGeneration(t *testing.T) {
	ident := &ast.Ident{Name: "value"}
	global := NewScope(nil)
	global.Define(&Symbol{Name: "value", Kind: SymbolGlobal, Mutable: true})
	local := NewScope(global)
	analyzer := &Analyzer{globalScope: global, currentScope: local, exprTypes: map[ast.Expr]Type{ident: &BuiltinType{Name: "bool"}}, shortCircuitStability: map[shortCircuitStabilityKey]bool{}}
	if analyzer.shortCircuitStablePredicate(ident) {
		t.Fatal("mutable global became stable")
	}
	local.Define(&Symbol{Name: "value", Kind: SymbolLocal})
	if !analyzer.shortCircuitStablePredicate(ident) {
		t.Fatal("same-scope definition reused old classification")
	}
	analyzer.exprTypes[ident] = &RefType{Storage: RefStorageStatic}
	analyzer.symbolTypeEpoch++
	if analyzer.shortCircuitStablePredicate(ident) {
		t.Fatal("type generation reused old classification")
	}
}
