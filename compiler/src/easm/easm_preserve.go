package easm

// Register-polymorphic preservation (docs/104 upgrade-ladder item 3).
//
// A routine that "preserves callee_saved" has, in TAL terms, the type ∀ρ. (args; callee_saved=ρ) →
// (ret; callee_saved=ρ): for every entry value ρ of a callee-saved register, the exit value is the
// same ρ. The structural check (calleeSavedPushPopProven) verifies push/pop ORDERING — a save before
// the first write and a restore after the last. That is a presence checklist: it cannot see the
// VALUE that flows back. It passes save-but-restore-wrong-value bugs, e.g.
//
//     pushq %r12        ; save ρ at [rsp-8]
//     pushq %rax        ; [rsp-16] = rax   (stack now misaligned w.r.t. the save)
//     popq  %r12        ; r12 <- [rsp-16] = rax, NOT ρ
//
// This check models a symbolic stack: push stores the source term at a decremented rsp offset, pop
// loads whatever term currently sits at that offset. After the body, it asks z3 whether any input can
// make a preserved register's exit term differ from its entry symbol. sat ⇒ the value is not always
// restored (callee-saved-value-unrestored); unsat ⇒ preserved by parametricity. Opt-in via
// ELISA_EASM_PRESERVE_PARAMETRIC=1, off by default. Bodies outside the modelable subset (any non-stack
// memory operand, control flow, sub-register writes) are skipped, never silently passed.

import (
	"fmt"
	"os"
	"strings"

	"elisacore/src/smt"
)

const preserveParametricEnv = "ELISA_EASM_PRESERVE_PARAMETRIC"

func calleeSavedGPRs() []string {
	return []string{"rbx", "rbp", "r12", "r13", "r14", "r15"}
}

// verifyPreservationParametric proves callee-saved value preservation for the modelable subset. Gated.
func verifyPreservationParametric(path string, fn *Function) []Issue {
	if fn == nil || len(fn.Instructions) == 0 {
		return nil
	}
	controlSet := setOf(fn.Control)
	if !controlSet["returns"] {
		return nil // a noreturn/tail-jumping routine has no "exit value" to compare
	}
	clobberSet := setOf(fn.Clobbers)
	preserveSet := setOf(fn.Preserves)
	requireSet := setOf(fn.Requires)
	if requireSet["callee_saved.preservation.unchecked"] {
		return nil
	}
	// The registers this routine claims to preserve AND clobbers (the ones at risk).
	var targets []string
	for _, reg := range calleeSavedGPRs() {
		if clobberSet[reg] && (preserveSet[reg] || preserveSet["callee_saved"]) {
			targets = append(targets, reg)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	finalRegs, reason := preserveEncodeBody(fn.Instructions)
	if reason != "" {
		return []Issue{{Severity: "warning", Code: "preserve-parametric-skip", File: path, Line: fn.Line, Message: fmt.Sprintf("preservation parametricity skipped %s: %s", fn.Name, reason)}}
	}

	solverBin := "z3"
	if override := os.Getenv("ELISA_EASM_PRESERVE_PARAMETRIC_Z3"); override != "" {
		solverBin = override
	}
	solver, err := smt.Open(smt.Options{Binary: solverBin, PerQueryTimeoutMillis: 5000})
	if err != nil {
		return []Issue{{Severity: "warning", Code: "preserve-parametric-skip", File: path, Line: fn.Line, Message: fmt.Sprintf("preservation parametricity skipped %s: no SMT solver: %v", fn.Name, err)}}
	}
	defer solver.Close()

	var issues []Issue
	for _, reg := range targets {
		exit := finalRegs[reg]
		if exit == "" {
			exit = "in_" + reg // never written ⇒ trivially preserved
		}
		// Declare every input symbol the exit term references, plus the entry symbol.
		var b strings.Builder
		for _, sym := range collectInputSymbols(exit + " in_" + reg) {
			fmt.Fprintf(&b, "(declare-const %s (_ BitVec 64))\n", sym)
		}
		fmt.Fprintf(&b, "(assert (not (= %s in_%s)))\n", exit, reg)
		result, _ := solver.Check(b.String())
		switch result {
		case smt.Unsat:
			// preserved for all inputs
		case smt.Sat:
			issues = append(issues, Issue{Severity: "error", Code: "callee-saved-value-unrestored", File: path, Line: fn.Line, Message: fmt.Sprintf("callee-saved register %s is not restored to its entry value on every path (save/restore preserves the slot but not the value)", reg)})
		default:
			issues = append(issues, Issue{Severity: "warning", Code: "preserve-parametric-skip", File: path, Line: fn.Line, Message: fmt.Sprintf("preservation parametricity for %s on %s: solver unknown (declined)", reg, fn.Name)})
		}
	}
	return issues
}

// preserveEncodeBody symbolically executes a body with a modeled stack, returning the final
// register→term map. A non-empty reason means the body is outside the modelable subset.
func preserveEncodeBody(insts []Instruction) (map[string]string, string) {
	regs := map[string]string{}
	stack := map[int]string{}
	rspOff := 0
	freshN := 0

	termOf := func(operand string) (string, bool) {
		operand = strings.TrimSpace(operand)
		switch {
		case strings.HasPrefix(operand, "%"):
			reg := canonicalX86GPR(strings.TrimPrefix(operand, "%"))
			if !isX86GPR(reg) {
				return "", false
			}
			if t, ok := regs[reg]; ok {
				return t, true
			}
			sym := "in_" + reg
			regs[reg] = sym
			return sym, true
		case strings.HasPrefix(operand, "$"):
			v, err := parseImmediate64(strings.TrimPrefix(operand, "$"))
			if err != nil {
				return "", false
			}
			return bv64(v), true
		default:
			return "", false // memory operand or label
		}
	}

	for _, inst := range insts {
		if inst.Label != "" {
			return nil, "contains a label (control flow not modeled)"
		}
		if inst.Pseudo {
			continue // state pseudo-ops don't touch GPR values
		}
		op := normalizeOp(inst.Op)
		if op == "ret" || op == "retq" {
			continue
		}
		ops := splitInstructionOperands(inst.Text)
		switch op {
		case "push", "pushq":
			if len(ops) != 1 {
				return nil, "malformed push"
			}
			t, ok := termOf(ops[0])
			if !ok {
				return nil, "push of a non-register/immediate operand"
			}
			rspOff -= 8
			stack[rspOff] = t
		case "pop", "popq":
			if len(ops) != 1 || !strings.HasPrefix(strings.TrimSpace(ops[0]), "%") {
				return nil, "malformed pop"
			}
			reg := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(ops[0]), "%"))
			if !isX86GPR(reg) {
				return nil, "pop into a non-GPR"
			}
			t, ok := stack[rspOff]
			if !ok {
				t = fmt.Sprintf("stale_%d", freshN)
				freshN++
			}
			rspOff += 8
			regs[reg] = t
		case "movq":
			if len(ops) != 2 {
				return nil, "malformed movq"
			}
			src, ok := termOf(ops[0])
			if !ok {
				return nil, "movq with a memory/sub-register operand (not modeled)"
			}
			if !strings.HasPrefix(strings.TrimSpace(ops[1]), "%") {
				return nil, "movq into memory (not modeled)"
			}
			regs[canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(ops[1]), "%"))] = src
		case "addq", "subq", "andq", "xorq":
			if len(ops) != 2 || !strings.HasPrefix(strings.TrimSpace(ops[1]), "%") {
				return nil, op + " with a non-register destination (not modeled)"
			}
			dst := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(ops[1]), "%"))
			dstTerm, ok := termOf(ops[1])
			srcTerm, ok2 := termOf(ops[0])
			if !ok || !ok2 {
				return nil, op + " with a memory operand (not modeled)"
			}
			switch op {
			case "addq":
				regs[dst] = fmt.Sprintf("(bvadd %s %s)", dstTerm, srcTerm)
			case "subq":
				regs[dst] = fmt.Sprintf("(bvsub %s %s)", dstTerm, srcTerm)
			case "andq":
				regs[dst] = fmt.Sprintf("(bvand %s %s)", dstTerm, srcTerm)
			case "xorq":
				regs[dst] = fmt.Sprintf("(bvxor %s %s)", dstTerm, srcTerm)
			}
		case "incq", "decq":
			if len(ops) != 1 || !strings.HasPrefix(strings.TrimSpace(ops[0]), "%") {
				return nil, "malformed " + op
			}
			dst := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(ops[0]), "%"))
			dstTerm, _ := termOf(ops[0])
			if op == "incq" {
				regs[dst] = fmt.Sprintf("(bvadd %s %s)", dstTerm, bv64(1))
			} else {
				regs[dst] = fmt.Sprintf("(bvsub %s %s)", dstTerm, bv64(1))
			}
		case "call", "callq":
			// ABI: the callee preserves callee-saved registers; caller-saved become indeterminate.
			for _, reg := range callerSavedGPRs() {
				regs[reg] = fmt.Sprintf("call_%d_%s", freshN, reg)
			}
			freshN++
		default:
			return nil, fmt.Sprintf("opcode %q outside the modelable subset", inst.Op)
		}
	}
	return regs, ""
}

// collectInputSymbols extracts the distinct in_*/stale_*/call_* symbols mentioned in an SMT term blob.
func collectInputSymbols(blob string) []string {
	seen := map[string]bool{}
	var out []string
	fields := strings.FieldsFunc(blob, func(r rune) bool {
		return r == '(' || r == ')' || r == ' '
	})
	for _, f := range fields {
		if strings.HasPrefix(f, "in_") || strings.HasPrefix(f, "stale_") || strings.HasPrefix(f, "call_") {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}
