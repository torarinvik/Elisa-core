package easm

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/smt"
)

// docs/104 rigor rung 3: symbolic equivalence proves reference ≡ target for ALL inputs via SMT,
// strictly stronger than the fuzz oracle's sampled agreement.

func requireZ3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; symbolic equivalence requires an SMT solver")
	}
}

// Two textually-different but semantically-identical bodies: reference computes a+b via mov+add;
// target computes b+a (addition commutes). The fuzz oracle would sample; SMT proves it for all inputs.
func TestSymbolicProvesCommutativeEquivalence(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def add_pair(a: u64, b: u64) -> u64 abi c:
    inputs: a = rdi, b = rsi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        addq %rsi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rsi, %rax
        addq %rdi, %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("expected the body to be modelable, got skip: %#v", issues)
	}
	if len(issues) != 0 {
		t.Fatalf("expected symbolic equivalence to PROVE a+b == b+a, got %#v", issues)
	}
}

// The real emulator AeroLib stub: xorq %rax,%rax on both sides. Proven equivalent (rax == 0 always).
func TestSymbolicProvesRealAeroLibStub(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module guest_exec_x86_64
target x86_64
export def ElisaGuestExec_AeroLibFallbackUnknownStubAsm() -> u64 abi c:
    outputs: ret = rax
    clobbers: cc
    stack: unchanged
    control: returns
    reference:
        xorq %rax, %rax
        ret
    target x86_64 lockstep reference:
        xorq %rax, %rax
        ret
`
	_, issues := Parse("guest_exec_x86_64.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected real stub to be proven equivalent, got %#v", issues)
	}
}

// A genuinely-divergent target (adds an extra increment) must be DISPROVED, not passed.
func TestSymbolicDisprovesDivergentTarget(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def add_pair(a: u64, b: u64) -> u64 abi c:
    inputs: a = rdi, b = rsi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        addq %rsi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq %rsi, %rax
        incq %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if !containsIssue(issues, "lockstep-symbolic-divergence") {
		t.Fatalf("expected lockstep-symbolic-divergence, got %#v", issues)
	}
}

// A body outside the modelable subset (memory operand) is skipped, never silently passed.
func TestSymbolicSkipsUnmodelableBody(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def load_it(p: HostPtr[u64]) -> u64 abi c:
    inputs: p = rdi
    outputs: ret = rax
    clobbers: rax, memory
    stack: unchanged
    control: returns
    requires: relocation.symbol
    reference:
        movq (%rdi), %rax
        ret
    target x86_64 lockstep reference:
        movq (%rdi), %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if !containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("expected lockstep-symbolic-skip for a memory-operand body, got %#v", issues)
	}
}

// --- Direct-encoder tests --------------------------------------------------------------------
// Several newly-modeled mnemonics (orq/notq/negq/shlq/shrq/sarq, the q-suffixed lea spelling) are
// not yet in the base verifier's allowedOps, so a full Parse() of a body using them produces a
// base-verifier error and the symbolic checker (gated on a clean body) never runs. These tests
// exercise symbolicEncodeBody + z3 directly to prove the bit-precise encoding is sound. The
// pipeline-reachable forms (movl, lea, xchg) additionally get end-to-end Parse() tests below.

// encodeBodyOrFail symbolically encodes a slice of (op, "op operands...") instruction texts.
func encodeBodyOrFail(t *testing.T, lines [][2]string) *symbolicState {
	t.Helper()
	var insts []Instruction
	for _, l := range lines {
		insts = append(insts, Instruction{Op: l[0], Text: l[1]})
	}
	st, reason := symbolicEncodeBody(insts)
	if reason != "" {
		t.Fatalf("expected body to be modelable, got skip reason %q", reason)
	}
	return st
}

// symbolicEqual asks z3 whether the two states agree on outReg for all inputs. Returns true if proven
// equal (unsat on the disequality), false if a divergence witness exists.
func symbolicEqual(t *testing.T, a, b *symbolicState, outReg string) bool {
	t.Helper()
	solver, err := smt.Open(smt.Options{Binary: "z3", PerQueryTimeoutMillis: 5000})
	if err != nil {
		t.Skipf("no SMT solver: %v", err)
	}
	defer solver.Close()
	inputs := map[string]bool{}
	for r := range a.inputs {
		inputs[r] = true
	}
	for r := range b.inputs {
		inputs[r] = true
	}
	inputs[outReg] = true // identity-default may reference in_<out>
	var sb strings.Builder
	for r := range inputs {
		fmt.Fprintf(&sb, "(declare-const in_%s (_ BitVec 64))\n", r)
	}
	fmt.Fprintf(&sb, "(assert (not (= %s %s)))\n", outputTerm(a, outReg), outputTerm(b, outReg))
	res, _ := solver.Check(sb.String())
	if res != smt.Unsat && res != smt.Sat {
		t.Fatalf("solver returned non-decisive result %v", res)
	}
	return res == smt.Unsat
}

// leaq 16(%rdi) == movq %rdi,%rax; addq $16,%rax. Off-by-one displacement diverges.
func TestSymbolicLeaDisplacementEncoding(t *testing.T) {
	requireZ3(t)
	ref := encodeBodyOrFail(t, [][2]string{{"leaq", "leaq 16(%rdi), %rax"}})
	good := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}, {"addq", "addq $16, %rax"}})
	if !symbolicEqual(t, ref, good, "rax") {
		t.Fatalf("leaq 16(%%rdi) must equal mov+add $16")
	}
	bad := encodeBodyOrFail(t, [][2]string{{"leaq", "leaq 17(%rdi), %rax"}})
	if symbolicEqual(t, ref, bad, "rax") {
		t.Fatalf("off-by-one displacement must diverge")
	}
	// lea (%reg) (disp 0) is a plain copy.
	zero := encodeBodyOrFail(t, [][2]string{{"leaq", "leaq (%rdi), %rax"}})
	copyq := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}})
	if !symbolicEqual(t, zero, copyq, "rax") {
		t.Fatalf("leaq (%%rdi) must be a plain copy")
	}
}

// An index/scale lea form stays outside the modeled subset (skip, never a false proof).
func TestSymbolicLeaIndexScaleSkips(t *testing.T) {
	_, reason := symbolicEncodeBody([]Instruction{{Op: "leaq", Text: "leaq (%rdi,%rsi,1), %rax"}})
	if reason == "" {
		t.Fatalf("expected index/scale lea to be skipped, was modeled")
	}
}

// De Morgan: (a | b) == ~(~a & ~b), proven for all inputs.
func TestSymbolicOrNotEncoding(t *testing.T) {
	requireZ3(t)
	ref := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}, {"orq", "orq %rsi, %rax"}})
	dem := encodeBodyOrFail(t, [][2]string{
		{"notq", "notq %rdi"}, {"notq", "notq %rsi"},
		{"movq", "movq %rdi, %rax"}, {"andq", "andq %rsi, %rax"}, {"notq", "notq %rax"},
	})
	if !symbolicEqual(t, ref, dem, "rax") {
		t.Fatalf("or must equal ~(~a & ~b)")
	}
}

// negq %x == 0 - x.
func TestSymbolicNegEncoding(t *testing.T) {
	requireZ3(t)
	ref := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}, {"negq", "negq %rax"}})
	sub := encodeBodyOrFail(t, [][2]string{{"xorq", "xorq %rax, %rax"}, {"subq", "subq %rdi, %rax"}})
	if !symbolicEqual(t, ref, sub, "rax") {
		t.Fatalf("negq must equal 0 - x")
	}
}

// shlq $3 == doubling thrice; shrq (logical) != sarq (arithmetic) on the sign bit.
func TestSymbolicShiftEncoding(t *testing.T) {
	requireZ3(t)
	shl := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}, {"shlq", "shlq $3, %rax"}})
	dbl := encodeBodyOrFail(t, [][2]string{
		{"movq", "movq %rdi, %rax"}, {"addq", "addq %rax, %rax"},
		{"addq", "addq %rax, %rax"}, {"addq", "addq %rax, %rax"},
	})
	if !symbolicEqual(t, shl, dbl, "rax") {
		t.Fatalf("shlq $3 must equal three doublings")
	}
	shr := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}, {"shrq", "shrq $1, %rax"}})
	sar := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}, {"sarq", "sarq $1, %rax"}})
	if symbolicEqual(t, shr, sar, "rax") {
		t.Fatalf("logical shrq must differ from arithmetic sarq")
	}
}

// Variable (%cl) shift count stays a skip.
func TestSymbolicVariableShiftSkips(t *testing.T) {
	_, reason := symbolicEncodeBody([]Instruction{{Op: "shlq", Text: "shlq %cl, %rax"}})
	if reason == "" {
		t.Fatalf("expected variable %%cl shift count to be skipped")
	}
}

// --- Pipeline (full Parse) tests for base-verifier-supported mnemonics ------------------------

// xchgq swaps two registers; a single swap puts rsi into rax, a double swap is identity. Encoder-level
// (the base verifier doesn't track xchg reads, so a pipeline fixture trips input-register-unused).
func TestSymbolicXchgEncoding(t *testing.T) {
	requireZ3(t)
	single := encodeBodyOrFail(t, [][2]string{
		{"xchgq", "xchgq %rdi, %rsi"}, {"movq", "movq %rdi, %rax"},
	})
	fromRsi := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rsi, %rax"}})
	if !symbolicEqual(t, single, fromRsi, "rax") {
		t.Fatalf("one xchg then mov %%rdi must equal mov %%rsi")
	}
	double := encodeBodyOrFail(t, [][2]string{
		{"xchgq", "xchgq %rdi, %rsi"}, {"xchgq", "xchgq %rdi, %rsi"}, {"movq", "movq %rdi, %rax"},
	})
	fromRdi := encodeBodyOrFail(t, [][2]string{{"movq", "movq %rdi, %rax"}})
	if !symbolicEqual(t, double, fromRdi, "rax") {
		t.Fatalf("double xchg must be identity")
	}
}

// leaq disp(%base) via the base-verifier-supported `lea` spelling (requires memory.base.untyped for
// the raw param base): lea 16(%rdi) == mov+add $16.
func TestSymbolicProvesLeaDisplacementPipeline(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def addc(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, cc
    requires: memory.base.untyped
    stack: unchanged
    control: returns
    reference:
        lea 16(%rdi), %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq $16, %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected lea 16(%%rdi) == mov+add $16, got %#v", issues)
	}
}

// movl zero-extends to 64 bits: movl $-1,%eax leaves rax = 0x00000000FFFFFFFF, proven == that const.
func TestSymbolicProvesMovlZeroExtend(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def f() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movl $-1, %eax
        ret
    target x86_64 lockstep reference:
        movq $4294967295, %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected movl $-1 zero-extend == 0xFFFFFFFF, got %#v", issues)
	}
}

// movl is NOT a 64-bit move: movl $-1,%eax (0xFFFFFFFF) differs from movq $-1,%rax (all ones).
func TestSymbolicDisprovesMovlVsMovq(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def f() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movl $-1, %eax
        ret
    target x86_64 lockstep reference:
        movq $-1, %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if !containsIssue(issues, "lockstep-symbolic-divergence") {
		t.Fatalf("expected movl != movq divergence (zero-extend), got %#v", issues)
	}
}

// Off by default: with the env unset, no symbolic issues appear even on a divergent pair.
func TestSymbolicOffByDefault(t *testing.T) {
	src := `module sym
target x86_64
export def add_pair(a: u64, b: u64) -> u64 abi c:
    inputs: a = rdi, b = rsi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        addq %rsi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq %rsi, %rax
        incq %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if containsIssue(issues, "lockstep-symbolic-divergence") || containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("symbolic checks must be off by default, got %#v", issues)
	}
}

// --- Full-pipeline reachability (allowedOps + opRules widened) ----------------------------------
// These prove the newly-allowed mnemonics (leaq/orq/notq/negq/shifts) pass the BASE verifier and
// reach the symbolic checker end-to-end via Parse() -- not just the direct-encoder tests.

// A plain routine using the new opcodes verifies clean (no unsupported-instruction).
func TestPipelineAcceptsNewOpcodes(t *testing.T) {
	src := `module newops
target x86_64
export def churn(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    body:
        leaq 8(%rdi), %rax
        shlq $2, %rax
        orq %rdi, %rax
        notq %rax
        negq %rax
        ret
`
	_, issues := Parse("newops.easm", src)
	if len(issues) != 0 {
		t.Fatalf("new opcodes must verify clean through the base pipeline, got %#v", issues)
	}
}

// leaq 8(%rdi) proven equivalent to mov+add $8 FOR ALL INPUTS, end-to-end through Parse().
func TestPipelineSymbolicProvesLeaq(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module newops
target x86_64
export def addeight(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        leaq 8(%rdi), %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq $8, %rax
        ret
`
	_, issues := Parse("newops.easm", src)
	if containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("leaq body must now be modelable end-to-end, got skip: %#v", issues)
	}
	if len(issues) != 0 {
		t.Fatalf("expected leaq == mov+add proven, got %#v", issues)
	}
}

// shlq $1 proven equivalent to add-self, end-to-end.
func TestPipelineSymbolicProvesShift(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module newops
target x86_64
export def dbl(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        shlq $1, %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq %rax, %rax
        ret
`
	_, issues := Parse("newops.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected shlq $1 == add-self proven end-to-end, got %#v", issues)
	}
}
