package semantic

import (
	"elisacore/src/smt"
	"strings"
	"testing"
)

func TestSMTXorAbstractionRetainsOnlyCongruence(t *testing.T) {
	solver := openCOISolver(t, 20000)
	xor := func(left, right string) string {
		return "(bv2nat (bvxor ((_ int2bv 64) " + left + ") ((_ int2bv 64) " + right + ")))"
	}
	base := "(declare-const x Int)\n(declare-const y Int)\n(declare-const z Int)\n"
	for _, tc := range []struct {
		name, facts string
		want        smt.Result
	}{
		{"equal_inputs", "(assert (= x y))\n", smt.Unsat},
		{"unrelated_inputs", "", smt.Sat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := base + tc.facts + "(assert (not (= " + xor("x", "z") + " " + xor("y", "z") + ")))\n"
			abstracted, ok := smtAbstractBitvectorReads(query)
			if !ok || !strings.Contains(abstracted, "declare-fun bvread$xor$64") {
				t.Fatalf("missing congruence abstraction: %s", abstracted)
			}
			if got, _ := solver.Check(abstracted); got != tc.want {
				t.Fatalf("got%s want%s", got, tc.want)
			}
		})
	}
}

func TestSMTXorAbstractionDeclinesUnsupportedShapes(t *testing.T) {
	for _, term := range []string{
		"(bv2nat (bvxor ((_ int2bv 64) x y) ((_ int2bv 64) z)))",
		"(bv2nat (bvand ((_ int2bv 64) x) ((_ int2bv 64) y)))",
		"(bv2nat (bvxor ((_ int2bv 64) x) ((_ int2bv 8) y)))",
		"(bv2nat (bvxor ((_ int2bv 0) x) ((_ int2bv 0) y)))",
		"(bv2nat (bvxor ((_ int2bv 65) x) ((_ int2bv 65) y)))",
		"(bv2nat (bvxor ((_ int2bv 64) x) ((_ int2bv 64) y) ((_ int2bv 64) z)))",
		"(bv2nat (bvxor ((_ int2bv 64) (bv2nat ((_ int2bv 64) x))) ((_ int2bv 64) y)))",
	} {
		if _, _, ok := smtAbstractXorRead(term); ok {
			t.Fatalf("unsupported form admitted: %s", term)
		}
	}
}

func TestSMTXorAbstractionKeepsQuantifiedInputsScoped(t *testing.T) {
	solver := openCOISolver(t, 20000)
	read := func(x string) string { return "(bv2nat (bvxor ((_ int2bv 64) " + x + ") ((_ int2bv 64) 0)))" }
	query := "(assert (not (forall ((q_x Int) (q_y Int)) (=> (= q_x q_y) (= " + read("q_x") + " " + read("q_y") + ")))))\n"
	abstracted, ok := smtAbstractBitvectorReads(query)
	if !ok {
		t.Fatal("quantified congruence declined")
	}
	if got, _ := solver.Check(abstracted); got != smt.Unsat {
		t.Fatalf("scoped congruence: %s\n%s", got, abstracted)
	}
	falseQuery := "(assert (not (forall ((q_x Int)) (= " + read("q_x") + " q_x))))\n"
	abstracted, ok = smtAbstractBitvectorReads(falseQuery)
	if !ok {
		t.Fatal("quantified false-control declined")
	}
	if got, _ := solver.Check(abstracted); got != smt.Sat {
		t.Fatalf("unbounded identity must not prove: %s", got)
	}
	unsupported := strings.ReplaceAll(query, "bvxor", "bvand")
	if _, ok := smtAbstractBitvectorReads(unsupported); ok {
		t.Fatal("binder-dependent unsupported read must not become a constant")
	}
}
