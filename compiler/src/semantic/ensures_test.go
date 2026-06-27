package semantic

import (
	"strings"
	"testing"
)

func TestSemanticEnsuresNamedStateCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_named_state.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_ready(job: ParseJob[Ready]&) -> void:
	pass

def finish_ok(mutable job: ParseJob[Pending]&) -> void can[Abort] ensures job => Ready:
	job.checksum <- 7
	job.stage <- 1

def use(job: ParseJob[Pending]&) -> void can[Abort]:
	finish_ok(job)
	expect_ready(job)
`)
	sym, ok := result.GlobalScope.Lookup("finish_ok")
	if !ok {
		t.Fatal("expected finish_ok symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected finish_ok function type, got %T", sym.Type)
	}
	if len(fnType.Poststates) != 1 {
		t.Fatalf("expected finish_ok to record one poststate, got %#v", fnType.Poststates)
	}
	if fnType.Poststates[0].Kind != FuncPoststateKindNamedState || len(fnType.Poststates[0].StateCases) != 1 || fnType.Poststates[0].StateCases[0] != "Ready" {
		t.Fatalf("expected finish_ok poststate to record Ready, got %#v", fnType.Poststates[0])
	}
	analysis, ok := result.FunctionAnalysisByName("finish_ok")
	if !ok || analysis == nil {
		t.Fatal("expected finish_ok function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformEnsure, FactTypestate, "job", "ensures typestate Ready") {
		t.Fatalf("expected function analysis to expose named-state ensure transform, got %#v", analysis.FactTransforms)
	}
}

func TestSemanticEnsuresRejectsWrongNamedStateProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_bad_named_state.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def bad_finish(mutable job: ParseJob[Pending]&) -> void can[Abort] ensures job => Ready:
	job.stage <- 2
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures job => Ready") {
		t.Fatalf("expected named-state ensures proof failure, got:\n%s", errText)
	}
	if !strings.Contains(errText, "current tracked facts are ParseJob[Failed]") {
		t.Fatalf("expected named-state ensures fact diagnostic, got:\n%s", errText)
	}
}

func TestSemanticEnsuresRefStateCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_refstate.elisa", `struct HeapPairNode:
	value: i32

def expect_null(node: heap HeapPairNode!) -> void:
	pass

extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void can[Abort] ensures node => !

def use(node: heap HeapPairNode&) -> void can[Abort]:
	sfree_heap_pair_node(node)
	expect_null(node)
`)
	sym, ok := result.GlobalScope.Lookup("sfree_heap_pair_node")
	if !ok {
		t.Fatal("expected extern sfree_heap_pair_node symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected sfree_heap_pair_node function type, got %T", sym.Type)
	}
	if len(fnType.Poststates) != 1 || fnType.Poststates[0].Kind != FuncPoststateKindRefState || fnType.Poststates[0].RefState != RefStateNull {
		t.Fatalf("expected extern refstate poststate to record node => !, got %#v", fnType.Poststates)
	}
}

func TestSemanticEnsuresDefRefStateProofAndCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_require_non_null.elisa", `struct HeapPairNode:
	value: i32

def expect_non_null(node: heap HeapPairNode&) -> void:
	pass

def require_non_null(node: heap HeapPairNode&?) -> void can[Abort] ensures node => &:
	assert node != null

def use(node: heap HeapPairNode&?) -> void can[Abort]:
	require_non_null(node)
	expect_non_null(node)
`)
	analysis, ok := result.FunctionAnalysisByName("require_non_null")
	if !ok || analysis == nil {
		t.Fatal("expected require_non_null function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformEnsure, FactRefState, "node", "ensures refstate &") {
		t.Fatalf("expected function analysis to expose refstate ensure transform, got %#v", analysis.FactTransforms)
	}
}

func TestSemanticEnsuresPreserveCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_preserve.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def expect_alive(player: Player[Alive]&) -> void:
	pass

def bump_score(mutable player: Player[Alive]&) -> void can[Abort] ensures player => preserve:
	player.score <- player.score + 1

def use(player: Player[Alive]&) -> void can[Abort]:
	bump_score(player)
	expect_alive(player)
`)
	analysis, ok := result.FunctionAnalysisByName("bump_score")
	if !ok || analysis == nil {
		t.Fatal("expected bump_score function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformEnsure, FactTypestate, "player", "ensures preserve") {
		t.Fatalf("expected function analysis to expose preserve ensure transform, got %#v", analysis.FactTransforms)
	}
}

func TestSemanticEnsuresRejectsInvalidPreserveAfterRelevantMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_bad_preserve.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad_preserve(mutable player: Player[Alive]&) -> void can[Abort] ensures player => preserve:
	player.health <- 0
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures player => preserve") {
		t.Fatalf("expected preserve proof failure, got:\n%s", errText)
	}
	if !strings.Contains(errText, "current tracked facts are Player[Dead]&") {
		t.Fatalf("expected preserve tracked-facts diagnostic, got:\n%s", errText)
	}
}

func TestSemanticEnsuresNestedFieldCallRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ensures_nested_fields.elisa", `struct HeapPairNode:
	value: i32

struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

struct Team:
	player: mutable Player[Alive]
	slot: mutable heap HeapPairNode&?

def expect_dead(player: Player[Dead]) -> void:
	pass

def expect_null(node: heap HeapPairNode!) -> void:
	pass

def kill_team(mutable team: Team&) -> void can[Abort] ensures team.player => Dead:
	team.player.health <- 0

def clear_slot(mutable team: Team&) -> void can[Abort] ensures team.slot => !:
	team.slot <- null

def use(team: Team&) -> void can[Abort]:
	kill_team(team)
	expect_dead(team.player)
	clear_slot(team)
	expect_null(team.slot)
`)
}

func TestSemanticEnsuresRejectsInvalidTargets(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_invalid_targets.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad_ref(count: int) -> void can[Abort] ensures count => !:
	pass

def bad_state(player: Player[Alive]&) -> void can[Abort] ensures player => Closed:
	pass
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "requires \"count\" to resolve to a ref target") {
		t.Fatalf("expected invalid ref-target diagnostic, got:\n%s", errText)
	}
	if !strings.Contains(errText, "uses unknown state \"Closed\"") {
		t.Fatalf("expected invalid named-state diagnostic, got:\n%s", errText)
	}
}

func TestSemanticConditionalEnsuresBranchRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "conditional_ensures_branch.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_ready(job: ParseJob[Ready]&) -> void:
	pass

def expect_failed(job: ParseJob[Failed]&) -> void:
	pass

def finish(mutable job: ParseJob[Pending]&, ok: bool) -> bool can[Abort] ensures return true => job => Ready, return false => job => Failed:
	if ok:
		job.stage <- 1
		job.checksum <- 7
		return true
	job.stage <- 2
	return false

def use(mutable job: ParseJob[Pending]&, ok: bool) -> void can[Abort] ensures job => Ready | Failed:
	if finish(job, ok):
		expect_ready(job)
	else:
		expect_failed(job)
`)
}

func TestSemanticConditionalEnsuresJoinOnPlainCall(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "conditional_ensures_join.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_done(job: ParseJob[Ready | Failed]&) -> void:
	pass

def finish(mutable job: ParseJob[Pending]&, ok: bool) -> bool can[Abort] ensures return true => job => Ready, return false => job => Failed:
	if ok:
		job.stage <- 1
		return true
	job.stage <- 2
	return false

def use(mutable job: ParseJob[Pending]&, ok: bool) -> void can[Abort] ensures job => Pending | Ready | Failed:
	finish(job, ok)
	expect_done(job)
`)
}

func TestSemanticConditionalEnsuresPreserveBranchRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "conditional_ensures_preserve.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def expect_alive(player: Player[Alive]&) -> void:
	pass

def expect_dead(player: Player[Dead]&) -> void:
	pass

def maybe_update(mutable player: Player[Alive]&, ok: bool) -> bool can[Abort] ensures return true => player => preserve, return false => player => Dead:
	if ok:
		player.score <- player.score + 1
		return true
	player.health <- 0
	return false

def use(mutable player: Player[Alive]&, ok: bool) -> void can[Abort] ensures player => Alive | Dead:
	if maybe_update(player, ok):
		expect_alive(player)
	else:
		expect_dead(player)
`)
}

func TestSemanticConditionalEnsuresRejectWrongReturnBranchProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "conditional_ensures_wrong_branch.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def bad_finish(mutable job: ParseJob[Pending]&, ok: bool) -> bool can[Abort] ensures return true => job => Ready, return false => job => Failed:
	if ok:
		job.stage <- 1
		return false
	job.stage <- 2
	return true
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures job => Failed") {
		t.Fatalf("expected false-branch conditional ensures proof failure, got:\n%s", errText)
	}
	if !strings.Contains(errText, "cannot prove ensures job => Ready") {
		t.Fatalf("expected true-branch conditional ensures proof failure, got:\n%s", errText)
	}
}

func TestSemanticConditionalEnsuresRejectNonBoolReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "conditional_ensures_non_bool.elisa", `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad(player: Player[Alive]&) -> void can[Abort] ensures return true => player => preserve:
	pass
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "requires a bool return type") {
		t.Fatalf("expected non-bool conditional ensures diagnostic, got:\n%s", errText)
	}
}

// docs/85/90: a general-boolean `ensure` postcondition is statically DISCHARGED under
// -strict via the SMT tier (substitute `result` -> the returned expression and prove the
// clause). A provable clause raises no diagnostic; an unprovable one is a hard error.
// Without -strict it stays a silent debug runtime check (no warning noise).
func TestEnsureBooleanStaticallyDischargedUnderStrict(t *testing.T) {
	provable := `
def add10(x: i64) -> i64:
    requires x >= 0
    requires x < 1000
    ensure result > x
    return x + 10
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_ok.elisa", provable, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("a provable `ensure result > x` for `return x + 10` should discharge under -strict, got:\n%s", strings.Join(errs, "\n"))
	}

	unprovable := `
def bad(x: i64) -> i64:
    ensure result > x
    return x - 1
`
	r2 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_bad.elisa", unprovable, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r2), "could not be proven statically") {
		t.Fatalf("an unprovable `ensure result > x` for `return x - 1` must be a -strict error, got:\n%s", allDiagnostics(r2))
	}

	// Non-strict: the same unprovable clause is silent (runtime check only).
	r3 := analyzeFunctionAnalysisTestSource(t, "ensure_silent.elisa", unprovable)
	if contains(allDiagnostics(r3), "could not be proven") {
		t.Fatalf("without -strict an unproven ensure must be silent, got:\n%s", allDiagnostics(r3))
	}
}

// A boolean postcondition phrased against a literal (`ensure result == true` / `== false`) must
// discharge exactly like the bare predicate it folds to — `result == true` ≡ `result`, `result ==
// false` ≡ `not result`. Before normalizeBoolLiteralEnsure, the literal comparison was modeled as an
// opaque Bool equality and a no-overflow law written `ensure result == true` over `return a >= b`
// failed even under -smt, while the bare `ensure result` proved. Soundness: an unsatisfiable literal
// postcondition still declines with a counterexample.
func TestEnsureBooleanLiteralComparisonNormalizes(t *testing.T) {
	provable := `
def no_overflow(a: u32, b: u32) -> bool:
    requires a < 1000
    requires b < 1000
    ensure result == true
    return a + b >= a

def empty_range(a: u64) -> bool:
    ensure result == false
    return a < a
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_boollit_ok.elisa", provable, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("`ensure result == true/false` should discharge like the bare predicate, got:\n%s", strings.Join(errs, "\n"))
	}

	unsound := `
def bad(a: u64, b: u64) -> bool:
    ensure result == true
    return a >= b
`
	r2 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_boollit_bad.elisa", unsound, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r2), "could not be proven statically") {
		t.Fatalf("an unprovable `ensure result == true` over `return a >= b` must still be a -strict error, got:\n%s", allDiagnostics(r2))
	}
}

// A `requires`-seeded interval bound makes a guarded integer modulo discharge via the SMT tier's
// NATIVE `mod`/`div` (Euclidean == truncating for non-negative operands). Both prove that the
// `requires alignment >= 1` precondition reaches the modulo-soundness gate (provablyPositive) AND
// that z3's native mod theory cracks the divisibility goal. align_down is fully provable; the
// requires-bound case verifies the seed path end-to-end.
func TestEnsureModuloDischargesWithRequiresSeededBound(t *testing.T) {
	src := `
def align_down(value: u64, alignment: u64) -> u64:
    ensure result <= value
    ensure alignment == 0 or (result % alignment) == 0
    if alignment == 0:
        return value
    return value - (value % alignment)

def round_down(value: u64, alignment: u64) -> u64:
    requires alignment >= 1
    ensure (result % alignment) == 0
    return value - (value % alignment)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_mod_ok.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("guarded/requires-bounded modulo postconditions should discharge under -strict, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestEnsureUsesOnlyProvenInvariantFacts(t *testing.T) {
	src := `
def bad(x: u64) -> u64:
    ensure result > x
    y: mutable u64 = 0
    invariant y > x
    return y
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_unproven_invariant.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "invariant could not be proven statically") {
		t.Fatalf("unproven invariant should be reported as runtime-only, got:\n%s", diags)
	}
	if !contains(diags, "ensure postcondition") {
		t.Fatalf("unproven invariant must not discharge a strict ensure, got:\n%s", diags)
	}
}

func TestEnsureDischargesFromProvenInvariantAndAssignmentFacts(t *testing.T) {
	src := `
def contains_out(addr: u64, start_out: mutable u64&, end_out: mutable u64&) -> bool:
    ensure not result or (start_out <= addr and addr < end_out)
    lo: u64 = 10
    hi: u64 = 20
    if addr >= lo and addr < hi:
        start_out <- lo
        end_out <- hi
        invariant start_out <= addr and addr < end_out
        return true
    return false
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_proven_invariant_out.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("proven invariant plus exact assignment facts should discharge strict ensure, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestEnsureDischargesFromCountUpWhileExitFact(t *testing.T) {
	src := `
const MAX_NAME: usize = 32

def capped_count(flag: bool) -> usize:
    ensure result <= MAX_NAME
    n: mutable usize = 0
    while n < MAX_NAME and flag:
        invariant n <= MAX_NAME
        n <- n + 1
    invariant n <= MAX_NAME
    return n
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_countup_exit.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("count-up loop exit fact should discharge strict cap ensure, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestEnsureDischargesFromReturnedCallPostcondition(t *testing.T) {
	// A SOUND postcondition (`n % 8 < 8` holds for all unsigned n — no wraparound) propagated through a
	// returned direct call, including the extern-arg and Pascal-cased variants.
	src := `
def cap8(n: usize) -> usize:
    ensure result < 8
    return n % 8

def low_bits(extra: usize) -> usize:
    ensure result < 8
    return cap8(extra + 1)

extern strlen_like(name: static u8&) -> usize

def low_bits_from_call(name: static u8&) -> usize:
    ensure result < 8
    extra: usize = strlen_like(name)
    return cap8(extra + 1)

def Cap8(n: usize) -> usize:
    ensure result < 8
    return n % 8

def LowBitsPascal(name: static u8&) -> usize:
    ensure result < 8
    extra: usize = strlen_like(name)
    return Cap8(extra + 1)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_returned_call.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("callee postcondition on a returned direct call should discharge caller ensure, got:\n%s", strings.Join(errs, "\n"))
	}
}

// TestEnsureRequiresRelationalNoUnderflow: a `requires` relational bound (`requires b <= a`, etc.)
// establishes that unsigned `a - b` cannot underflow, so `ensure result <= a` discharges. Each of the
// four equivalent relational forms works; the wrong-direction precondition and the unguarded case must
// still be rejected (soundness floor — a precondition that does NOT imply `a >= b` proves nothing).
func TestEnsureRequiresRelationalNoUnderflow(t *testing.T) {
	provable := []string{"b <= a", "a >= b", "b < a", "a > b"}
	for _, form := range provable {
		t.Run("proves_"+form, func(t *testing.T) {
			src := "def t(a: u64, b: u64) -> u64:\n    requires " + form + "\n    ensure result <= a\n    return a - b\n"
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "req_noundf.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if errs := r.Errors(); len(errs) != 0 {
				t.Fatalf("`requires %s` should discharge `ensure result <= a; return a - b`, got:\n%s", form, strings.Join(errs, "\n"))
			}
		})
	}
	rejected := []struct{ name, req string }{
		{"wrong_direction", "a <= b"},
		{"unrelated", "b >= 1"},
	}
	for _, tc := range rejected {
		t.Run("rejects_"+tc.name, func(t *testing.T) {
			src := "def t(a: u64, b: u64) -> u64:\n    requires " + tc.req + "\n    ensure result <= a\n    return a - b\n"
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "req_bad.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if errs := r.Errors(); len(errs) == 0 {
				t.Fatalf("`requires %s` does NOT establish a>=b, so unsigned a-b<=a must be rejected (unsound otherwise)", tc.req)
			}
		})
	}
}

// TestEnsureDischargesNewProverShapes covers three prover-power additions: a `.count` length read
// through a reference, a boolean-valued equality postcondition, and a pointer-null disjunction. Each
// has a paired negative case that must still be rejected (soundness floor).
func TestEnsureDischargesNewProverShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		ok   bool
	}{
		{"count_through_ref", `
struct Reader:
    data: darray[u8]
def remaining(r: Reader&) -> u64:
    ensure result <= r.data.count
    return r.data.count
`, true},
		{"count_through_ref_guarded_sub", `
struct Reader:
    data: darray[u8]
def left(r: Reader&, used: u64) -> u64:
    requires used <= r.data.count
    ensure result <= r.data.count
    return r.data.count - used
`, true},
		{"bool_modulo_equality", `
def is_aligned(x: u64, m: u64) -> bool:
    requires m >= 1
    ensure result == ((x % m) == 0)
    return (x % m) == 0
`, true},
		{"bool_modulo_equality_wrong", `
def bad(x: u64, m: u64) -> bool:
    requires m >= 1
    ensure result == ((x % m) == 1)
    return (x % m) == 0
`, false},
		{"free_bool_xor", `
def neq(p: bool, q: bool) -> bool:
    ensure result == (p != q)
    return p != q
`, true},
		{"pointer_null_disjunction", `
def write_out(out_ptr: void&?, val: i64) -> bool:
    ensure (out_ptr != null) or (result == false)
    if out_ptr == null:
        return false
    return true
`, true},
		{"pointer_null_disjunction_wrong", `
def bad(out_ptr: void&?, val: i64) -> bool:
    ensure (out_ptr != null) or (result == false)
    return true
`, false},
		{"pointer_null_disjunction_via_cast", `
struct OrbisKernelEvent:
    data: u64
def GetEventData(ev: void&?) -> u64:
    ensure (ev != null) or (result == 0)
    e: OrbisKernelEvent&? = ev.cast[OrbisKernelEvent&?]
    if e == null:
        return 0
    return e.data
`, true},
		{"pointer_null_disjunction_via_cast_wrong", `
struct OrbisKernelEvent:
    data: u64
def bad(ev: void&?) -> u64:
    ensure (ev != null) or (result == 0)
    e: OrbisKernelEvent&? = ev.cast[OrbisKernelEvent&?]
    return e.data
`, false},
		{"pointer_null_result_disjunction_via_cast", `
struct OrbisKernelEvent:
    udata: void&?
def GetEventUserData(ev: void&?) -> void&?:
    ensure (ev != null) or (result == null)
    e: OrbisKernelEvent&? = ev.cast[OrbisKernelEvent&?]
    if e == null:
        return null
    return e.udata
`, true},
		{"pointer_null_result_disjunction_missing_fallback", `
struct OrbisKernelEvent:
    udata: void&?
def bad_missing(ev: void&?) -> void&?:
    ensure ev != null
    e: OrbisKernelEvent&? = ev.cast[OrbisKernelEvent&?]
    if e == null:
        return null
    return e.udata
`, false},
		{"pointer_null_result_disjunction_nonnull_on_null_path", `
struct OrbisKernelEvent:
    udata: void&?
def bad_nonnull(ev: void&?, fallback: void&?) -> void&?:
    ensure result == null
    e: OrbisKernelEvent&? = ev.cast[OrbisKernelEvent&?]
    if e == null:
        return fallback
    return e.udata
`, false},
		// Gap C: two-pointer conjunction — `(p != null) and (q != null)` in the ensure must discharge
		// when both pointers are independently established non-null via `requires`.
		{"two_pointer_conjunction_both_requires", `
def f(p: void&?, q: void&?) -> bool:
    requires p != null
    requires q != null
    ensure (p != null) and (q != null)
    return true
`, true},
		// Soundness-negative: `requires p != null` alone must NOT prove `q != null`.
		{"two_pointer_conjunction_only_p_requires", `
def bad(p: void&?, q: void&?) -> bool:
    requires p != null
    ensure (p != null) and (q != null)
    return true
`, false},
		// Disjunction: `(p != null) or (q != null)` discharges when `requires p != null` is established.
		{"two_pointer_disjunction_p_requires", `
def either(p: void&?, q: void&?) -> bool:
    requires p != null
    ensure (p != null) or (q != null)
    return true
`, true},
		// Gap C guard-path: conjunction of two per-pointer nullness facts from fallthrough guards.
		// At `return true`, both `not(p==null)` and `not(q==null)` are scope facts. The ensure
		// uses `result` to make the obligation trivial at the early-exit `return false` sites.
		{"two_pointer_conjunction_via_guards", `
def check_both(p: void&?, q: void&?) -> bool:
    ensure (not result) or ((p != null) and (q != null))
    if p == null:
        return false
    if q == null:
        return false
    return true
`, true},
		// Soundness-negative for the guard path: `q != null` must NOT be provable without the second guard.
		{"two_pointer_conjunction_via_guards_only_p", `
def bad_guard(p: void&?, q: void&?) -> bool:
    ensure (not result) or ((p != null) and (q != null))
    if p == null:
        return false
    return true
`, false},
		// Pointer-equality implication: `(p == q) implies (p != null == q != null)`.
		// boolTerm lowers `p == q` for pointer types to `(= isnull_p isnull_q)` — Gap C fix.
		// This discharges the null-equivalence postcondition from the p==q requires hypothesis.
		{"pointer_eq_implies_null_equiv", `
def eq_null_equiv(p: void&?, q: void&?) -> bool:
    requires p == q
    ensure (p != null) == (q != null)
    return true
`, true},
		// Soundness-negative: WITHOUT p==q requires, null equivalence must NOT discharge.
		{"pointer_eq_implies_null_equiv_unsound", `
def bad_no_eq(p: void&?, q: void&?) -> bool:
    ensure (p != null) == (q != null)
    return true
`, false},
		{"struct_result_field", `
struct Pair:
    a: i64
    b: i64
def mk() -> Pair:
    ensure result.a == 1
    ensure result.b == 2
    return Pair(1, 2)
`, true},
		{"struct_result_field_wrong", `
struct Pair:
    a: i64
    b: i64
def bad() -> Pair:
    ensure result.a == 9
    return Pair(1, 2)
`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", tc.src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			errs := r.Errors()
			if tc.ok && len(errs) != 0 {
				t.Fatalf("expected discharge, got:\n%s", strings.Join(errs, "\n"))
			}
			if !tc.ok && len(errs) == 0 {
				t.Fatalf("expected rejection (unsound otherwise), but it discharged")
			}
		})
	}
}

// TestEnsureRejectsUnsignedWraparoundPostconditions pins the soundness fix: a postcondition that holds
// only in unbounded ℤ but is FALSE on the wrapping machine must NOT discharge under -strict. Unsigned
// `a - b` underflows when b > a, and `n + k` overflows near the type max, so neither `result <= a` nor
// `result >= n` is a valid postcondition without a bound or guard.
func TestEnsureRejectsUnsignedWraparoundPostconditions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"sub_underflow", `
def f(a: u64, b: u64) -> u64:
    ensure result <= a
    return a - b
`},
		{"sub_underflow_field", `
struct Mgr:
    total: u64
    usage: u64
def avail(self: Mgr&) -> u64:
    ensure result <= self.total
    return self.total - self.usage
`},
		{"add_overflow", `
def g(n: usize) -> usize:
    ensure result >= n
    return n + 8
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", tc.src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if errs := r.Errors(); len(errs) == 0 {
				t.Fatalf("unsigned wraparound postcondition must NOT discharge under -strict (unsound), but it was accepted")
			}
		})
	}
}
