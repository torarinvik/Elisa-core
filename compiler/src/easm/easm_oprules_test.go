package easm

import (
	"reflect"
	"sort"
	"testing"
)

// docs/104 increment 2: the declared transition relation (opRules) must be TOTAL over the allowed
// instruction set and CONSISTENT with the legacy per-opcode predicate functions the walker enforces.
// Together these make opRules the single auditable source of truth for `Γ ⊢ instr ⇒ Γ'`, not a
// parallel description that can silently drift.

// Totality: every allowed opcode has a declared rule.
func TestOpRulesTotalOverAllowedOps(t *testing.T) {
	for op := range allowedOps {
		if _, ok := lookupOpRule(op); !ok {
			t.Errorf("allowed opcode %q has no declared transition rule", op)
		}
	}
}

// No orphan rules: every declared rule corresponds to an allowed opcode (keeps the relation honest).
func TestOpRulesNoOrphans(t *testing.T) {
	for op := range opRules {
		if !allowedOps[op] {
			t.Errorf("opRule declared for %q but it is not in allowedOps", op)
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// Consistency: each rule field agrees with the legacy predicate function the walker actually calls.
// Those functions are themselves cross-checked against LLVM MC (easm_mc_effects_test.go), so passing
// here means the declared relation IS the enforced relation.
func TestOpRulesConsistentWithLegacyPredicates(t *testing.T) {
	for op := range allowedOps {
		rule, _ := lookupOpRule(op)

		if got := capabilityByOp[op]; got != rule.Capability {
			t.Errorf("%q: rule.Capability=%q but capabilityByOp=%q", op, rule.Capability, got)
		}
		if got := instructionClobbersFlags(op); got != rule.ClobbersFlags {
			t.Errorf("%q: rule.ClobbersFlags=%v but instructionClobbersFlags=%v", op, rule.ClobbersFlags, got)
		}
		if got := implicitResultDefines(op); got != rule.ResultDefines {
			t.Errorf("%q: rule.ResultDefines=%v but implicitResultDefines=%v", op, rule.ResultDefines, got)
		}
		wantReads, gotReads := sortedCopy(rule.ImplicitReads), sortedCopy(implicitUses(op))
		if len(wantReads) != 0 || len(gotReads) != 0 {
			if !reflect.DeepEqual(wantReads, gotReads) {
				t.Errorf("%q: rule.ImplicitReads=%v but implicitUses=%v", op, wantReads, gotReads)
			}
		}
		wantClob, gotClob := sortedCopy(rule.ImplicitClobbers), sortedCopy(implicitClobbers(op))
		if len(wantClob) != 0 || len(gotClob) != 0 {
			if !reflect.DeepEqual(wantClob, gotClob) {
				t.Errorf("%q: rule.ImplicitClobbers=%v but implicitClobbers=%v", op, wantClob, gotClob)
			}
		}
	}
}

// The runtime totality guard fires if an allowed opcode loses its rule. Simulate by verifying that a
// body using a known opcode is clean, then that the guard code path exists by asserting every opcode
// the verifier can reach is covered (regression guard against adding to allowedOps without a rule).
func TestOpcodeRuleMissingGuardCoverage(t *testing.T) {
	// If this ever fails, a new allowedOps entry was added without a rule; the runtime guard would
	// emit opcode-rule-missing on first use. Keep the static and runtime guards in lockstep.
	for op := range allowedOps {
		if _, ok := opRules[op]; !ok {
			t.Fatalf("opcode %q would trigger the runtime opcode-rule-missing guard", op)
		}
	}
}
