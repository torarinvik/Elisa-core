//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func proofReportContainsSubject(report []ProofFact, want string) bool {
	for _, fact := range report {
		if strings.Contains(fact.Subject, want) {
			return true
		}
	}
	return false
}

func TestRecursiveEquationFallthroughReturnPaths(t *testing.T) {
	src := `
def inc(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 1
    decreases n
    if n == 0:
        return 1
    return inc(n - 1) + 1

def caller(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 1
    return inc(n)
`
	r := analyzeContractStrict(t, "recursive_fallthrough.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("fallthrough return-path equation should prove inc(n)==n+1, got: %v", errs)
	}
	if !proofReportContainsSubject(r.ProofReport, "recursive equation of inc (direct numeric)") {
		t.Fatalf("expected direct numeric recursive equation certificate in proof report, got: %+v", r.ProofReport)
	}
	if !proofReportContainsSubject(r.ProofReport, "recursive IH of inc (direct numeric)") {
		t.Fatalf("expected direct numeric recursive IH certificate in proof report, got: %+v", r.ProofReport)
	}
}

func TestRecursiveAffineBoundsThroughInduction(t *testing.T) {
	src := `
def add_const(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 4
    ensure result >= 4
    decreases n
    if n == 0:
        return 4
    return add_const(n - 1) + 1

def caller(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 4
    ensure result >= 4
    return add_const(n)
`
	r := analyzeContractStrict(t, "recursive_affine_bounds.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("recursive affine equality and lower bound should prove through induction, got: %v", errs)
	}
}

func TestMutualRecursiveEnsuresProveBoundedFact(t *testing.T) {
	src := `
def is_even(n: usize) -> i64:
    ensure result >= 0
    decreases n
    if n == 0:
        return 1
    return is_odd(n - 1)

def is_odd(n: usize) -> i64:
    ensure result >= 0
    decreases n
    if n == 0:
        return 0
    return is_even(n - 1)

def caller(n: usize) -> i64:
    ensure result >= 0
    return is_even(n)
`
	r := analyzeContractStrict(t, "mutual_even_bound.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("verified mutual recursion should expose bounded ensure facts, got: %v", errs)
	}
	if !proofReportContainsSubject(r.ProofReport, "mutual termination of is_even") {
		t.Fatalf("expected mutual termination certificate in proof report, got: %+v", r.ProofReport)
	}
	if !proofReportContainsSubject(r.ProofReport, "recursive IH of is_odd (mutual numeric)") {
		t.Fatalf("expected mutual numeric recursive IH certificate in proof report, got: %+v", r.ProofReport)
	}
}

func TestMutualRecursiveNonDecreasingEdgeRejected(t *testing.T) {
	src := `
def bad_even(n: usize) -> i64:
    ensure result >= 0
    decreases n
    if n == 0:
        return 1
    return bad_odd(n)

def bad_odd(n: usize) -> i64:
    ensure result >= 0
    decreases n
    if n == 0:
        return 0
    return bad_even(n)
`
	r := analyzeContractStrict(t, "mutual_bad_edge.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("mutual recursion with non-decreasing SCC edges must be rejected; proof report: %+v warnings: %v", r.ProofReport, r.Warnings())
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "ensure") && !strings.Contains(strings.Join(r.Errors(), "\n"), "decreases") {
		t.Fatalf("expected proof/termination diagnostics for bad mutual recursion, got: %v", r.Errors())
	}
}

func TestMutualRecursiveUnrelatedDecreasingArgRejected(t *testing.T) {
	src := `
def bad_a(n: usize, fuel: usize) -> i64:
    ensure result >= 0
    decreases n
    if n == 0:
        return 0
    return bad_b(n, fuel - 1)

def bad_b(n: usize, fuel: usize) -> i64:
    ensure result >= 0
    decreases n
    if n == 0:
        return 0
    return bad_a(n, fuel - 1)
`
	r := analyzeContractStrict(t, "mutual_unrelated_arg_decreases.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("mutual recursion must reject a decrease in an unrelated argument when the declared measure does not decrease")
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "decreases") {
		t.Fatalf("expected decreases diagnostic for unrelated decreasing arg, got: %v", r.Errors())
	}
}

func TestStructuralRecursiveEnumDecreasesAllowsChildCalls(t *testing.T) {
	src := `
enum Tree:
    Leaf(value: i64)
    Node(child: Tree)

def size(t: Tree) -> i64:
    ensure result >= 1
    decreases t
    match t:
        Tree.Leaf(value: v):
            return 1
        Tree.Node(child: c):
            return size(c)
`
	r := analyzeContractStrict(t, "structural_tree_size.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("structural recursion over match-bound children should verify, got: %v", errs)
	}
	if !proofReportContainsSubject(r.ProofReport, "structural termination of size") {
		t.Fatalf("expected structural termination certificate in proof report, got: %+v", r.ProofReport)
	}
	if !proofReportContainsSubject(r.ProofReport, "recursive IH of size (structural enum)") {
		t.Fatalf("expected structural recursive IH certificate in proof report, got: %+v", r.ProofReport)
	}
}

func TestStructuralRecursiveEnumBinaryTreeAffineBoundDeclinesWithoutOverflowProof(t *testing.T) {
	src := `
enum Tree:
    Leaf(value: i64)
    Node(left: Tree, right: Tree)

def size(t: Tree) -> i64:
    ensure result >= 1
    decreases t
    match t:
        Tree.Leaf(value: v):
            return 1
        Tree.Node(left: l, right: r):
            return size(l) + size(r) + 1
`
	r := analyzeContractStrict(t, "structural_binary_tree_size.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("structural recursive arithmetic over two child sizes must decline without an overflow/domain proof")
	}
	report := ""
	for _, fact := range r.ProofReport {
		report += fact.Subject + "\n"
	}
	if !strings.Contains(report, "recursive IH of size") {
		t.Fatalf("expected structural child IHs to be emitted before arithmetic proof declines, report: %+v", r.ProofReport)
	}
}

func TestStructuralRecursiveEnumSameValueHiddenCallRejected(t *testing.T) {
	src := `
enum Tree:
    Leaf(value: i64)
    Node(child: Tree)

def bad_size(t: Tree) -> i64:
    ensure result >= 1
    decreases t
    match t:
        Tree.Leaf(value: v):
            return 1
        Tree.Node(child: c):
            return bad_size(t) + 1
`
	r := analyzeContractStrict(t, "structural_tree_bad_hidden.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("structural decreases must reject a hidden recursive call on the original value")
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "structural `decreases`") {
		t.Fatalf("expected structural decreases diagnostic, got: %v", r.Errors())
	}
}

func TestStructuralRecursiveEnumNestedSameValueCallRejected(t *testing.T) {
	src := `
enum Tree:
    Leaf(value: i64)
    Node(child: Tree)

def bad_size(t: Tree) -> i64:
    ensure result >= 1
    decreases t
    match t:
        Tree.Leaf(value: v):
            return 1
        Tree.Node(child: c):
            match c:
                Tree.Leaf(value: v):
                    return bad_size(t)
                Tree.Node(child: gc):
                    return bad_size(gc)
`
	r := analyzeContractStrict(t, "structural_tree_nested_bad.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("structural decreases must reject nested recursive calls on the original value")
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "structural `decreases`") {
		t.Fatalf("expected structural decreases diagnostic, got: %v", r.Errors())
	}
}

func TestRecursiveRequiresGuardDoesNotLeakEnsure(t *testing.T) {
	src := `
def guarded_positive(n: i64) -> i64:
    requires n > 0
    ensure result > 0
    return n

def caller(n: i64) -> i64:
    ensure result > 0
    return guarded_positive(n)
`
	r := analyzeContractStrict(t, "recursive_requires_guard_no_leak.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("guarded callee ensure must not prove caller facts outside the callee requires domain")
	}
}

func TestRecursiveIHDeclinesWhenCalleeRequiresNotProven(t *testing.T) {
	src := `
def bad_count(n: i64) -> i64:
    requires n >= 0
    ensure result >= 0
    decreases n
    if n == 0:
        return 0
    return bad_count(n - 2) + 1
`
	r := analyzeContractStrict(t, "recursive_ih_requires_not_proven.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("recursive call outside callee requires domain must be rejected")
	}
	if !proofReportContainsSubject(r.ProofReport, "recursive proof declined for bad_count") {
		t.Fatalf("expected recursive proof declined diagnostic in proof report, got: %+v", r.ProofReport)
	}
}

func TestRecursiveRequiresGuardAllowsValidDomain(t *testing.T) {
	src := `
def guarded_positive(n: i64) -> i64:
    requires n > 0
    ensure result > 0
    return n

def caller(n: i64) -> i64:
    requires n > 0
    ensure result > 0
    return guarded_positive(n)
`
	r := analyzeContractStrict(t, "recursive_requires_guard_valid.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("guarded callee ensure should apply when caller proves the requires domain, got: %v", errs)
	}
}

func TestEmulatorStyleRecursiveDecodeProofFixture(t *testing.T) {
	src := `
def decode_prefix_slots(remaining: i64) -> i64:
    requires remaining >= 0
    ensure result >= 0
    decreases remaining
    if remaining == 0:
        return 0
    return decode_prefix_slots(remaining - 1) + 1

def decode_instruction_prefix_count(words: i64) -> i64:
    requires words >= 0
    ensure result >= 0
    return decode_prefix_slots(words)
`
	r := analyzeContractStrict(t, "emulator_decode_recursive_fixture.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("emulator-style recursive decode countdown should verify affine bounds through the IH, got: %v", errs)
	}
}

func TestStructuralRecursiveEnumSameValueCallRejected(t *testing.T) {
	src := `
enum Tree:
    Leaf(value: i64)
    Node(child: Tree)

def bad_size(t: Tree) -> i64:
    ensure result >= 1
    decreases t
    return bad_size(t)
`
	r := analyzeContractStrict(t, "structural_tree_bad.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("structural decreases must reject a recursive call on the original value")
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "structural `decreases`") {
		t.Fatalf("expected structural decreases diagnostic, got: %v", r.Errors())
	}
}
