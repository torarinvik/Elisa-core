//go:build cgo

package semantic

import (
	"testing"
)

// An ENUM written constant proves an enum law: `d <- Door.Open` makes `ensures d is Opened` provable.
// (Const-enum members const-evaluate; plain `enum` members do not const-fold in the engine yet, so the
// channel — which tracks any const-evaluable RHS — covers const enums here.)
func TestEnsuresRefinementProvenByWrittenEnum(t *testing.T) {
	src := `
const enum Door of u8:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def open_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Open
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_enum_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- Door.Open` should statically prove `ensures p is Opened`, got: %v", result.Errors())
	}
}

// An ENUM written constant that violates the law is refuted.
func TestEnsuresRefinementRefutedByWrittenEnum(t *testing.T) {
	src := `
const enum Door of u8:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def shut_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Shut
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_enum_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- Door.Shut` should REFUTE `ensures p is Opened`, got:\n%s", allDiagnostics(result))
	}
}

// A PARAMETRIC postcondition `ensures p is Bounded[0, 500]` is proven by a written constant in range.
func TestEnsuresParametricRefinementProvenByWrittenConst(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 500]:
    p <- 42
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- 42` should statically prove `ensures p is Bounded[0, 500]`, got: %v", result.Errors())
	}
}

// The range-sugar form `Bounded[0..=500]` desugars to the same two endpoints.
func TestEnsuresParametricRefinementRangeSugarProven(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0..=500]:
    p <- 500
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_range_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- 500` should statically prove `ensures p is Bounded[0..=500]`, got: %v", result.Errors())
	}
}

// A written constant OUT of the parametric bounds is refuted.
func TestEnsuresParametricRefinementRefutedByWrittenConst(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 500]:
    p <- 600
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- 600` should REFUTE `ensures p is Bounded[0, 500]`, got:\n%s", allDiagnostics(result))
	}
}

// The arg count must match the law's non-subject parameters.
func TestEnsuresParametricRefinementArgCountMismatch(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0]:
    p <- 1
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_argcount.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "expects 2 argument") {
		t.Fatalf("`Bounded[0]` should error on arg count, got:\n%s", allDiagnostics(result))
	}
}

// A caller GAINS the parametric fact across a call whose callee declares the matching postcondition.
func TestEnsuresParametricRefinementCallerGain(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 500]:
    p <- 1
    return
def needs(q: i64 is Bounded[0, 500]) -> void:
    return
def caller() -> void:
    x: mutable i64 = 9999
    clamp(&x)
    needs(x)
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_caller_gain.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("caller should GAIN `Bounded[0, 500]` on x after clamp(&x) to satisfy needs(x), got: %v", result.Errors())
	}
}

func TestMutableRefinedOutParamSeedsReturnRefinement(t *testing.T) {
	src := `
law Bounded(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
type Small = u32 is Bounded[0, 103]

def TrySmall(value: u32, out: mutable Small&) -> bool:
    if value <= 103:
        out <- value
        return true
    return false

def get(value: u32) -> Small:
    out: mutable Small = 0
    if TrySmall(value, &out):
        return out
    assert false
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_refined_out_param_return.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("mutable refined out-param should seed the returned local's range, got:\n%s", allDiagnostics(result))
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("returning the refined out-param should be proven without runtime checks, got %d", len(result.RefinementChecks))
	}
}

// A guard or assert that bounds an UNSIGNED integer from above (`x <= 103`) supplies the upper
// bound; the variable's unsigned declared type supplies non-negativity (lo=0). The flow prover must
// MERGE the two so `return x` discharges `is Bounded[0, 103]` with no runtime check. Before the
// merge, a present guard range fact (hi=103, lo open) shadowed the declared-type non-negativity
// fallback, so the law's `self >= 0` conjunct was never entailed and the return paid a runtime check.
func TestGuardUpperBoundMergesUnsignedNonNegativityForReturn(t *testing.T) {
	law := "law Bounded(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi\ntype Small = u32 is Bounded[0, 103]\n"
	cases := map[string]string{
		"if_guard": law + `def f(x: u32) -> Small:
    if x <= 103:
        return x
    return 0
`,
		"assert": law + `def f(x: u32) -> Small:
    can Abort.Panic:
        assert x <= 103
        return x
`,
		// The bind-a-field-to-a-local idiom: a refined accessor reads a raw u32 field, asserts the
		// in-range bound, and returns the local. This is the shader register-index accessor shape.
		"field_via_local": law + `struct V:
    reg: mutable u32
def f(v: V) -> Small:
    can Abort.Panic:
        r: u32 = v.reg
        assert r <= 103
        return r
`,
	}
	for name, src := range cases {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name+"_merge.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
		if len(result.Errors()) != 0 {
			t.Fatalf("case %s: upper-bound guard + unsigned non-negativity should prove the return, got:\n%s", name, allDiagnostics(result))
		}
		if len(result.RefinementChecks) != 0 {
			t.Fatalf("case %s: return should be proven without a runtime check, got %d", name, len(result.RefinementChecks))
		}
	}
}

// Soundness guard for the non-negativity merge: a SIGNED integer bounded only from above (`x <= 103`)
// must NOT discharge `is Bounded[0, 103]`, because the lower bound `x >= 0` is not entailed (x may be
// negative). The merge only adds declared-type bounds, which for a signed type do not include lo=0.
func TestSignedUpperBoundDoesNotProveLowerBound(t *testing.T) {
	src := "law Bounded(self: i32, lo: i32, hi: i32) = self >= lo and self <= hi\ntype SmallI = i32 is Bounded[0, 103]\n" +
		`def f(x: i32) -> SmallI:
    can Abort.Panic:
        assert x <= 103
        return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "signed_upper_only.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatalf("a signed value bounded only from above must NOT prove the [0,103] return refinement")
	}
}

// A PLAIN (non-packed) enum written constant proves the law: plain enum variants without payloads
// are now const-folded (their tag value), so the written-const channel reasons about them too.
func TestEnsuresRefinementProvenByWrittenPlainEnum(t *testing.T) {
	src := `
enum Door:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def open_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Open
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_plain_enum_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- Door.Open` (plain enum) should statically prove `ensures p is Opened`, got: %v", result.Errors())
	}
}

// A PLAIN enum written constant that violates the law is refuted.
func TestEnsuresRefinementRefutedByWrittenPlainEnum(t *testing.T) {
	src := `
enum Door:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def shut_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Shut
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_plain_enum_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- Door.Shut` (plain enum) should REFUTE `ensures p is Opened`, got:\n%s", allDiagnostics(result))
	}
}

// --- tier-2: bounded linear arithmetic (docs/86) ---------------------------------------------

// Tier-2 proves a DERIVED affine subject: `tx*64 + ty` with tx,ty ∈ [0,63] is bounded by
// [0, 4095], discharging the return refinement with NO runtime check. Tier-1 cannot — the
// subject is an expression, not a bare variable.
func TestLinearDerivedIndexProven(t *testing.T) {
	src := boundedLaw + `
def tile_index(tx: i64, ty: i64) -> i64 is Bounded[0, 4095]:
    if tx >= 0 and tx <= 63 and ty >= 0 and ty <= 63:
        return tx * 64 + ty
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "linear_index.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("64*tx + ty over [0,63]² ⊆ [0,4095] should prove via tier-2, got: %v", result.Errors())
	}
	var linear int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear {
			linear++
		}
	}
	if linear == 0 {
		t.Fatalf("the derived-index discharge should be recorded as proven (linear), got report=%+v", result.ProofReport)
	}
}

// Soundness: without the upper-bound guard, the affine bound is open on top and tier-2 must
// DECLINE (stays unproven → runtime check / strict error), never over-prove.
func TestLinearDerivedIndexInsufficientGuardDeclines(t *testing.T) {
	src := boundedLaw + `
def tile_index(tx: i64, ty: i64) -> i64 is Bounded[0, 4095]:
    if tx >= 0 and ty >= 0:
        return tx * 64 + ty
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "linear_index_weak.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("an unbounded-above affine subject must NOT prove the top of [0,4095], got:\n%s", allDiagnostics(result))
	}
}

// A const module value folds into the affine form: `tx*MAPHEIGHT + ty` with MAPHEIGHT a const.
func TestLinearDerivedIndexFoldsNamedConst(t *testing.T) {
	src := boundedLaw + `
const module Map:
    MAPHEIGHT: i64 = 64

def tile_index(tx: i64, ty: i64) -> i64 is Bounded[0, 4095]:
    if tx >= 0 and tx <= 63 and ty >= 0 and ty <= 63:
        return tx * Map::MAPHEIGHT + ty
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "linear_index_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("a named const coefficient should fold into the affine form and prove, got: %v", result.Errors())
	}
}

// --- tier-2 brick 86-2: param-refinement seeding (docs/86) -----------------------------------

// A refinement-typed param (via alias) carries its bound on ENTRY: the docs/85 §13 form proves
// with NO body guard — tx,ty ∈ [0,63] seed the range facts, tier-2 bounds tx*64+ty ⊆ [0,4095].
func TestParamRefinementSeedsDerivedIndex(t *testing.T) {
	src := boundedLaw + `
type TileX = i64 is Bounded[0, 63]
type TileY = i64 is Bounded[0, 63]

def tile_index(tx: TileX, ty: TileY) -> i64 is Bounded[0, 4095]:
    return tx * 64 + ty
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_seed_index.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("refinement-typed params should seed entry bounds and prove the derived index, got: %v", result.Errors())
	}
	var linear int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear {
			linear++
		}
	}
	if linear == 0 {
		t.Fatalf("the seeded derived index should be proven (linear), got report=%+v", result.ProofReport)
	}
}

// Seeding also feeds tier-1: a bare refinement param proves a same-bound return with no guard.
func TestParamRefinementSeedsBareReturn(t *testing.T) {
	src := boundedLaw + `
def echo(n: i64 is Bounded[0, 100]) -> i64 is Bounded[0, 100]:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_seed_bare.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("a bare refinement param should seed its bound and prove a same-bound return, got: %v", result.Errors())
	}
}

// Soundness: a WIDER param refinement must NOT prove a tighter obligation. n ∈ [0,1000] does not
// entail [0,100] — the seed is exact, never over-wide.
func TestParamRefinementSeedDoesNotOverprove(t *testing.T) {
	src := boundedLaw + `
def narrow(n: i64 is Bounded[0, 1000]) -> i64 is Bounded[0, 100]:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_seed_overprove.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("a [0,1000] param must not prove a [0,100] return; must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

// --- module-level const resolution in refinements (docs/85 const-global range) -----

// A top-level `const LIMIT: i64 = 500` used as a bracket arg in `Bounded[0, LIMIT]`
// resolves to its concrete value via constIntValue → evalConstExpr → lookupVisibleConst,
// so the refinement proves WITHOUT z3 (const tier only).
func TestRefinementBracketArgResolvesModuleLevelConst(t *testing.T) {
	src := boundedLaw + `
const LIMIT: i64 = 500

def f() -> i64:
    x: i64 is Bounded[0, LIMIT] = 42
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_bracket_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("42 ∈ [0, LIMIT=500] should prove via const-fold (no z3), got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("const-proven bracket refinement should emit no runtime check")
	}
}

// `requires x < K` where K is a module-level const resolves to a concrete bound;
// a guard `if a < LIMIT` should satisfy it at the flow tier — no z3 needed.
func TestRefinementRequiresDischargesFromModuleLevelConst(t *testing.T) {
	src := `
const LIMIT: i64 = 100

def needs_small(x: i64) -> i64:
    requires x < LIMIT
    return x

def caller(a: i64) -> i64:
    if a < LIMIT:
        return needs_small(a)
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_requires_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`a < LIMIT` guard should satisfy `requires x < LIMIT` via const-fold, got: %v", result.Errors())
	}
}

// SOUNDNESS-NEGATIVE: a `global mutable` variable must NOT be treated as a constant bound.
// `requires x < mutableCap` cannot be discharged at compile time — the value changes at
// runtime. Under -strict the prover must reject this as unprovable.
func TestRefinementMutableGlobalIsNotConstBound(t *testing.T) {
	src := `
global mutable mutableCap: i64 = 100

def needs_small(x: i64) -> i64:
    requires x < mutableCap
    return x

def caller(a: i64) -> i64:
    return needs_small(a)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_mutable_global_not_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatal("mutable global must NOT be treated as a constant bound — strict mode must reject unprovable requires")
	}
}

// Two parameters, each carrying a parametric refinement, must PARSE (the `,` in
// `Bounded[lo, hi]` previously collided with the parameter separator, breaking the
// param list) AND statically discharge a multiplicative return bound from both
// input bounds (31*4096 + 4095 == 131071) — the voice-state index proof shape.
func TestMultiParamRefinementParsesAndDischarges(t *testing.T) {
	src := `
law Bounded(self: usize, lo: usize, hi: usize) = self >= lo and self <= hi

def slot_index(rack: usize is Bounded[0, 31], voice: usize is Bounded[0, 4095]) -> usize is Bounded[0, 131071]:
    return rack * 4096 + voice
`
	result := analyzeTreeTestSource(t, "multi_param_refine.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("two-param refinement should parse and analyze cleanly, got:\n%s", allDiagnostics(result))
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("the multiplicative return bound should be PROVEN from the input bounds (no runtime check), got %d", len(result.RefinementChecks))
	}
}

// POSITIVE: a string literal's `.len` is known at compile time, so a refinement
// bound on it proves at the const tier — no runtime check emitted.
func TestStringLiteralLenProvesConst(t *testing.T) {
	src := `
law AtLeast3(self: i64) = self >= 3

def f() -> i64 is AtLeast3:
    return "abc".len
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "strlit_len_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`\"abc\".len` is 3, should prove AtLeast3 statically (no error under -strict), got: %v", result.Errors())
	}
	// Verify the proof was recorded as const-tier, not runtime.
	var constCount int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenConst {
			constCount++
		}
	}
	if constCount == 0 {
		t.Fatalf("`\"abc\".len >= 3` should discharge at const tier, proof report: %+v", result.ProofReport)
	}
}

// POSITIVE: empty string literal `.len` == 0 — the zero case proves correctly.
func TestStringLiteralEmptyLenIsZero(t *testing.T) {
	src := `
law IsZero(self: i64) = self == 0

def f() -> i64 is IsZero:
    return "".len
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "strlit_empty_len.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`\"\".len` is 0, should prove IsZero statically, got: %v", result.Errors())
	}
}

// SOUNDNESS-NEGATIVE: a non-literal (parameter) string's length must NOT be
// fabricated as a constant — the length stays opaque and a strict upper bound
// cannot be proved, so EnforceStrictProofs must produce an error.
func TestNonLiteralStringLenIsOpaque(t *testing.T) {
	src := `
law AtLeast3(self: i64) = self >= 3

def f(s: cstr) -> i64 is AtLeast3:
    return s.len
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "strlit_len_opaque.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatal("a parameter string's length is opaque — prover must NOT assume it satisfies AtLeast3 (should error under -strict)")
	}
}
