package semantic

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

func TestTaskGroupStoreFlowProofPrecedesDeclarationOrder(t *testing.T) {
	const runtimeSource = "runtime/elisacore_std/elisacore_runtime_concurrency.elisa"
	source := submitStoreCapturePrelude + `
def main(pool: mutable ThreadPool&) -> i64:
    can Memory.Allocate, Abort.Panic:
        e: Expr = build(10)
        group: mutable TaskGroup = task_group_new()
        for i in 0..<1:
            task_group_add(&group, submit[pool] worker(e))
        task_group_wait_all(&group)
        return eval(e)

def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(e)
`
	l := lexer.New(runtimeSource, []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile(runtimeSource)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	// This allowlist is the semantic equivalent of the compiler loader marking
	// an actually expanded runtime source; Position.File by itself grants no trust.
	result := AnalyzeWithOptions(file, AnalyzeOptions{TrustedRuntimeSourceFiles: []string{runtimeSource}})

	if len(result.TaskGroupSubmits) != 1 {
		t.Fatalf("caller-before-worker declaration order must pre-record the joined submit, got %d proofs", len(result.TaskGroupSubmits))
	}
	for call := range result.TaskGroupSubmits {
		if name := callIdentName(call); name != "pool_submit1" {
			t.Fatalf("TaskGroup proof attached to %q, want pool_submit1", name)
		}
	}
	got := strings.Join(result.Errors(), "\n")
	if strings.Contains(got, "call may store a reference to function-local storage") || strings.Contains(got, "shares the submitting scope's Node store") {
		t.Fatalf("a joined store-reading worker must not escape through the pool, got:\n%s", got)
	}
}

func TestTaskGroupStoreFlowProofRejectsUntrustedRuntimeLookingPath(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "vendor/runtime/elisacore_std/elisacore_runtime_concurrency.elisa", submitStoreCapturePrelude+`
def main(pool: mutable ThreadPool&) -> i64:
    can Memory.Allocate, Abort.Panic:
        e: Expr = build(10)
        group: mutable TaskGroup = task_group_new()
        for i in 0..<1:
            task_group_add(&group, submit[pool] worker(e))
        task_group_wait_all(&group)
        return eval(e)

def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(e)
`)
	if len(result.TaskGroupSubmits) != 0 {
		t.Fatalf("a runtime-looking path must not trust a source stub, got %d TaskGroup proofs", len(result.TaskGroupSubmits))
	}
}

func TestUnjoinedPoolSubmitHasNoTaskGroupStoreFlowExemption(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "unjoined_pool_submit.elisa", `extern pool_submit1[A, R, permission P](pool: mutable ThreadPool&, fn: fn(A) -> R can[P], arg: A) -> Task[R, Pending]
def first(s: sview) -> i64:
    return s[0].i64()

def main(pool: mutable ThreadPool&) -> i64:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        v: sview = buf.as_sview()
        task: Task[i64, Pending] = submit[pool] first(v)
        return first(v)
`)

	if len(result.TaskGroupSubmits) != 0 {
		t.Fatalf("an unjoined submit must not receive the post-join store-flow exemption, got %d proofs", len(result.TaskGroupSubmits))
	}
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, "value escapes its `in auto:` scope via store into longer-lived storage through argument 1") {
		t.Fatalf("an unjoined submit through a caller-owned pool must retain the borrow-escape diagnostic, got:\n%s", got)
	}
}
