package easm

// This test proves the EASM validator's hand-maintained instruction-effect tables
// (instructionClobbersFlags and implicitClobbers) never UNDER-declare the real effects
// of an instruction, by cross-checking them against LLVM's MC layer -- the same
// ground truth the assembler and code generator use.
//
// Why this matters: the validator's safety proof rests on knowing which registers and
// flags each instruction writes. If a table says "addq does not touch flags" while the
// hardware (and LLVM) say it does, the validator would happily let the surrounding code
// keep a flags-dependent value live across the asm, producing a silent miscompile that
// no amount of EASM-level review would catch. A hand table can drift; LLVM's MCInstrDesc
// cannot, because it is generated from the same target description that assembles the
// instruction. This test makes the two agree or fails the build.
//
// The check runs only where the LLVM developer tools are present (llvm-config, clang++,
// llvm-mc). Elsewhere it skips, so it never breaks a machine that only has the runtime
// LLVM library. The C++ probe touches nothing but MCInstrInfo/MCRegisterInfo -- no asm
// parser, no streamer, no module state -- so it is about as small and robust as an LLVM
// MC client can be.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mcDumpSource enumerates every x86-64 opcode and prints its implicit register defs plus
// mayLoad/mayStore. Implicit defs are exactly the registers and flags an instruction
// writes WITHOUT them appearing as explicit operands -- EFLAGS for arithmetic, RAX/RDX
// for rdtsc, the caller-clobbered scratch for some intrinsics, and so on. That is the
// precise notion EASM's implicitClobbers/instructionClobbersFlags are trying to capture.
const mcDumpSource = `
#include "llvm/MC/MCInstrInfo.h"
#include "llvm/MC/MCInstrDesc.h"
#include "llvm/MC/MCRegisterInfo.h"
#include "llvm/MC/TargetRegistry.h"
#include "llvm/Support/TargetSelect.h"
#include <cstdio>
#include <memory>
#include <string>

using namespace llvm;

int main() {
  LLVMInitializeX86TargetInfo();
  LLVMInitializeX86TargetMC();
  std::string triple = "x86_64-apple-darwin";
  std::string err;
  const Target *T = TargetRegistry::lookupTarget(triple, err);
  if (!T) { fprintf(stderr, "lookupTarget failed: %s\n", err.c_str()); return 1; }
  std::unique_ptr<MCInstrInfo> MII(T->createMCInstrInfo());
  std::unique_ptr<MCRegisterInfo> MRI(T->createMCRegInfo(triple));
  if (!MII || !MRI) { fprintf(stderr, "create MII/MRI failed\n"); return 1; }
  for (unsigned op = 0, e = MII->getNumOpcodes(); op < e; ++op) {
    const MCInstrDesc &D = MII->get(op);
    printf("%s\tuses:", MII->getName(op).str().c_str());
    { bool f = true; for (MCPhysReg r : D.implicit_uses()) { printf("%s%s", f ? "" : ",", MRI->getName(r)); f = false; } }
    printf("\tdefs:");
    { bool f = true; for (MCPhysReg r : D.implicit_defs()) { printf("%s%s", f ? "" : ",", MRI->getName(r)); f = false; } }
    printf("\tmayLoad:%d\tmayStore:%d\tside:%d\n", D.mayLoad(), D.mayStore(), D.hasUnmodeledSideEffects());
  }
  return 0;
}
`

// easmOpWitness gives one concrete AT&T instruction per x86-64 op the validator allows.
// These are SYNTACTIC witnesses only: they exist so llvm-mc can resolve the mnemonic to a
// concrete MC opcode. No effect is asserted here -- every effect comes from MC. A witness
// that fails to assemble simply skips that op (logged), so a bad example can never create
// a false pass. Ops with no entry (yield/mrs/isb) are aarch64 and are not x86-checkable.
var easmOpWitness = map[string]string{
	"mov":      "movq %rax, %rbx",
	"movq":     "movq %rax, %rbx",
	"movb":     "movb %al, %cl",
	"movw":     "movw %ax, %cx",
	"movl":     "movl %eax, %ecx",
	"movsx":    "movsbl %al, %ecx",
	"movsxd":   "movslq %eax, %rbx",
	"movsbw":   "movsbw %al, %cx",
	"movsbl":   "movsbl %al, %ecx",
	"movsbq":   "movsbq %al, %rcx",
	"movswl":   "movswl %ax, %ecx",
	"movswq":   "movswq %ax, %rcx",
	"movslq":   "movslq %eax, %rcx",
	"movzx":    "movzbl %al, %ecx",
	"movzbw":   "movzbw %al, %cx",
	"movzbl":   "movzbl %al, %ecx",
	"movzbq":   "movzbq %al, %rcx",
	"movzwl":   "movzwl %ax, %ecx",
	"movzwq":   "movzwq %ax, %rcx",
	"lea":      "leaq (%rax), %rbx",
	"push":     "pushq %rax",
	"pushq":    "pushq %rax",
	"pop":      "popq %rax",
	"popq":     "popq %rax",
	"xchg":     "xchgq %rax, %rbx",
	"xchgl":    "xchgl %eax, %ecx",
	"xchgq":    "xchgq %rax, %rbx",
	"add":      "addq %rax, %rbx",
	"addq":     "addq %rax, %rbx",
	"sub":      "subq %rax, %rbx",
	"subq":     "subq %rax, %rbx",
	"and":      "andq %rax, %rbx",
	"andq":     "andq %rax, %rbx",
	"cmp":      "cmpq %rax, %rbx",
	"cmpq":     "cmpq %rax, %rbx",
	"test":     "testq %rax, %rbx",
	"testq":    "testq %rax, %rbx",
	"inc":      "incq %rax",
	"incq":     "incq %rax",
	"dec":      "decq %rax",
	"decq":     "decq %rax",
	"xor":      "xorq %rax, %rbx",
	"xorq":     "xorq %rax, %rbx",
	"call":     "callq *%rax",
	"callq":    "callq *%rax",
	"jmp":      "jmp *%rax",
	"jmpq":     "jmpq *%rax",
	"ret":      "retq",
	"retq":     "retq",
	"cpuid":    "cpuid",
	"cld":      "cld",
	"std":      "std",
	"lfence":   "lfence",
	"rdtsc":    "rdtsc",
	"pause":    "pause",
	"fldcw":    "fldcw (%rax)",
	"fnstcw":   "fnstcw (%rax)",
	"stmxcsr":  "stmxcsr (%rax)",
	"ldmxcsr":  "ldmxcsr (%rax)",
	"emms":     "emms",
	"vzeroall": "vzeroall",
	"trap":     "ud2",
}

// mcEffect is one opcode's implicit effects as reported by MCInstrDesc.
type mcEffect struct {
	uses     []string // implicit use (read) register names, verbatim from MCRegisterInfo
	defs     []string // implicit def register names, verbatim from MCRegisterInfo (e.g. EFLAGS, RAX, DF)
	mayLoad  bool
	mayStore bool
	side     bool // hasUnmodeledSideEffects: an ambient effect beyond the operand/def model
}

// isX86FlagRegister reports whether an MC register name denotes the integer condition
// codes that EASM's instructionClobbersFlags models. EASM folds the arithmetic flags
// (EFLAGS) and the direction flag (DF, set by cld/std) into one "flags"/"cc" clobber, so
// both count -- but ONLY those two. The x87/SSE control-status registers are a separate
// state class (see isX86FPUControlRegister) gated by a capability, not by a flags clobber.
func isX86FlagRegister(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "EFLAGS", "DF":
		return true
	}
	return false
}

// isX86FPUControlRegister reports whether an MC register name denotes x87/SSE control or
// status state (the FPU status/control words and the SSE control/status register). These
// are not GPR or condition-code clobbers; LLVM does not allocate program values into them,
// so they need no clobber declaration. EASM instead requires an explicit fpu_control
// capability for any instruction that touches them, which this test verifies positively.
func isX86FPUControlRegister(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "FPSW", "FPCW", "MXCSR":
		return true
	}
	return false
}

// witnessRegisters returns the canonical GPRs that appear as explicit operands in an AT&T
// instruction (every %-prefixed token). A register the validator can see in the source
// text is handled by ordinary operand analysis, so it is not an "implicit" clobber even
// when a particular machine encoding (e.g. the XCHG accumulator form) lists it implicitly.
func witnessRegisters(instr string) map[string]bool {
	regs := map[string]bool{}
	runes := []rune(instr)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '%' {
			continue
		}
		j := i + 1
		for j < len(runes) {
			r := runes[j]
			if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				j++
				continue
			}
			break
		}
		if j > i+1 {
			regs[canonicalX86GPR(string(runes[i+1:j]))] = true
		}
	}
	return regs
}

// x86GPRSet is the 16 general-purpose registers, canonical 64-bit names. Used to decide
// whether an implicit def is a GPR clobber the validator must declare. RSP and RIP are
// deliberately excluded: the stack pointer and instruction pointer are modeled
// structurally (the `stack:`/`control:` sections), not as register clobbers.
var x86GPRSet = map[string]bool{
	"rax": true, "rbx": true, "rcx": true, "rdx": true,
	"rsi": true, "rdi": true, "rbp": true,
	"r8": true, "r9": true, "r10": true, "r11": true,
	"r12": true, "r13": true, "r14": true, "r15": true,
}

// isControlFlowOp reports whether an op transfers control. Such ops carry ambient side
// effects (a ret pops and jumps) that EASM models structurally via the control:/stack:
// sections rather than via a capability or clobber, so they are exempt from the
// side-effect-requires-capability rule.
func isControlFlowOp(op string) bool {
	switch op {
	case "call", "callq", "jmp", "jmpq", "ret", "retq":
		return true
	}
	return isConditionalJump(op)
}

func findLLVMTool(name string) string {
	for _, prefix := range []string{"/opt/homebrew/opt/llvm/bin", "/usr/local/opt/llvm/bin"} {
		p := filepath.Join(prefix, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// buildMCDump compiles the probe once and caches the binary under the OS temp dir keyed by
// (source, llvm version), so repeated test runs do not pay the ~2s compile.
func buildMCDump(t *testing.T, llvmConfig, clangxx string) string {
	t.Helper()
	version := strings.TrimSpace(runTool(t, llvmConfig, "--version"))
	key := sha256.Sum256([]byte(mcDumpSource + "\x00" + version))
	binPath := filepath.Join(os.TempDir(), "elisacore_easm_mc_dump_"+hex.EncodeToString(key[:8]))
	if fi, err := os.Stat(binPath); err == nil && fi.Mode()&0o111 != 0 {
		return binPath
	}
	srcPath := binPath + ".cpp"
	if err := os.WriteFile(srcPath, []byte(mcDumpSource), 0o644); err != nil {
		t.Fatalf("write probe source: %v", err)
	}
	cxxFlags := strings.Fields(runTool(t, llvmConfig, "--cxxflags"))
	libDir := strings.TrimSpace(runTool(t, llvmConfig, "--libdir"))
	args := append([]string{}, cxxFlags...)
	args = append(args, srcPath, "-o", binPath, "-L"+libDir, "-lLLVM", "-Wl,-rpath,"+libDir, "-Wno-deprecated-declarations")
	cmd := exec.Command(clangxx, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not compile the LLVM MC probe (this machine lacks a working clang++/LLVM toolchain): %v\n%s", err, out)
	}
	return binPath
}

func runTool(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return string(out)
}

// runMCDump parses the probe output into an opcode-name -> effects map.
func runMCDump(t *testing.T, binPath string) map[string]mcEffect {
	t.Helper()
	out, err := exec.Command(binPath).Output()
	if err != nil {
		t.Fatalf("run MC probe: %v", err)
	}
	effects := map[string]mcEffect{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		name := fields[0]
		var eff mcEffect
		if uses := strings.TrimPrefix(fields[1], "uses:"); uses != "" {
			eff.uses = strings.Split(uses, ",")
		}
		if defs := strings.TrimPrefix(fields[2], "defs:"); defs != "" {
			eff.defs = strings.Split(defs, ",")
		}
		eff.mayLoad = fields[3] == "mayLoad:1"
		eff.mayStore = fields[4] == "mayStore:1"
		eff.side = fields[5] == "side:1"
		effects[name] = eff
	}
	if len(effects) == 0 {
		t.Fatal("MC probe produced no opcodes")
	}
	return effects
}

// opcodeNameFor assembles a single AT&T instruction with `llvm-mc --show-inst` and returns
// the concrete MC opcode name (e.g. ADD64rr) that the mnemonic resolved to.
func opcodeNameFor(llvmMC, instr string) (string, error) {
	cmd := exec.Command(llvmMC, "--triple=x86_64-apple-darwin", "--show-inst", "--filetype=asm")
	cmd.Stdin = strings.NewReader(instr + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, out)
	}
	// Output carries a comment of the form: ## <MCInst #671 ADD64rr ...
	marker := "<MCInst #"
	idx := strings.Index(string(out), marker)
	if idx < 0 {
		return "", fmt.Errorf("no MCInst marker in llvm-mc output: %s", out)
	}
	rest := string(out)[idx+len(marker):]
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return "", fmt.Errorf("malformed MCInst marker: %q", rest)
	}
	// fields[0] is the numeric opcode id, fields[1] is the opcode name (possibly with a
	// trailing '>' when the instruction has no operands, e.g. "RDTSC>").
	return strings.TrimRight(fields[1], ">"), nil
}

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
