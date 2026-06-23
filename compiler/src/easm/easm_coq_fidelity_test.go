package easm

// Go<->Coq fidelity test.
//
// The Coq development formal/easm-tal/EasmTAL.v proves progress/preservation
// for a MODEL of the definite-initialization relation. Those proofs are only
// as valuable as the fidelity of that model to the actual Go verifier. The
// README correspondence table (formal/easm-tal/README.md) asserts that
// correspondence by hand; this test makes it a continuously-checked invariant.
//
// Strategy: re-implement the EXACT Coq relation in Go (op_ok / has_type /
// adefine / seq_type, transcribed line-for-line from EasmTAL.v over the same
// mov/add/sub/and/xor/inc/dec subset), fuzz straight-line bodies over that
// subset, run them through the real verifier (Parse), and assert:
//
//   (1) the Coq seq_type relation gets STUCK (a read of an undefined register,
//       no typing rule applies)  <=>  the Go verifier emits
//       register-read-uninitialized.  [the progress correspondence]
//
//   (2) when the Coq relation accepts, the final Coq abstract state (which
//       registers are Defined) equals the verifier's final LiveRegs over the
//       modeled register set.  [the preservation / state correspondence]
//
// If the Go walk and the Coq model ever drift on this subset, this test fails,
// turning the hand-maintained correspondence into a tested one.
//
// FLAGS SLOT (EasmTAL.v `aflags`, docs/106): the Coq model now also carries a
// flags-defined lattice slot — ALU ops (add/sub/and/xor/inc/dec) DEFINE it,
// mov PRESERVES it — mirroring opRules.ClobbersFlags. We mirror that slot here
// (coqState.flags / coqHasType) and assert the model's own flags invariant
// (ALU defines, mov preserves) holds on every fuzzed body, so the Go mirror
// stays a faithful transcription of the EXTENDED relation. We do NOT cross-
// check flag definedness against the real verifier: machineFactState exposes
// no flags-defined fact (it only enforces, separately, that ClobbersFlags ops
// list `cc` in their clobbers — the cc-clobber-missing check, easm.go), and
// the modeled subset has no flag-READING op. So flags definedness is not an
// observable verifier state to compare against; asserting equality would be
// asserting something the verifier does not expose. The register-definedness
// correspondence below remains the cross-checked invariant.

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const easmCoqFidelityCases = 6000

// The Coq model (EasmTAL.v) has a fixed 8-register set (RAX..R9) with decidable
// equality. The relation is parametric in register identity, so we realize the
// 8 abstract registers as 8 distinct caller-saved x86 GPRs (no callee-saved
// preservation entanglement). Index i <-> Coq reg i <-> coqFidelityRegs[i].
var coqFidelityRegs = []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10"}

// coqOperand mirrors EasmTAL.v `operand` (OReg reg | OImm nat).
type coqOperand struct {
	isImm bool
	reg   int // valid when !isImm; index into coqFidelityRegs
	imm   int // valid when isImm
}

// coqInstr mirrors EasmTAL.v `instr`. kind is the mnemonic family; for the RMW
// one-operand forms (inc/dec) src is unused.
type coqInstr struct {
	kind string // mov|add|sub|and|xor|inc|dec
	dst  int
	src  coqOperand
}

// coqOpOk mirrors `op_ok (g:absstate) (o:operand) : bool` (EasmTAL.v:131-135):
// an immediate is always readable; a register is readable iff Defined in g.
func coqOpOk(g [8]bool, o coqOperand) bool {
	if o.isImm {
		return true
	}
	return g[o.reg]
}

// coqState mirrors the EXTENDED EasmTAL.v `absstate` record: per-register
// definedness (regs, == aregs) plus the EFLAGS-defined slot (flags == aflags).
type coqState struct {
	regs  [8]bool
	flags bool
}

// coqAdefine mirrors `adefine g r = aset g r true` (EasmTAL.v): defining a
// register sets it Defined, leaving the rest — including the flags slot —
// untouched (aset_flags / adefine_flags).
func coqAdefine(g coqState, r int) coqState {
	g.regs[r] = true
	return g
}

// coqAdefineCC mirrors `adefine_cc g r = asetflags (adefine g r) true`
// (EasmTAL.v): an ALU op defines BOTH the destination register AND the flags
// slot — the abstract image of opRules.ClobbersFlags=true.
func coqAdefineCC(g coqState, r int) coqState {
	g.regs[r] = true
	g.flags = true
	return g
}

// coqHasType mirrors `has_type : absstate -> instr -> absstate -> Prop`
// (EasmTAL.v). ok=false means NO rule applies = ill-typed = stuck (the abstract
// counterpart of a stuck uninitialized read). On ok=true the returned state is
// g' such that has_type g i g'. mov uses adefine (flags preserved); the ALU ops
// use adefine_cc (flags defined), per ClobbersFlags.
func coqHasType(g coqState, i coqInstr) (gPrime coqState, ok bool) {
	switch i.kind {
	case "mov": // T_mov: op_ok g s = true -> g' = adefine g d   (no ClobbersFlags)
		if !coqOpOk(g.regs, i.src) {
			return g, false
		}
		return coqAdefine(g, i.dst), true
	case "add", "sub", "and", "xor": // T_{add,sub,and,xor}: g d = true /\ op_ok g s = true; g' = adefine_cc g d
		if !g.regs[i.dst] || !coqOpOk(g.regs, i.src) {
			return g, false
		}
		return coqAdefineCC(g, i.dst), true
	case "inc", "dec": // T_{inc,dec}: g d = true; g' = adefine_cc g d
		if !g.regs[i.dst] {
			return g, false
		}
		return coqAdefineCC(g, i.dst), true
	}
	return g, false
}

// coqSeqType mirrors `seq_type` (EasmTAL.v): fold has_type along the list,
// stopping (stuck) at the first instruction with no applicable rule.
func coqSeqType(g coqState, insts []coqInstr) (gFinal coqState, ok bool) {
	for _, i := range insts {
		var step bool
		g, step = coqHasType(g, i)
		if !step {
			return g, false
		}
	}
	return g, true
}

func TestEASMGoCoqRelationFidelity(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))
	for c := 0; c < easmCoqFidelityCases; c++ {
		var g0 [8]bool
		insts := generateCoqFidelityBody(rng, &g0)

		// Coq entry state: declared-input regs Defined, flags Undefined (no
		// flag-writing op has run yet on entry — aempty's flags slot is false).
		coqFinal, coqOK := coqSeqType(coqState{regs: g0, flags: false}, insts)

		// Flags-slot invariant (the EXTENDED relation, not cross-checked against
		// the verifier since it exposes no flags fact — see file header): on an
		// accepted body, the final flags slot is Defined iff the LAST modeled
		// instruction clobbers flags (any ALU op), and Undefined only if the
		// body is empty or ends in a non-clobbering op (mov) with flags never
		// established earlier. We assert the local recomputation matches.
		if coqOK {
			wantFlags := false
			for _, in := range insts {
				switch in.kind {
				case "mov":
					// preserves: leaves wantFlags as-is
				default: // ALU: add/sub/and/xor/inc/dec define flags
					wantFlags = true
				}
			}
			if coqFinal.flags != wantFlags {
				t.Fatalf("case %d: Coq flags-slot mismatch: got %v want %v\nbody:\n%s",
					c, coqFinal.flags, wantFlags, wrapCoqFidelityBody(insts, g0))
			}
		}

		src := wrapCoqFidelityBody(insts, g0)

		var abstract machineFactState
		var sawState bool
		verifyFunctionStateHook = func(path string, fn *Function, state machineFactState) {
			if fn.Name == "coq_fidelity" {
				abstract = state
				sawState = true
			}
		}
		_, issues := Parse(fmt.Sprintf("coq_fidelity_%d.easm", c), src)
		verifyFunctionStateHook = nil

		hasUninit := containsIssue(issues, "register-read-uninitialized")

		// (1) progress correspondence: Coq-stuck <=> verifier flags an
		// uninitialized read.
		if !coqOK && !hasUninit {
			t.Fatalf("case %d: Coq relation is STUCK but verifier accepted the read\nbody:\n%s\nissues: %#v", c, src, issues)
		}
		if coqOK && hasUninit {
			t.Fatalf("case %d: Coq relation ACCEPTS but verifier flagged register-read-uninitialized\nbody:\n%s\nissues: %#v", c, src, issues)
		}

		// (2) preservation / state correspondence: on accept the final Coq
		// abstract definedness equals the verifier's LiveRegs over the modeled
		// register set. (Only meaningful when the verifier produced a state and
		// did not abort on the modeled subset.)
		if coqOK && sawState {
			for idx, name := range coqFidelityRegs {
				goDefined := abstract.LiveRegs[name]
				if goDefined != coqFinal.regs[idx] {
					t.Fatalf("case %d: definedness mismatch for %s: Coq=%v Go=%v\nbody:\n%s\nLiveRegs: %#v",
						c, name, coqFinal.regs[idx], goDefined, src, abstract.LiveRegs)
				}
			}
		}
	}
}

// generateCoqFidelityBody builds a random straight-line body over EXACTLY the
// Coq-modeled subset and records the declared-input definedness in g0. It
// deliberately avoids constructs the Coq model does not capture (so the
// correspondence stays exact rather than spuriously diverging):
//   - only mov/add/sub/and/xor/inc/dec, reg/imm operands;
//   - no self-zeroing idiom (xorq/subq %r,%r), which the verifier defines
//     WITHOUT reading the dst — a value-tracking optimization outside the
//     definite-init RMW rule the Coq model encodes.
func generateCoqFidelityBody(rng *rand.Rand, g0 *[8]bool) []coqInstr {
	for i := range g0 {
		// Declare ~half the registers as inputs (Defined on entry).
		if rng.Intn(2) == 0 {
			g0[i] = true
		}
	}
	ops := []string{"mov", "add", "sub", "and", "xor", "inc", "dec"}
	imms := []int{0, 1, 2, 7, 15, 16}
	n := 1 + rng.Intn(20)
	insts := make([]coqInstr, 0, n)
	for len(insts) < n {
		kind := ops[rng.Intn(len(ops))]
		dst := rng.Intn(8)
		switch kind {
		case "inc", "dec":
			insts = append(insts, coqInstr{kind: kind, dst: dst})
		case "mov":
			insts = append(insts, coqInstr{kind: kind, dst: dst, src: randCoqOperand(rng, imms, -1)})
		default: // add/sub/and/xor: forbid src==dst register (self-zero idiom)
			insts = append(insts, coqInstr{kind: kind, dst: dst, src: randCoqOperand(rng, imms, dst)})
		}
	}
	return insts
}

// randCoqOperand returns a random immediate or register operand. If
// forbidReg >= 0, a register operand equal to it is excluded (used to avoid the
// self-modifying src==dst forms for RMW ops).
func randCoqOperand(rng *rand.Rand, imms []int, forbidReg int) coqOperand {
	if rng.Intn(2) == 0 {
		return coqOperand{isImm: true, imm: imms[rng.Intn(len(imms))]}
	}
	r := rng.Intn(8)
	if forbidReg >= 0 {
		for r == forbidReg {
			r = rng.Intn(8)
		}
	}
	return coqOperand{reg: r}
}

func wrapCoqFidelityBody(insts []coqInstr, g0 [8]bool) string {
	var b strings.Builder
	b.WriteString("module coq_fidelity\n")
	b.WriteString("target x86_64\n")
	b.WriteString("export def coq_fidelity(")
	first := true
	for i, name := range coqFidelityRegs {
		if !g0[i] {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(&b, "%s_in: u64", name)
	}
	b.WriteString(") -> void abi c:\n")
	anyInput := false
	for i := range g0 {
		if g0[i] {
			anyInput = true
		}
	}
	if anyInput {
		b.WriteString("    inputs: ")
		first = true
		for i, name := range coqFidelityRegs {
			if !g0[i] {
				continue
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "%s_in = %s", name, name)
		}
		b.WriteByte('\n')
	}
	b.WriteString("    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, cc, memory\n")
	b.WriteString("    stack: synthetic\n")
	b.WriteString("    control: returns\n")
	b.WriteString("    requires: input.unused, x86_64.atomic.rmw\n")
	b.WriteString("    body:\n")
	for _, i := range insts {
		b.WriteString("        ")
		b.WriteString(formatCoqInstr(i))
		b.WriteByte('\n')
	}
	b.WriteString("        ret\n")
	return b.String()
}

func formatCoqInstr(i coqInstr) string {
	dst := "%" + coqFidelityRegs[i.dst]
	switch i.kind {
	case "inc":
		return "incq " + dst
	case "dec":
		return "decq " + dst
	case "mov":
		return "movq " + formatCoqOperand(i.src) + ", " + dst
	default:
		return i.kind + "q " + formatCoqOperand(i.src) + ", " + dst
	}
}

func formatCoqOperand(o coqOperand) string {
	if o.isImm {
		return fmt.Sprintf("$%d", o.imm)
	}
	return "%" + coqFidelityRegs[o.reg]
}
