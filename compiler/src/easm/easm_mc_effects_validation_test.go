package easm

import (
	"strings"
	"testing"
)

func TestEASMEffectsMatchLLVMMC(t *testing.T) {
	llvmConfig := findLLVMTool("llvm-config")
	clangxx := findLLVMTool("clang++")
	llvmMC := findLLVMTool("llvm-mc")
	if llvmConfig == "" || clangxx == "" || llvmMC == "" {
		t.Skip("LLVM developer tools (llvm-config/clang++/llvm-mc) not found; skipping MC ground-truth cross-check")
	}

	dumpBin := buildMCDump(t, llvmConfig, clangxx)
	mcDefs := runMCDump(t, dumpBin)

	checked := 0
	mcConfirmedFlags := map[string]bool{}
	cpuidUsesObserved := false
	for op := range allowedOps {
		witness, ok := easmOpWitness[op]
		if !ok {
			continue // non-x86 op (aarch64), nothing to cross-check against the x86 MC tables
		}
		opcodeName, err := opcodeNameFor(llvmMC, witness)
		if err != nil {
			t.Logf("skip %-8s: llvm-mc could not assemble witness %q: %v", op, witness, err)
			continue
		}
		eff, ok := mcDefs[opcodeName]
		if !ok {
			t.Logf("skip %-8s: opcode %s absent from MC dump", op, opcodeName)
			continue
		}
		checked++

		// --- Flags ---------------------------------------------------------------
		mcFlags := false
		var mcFlagNames []string
		for _, d := range eff.defs {
			if isX86FlagRegister(d) {
				mcFlags = true
				mcFlagNames = append(mcFlagNames, d)
			}
		}
		if mcFlags {
			mcConfirmedFlags[op] = true
		}
		switch {
		case mcFlags && !instructionClobbersFlags(op):
			t.Errorf("UNDER-DECLARED FLAGS: MC says %s (%s) implicitly writes %v, "+
				"but instructionClobbersFlags(%q)=false. The validator would let the "+
				"compiler keep a flags-dependent value live across this instruction.",
				op, opcodeName, mcFlagNames, op)
		case !mcFlags && instructionClobbersFlags(op):
			t.Logf("note: %-8s (%s) declared flag-clobbering but MC reports no flag def; "+
				"conservative, not a bug", op, opcodeName)
		}

		// --- FPU/SSE control state ----------------------------------------------
		// An instruction that touches the x87/SSE control-status registers does not need a
		// clobber (LLVM keeps no values there), but EASM must gate it behind an explicit
		// capability so a function cannot silently perturb FP rounding/exception state.
		for _, d := range eff.defs {
			if isX86FPUControlRegister(d) && opCapability(op) == "" {
				t.Errorf("UNGATED FPU STATE: MC says %s (%s) writes %s, but opCapability(%q) "+
					"is empty. Touching FP control/status state must require a capability.",
					op, opcodeName, d, op)
				break
			}
		}

		// --- Unmodeled side effects ---------------------------------------------
		// If MC flags an instruction as having ambient side effects (it perturbs state
		// beyond its operands and defs), EASM must account for that intent through exactly
		// one of its mechanisms: a required capability, a declared flags clobber (cld/std
		// touch DF), or structural control-flow modeling (ret). Anything that slips through
		// would let a future whitelisted op smuggle in an undeclared ambient effect.
		if eff.side && opCapability(op) == "" && !instructionClobbersFlags(op) && !isControlFlowOp(op) {
			t.Errorf("UNGATED SIDE EFFECT: MC marks %s (%s) as having unmodeled side effects, "+
				"but it has no capability requirement, no flags clobber, and is not control flow. "+
				"The op can perturb ambient processor state with nothing declaring that intent.",
				op, opcodeName)
		}

		// --- Implicit GPR clobbers ----------------------------------------------
		// Only registers the validator CANNOT see in the source text count as implicit:
		// an explicit %-operand (even one a particular encoding lists as implicit, e.g. the
		// XCHG accumulator form) is handled by ordinary operand analysis. `call` clobbers
		// the System V caller-saved set by ABI convention, broader than any single
		// instruction's MC effect, so its direction is checked separately below.
		explicit := witnessRegisters(witness)
		declared := map[string]bool{}
		for _, r := range implicitClobbers(op) {
			declared[canonicalX86GPR(r)] = true
		}
		for _, d := range eff.defs {
			reg := canonicalX86GPR(strings.ToLower(d))
			if !x86GPRSet[reg] {
				continue // flags, RSP, RIP, segment/MMX/XMM, etc. -- not a GPR clobber
			}
			if explicit[reg] {
				continue // visible in the source text -> handled as an operand, not implicit
			}
			if op == "call" || op == "callq" {
				continue // ABI-modeled; handled below
			}
			if !declared[reg] {
				t.Errorf("UNDER-DECLARED CLOBBER: MC says %s (%s) implicitly writes %s, "+
					"but implicitClobbers(%q)=%v omits it. The validator would treat %s as "+
					"preserved across this instruction.",
					op, opcodeName, reg, op, implicitClobbers(op), reg)
			}
		}

		if op == "call" || op == "callq" {
			// Sanity: the special-cased ABI table must be populated and include the
			// condition codes a call can clobber via the callee.
			if !declared["cc"] || !declared["rax"] {
				t.Errorf("%s: implicitClobbers is expected to declare the caller-saved ABI "+
					"set including cc and rax, got %v", op, implicitClobbers(op))
			}
		}

		// --- Implicit GPR uses (reads) ------------------------------------------
		// A register an instruction reads implicitly -- not as an operand -- must be in the
		// validator's implicitUses table so it can require the register initialized. cpuid
		// reading eax/ecx is the case in the allowed set; missing it would let a function
		// silently consume an indeterminate caller-left value.
		declaredUses := map[string]bool{}
		for _, r := range implicitUses(op) {
			declaredUses[canonicalX86GPR(r)] = true
		}
		for _, u := range eff.uses {
			reg := canonicalX86GPR(strings.ToLower(u))
			if !x86GPRSet[reg] {
				continue // flags, rsp, rip, vector/segment -- not a GPR read we model
			}
			if explicit[reg] {
				continue // visible in the source text -> an operand read, not implicit
			}
			if op == "cpuid" && (reg == "rax" || reg == "rcx") {
				cpuidUsesObserved = true
			}
			if !declaredUses[reg] {
				t.Errorf("UNDER-DECLARED IMPLICIT READ: MC says %s (%s) implicitly reads %s, "+
					"but implicitUses(%q)=%v omits it; the validator cannot require it initialized.",
					op, opcodeName, reg, op, implicitUses(op))
			}
		}
	}

	if checked == 0 {
		t.Fatal("cross-checked zero opcodes; witness/llvm-mc wiring is broken")
	}
	// Liveness: prove the MC pipeline actually observes EFLAGS, so a future silent
	// degradation of the probe (defs parsed but flags lost) fails loudly here instead of
	// turning every flags assertion into a vacuous pass. These ops indisputably set flags.
	for _, op := range []string{"add", "sub", "cmp", "test"} {
		if !mcConfirmedFlags[op] {
			t.Fatalf("MC pipeline liveness failed: %q was not observed writing flags via MC; "+
				"the probe or llvm-mc wiring is broken and the flags cross-check is vacuous", op)
		}
	}
	if !cpuidUsesObserved {
		t.Fatal("MC pipeline liveness failed: cpuid's implicit eax/ecx reads were not observed; " +
			"the implicit-uses probe column is broken and the read cross-check is vacuous")
	}
	// --- Memory effects: ground writesMemory/hasMemoryOperand in MC -------------
	// These text predicates decide whether the validator demands a `memory` clobber. If
	// they under-detect a store, that demand never fires and LLVM is free to reorder or
	// cache loads across the asm. Cross-check them against MC's mayLoad/mayStore for
	// explicit-memory instruction forms. Stack-implicit ops (push/pop/call) are modeled by
	// the stack analysis instead and are intentionally not in this list. `leaq` is included
	// precisely because it has a parenthesized operand yet accesses no memory -- the case a
	// naive "has parens => touches memory" check would get wrong.
	memWitnesses := []string{
		"movq %rax, (%rbx)",
		"movq (%rax), %rbx",
		"movl %eax, (%rbx)",
		"movb %al, (%rbx)",
		"addq %rax, (%rbx)",
		"addq (%rax), %rbx",
		"subq %rax, (%rbx)",
		"andq %rax, (%rbx)",
		"xorq %rax, (%rbx)",
		"xchgq %rax, (%rbx)",
		"cmpq (%rax), %rbx",
		"incq (%rax)",
		"decq (%rax)",
		"pushq (%rax)",
		"popq (%rax)",
		"callq *(%rax)",
		"jmp *(%rax)",
		"fldcw (%rax)",
		"ldmxcsr (%rax)",
		"fnstcw (%rax)",
		"stmxcsr (%rax)",
		"movq %rax, %rbx",   // control: register-only, no memory access
		"leaq (%rax), %rbx", // control: address computation, NOT a memory access
	}
	memChecked := 0
	mcConfirmedStore := false
	mcConfirmedLoad := false
	for _, text := range memWitnesses {
		opcodeName, err := opcodeNameFor(llvmMC, text)
		if err != nil {
			t.Logf("skip mem witness %q: %v", text, err)
			continue
		}
		eff, ok := mcDefs[opcodeName]
		if !ok {
			t.Logf("skip mem witness %q: opcode %s absent from MC dump", text, opcodeName)
			continue
		}
		memChecked++
		if eff.mayStore {
			mcConfirmedStore = true
		}
		if eff.mayLoad {
			mcConfirmedLoad = true
		}
		if eff.mayStore && !mcMayStoreCanBeMachineStateOnly(opcodeName) && !mcMayStoreCanBeImplicitStackOnly(text) && !writesMemory(text) {
			t.Errorf("UNDER-DETECTED STORE: MC says %q (%s) writes memory (mayStore), but "+
				"writesMemory=false, so the validator would not require a `memory` clobber.",
				text, opcodeName)
		}
		if eff.mayLoad && !mcMayLoadCanBeImplicitStackOnly(text) && !readsMemory(text) {
			t.Errorf("UNDER-DETECTED LOAD: MC says %q (%s) reads memory (mayLoad), but "+
				"readsMemory=false, so the validator would not require a `memory.read` clobber.",
				text, opcodeName)
		}
		if (eff.mayLoad || eff.mayStore) && !hasMemoryOperand(text) {
			t.Errorf("UNDER-DETECTED MEMORY: MC says %q (%s) accesses memory (load=%v store=%v), "+
				"but hasMemoryOperand=false.", text, opcodeName, eff.mayLoad, eff.mayStore)
		}
		if writesMemory(text) && !eff.mayStore {
			t.Logf("note: writesMemory(%q)=true but MC reports no store for %s; conservative", text, opcodeName)
		}
	}
	if memChecked == 0 {
		t.Fatal("cross-checked zero memory witnesses; llvm-mc wiring is broken")
	}
	if !mcConfirmedStore {
		t.Fatal("MC pipeline liveness failed: no memory witness observed storing; the memory cross-check is vacuous")
	}
	if !mcConfirmedLoad {
		t.Fatal("MC pipeline liveness failed: no memory witness observed loading; the load cross-check is vacuous")
	}

	t.Logf("cross-checked %d x86 EASM ops and %d memory forms against LLVM MC ground truth", checked, memChecked)
}

func mcMayStoreCanBeMachineStateOnly(opcodeName string) bool {
	switch strings.ToUpper(strings.TrimSpace(opcodeName)) {
	case "LDMXCSR":
		return true
	}
	return false
}

func mcMayStoreCanBeImplicitStackOnly(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	switch normalizeOp(fields[0]) {
	case "push", "pushq", "call", "callq":
		return true
	}
	return false
}

func mcMayLoadCanBeImplicitStackOnly(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	switch normalizeOp(fields[0]) {
	case "pop", "popq", "ret":
		return true
	}
	return false
}
