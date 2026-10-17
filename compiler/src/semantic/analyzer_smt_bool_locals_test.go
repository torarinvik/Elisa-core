//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/smt"
)

func requireBoolLocalEnsurePasses(t *testing.T, name, source, function string) {
	t.Helper()
	result := analyzeContractStrict(t, name+".elisa", source)
	if failures := ensureFailuresFor(result.Errors(), function); failures != 0 {
		t.Fatalf("expected %q ensure to pass, got %v", function, result.Errors())
	}
}

func requireBoolLocalEnsureFails(t *testing.T, name, source, function string) {
	t.Helper()
	result := analyzeContractStrict(t, name+".elisa", source)
	if failures := ensureFailuresFor(result.Errors(), function); failures == 0 {
		t.Fatalf("expected %q ensure to remain unproven, got %v", function, result.Errors())
	}
}

func TestSMTBoolLocalInitializerEquations(t *testing.T) {
	t.Run("direct immutable bool", func(t *testing.T) {
		requireBoolLocalEnsurePasses(t, "bool_local_direct", `def direct(x: bool) -> bool:
	value: bool = x
	ensure result == x
	return value
`, "direct")
	})

	t.Run("reverse-name alias closure and mixed numeric inputs", func(t *testing.T) {
		requireBoolLocalEnsurePasses(t, "bool_local_alias_chain", `def chained(x: i64, gate: bool) -> bool:
	n: i64 = x + 1
	z_alias: bool = n > x
	a_alias: bool = z_alias and gate
	ensure result == gate
	return a_alias
`, "chained")
	})

	t.Run("numeric ternary depends on bool local", func(t *testing.T) {
		requireBoolLocalEnsurePasses(t, "bool_local_numeric_ternary", `def numeric_ternary(x: i64) -> bool:
	positive: bool = x > 0
	n: i64 = 1 if positive else 0
	ensure result == positive
	return n == 1
`, "numeric_ternary")
	})

	t.Run("bool local depends on numeric local", func(t *testing.T) {
		requireBoolLocalEnsurePasses(t, "bool_local_numeric_dependency", `def numeric_dependency(x: i64) -> bool:
	n: i64 = x + 1
	answer: bool = n > x
	ensure result == true
	return answer
`, "numeric_dependency")
	})

	t.Run("record field initializer", func(t *testing.T) {
		requireBoolLocalEnsurePasses(t, "bool_local_record_field", `struct Flags:
	ready: bool

def record_field(gate: bool) -> bool:
	flags: Flags = Flags{ready: gate}
	answer: bool = flags.ready
	ensure result == gate
	return answer
`, "record_field")
	})

	t.Run("aggregate result field from boolean local", func(t *testing.T) {
		requireBoolLocalEnsurePasses(t, "bool_local_aggregate_result", `struct Pair:
	ready: bool

def join(left: Pair, right: Pair) -> Pair:
	ensure result.ready == (left.ready or right.ready)
	reachable: bool = left.ready or right.ready
	return Pair{ready: reachable}
`, "join")
	})
}

func TestSMTBoolLocalEquationsDoNotOverclaim(t *testing.T) {
	t.Run("wrong initializer", func(t *testing.T) {
		requireBoolLocalEnsureFails(t, "bool_local_wrong_initializer", `def wrong(x: bool) -> bool:
	ensure result == x
	answer: bool = not x
	return answer
`, "wrong")
	})

	t.Run("mutable reference changes after capture", func(t *testing.T) {
		requireBoolLocalEnsureFails(t, "bool_local_mutated_input", `def stale(x: mutable bool&) -> bool:
	requires x
	ensure result == false
	before: bool = x
	x <- false
	return before
`, "stale")
	})

	t.Run("shadowing self initializer", func(t *testing.T) {
		requireBoolLocalEnsureFails(t, "bool_local_shadow_self_initializer", `def shadowed(x: bool) -> bool:
	ensure result == true
	if x:
		x: bool = not x
		return x
	return true
`, "shadowed")
	})

	t.Run("reference-bearing record projection", func(t *testing.T) {
		requireBoolLocalEnsureFails(t, "bool_local_reference_record", `struct RefRecord:
	flag: mutable bool&

def unrelated(record: RefRecord, value: bool) -> bool:
	ensure result == value
	answer: bool = record.flag
	return answer
`, "unrelated")
	})

	t.Run("legacy flattened-name spelling remains distinct", func(t *testing.T) {
		requireBoolLocalEnsureFails(t, "bool_local_old_field_spelling", `struct P:
	flag: bool

def distinct(p: P, p__field__flag: bool) -> bool:
	ensure result == p.flag
	answer: bool = p__field__flag
	return answer
`, "distinct")
	})
}

func TestSMTBoolLocalMacroPreambleOrder(t *testing.T) {
	tr := &smtTranslator{
		a:            &Analyzer{},
		decls:        map[string]bool{},
		boolDecls:    map[string]bool{"b_local": true, "b_input": true},
		boolDefs:     map[string]string{"b_local": "(= b_input true)"},
		boolDefOrder: []string{"b_local"},
		auxDecls: []string{
			"(declare-const aux_1 Int)\n",
			"(assert (= aux_1 b_local))\n",
		},
	}
	preamble := tr.factPreamble()
	auxDecl := strings.Index(preamble, "(declare-const aux_1 Int)")
	definition := strings.Index(preamble, "(define-fun b_local () Bool")
	auxAssertion := strings.Index(preamble, "(assert (= aux_1 b_local))")
	if auxDecl < 0 || definition < 0 || auxAssertion < 0 || !(auxDecl < definition && definition < auxAssertion) {
		t.Fatalf("expected aux declaration, bool macro, then aux assertion; preamble:\n%s", preamble)
	}
	if strings.Contains(preamble, "(declare-const b_local Bool)") {
		t.Fatalf("macro local was also declared as a free Boolean constant:\n%s", preamble)
	}
	if context := smtPreambleContext(preamble); strings.Contains(context, "b_input") {
		t.Fatalf("macro definition unexpectedly made its dependencies query-reachable: %s", context)
	}
}

func TestSMTViewLengthEquationPreambleOrder(t *testing.T) {
	tr := &smtTranslator{
		a:        &Analyzer{},
		lenDecls: map[string]bool{"view_len": true},
		enumDecls: map[string]string{
			"~slice_len":        "(assert (= view_len (ite b_local aux_1 0)))\n",
			"tag":               "(declare-const tag Int)\n(assert (= tag 0))\n",
			"~binder_slice_len": "(assert (= view_len q_index))\n",
		},
		boolDecls:    map[string]bool{"b_local": true},
		boolDefs:     map[string]string{"b_local": "(>= aux_1 0)"},
		boolDefOrder: []string{"b_local"},
		auxDecls:     []string{"(declare-const aux_1 Int)\n"},
	}
	preamble := tr.factPreamble()
	if strings.Contains(preamble, "q_index") {
		t.Fatalf("binder-dependent view equation escaped to top level:\n%s", preamble)
	}
	decl := strings.Index(preamble, "(declare-const aux_1 Int)")
	macro := strings.Index(preamble, "(define-fun b_local () Bool")
	equation := strings.Index(preamble, "(assert (= view_len (ite b_local aux_1 0)))")
	if decl < 0 || macro < 0 || equation < 0 || !(decl < macro && macro < equation) {
		t.Fatalf("view-length equation precedes its dependencies:\n%s", preamble)
	}
	solver := openCOISolver(t, 100_000)
	if verdict, _ := solver.Check(preamble + "(assert (= aux_1 3))\n"); verdict != smt.Sat {
		t.Fatalf("valid view-length query should be satisfiable, got %v", verdict)
	}
	if verdict, _ := solver.Check(preamble + "(assert (= aux_1 3))\n(assert (distinct view_len 3))\n"); verdict != smt.Unsat {
		t.Fatalf("view-length equation should prove length 3, got %v", verdict)
	}
}

func TestSMTBoolMacroContextReachesNumericLocals(t *testing.T) {
	tr := &smtTranslator{boolDefs: map[string]string{
		"b_answer":    "(> v_n v_x)",
		"b_unrelated": "(= v_secret 0)",
	}}
	context := smtBoolMacroContext(tr, "b_answer")
	locals := "(assert (= v_n (+ v_x 1)))\n(assert (= v_secret 3))\n"
	sliced := sliceImmutableLocalHypotheses(locals, context)
	if !strings.Contains(sliced, "v_n") {
		t.Fatalf("reachable Bool macro did not retain its numeric local equation: %s", sliced)
	}
	if strings.Contains(sliced, "v_secret") {
		t.Fatalf("unreachable Bool macro retained an unrelated numeric local equation: %s", sliced)
	}
}
