package semantic_test

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func requireFunctionType(t *testing.T, result *semantic.Result, name string) *semantic.FuncType {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected %s to be a function, got %T", name, sym.Type)
	}
	return fnType
}

func requireFunctionDecl(t *testing.T, result *semantic.Result, name string) *ast.FuncDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected %s decl to be a function, got %T", name, sym.Node)
	}
	return decl
}

func TestAnalyzeInfersSinkParamsAndAllowsImplicitSinkCalls(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]


def take(thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]:
	return join(move thread)


def branch_take(flag: bool, thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]:
	if flag:
		return take(thread)
	return take(thread)


def run(flag: bool, thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]:
	result: i64 = branch_take(flag, thread)
	return result
`
	result, errs := parseAndAnalyze(t, "sink_inference_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	takeType := requireFunctionType(t, result, "take")
	if !takeType.SinkParamsKnown || len(takeType.SinkParams) != 1 || !takeType.SinkParams[0] {
		t.Fatalf("expected take to infer a sink first parameter, got %#v", takeType.SinkParams)
	}
	branchType := requireFunctionType(t, result, "branch_take")
	if !branchType.SinkParamsKnown || len(branchType.SinkParams) != 2 || !branchType.SinkParams[1] {
		t.Fatalf("expected branch_take to infer a sink thread parameter, got %#v", branchType.SinkParams)
	}
	analysis, ok := result.FunctionAnalysisByName("branch_take")
	if !ok || analysis == nil || analysis.CFG == nil || len(analysis.CFG.Blocks) < 3 {
		t.Fatalf("expected branch_take to expose a non-trivial CFG-backed analysis, got %#v", analysis)
	}
}

func TestAnalyzeRejectsImplicitSinkCallsWhenParamIsNotConsumedOnAllPaths(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]


def maybe_take(flag: bool, thread: Thread[i64, Joinable]) -> i64:
	if flag:
		return join(move thread)
	return 0


def run(flag: bool, thread: Thread[i64, Joinable]) -> i64:
	return maybe_take(flag, thread)
`
	_, errs := parseAndAnalyze(t, "sink_inference_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected implicit sink call to be rejected when the callee does not consume on all paths")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "must be moved explicitly") {
		t.Fatalf("expected explicit-move diagnostic, got:\n%s", joined)
	}
}

func TestAnalyzeRecordsReturnIsolationSummary(t *testing.T) {
	src := `def borrow_ref(slot: mutable any i32&) -> mutable any i32&:
	slot[0] <- 3
	return slot
`
	result, errs := parseAndAnalyze(t, "return_isolation_summary.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fnType := requireFunctionType(t, result, "borrow_ref")
	if !fnType.ReturnIsolationKnown {
		t.Fatal("expected borrow_ref to record return isolation")
	}
	if fnType.ReturnIsolation.Isolated {
		t.Fatalf("expected borrow_ref return to alias its input, got %#v", fnType.ReturnIsolation)
	}
	if !fnType.ReturnIsolation.CanAlias(0) {
		t.Fatalf("expected borrow_ref return to alias parameter 0, got %#v", fnType.ReturnIsolation)
	}
	if !fnType.ReturnIsolation.AliasesMutableParam(0) {
		t.Fatalf("expected borrow_ref return to report aliasing a mutated parameter, got %#v", fnType.ReturnIsolation)
	}
}

func TestAnalyzeRecordsAliasPartitionsAndGuardedCFGEdges(t *testing.T) {
	src := `repr(c) struct Box:
	value: i32


def alias_flow(seed: i32, box: heap Box&?) -> i32:
	region scratch(1024)
	left: mutable scratch i32& = new[scratch] seed
	alias: mutable scratch i32& = left
	alias[0] <- seed
	if box == null:
		return left[0]
	return left[0] + box.value
`
	result, errs := parseAndAnalyze(t, "function_analysis_cfg_alias.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	analysis, ok := result.FunctionAnalysisByName("alias_flow")
	if !ok || analysis == nil {
		t.Fatal("expected alias_flow function analysis")
	}
	if analysis.Partitions == nil || !analysis.Partitions.SameClass("left", "alias") {
		t.Fatalf("expected left and alias to share an alias partition, got %#v", analysis.Partitions)
	}
	if !analysis.Partitions.ClassMutated("left") {
		t.Fatalf("expected left alias class to be marked mutated, got %#v", analysis.Partitions)
	}
	decl := requireFunctionDecl(t, result, "alias_flow")
	cfg := semantic.ConstructCFG(decl)
	if len(cfg.Blocks) < 3 {
		t.Fatalf("expected alias_flow CFG to contain branching structure, got %d blocks", len(cfg.Blocks))
	}
	var sawNullGuard bool
	var sawNonNullGuard bool
	for _, edge := range cfg.Blocks[cfg.Entry].Edges {
		if edge.Guard.ProvesNull(&ast.Ident{Name: "box"}) {
			sawNullGuard = true
		}
		if edge.Guard.ProvesNonNull(&ast.Ident{Name: "box"}) {
			sawNonNullGuard = true
		}
	}
	if !sawNullGuard || !sawNonNullGuard {
		t.Fatalf("expected entry CFG edges to carry null/non-null guards, got %#v", cfg.Blocks[cfg.Entry].Edges)
	}
}
