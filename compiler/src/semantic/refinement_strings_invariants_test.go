//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS-NEGATIVE: `.len` is a compile-time string's byte length, not a property of the type
// `u8&`. A parameter, a byte buffer and a nullable ref share that type and carry no length at all;
// the rule used to accept `.len` on every one of them, and the backend then failed with a
// location-less "field access requires a struct type, got u8". Each must be rejected, located.
func TestU8RefLenRequiresCompileTimeString(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"param", "def f(p: u8&) -> i64:\n    return p.len\n", "field access requires struct type, got u8"},
		{"mutable_buffer", "def f(p: mutable u8&) -> i64:\n    return p.len\n", "field access requires struct type, got u8"},
		{"local", "def f() -> i64:\n    s: u8& = \"abc\"\n    return s.len\n", "field access requires struct type, got u8"},
		{"nullable", "def f(p: u8&?) -> i64:\n    return p.len\n", "field access requires proven non-null reference, got u8&?"},
		// A binding that SHADOWS a const is a runtime value. The const evaluator reads only the
		// global table, so without the scope check these folded the global's length -- 5 and 3
		// whatever the argument held.
		{"ternary_branch_shadows_const", "const A = \"hello\"\nconst B = \"hi\"\n\ndef f(A: u8&) -> i64:\n    return (A if true else B).len\n", "field access requires struct type, got u8"},
		{"ternary_local_shadows_const", "const A = \"hello\"\n\ndef f() -> i64:\n    A: u8& = \"xyz\"\n    return (A if true else \"zz\").len\n", "field access requires struct type, got u8"},
		{"ternary_condition_shadows_const", "const FLAG = true\n\ndef f(FLAG: bool) -> i64:\n    return (\"abc\" if FLAG else \"de\").len\n", "field access requires struct type, got u8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "u8ref_len_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			found := false
			for _, err := range result.Errors() {
				if strings.Contains(err, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("`.len` on a %s `u8&` has no length to read; want %q, got: %v", tc.name, tc.want, result.Errors())
			}
		})
	}
}

// POSITIVE: a const bound to a string literal folds like the literal itself.
func TestConstStringLenFolds(t *testing.T) {
	src := `
law AtLeast5(self: i64) = self >= 5

const GREETING = "hello"

def f() -> i64 is AtLeast5:
    return GREETING.len

# A loop variable that shadowed the const is out of scope again after the loop.
def g() -> i64 is AtLeast5:
    for GREETING in 0..<3:
        pass
    return (GREETING if true else "x").len
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "const_string_len.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`GREETING.len` is 5, should prove AtLeast5 statically, got: %v", result.Errors())
	}
}

// A binding that SHADOWS a const is a runtime value everywhere the constant evaluator is asked,
// not only for `.len`: it read the global table, so each of these took the CONST's value.
func TestConstEvalDoesNotReadConstThroughShadowingBinding(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		want    string
		wantNot string
	}{
		// Proven from N = 7 and compiled with NO runtime check: `f(0)` returned 0 from an AtLeast5 function.
		{"refinement", "law AtLeast5(self: i64) = self >= 5\n\nconst N = 7\n\ndef f(N: i64) -> i64 is AtLeast5:\n    return N\n", "could not be proven statically", ""},
		// Took the const's branch whatever the argument held.
		{"static_if", "const FLAG = true\n\ndef f(FLAG: bool) -> i64:\n    static if FLAG:\n        return 1\n    return 0\n", "static if condition must be a compile-time bool", ""},
		// A valid runtime index rejected as the const's out-of-range value.
		{"constant_index", "const N = 70\n\ndef f(N: i64) -> i64:\n    xs: array[i64, 4] = [1, 2, 3, 4]\n    return xs[N]\n", "", "constant index"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "const_shadow_"+tc.name+".elisa", tc.src, AnalyzeOptions{EnforceStrictProofs: true})
			found := tc.want == ""
			for _, err := range result.Errors() {
				if tc.want != "" && strings.Contains(err, tc.want) {
					found = true
				}
				if tc.wantNot != "" && strings.Contains(err, tc.wantNot) {
					t.Fatalf("a parameter shadowing a const is not that const; unexpected %q in: %v", tc.wantNot, result.Errors())
				}
			}
			if !found {
				t.Fatalf("a parameter shadowing a const is not that const; want %q, got: %v", tc.want, result.Errors())
			}
		})
	}
}

// A cstr's `.len` is strlen, so it stops at an embedded NUL -- at compile time exactly as at run
// time. The fold used the byte count (5) and proved facts the running program contradicted.
func TestCStrConstLenIsStrlen(t *testing.T) {
	src := `
law Exactly2(self: i64) = self == 2

const G: cstr = "ab\0cd"
const N = G.len

def f() -> i64 is Exactly2:
    return N

def g() -> i64 is Exactly2:
    return G.len
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cstr_const_len_strlen.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`.len` of cstr \"ab\\0cd\" is strlen = 2, should prove Exactly2, got: %v", errs)
	}
}

// --- cross-module tail-call return-refinement passthrough (docs/90 brick 90-7 cross-module) ---

// Positive case: `return M::clamp()` where `M::clamp -> i64 is Bounded[0,100]` must discharge
// the caller's own `-> i64 is Bounded[0,100]` by composition, without a runtime check.
func TestCrossModuleTailCallReturnRefinementPassthrough(t *testing.T) {
	src := boundedLaw + `
module M:
    def clamp() -> i64 is Bounded[0, 100]:
        return 50

def wrapper() -> i64 is Bounded[0, 100]:
    return M::clamp()
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cross_module_tail_call.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("cross-module tail-call return refinement should discharge by composition, got: %v", errs)
	}
	for _, f := range result.ProofReport {
		if f.Outcome == ProofRuntime {
			t.Fatalf("cross-module tail-call should not fall back to runtime check, got ProofReport=%+v", result.ProofReport)
		}
	}
}

// Soundness-negative: callee return refinement Bounded[0,200] does NOT entail caller's Bounded[0,100].
// A callee returning Bounded[0,200] does NOT entail Bounded[0,100]; the caller must still check.
func TestCrossModuleTailCallReturnRefinementNonEntailmentFails(t *testing.T) {
	src := boundedLaw + `
module M:
    def wide() -> i64 is Bounded[0, 200]:
        return 150

def wrapper() -> i64 is Bounded[0, 100]:
    return M::wide()
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cross_module_tail_call_neg.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	// Must NOT prove statically via composition — callee bound is wider than caller's.
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenContract {
			t.Fatalf("callee Bounded[0,200] must NOT entail caller Bounded[0,100]; got contract-proven in ProofReport=%+v", result.ProofReport)
		}
	}
}

// --- modulo range derivation (docs task: x % m → [0, m-1]) ------------------------------------

// COMPLETENESS-POSITIVE: `x % m` for an unsigned x and positive constant m proves `is InRange[0, m-1]`
// via the flow/linear tier WITHOUT needing SMT. The return refinement must discharge with no runtime check.
func TestModuloRangeProvenForUnsignedDividend(t *testing.T) {
	src := `
law InRange(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi

def wrap(x: u64) -> u64 is InRange[0, 7]:
    return x % 8
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "modulo_range.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("x %% 8 (unsigned) should prove is InRange[0,7] via flow tier, got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("modulo range should be proven statically (no runtime check), got %d checks", len(result.RefinementChecks))
	}
}

// COMPLETENESS-POSITIVE (array index elision): `xs[x % N]` for unsigned x and array[T,N] must
// record the index as proven-in-bounds so codegen can elide the runtime guard.
func TestModuloIndexBoundsElided(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "modulo_index_elide.elisa", `
law InRange(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi

def lookup(x: u64, table: array[u64, 8]&) -> u64:
    return table[x % 8]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("table[x %% 8] into array[u64,8] should have no errors, got: %v", errs)
	}
	proven := 0
	for _, ok := range result.IndexBoundsProven {
		if ok {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("x %% 8 into array[_,8] must record the index in IndexBoundsProven (bounds elided)")
	}
}

// --- cast chain range propagation (docs/85 §cast-widening) ------------------------------------

// A WIDENING cast (u32 → u64) must carry the range fact through: a u32 parameter bounded
// [0, 100] widened to u64 should discharge `is Bounded[0, 100]` on the widened result via
// tier-2 (no runtime check). This is value-preserving: [0,100] fits in u64.
func TestCastWideningU32ToU64PreservesRange(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def widen(x: u32) -> u64 is Bounded[0, 100]:
    if x >= 0 and x <= 100:
        return x.u64()
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_widen_u32_u64.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("widening u32→u64 of [0,100] should prove the return, got: %s", allDiagnostics(result))
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear || f.Outcome == ProofProvenFlow {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("widening cast should preserve the range and prove via flow/linear tier")
	}
}

// SOUNDNESS-NEGATIVE #1: `x % m` must NOT prove `< m-1` (only `<= m-1`).
// `x % 8 is InRange[0, 6]` requires hi < 7 but the proven hi is 7 — must stay unproven.
func TestModuloRangeDoesNotOverproveTight(t *testing.T) {
	src := `
law InRange(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi

def wrap_tight(x: u64) -> u64 is InRange[0, 6]:
    return x % 8
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "modulo_tight.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("x %% 8 must NOT prove InRange[0,6] (hi=6 < m-1=7); must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS-NEGATIVE #2: a SIGNED dividend must not derive [0, m-1] (signed remainder can be negative).
func TestModuloRangeSignedDividendDeclines(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def wrap_signed(x: i64) -> i64 is InRange[0, 7]:
    return x % 8
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "modulo_signed.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("signed x %% 8 must NOT prove InRange[0,7] (signed remainder can be negative); must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS-NEGATIVE #3: a VARIABLE modulus must not derive a range (non-const divisor).
func TestModuloRangeVariableModulusDeclines(t *testing.T) {
	src := `
law InRange(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi

def wrap_varmod(x: u64, m: u64) -> u64 is InRange[0, 7]:
    return x % m
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "modulo_varmod.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("x %% m (variable m) must NOT prove InRange[0,7]; must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

// --- Transitive ordering (linear/flow tier, docs/85) ---
// Given `requires a <= b` and `requires b <= c` on immutable integer params, the linear tier
// must prove `a <= c` without invoking SMT. This is the "transitive-chain" gap: a two-hop
// relational path through an intermediate variable that has no concrete constant range.

// Completeness-positive: `a <= b, b <= c` transitively proves `a <= c` under -strict (no SMT).
// The callee sink(lo, hi) has `requires lo <= hi`; the caller proves it from the chain.
func TestTransitiveOrderingLeqLeqProvenNoSMT(t *testing.T) {
	src := `
def sink(lo: i64, hi: i64) -> i64:
    requires lo <= hi
    return hi

def caller(a: i64, b: i64, c: i64) -> i64:
    requires a <= b
    requires b <= c
    return sink(a, c)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "transitive_leq.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false},
	)
	if len(result.Errors()) != 0 {
		t.Fatalf("`a<=b, b<=c` should transitively prove `a<=c` (no error under -strict/no-smt), got: %v", result.Errors())
	}
}

// Completeness-positive (mixed strict): `a <= b, b < c` transitively proves `a < c` (strictly
// less, since at least one edge is strict). The callee requires `lo < hi`.
func TestTransitiveOrderingLeqLtProvenNoSMT(t *testing.T) {
	src := `
def sink_strict(lo: i64, hi: i64) -> i64:
    requires lo < hi
    return hi

def caller(a: i64, b: i64, c: i64) -> i64:
    requires a <= b
    requires b < c
    return sink_strict(a, c)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "transitive_leq_lt.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false},
	)
	if len(result.Errors()) != 0 {
		t.Fatalf("`a<=b, b<c` should transitively prove `a<c` (no error under -strict/no-smt), got: %v", result.Errors())
	}
}

// Soundness-negative (missing link): only `a <= b` is known, with no fact connecting b to c.
// The goal `a <= c` must NOT be proven — the prover must decline (requiresUnknown → runtime).
func TestTransitiveOrderingMissingLinkDeclines(t *testing.T) {
	src := `
def sink(lo: i64, hi: i64) -> i64:
    requires lo <= hi
    return hi

def caller(a: i64, b: i64, c: i64) -> i64:
    requires a <= b
    return sink(a, c)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "transitive_missing_link.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false},
	)
	if len(result.Errors()) == 0 {
		t.Fatalf("without `b<=c` the goal `a<=c` must NOT be proven (missing link must decline); got no error under -strict")
	}
}

// Soundness-negative (wrong direction): `a <= b, b < c` must NOT prove `a >= c`.
// The callee sink_geq requires `lo >= hi`; the prover must refute or decline — never conclude
// `a >= c` from evidence that proves `a < c`.
func TestTransitiveOrderingOppositeDirectionNotProven(t *testing.T) {
	src := `
def sink_geq(lo: i64, hi: i64) -> i64:
    requires lo >= hi
    return lo

def caller(a: i64, b: i64, c: i64) -> i64:
    requires a <= b
    requires b < c
    return sink_geq(a, c)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "transitive_wrong_dir.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false},
	)
	if len(result.Errors()) == 0 {
		t.Fatalf("`a<=b, b<c` must NOT prove `a>=c` (opposite direction); got no error under -strict")
	}
}

// --- in-loop monotonic counter bound (docs/85 Fallback 3) ------------------------------------
//
// Inside `while i < K:` the loop body sees `i < K` as a live smtAssertFact (seeded by
// analyzeBlockWithConditionAffineClone). The Fallback-3 path in tryProveRefinementByFlow
// harvests constant comparisons from those facts to build a range for the mutable counter,
// letting tier-1 discharge a refinement on `i` without the SMT tier.
// Soundness is inherited from smtAssertFacts invalidation: `i <- i + 1` drops the `i < K`
// fact, so after the increment the stale `i < K` fact is gone and the proof does not carry.

// COMPLETENESS-POSITIVE: inside `while i >= 0 and i < 10:` with a signed counter `i` seeded
// from a parameter (not a compile-time constant, so writtenConst and Fallback 2 do not apply),
// a binding `x: i64 is Bounded[0..=9] = i` must be proven statically (no runtime check, no
// error under -strict). Fallback 3 harvests the constant comparisons from the live smtAssertFacts
// seeded by the two-sided loop condition and derives the range [0, 9] for the mutable counter.
func TestLoopCounterBoundInBodyProven(t *testing.T) {
	src := boundedLaw + `
def f(start: i64) -> i64:
    i: mutable i64 = start
    while i >= 0 and i < 10:
        x: i64 is Bounded[0..=9] = i
        i <- i + 1
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "loop_counter_in_body.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("inside `while i >= 0 and i < 10:` body, `i is Bounded[0..=9]` should prove via in-body range facts (no error under -strict), got:\n%s", allDiagnostics(result))
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("loop-condition fact should prove without a runtime check, got %d runtime check(s)", len(result.RefinementChecks))
	}
}

// SOUNDNESS-NEGATIVE: after `i <- i + 1` inside the body, the smtAssertFacts seeded by the
// loop condition (`i >= 0`, `i < 10`) are invalidated via invalidateSMTAssertFactsForTarget,
// so the obligation `i is Bounded[0..=9]` MUST NOT still be assumed proved — it must fall
// back to a warning (runtime check). This verifies Fallback 3 does not carry a stale range
// past a mutation of the counter.
func TestLoopCounterBoundAfterIncrementNotAssumed(t *testing.T) {
	src := boundedLaw + `
def f(start: i64) -> i64:
    i: mutable i64 = start
    while i >= 0 and i < 10:
        i <- i + 1
        x: i64 is Bounded[0..=9] = i
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "loop_counter_after_inc.elisa", src, AnalyzeOptions{})
	// After the increment `i <- i + 1`, the smtAssertFacts `i >= 0` and `i < 10` are
	// invalidated. The obligation `i is Bounded[0..=9]` cannot be proven statically.
	if len(result.RefinementChecks) == 0 && !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("after `i <- i + 1`, the in-body range facts are invalidated; `i is Bounded[0..=9]` must NOT be statically proven (expected runtime check or proof warning), got:\n%s", allDiagnostics(result))
	}
}

// ── if/ternary range-merge ────────────────────────────────────────────────────

// Completeness: both branches of a ternary produce values in [0,100], so
// r = (a if cond else b) must carry that range and prove `r is Bounded[0,100]`
// statically without a runtime check.
func TestTernaryRangeBothBranchesInBound(t *testing.T) {
	src := boundedLaw + `
def f(a: i64 is Bounded[0, 100], b: i64 is Bounded[0, 100], cond: bool) -> i64 is Bounded[0, 100]:
    r: i64 = a if cond else b
    return r
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ternary_range_both.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("both branches in [0,100] must prove the return bound statically, got errors: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("both branches in [0,100] must NOT emit a runtime check, got %d check(s)", len(result.RefinementChecks))
	}
}

// Soundness: ONE branch is out of range ([0,200] vs [0,100]) so the union is
// [0,200] which does NOT entail [0,100]. The bound MUST NOT prove statically.
func TestTernaryRangeOneBranchOutOfBoundDoesNotProve(t *testing.T) {
	src := boundedLaw + `
def f(a: i64 is Bounded[0, 100], b: i64 is Bounded[0, 200], cond: bool) -> i64 is Bounded[0, 100]:
    r: i64 = a if cond else b
    return r
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ternary_range_one_out.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("one branch out of [0,100] must NOT prove the return bound; must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

// POSITIVE: A struct field invariant (`invariant self.x >= 0`) is established at every construction
// and after every mutation, so a caller that reads the field from a value returned by a builder may
// ASSUME the invariant holds. Reading `s.x` where `s` has `invariant self.x >= 0` must therefore
// let `y: i64 is Nat = s.x` discharge statically with NO runtime check — the struct-level invariant
// is the postcondition of the builder, analogous to a function-level return refinement.
func TestStructInvariantFieldReadDischargesRefinement(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

struct Pos:
    x: i64
    invariant self.x >= 0

def make_pos() -> Pos:
    return Pos{x: 5}

def use() -> i64:
    s = make_pos()
    y: i64 is Nat = s.x
    return y
`
	result := analyzeTreeTestSource(t, "struct_invariant_field_refine.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("struct invariant `self.x >= 0` must let `s.x is Nat` prove, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("struct invariant covers the field: expected NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// SOUNDNESS NEGATIVE: a struct WITHOUT a field invariant gives no static guarantee on the field —
// the caller must fall back to a runtime check. This is the soundness floor: the positive test above
// must not be caused by an over-approximation that also fires here.
func TestStructNoInvariantFieldReadRequiresRuntimeCheck(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

struct AnyInt:
    x: i64

def make_any() -> AnyInt:
    return AnyInt{x: 5}

def use() -> i64:
    s = make_any()
    y: i64 is Nat = s.x
    return y
`
	result := analyzeTreeTestSource(t, "struct_no_invariant_field_refine.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a struct without an invariant should still analyze (runtime fallback), got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("without a struct invariant there is no static guarantee: expected a runtime check")
		t.Fatalf("widening cast should be statically proven (not runtime), report=%+v", result.ProofReport)
	}
}
