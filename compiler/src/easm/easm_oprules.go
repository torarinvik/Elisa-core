package easm

import (
	"fmt"
	"sort"
	"strings"
)

// Explicit transition relation (docs/104, TAL increment 2).
//
// EASM's per-instruction effects were historically encoded as a scatter of predicate functions
// (required capability, instructionClobbersFlags, implicitClobbers, implicitUses,
// implicitResultDefines).
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
	// Capability required to use the opcode at all; "" if none.
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
	"call":  {ImplicitClobbers: []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "cc"}},
	"callq": {ImplicitClobbers: []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "cc"}},
	"jmp":   {}, "jmpq": {}, "ret": {}, "retq": {},

	// Serializing / timing / fences.
	"cpuid":  {Capability: "x86_64.cpuid", ImplicitReads: []string{"rax", "rcx"}, ImplicitClobbers: []string{"rax", "rbx", "rcx", "rdx"}, ResultDefines: true},
	"rdtsc":  {Capability: "x86_64.rdtsc", ImplicitClobbers: []string{"rax", "rdx"}, ResultDefines: true},
	"lfence": {Capability: "x86_64.sse.lfence"},
	"pause":  {Capability: "x86_64.sse.pause"},
	"yield":  {Capability: "aarch64.yield"},
	"mrs":    {Capability: "aarch64.cntvct"},
	"isb":    {Capability: "aarch64.cntvct"},

	// FPU/SIMD control-word state.
	"fldcw":    {Capability: "x86_64.fpu_control"},
	"fnstcw":   {Capability: "x86_64.fpu_control"},
	"stmxcsr":  {Capability: "x86_64.fpu_control"},
	"ldmxcsr":  {Capability: "x86_64.fpu_control"},
	"emms":     {Capability: "x86_64.fpu_control"},
	"vzeroall": {Capability: "x86_64.simd_state"},

	// Debug trap.
	"trap": {Capability: "debug.trap"},
}

// easmMergeSnap captures the abstract machine state at a control-flow edge: a jump site or a label
// entry. Used by checkMergeConsistency to verify dataflow joins at control-flow merges (docs/104
// increment 2).
type easmMergeSnap struct {
	live            map[string]bool
	fs              string
	stackMod16      int
	stackMod16Known bool
	known           map[string]uint64
	line            int
}

func easmCopyLive(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		if v {
			out[k] = true
		}
	}
	return out
}

func easmCopyKnownUInt(m map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// checkMergeConsistency enforces the dataflow-join soundness condition at control-flow merges.
//
// The verifier walks the body linearly, carrying one machine state. At a label that is a jump target,
// that label is a MERGE: control arrives from the textual predecessor (if it falls through) AND from
// every jump targeting the label. The linear walk only carries the textual-predecessor state, so a
// register it treats as established right after the label might be dead on a jump-in path — and code
// after the label reading it would be unsound on that path.
//
// A label WITH a declared contract is already sound: every predecessor edge is checked against the
// contract at its site, and the post-label state is reset to the declared interface. So this check
// targets only contract-less merge labels. For each, it computes the MEET (intersection) of live
// registers over all real predecessor edges and flags any register the linear walk carries past the
// label that is not guaranteed live by every predecessor. The fix an author applies is to declare a
// `labels:` contract — i.e. to give the merge point an explicit type, exactly as TAL requires.
func checkMergeConsistency(path string, fn *Function, jumpPreds map[string][]easmMergeSnap, entry map[string]easmMergeSnap, fallReachable map[string]bool, contracts map[string]LabelContract) []Issue {
	var issues []Issue
	for label, preds := range jumpPreds {
		if len(preds) == 0 {
			continue
		}
		// Contract labels declare their merge interface and are enforced per-edge elsewhere.
		if _, hasContract := contracts[label]; hasContract {
			continue
		}
		walker, ok := entry[label]
		if !ok {
			continue
		}
		// Real predecessors: every jump targeting the label, plus the fallthrough edge if reachable.
		realPreds := append([]easmMergeSnap(nil), preds...)
		if fallReachable[label] {
			realPreds = append(realPreds, walker)
		}
		if len(realPreds) == 0 {
			continue
		}
		// Meet of live-register sets across all predecessors (a register survives only if live on every edge).
		meet := easmCopyLive(realPreds[0].live)
		for _, p := range realPreds[1:] {
			for reg := range meet {
				if !p.live[reg] {
					delete(meet, reg)
				}
			}
		}
		// A merge is unsound only for registers actually DEMANDED after the label -- read before being
		// rewritten in the block the label heads. A register the walk happens to carry but never reads is
		// harmless. Flag demanded registers the meet does not guarantee: they are read on the linear path
		// but undefined on some jump-in edge.
		var unsound []string
		for _, reg := range demandedAfterLabel(fn, label) {
			if !meet[reg] {
				unsound = append(unsound, reg)
			}
		}
		if len(unsound) != 0 {
			sort.Strings(unsound)
			issues = append(issues, Issue{Severity: "error", Code: "merge-state-unsound", File: path, Line: entry[label].line, Message: fmt.Sprintf("merge label %s: register(s) %s are carried past the label but not established on every incoming edge; declare a labels: contract for %s to type the merge point", label, strings.Join(unsound, ", "), label)})
		}
		// FS segment state: if the walk assumes a definite FS but a predecessor disagrees, control after
		// the merge could run with the wrong TLS addressing — a safety-critical divergence.
		if walker.fs != "" {
			for _, p := range realPreds {
				if p.fs != walker.fs {
					issues = append(issues, Issue{Severity: "error", Code: "merge-fs-state-unsound", File: path, Line: entry[label].line, Message: fmt.Sprintf("merge label %s: fs state %q is assumed after the label but an incoming edge (line %d) does not establish it; assert state fs: or declare a labels: contract", label, walker.fs, p.line)})
					break
				}
			}
		}
		// Stack alignment is another meet fact. Only demand it when the block is about to rely on the
		// linear walk's alignment proof for a call or indirect control transfer.
		if walker.stackMod16Known && stackAlignmentDemandedAfterLabel(fn, label) {
			for _, p := range realPreds {
				if !p.stackMod16Known || p.stackMod16 != walker.stackMod16 {
					issues = append(issues, Issue{Severity: "error", Code: "merge-stack-alignment-unsound", File: path, Line: entry[label].line, Message: fmt.Sprintf("merge label %s: rsp mod 16 = %d is assumed after the label but an incoming edge (line %d) does not establish the same alignment; declare a labels: contract or realign on every edge", label, walker.stackMod16, p.line)})
					break
				}
			}
		}
		for _, reg := range knownControlTargetDemandedAfterLabel(fn, label) {
			value, ok := walker.known[reg]
			if !ok {
				continue
			}
			for _, p := range realPreds {
				if predValue, predOK := p.known[reg]; !predOK || predValue != value {
					issues = append(issues, Issue{Severity: "error", Code: "merge-known-value-unsound", File: path, Line: entry[label].line, Message: fmt.Sprintf("merge label %s: %s is assumed to have concrete value 0x%x for an indirect control target but an incoming edge (line %d) does not establish the same value; declare a labels: contract or materialize the value on every edge", label, reg, value, p.line)})
					break
				}
			}
		}
	}
	return issues
}

// demandedAfterLabel returns the registers read-before-written in the straight-line block headed by
// the named label: the registers whose value the merge actually consumes. The scan runs from the
// label to the next label or terminator (ret/jmp); registers written before they are read in the block
// are satisfied locally and not demanded. rsp/rip are structural and excluded. This bounds the demand
// to the block the label dominates linearly -- a conservative under-approximation (any downstream
// label is its own merge point), which keeps the check free of false positives.
func demandedAfterLabel(fn *Function, label string) []string {
	demanded := map[string]bool{}
	written := map[string]bool{}
	started := false
	for _, inst := range fn.Instructions {
		if inst.Label != "" {
			if inst.Label == label {
				started = true
				continue
			}
			if started {
				break // reached the next label: end of this block
			}
			continue
		}
		if !started || inst.Pseudo {
			continue
		}
		for _, reg := range registersReadBy(inst.Text) {
			r := canonicalX86GPR(reg)
			if r == "rsp" || r == "rip" {
				continue
			}
			if !written[r] {
				demanded[r] = true
			}
		}
		for _, w := range writtenRegisters(inst.Text) {
			written[canonicalX86GPR(w)] = true
		}
		op := normalizeOp(inst.Op)
		if op == "ret" || op == "retq" || op == "jmp" || op == "jmpq" {
			break
		}
	}
	out := make([]string, 0, len(demanded))
	for r := range demanded {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func stackAlignmentDemandedAfterLabel(fn *Function, label string) bool {
	started := false
	for _, inst := range fn.Instructions {
		if inst.Label != "" {
			if inst.Label == label {
				started = true
				continue
			}
			if started {
				return false
			}
			continue
		}
		if !started || inst.Pseudo {
			continue
		}
		op := normalizeOp(inst.Op)
		if strings.HasPrefix(op, "call") || isIndirectControlTransfer(op, inst.Text) {
			return true
		}
		if op == "ret" || op == "retq" || op == "jmp" || op == "jmpq" {
			return false
		}
	}
	return false
}

func knownControlTargetDemandedAfterLabel(fn *Function, label string) []string {
	demanded := map[string]bool{}
	started := false
	for _, inst := range fn.Instructions {
		if inst.Label != "" {
			if inst.Label == label {
				started = true
				continue
			}
			if started {
				break
			}
			continue
		}
		if !started || inst.Pseudo {
			continue
		}
		op := normalizeOp(inst.Op)
		if isIndirectControlTransfer(op, inst.Text) {
			if reg := indirectControlTargetRegister(inst.Text); reg != "" {
				demanded[reg] = true
			}
		}
		if op == "ret" || op == "retq" || op == "jmp" || op == "jmpq" {
			break
		}
	}
	out := make([]string, 0, len(demanded))
	for reg := range demanded {
		out = append(out, reg)
	}
	sort.Strings(out)
	return out
}

// lookupOpRule returns the declared rule for an opcode and whether one exists. Conditional jumps
// (matched by family, not membership) are flag-reading control transfers with no GPR effects and are
// intentionally not table rows; callers gate on isConditionalJump before consulting the table.
func lookupOpRule(op string) (opRule, bool) {
	r, ok := opRules[op]
	return r, ok
}

func opCapability(op string) string {
	if r, ok := lookupOpRule(op); ok {
		return r.Capability
	}
	return ""
}
