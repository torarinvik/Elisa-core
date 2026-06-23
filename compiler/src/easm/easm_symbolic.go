package easm

// Symbolic lockstep equivalence (docs/104 rigor ladder, rung 3).
//
// The fuzz oracle (easm_lockstep_oracle.go, rung 4) proves reference ≡ target on SAMPLED inputs.
// This rung proves it for ALL inputs by encoding both bodies as bit-precise SMT terms over 64-bit
// bit-vectors and asking z3 whether any input makes a declared output register differ. Unsat ⇒ the
// two bodies are observationally equivalent on the modeled state for every input — a static,
// all-inputs proof, strictly stronger than the fuzz agreement.
//
// Scope is deliberately the decidable straight-line GPR + 64-bit-ALU subset: movq/addq/subq/andq/
// xorq/incq/decq between registers and immediates, terminated by ret. Anything outside it (memory
// operands, control flow, sub-register writes, flags-dependent behavior, segment/stack effects) makes
// the body unmodelable and the routine is reported as lockstep-symbolic-skip — never silently passed.
// Opt-in via ELISA_EASM_LOCKSTEP_SYMBOLIC=1, off by default so normal verification is unaffected.

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"elisacore/src/smt"
)

const lockstepSymbolicEnv = "ELISA_EASM_LOCKSTEP_SYMBOLIC"

// verifyLockstepSymbolic proves reference ≡ target by SMT for the modelable subset. Gated by env.
func verifyLockstepSymbolic(path, target string, fn *Function) []Issue {
	if fn == nil || len(fn.Reference) == 0 || len(fn.Targets) == 0 {
		return nil
	}
	outputs := outputRegisterOrder(fn.Outputs)
	if len(outputs) == 0 {
		// Nothing observable to compare on the modeled (register) state.
		return []Issue{lockstepSymbolicSkip(path, fn.Line, fn.Name, "no output registers to compare")}
	}
	var issues []Issue
	for _, tb := range fn.Targets {
		if strings.ToLower(tb.Arch) != "x86_64" && strings.ToLower(target) != "x86_64" {
			issues = append(issues, lockstepSymbolicSkip(path, tb.Line, fn.Name, "symbolic equivalence currently models x86_64 only"))
			continue
		}
		refState, reason := symbolicEncodeBody(fn.Reference)
		if reason != "" {
			issues = append(issues, lockstepSymbolicSkip(path, tb.Line, fn.Name, "reference: "+reason))
			continue
		}
		tgtState, treason := symbolicEncodeBody(tb.Instructions)
		if treason != "" {
			issues = append(issues, lockstepSymbolicSkip(path, tb.Line, fn.Name, "target: "+treason))
			continue
		}
		if issue := symbolicCheckEquivalence(path, fn, tb, outputs, refState, tgtState); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// symbolicState maps a canonical 64-bit register to its current SMT bit-vector term. Reading a
// register that was never written lazily binds it to a fresh input symbol (recorded in inputs).
type symbolicState struct {
	terms  map[string]string
	inputs map[string]bool
}

func newSymbolicState() *symbolicState {
	return &symbolicState{terms: map[string]string{}, inputs: map[string]bool{}}
}

// read returns the SMT term for an operand: a register's current term (binding an input symbol on
// first use), or a 64-bit literal for an immediate. ok=false means the operand is not modelable.
func (s *symbolicState) read(operand string) (term string, ok bool) {
	operand = strings.TrimSpace(operand)
	switch {
	case strings.HasPrefix(operand, "%"):
		reg := canonicalX86GPR(strings.TrimPrefix(operand, "%"))
		if !isX86GPR(reg) {
			return "", false
		}
		if t, seen := s.terms[reg]; seen {
			return t, true
		}
		sym := "in_" + reg
		s.terms[reg] = sym
		s.inputs[reg] = true
		return sym, true
	case strings.HasPrefix(operand, "$"):
		v, err := parseImmediate64(strings.TrimPrefix(operand, "$"))
		if err != nil {
			return "", false
		}
		return bv64(v), true
	default:
		// Memory operand, label, or anything else: not modelable.
		return "", false
	}
}

func (s *symbolicState) writeReg(operand, term string) bool {
	if !strings.HasPrefix(operand, "%") {
		return false
	}
	reg := canonicalX86GPR(strings.TrimPrefix(operand, "%"))
	if !isX86GPR(reg) {
		return false
	}
	s.terms[reg] = term
	return true
}

// symbolicEncodeBody symbolically executes a body over the modelable subset, returning the final
// register→term state. A non-empty reason means the body is outside the subset (skip, do not prove).
func symbolicEncodeBody(insts []Instruction) (*symbolicState, string) {
	st := newSymbolicState()
	for _, inst := range insts {
		if inst.Label != "" {
			return nil, "contains a label (control flow not modeled)"
		}
		if inst.Pseudo {
			// state pseudo-ops imply segment/ambient effects outside the pure-register model.
			return nil, "contains a machine-state pseudo-op"
		}
		op := normalizeOp(inst.Op)
		if op == "ret" || op == "retq" {
			continue
		}
		ops := splitInstructionOperands(inst.Text)
		switch op {
		case "movq":
			if len(ops) != 2 {
				return nil, "malformed movq"
			}
			src, ok := st.read(ops[0])
			if !ok {
				return nil, "movq operand not modelable (memory or sub-register)"
			}
			if !st.writeReg(ops[1], src) {
				return nil, "movq destination not a register"
			}
		case "addq", "subq", "andq", "xorq":
			if len(ops) != 2 {
				return nil, "malformed " + op
			}
			dstTerm, ok := st.read(ops[1])
			if !ok {
				return nil, op + " destination not modelable"
			}
			srcTerm, ok2 := st.read(ops[0])
			if !ok2 {
				return nil, op + " source not modelable (memory or sub-register)"
			}
			var term string
			switch op {
			case "addq":
				term = fmt.Sprintf("(bvadd %s %s)", dstTerm, srcTerm)
			case "subq":
				term = fmt.Sprintf("(bvsub %s %s)", dstTerm, srcTerm)
			case "andq":
				term = fmt.Sprintf("(bvand %s %s)", dstTerm, srcTerm)
			case "xorq":
				term = fmt.Sprintf("(bvxor %s %s)", dstTerm, srcTerm)
			}
			if !st.writeReg(ops[1], term) {
				return nil, op + " destination not a register"
			}
		case "incq", "decq":
			if len(ops) != 1 {
				return nil, "malformed " + op
			}
			dstTerm, ok := st.read(ops[0])
			if !ok {
				return nil, op + " destination not modelable"
			}
			one := bv64(1)
			if op == "incq" {
				st.writeReg(ops[0], fmt.Sprintf("(bvadd %s %s)", dstTerm, one))
			} else {
				st.writeReg(ops[0], fmt.Sprintf("(bvsub %s %s)", dstTerm, one))
			}
		default:
			return nil, fmt.Sprintf("opcode %q outside the modelable GPR/ALU subset", inst.Op)
		}
	}
	return st, ""
}

// symbolicCheckEquivalence asks z3 whether any input makes a declared output register differ between
// the two encodings. Unsat → equivalent for all inputs (proof); Sat → divergence with a witness;
// Unknown → decline (never a pass).
func symbolicCheckEquivalence(path string, fn *Function, tb TargetBody, outputs []string, ref, tgt *symbolicState) *Issue {
	solverBin := "z3"
	if override := os.Getenv("ELISA_EASM_LOCKSTEP_SYMBOLIC_Z3"); override != "" {
		solverBin = override
	}
	solver, err := smt.Open(smt.Options{Binary: solverBin, PerQueryTimeoutMillis: 5000})
	if err != nil {
		issue := lockstepSymbolicSkip(path, tb.Line, fn.Name, "no SMT solver available: "+err.Error())
		return &issue
	}
	defer solver.Close()

	// Shared input symbols: every register either body reads uninitialized is a universally-quantified
	// 64-bit input. Declare the union so both encodings refer to the same symbol.
	inputs := map[string]bool{}
	for r := range ref.inputs {
		inputs[r] = true
	}
	for r := range tgt.inputs {
		inputs[r] = true
	}
	var b strings.Builder
	for r := range inputs {
		fmt.Fprintf(&b, "(declare-const in_%s (_ BitVec 64))\n", r)
	}
	// Output terms default to the input symbol if a body never writes the output register (identity).
	var diffs []string
	for _, out := range outputs {
		refTerm := outputTerm(ref, out)
		tgtTerm := outputTerm(tgt, out)
		diffs = append(diffs, fmt.Sprintf("(not (= %s %s))", refTerm, tgtTerm))
	}
	// Ensure any input symbol referenced via identity-default is declared.
	for _, out := range outputs {
		for _, st := range []*symbolicState{ref, tgt} {
			if _, written := st.terms[out]; !written {
				if !inputs[out] {
					fmt.Fprintf(&b, "(declare-const in_%s (_ BitVec 64))\n", out)
					inputs[out] = true
				}
			}
		}
	}
	if len(diffs) == 1 {
		fmt.Fprintf(&b, "(assert %s)\n", diffs[0])
	} else {
		fmt.Fprintf(&b, "(assert (or %s))\n", strings.Join(diffs, " "))
	}

	result, _ := solver.Check(b.String())
	switch result {
	case smt.Unsat:
		return nil // proven equivalent for all inputs
	case smt.Sat:
		issue := Issue{Severity: "error", Code: "lockstep-symbolic-divergence", File: path, Line: tb.Line, Message: fmt.Sprintf("symbolic equivalence DISPROVED for %s: reference and target produce different output registers on some input", fn.Name)}
		return &issue
	default:
		issue := lockstepSymbolicSkip(path, tb.Line, fn.Name, "SMT solver returned unknown (declined, not a proof)")
		return &issue
	}
}

// outputTerm returns the SMT term a body leaves in an output register, defaulting to that register's
// input symbol when the body never writes it (the register passes through unchanged).
func outputTerm(st *symbolicState, reg string) string {
	if t, ok := st.terms[reg]; ok {
		return t
	}
	return "in_" + reg
}

func lockstepSymbolicSkip(path string, line int, name, reason string) Issue {
	return Issue{Severity: "warning", Code: "lockstep-symbolic-skip", File: path, Line: line, Message: fmt.Sprintf("symbolic equivalence skipped %s: %s", name, reason)}
}

// outputRegisterOrder returns the declared output registers in a stable order.
func outputRegisterOrder(outputs []string) []string {
	set := outputRegisterSet(outputs)
	var out []string
	for r := range set {
		out = append(out, r)
	}
	// Stable order for deterministic SMT text.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// parseImmediate64 parses an AT&T immediate (decimal or 0x-hex, optionally negative) into a uint64
// (two's-complement for negatives).
func parseImmediate64(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base = 16
		s = s[2:]
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, err
	}
	if neg {
		return uint64(-int64(v)), nil
	}
	return v, nil
}

func bv64(v uint64) string {
	return fmt.Sprintf("(_ bv%d 64)", v)
}
