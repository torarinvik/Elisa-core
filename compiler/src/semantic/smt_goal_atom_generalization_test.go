//go:build cgo

package semantic

import "testing"

const smtGoalAtomPrelude = `
struct Instr:
    op: sview
    width: i64
    ratio: f64
`

// An ensure whose propositional skeleton holds for every value of an atom the prover cannot
// translate (`instruction.op != "label"`) is proven by generalizing that atom.
func TestSMTGoalAtomGeneralization(t *testing.T) {
	src := `
def label_len(instruction: Instr, x: i64) -> i64:
    requires x > 0
    ensure result >= 0 implies instruction.ratio > 0.5
    ensure (instruction.ratio < 0.25) or result < 0
    return 0 - x
`
	result := analyzeContractStrict(t, "label_len.elisa", smtGoalAtomPrelude+src)
	if n := ensureFailuresFor(result.Errors(), "label_len"); n != 0 {
		t.Fatalf("expected the generalized goal to prove, got: %v", result.Errors())
	}
	falseCases := map[string]string{
		// True only for some values of the untranslatable atom: must stay unproven.
		"label_depends": `
def label_depends(instruction: Instr, x: i64) -> i64:
    ensure instruction.ratio > 0.5 or result > 0
    return x
`,
		"label_negated": `
def label_negated(instruction: Instr) -> i64:
    ensure not (instruction.ratio > 0.5) or result == 1
    return 0
`,
	}
	for name, src := range falseCases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtGoalAtomPrelude+src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false for some input and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
