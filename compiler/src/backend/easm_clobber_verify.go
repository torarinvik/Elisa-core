package backend

import (
	"fmt"
	"strings"

	"elisacore/src/easm"
)

// EASM safety relies on two independent implementations agreeing: the validator
// (src/easm) proves the assembly body only writes registers/flags/memory that are
// declared in `clobbers:`/`outputs:`, and the lowering here (easmInlineAsmConstraints)
// must communicate every one of those clobbers to LLVM. If they diverge -- a clobber
// the validator accepted but the lowering drops or mistranslates -- LLVM treats the
// register (or the flags) as preserved and may allocate a live value across the asm,
// producing wrong machine code with no diagnostic. verifyEASMConstraintsCoverClobbers
// is the cross-check that turns that silent miscompile into a hard build error.

// canonicalEASMClobberRegister maps an x86-64 GPR name of any width to its 64-bit
// canonical form, or "" if it is not a general-purpose register we model. This lets
// the cross-check compare a `clobbers: eax` declaration against a `~{rax}`/`~{eax}`
// constraint regardless of the width the author wrote.
func canonicalEASMClobberRegister(name string) string {
	name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "%")
	switch name {
	case "rax", "eax", "ax", "al", "ah":
		return "rax"
	case "rbx", "ebx", "bx", "bl", "bh":
		return "rbx"
	case "rcx", "ecx", "cx", "cl", "ch":
		return "rcx"
	case "rdx", "edx", "dx", "dl", "dh":
		return "rdx"
	case "rsi", "esi", "si", "sil":
		return "rsi"
	case "rdi", "edi", "di", "dil":
		return "rdi"
	case "rbp", "ebp", "bp", "bpl":
		return "rbp"
	case "rsp", "esp", "sp", "spl":
		return "rsp"
	case "r8", "r8d", "r8w", "r8b":
		return "r8"
	case "r9", "r9d", "r9w", "r9b":
		return "r9"
	case "r10", "r10d", "r10w", "r10b":
		return "r10"
	case "r11", "r11d", "r11w", "r11b":
		return "r11"
	case "r12", "r12d", "r12w", "r12b":
		return "r12"
	case "r13", "r13d", "r13w", "r13b":
		return "r13"
	case "r14", "r14d", "r14w", "r14b":
		return "r14"
	case "r15", "r15d", "r15w", "r15b":
		return "r15"
	}
	return ""
}

// splitEASMClobberItems splits a clobber declaration such as "rax, rbx" or "memory"
// into its lowercase items.
func splitEASMClobberItems(clobber string) []string {
	fields := strings.FieldsFunc(clobber, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// easmConstraintCoverage parses an emitted LLVM inline-asm constraint string and
// reports which canonical GPRs and effects (memory, condition codes) it communicates
// to LLVM -- whether through a clobber (~{...}) or a bound operand ({...} / ={...}).
func easmConstraintCoverage(constraints string) (regs map[string]bool, hasMemory bool, hasCC bool) {
	regs = map[string]bool{}
	for _, part := range strings.Split(constraints, ",") {
		part = strings.TrimSpace(part)
		var body string
		switch {
		case strings.HasPrefix(part, "~{") && strings.HasSuffix(part, "}"):
			body = part[2 : len(part)-1]
		case strings.HasPrefix(part, "={") && strings.HasSuffix(part, "}"):
			body = part[2 : len(part)-1]
		case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
			body = part[1 : len(part)-1]
		default:
			continue
		}
		switch strings.ToLower(strings.TrimSpace(body)) {
		case "memory":
			hasMemory = true
		case "cc":
			hasCC = true
		default:
			if reg := canonicalEASMClobberRegister(body); reg != "" {
				regs[reg] = true
			}
		}
	}
	return regs, hasMemory, hasCC
}

// verifyEASMConstraintsCoverClobbers asserts that every clobber the EASM validator
// relied on is actually communicated to LLVM by the emitted constraint string. A
// failure here means the validator's safety proof does not hold at codegen time and
// LLVM would be free to corrupt the named register/flags across the asm.
func verifyEASMConstraintsCoverClobbers(fn *easm.Function, constraints string) error {
	if fn == nil {
		return nil
	}
	regs, hasMemory, hasCC := easmConstraintCoverage(constraints)
	for _, clobber := range fn.Clobbers {
		for _, item := range splitEASMClobberItems(clobber) {
			switch item {
			case "memory":
				if !hasMemory {
					return fmt.Errorf("EASM %s: 'memory' clobber was validated but the emitted inline-asm constraints %q omit ~{memory}; LLVM would treat memory as unclobbered", fn.Name, constraints)
				}
			case "cc", "flags":
				if !hasCC {
					return fmt.Errorf("EASM %s: flags/condition-code clobber was validated but the emitted inline-asm constraints %q omit ~{cc}; LLVM would keep flags-dependent values live across the asm", fn.Name, constraints)
				}
			default:
				reg := canonicalEASMClobberRegister(item)
				if reg == "" {
					// Not a GPR we canonicalize (e.g. a vector/segment register or a
					// named target clobber). The validator and lowering agree to pass
					// it through verbatim, so there is nothing to reconcile here.
					continue
				}
				if !regs[reg] {
					return fmt.Errorf("EASM %s: register clobber %q (%s) was validated but is not communicated to LLVM in constraints %q; LLVM would treat %s as preserved and may allocate it across the asm", fn.Name, item, reg, constraints, reg)
				}
			}
		}
	}
	return nil
}
