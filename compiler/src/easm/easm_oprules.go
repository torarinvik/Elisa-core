package easm

// Explicit transition relation (docs/104, TAL increment 2).
//
// EASM's per-instruction effects were historically encoded as a scatter of predicate functions
// (capabilityByOp, instructionClobbersFlags, implicitClobbers, implicitUses, implicitResultDefines).
// That is operationally a typing relation `Γ ⊢ instr ⇒ Γ'`, but with the rules spread across the
// file there was no single place to audit it for *totality* — "every allowed opcode has a declared
// rule, and no instruction mutates machine state ad-hoc."
//
// opRules is that single declared relation: one row per opcode describing its abstract effect
// signature. Two theorems anchor it (see easm_oprules_test.go):
//
//   - Totality: every entry in allowedOps has a row here, and the verifier emits opcode-rule-missing
//     if an allowed op is ever reached without one. No silent "unknown instruction effect".
//   - Consistency: the row agrees field-for-field with the legacy predicate functions, which in turn
//     are cross-checked against LLVM MC (easm_mc_effects_test.go). So the declared relation is not a
//     parallel truth that can drift — it is provably the same relation the walker enforces.
//
// This makes the effect signature a declared artifact rather than an emergent property of a 300-line
// switch, which is the precondition for the stronger increment-2 work (per-opcode state-transition
// rules + dataflow joins) to rest on a stable, auditable base.

type opRule struct {
	// Capability required to use the opcode at all (matches capabilityByOp); "" if none.
	Capability string
	// ClobbersFlags reports that the opcode writes the condition codes (matches instructionClobbersFlags).
	ClobbersFlags bool
	// ImplicitReads are registers read without appearing as an operand (matches implicitUses), canonical 64-bit.
	ImplicitReads []string
	// ImplicitClobbers are registers written/trashed without appearing as a destination operand
	// (matches implicitClobbers), canonical 64-bit; "cc" denotes flags.
	ImplicitClobbers []string
	// ResultDefines reports that ImplicitClobbers are defined results (cpuid/rdtsc) rather than
	// trashed-to-indeterminate (a call's caller-saved set). Matches implicitResultDefines.
	ResultDefines bool
}

// opRules is the declared transition relation. Every opcode in allowedOps MUST appear here; the
// totality test and the runtime opcode-rule-missing guard enforce that.
var opRules = map[string]opRule{
	// Data movement — no flags, no implicit registers, no capability.
	"mov": {}, "movq": {}, "lea": {}, "movb": {}, "movw": {}, "movl": {},
	"push": {}, "pushq": {}, "pop": {}, "popq": {},
	"movsx": {}, "movsxd": {}, "movsbw": {}, "movsbl": {}, "movsbq": {}, "movswl": {}, "movswq": {}, "movslq": {},
	"movzx": {}, "movzbw": {}, "movzbl": {}, "movzbq": {}, "movzwl": {}, "movzwq": {},

	// Atomic exchange — RMW capability, no flags.
	"xchg":  {Capability: "x86_64.atomic.rmw"},
	"xchgl": {Capability: "x86_64.atomic.rmw"},
	"xchgq": {Capability: "x86_64.atomic.rmw"},

	// ALU — write flags.
	"add": {ClobbersFlags: true}, "addq": {ClobbersFlags: true},
	"sub": {ClobbersFlags: true}, "subq": {ClobbersFlags: true},
	"and": {ClobbersFlags: true}, "andq": {ClobbersFlags: true},
	"xor": {ClobbersFlags: true}, "xorq": {ClobbersFlags: true},
	"inc": {ClobbersFlags: true}, "incq": {ClobbersFlags: true},
	"dec": {ClobbersFlags: true}, "decq": {ClobbersFlags: true},
	"cmp": {ClobbersFlags: true}, "cmpq": {ClobbersFlags: true},
	"test": {ClobbersFlags: true}, "testq": {ClobbersFlags: true},

	// Direction-flag control — write flags (DF), no operands.
	"cld": {ClobbersFlags: true}, "std": {ClobbersFlags: true},

	// Control transfer — no implicit GPR effects in the relation itself (call's caller-saved
	// trashing is modeled separately in the walker, see implicitClobbers/clobberedByCall).
	"call": {ImplicitClobbers: []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "cc"}},
	"callq": {ImplicitClobbers: []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "cc"}},
	"jmp": {}, "jmpq": {}, "ret": {}, "retq": {},

	// Serializing / timing / fences.
	"cpuid":  {Capability: "x86_64.cpuid", ImplicitReads: []string{"rax", "rcx"}, ImplicitClobbers: []string{"rax", "rbx", "rcx", "rdx"}, ResultDefines: true},
	"rdtsc":  {Capability: "x86_64.rdtsc", ImplicitClobbers: []string{"rax", "rdx"}, ResultDefines: true},
	"lfence": {Capability: "x86_64.sse.lfence"},
	"pause":  {Capability: "x86_64.sse.pause"},
	"yield":  {Capability: "aarch64.yield"},
	"mrs":    {Capability: "aarch64.cntvct"},
	"isb":    {Capability: "aarch64.cntvct"},

	// FPU/SIMD control-word state.
	"fldcw":  {Capability: "x86_64.fpu_control"},
	"fnstcw": {Capability: "x86_64.fpu_control"},
	"stmxcsr": {Capability: "x86_64.fpu_control"},
	"ldmxcsr": {Capability: "x86_64.fpu_control"},
	"emms":    {Capability: "x86_64.fpu_control"},
	"vzeroall": {Capability: "x86_64.simd_state"},

	// Debug trap.
	"trap": {Capability: "debug.trap"},
}

// lookupOpRule returns the declared rule for an opcode and whether one exists. Conditional jumps
// (matched by family, not membership) are flag-reading control transfers with no GPR effects and are
// intentionally not table rows; callers gate on isConditionalJump before consulting the table.
func lookupOpRule(op string) (opRule, bool) {
	r, ok := opRules[op]
	return r, ok
}
