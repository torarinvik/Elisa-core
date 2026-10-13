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

func TestBorrowedNurseryStoreFlowProofCoversJoinedUseAfterExit(t *testing.T) {
	const runtimeSource = "runtime/elisacore_std/elisacore_runtime_concurrency.elisa"
	source := submitStoreCapturePrelude + `
def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(e)

def run(pool: mutable ThreadPool&) -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        nursery on(pool):
            submit worker(e)
        return eval(e)
`
	l := lexer.New(runtimeSource, []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile(runtimeSource)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	// Use the loader's explicit trusted-path setting so this covers the
	// generated TaskGroup shape used by `nursery on(pool)`.
	result := AnalyzeWithOptions(file, AnalyzeOptions{TrustedRuntimeSourceFiles: []string{runtimeSource}})
	if len(result.TaskGroupSubmits) != 1 {
		t.Fatalf("borrowed nursery must pre-record its joined submit, got %d proofs", len(result.TaskGroupSubmits))
	}
	got := strings.Join(result.Errors(), "\n")
	if strings.Contains(got, "call may store a reference to function-local storage") || strings.Contains(got, "shares the submitting scope's Node store") {
		t.Fatalf("a joined store-reading worker must not escape through the borrowed pool, got:\n%s", got)
	}
}

func TestJoinedNurseryPoolDoesNotRetainHelperPayloadAfterReturn(t *testing.T) {
	const runtimeSource = "runtime/elisacore_std/elisacore_runtime_concurrency.elisa"
	source := submitStoreCapturePrelude + `
def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(e)

def par_pass_run(e: Expr, flags: bool, first: u32, count: u32, pool: mutable ThreadPool&) -> void:
    can Memory.Allocate, Parallel, Abort.Panic:
        nursery on(pool):
            submit worker(e)

def run(pool: mutable ThreadPool&) -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        par_pass_run(e, true, 0, 1, pool)
        return eval(e)
`
	l := lexer.New(runtimeSource, []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile(runtimeSource)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{TrustedRuntimeSourceFiles: []string{runtimeSource}, RecordDeathTimeCohorts: true})
	if len(result.TaskGroupSubmits) != 1 {
		t.Fatalf("helper nursery must pre-record its joined submit, got %d proofs", len(result.TaskGroupSubmits))
	}
	var helperRetention []bool
	for helper, retention := range result.analyzer.paramRetained {
		if helper.Name == "par_pass_run" {
			helperRetention = retention
			break
		}
	}
	if len(helperRetention) != 5 {
		t.Fatalf("expected a five-parameter retention summary for par_pass_run, got %v", helperRetention)
	}
	if helperRetention[4] {
		t.Fatalf("a pool used only by a verified joined nursery must not be retained after par_pass_run returns, got %v", helperRetention)
	}
	got := strings.Join(result.Errors(), "\n")
	if strings.Contains(got, "through argument 5") || strings.Contains(got, "call may store a reference to function-local storage") {
		t.Fatalf("the helper's joined worker must not retain its payload through pool argument 5 after return, got:\n%s", got)
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
