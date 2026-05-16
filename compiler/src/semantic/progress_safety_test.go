package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeProgressSafetyTestSourceAllowingErrors(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return AnalyzeWithOptions(file, AnalyzeOptions{EnforceProgressSafety: true})
}

func TestProgressSafetyWarnsForUnbudgetedWhileLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_unbudgeted_while.elisa", `
def spin(flag: bool) -> void:
    while flag:
        pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: while loop has no progress evidence") {
		t.Fatalf("expected unbudgeted while-loop progress warning, got:\n%s", all)
	}
}

func TestProgressSafetyAllowsLoopWithProgressTickEvidence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_tick_while.elisa", `
def spin(flag: bool) -> void:
    while flag:
        can Progress.Tick:
            signal Progress.Tick
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected Progress.Tick to discharge loop progress obligation, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("spin")
	if !ok {
		t.Fatal("expected spin symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected spin function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Progress.Tick]" {
		t.Fatalf("expected visible Progress.Tick permission, got %q", got)
	}
}

func TestProgressSafetyRequiresLoopLocalEvidence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_tick_outside_while.elisa", `
def spin(flag: bool) -> void:
    can Progress.Tick:
        signal Progress.Tick
    while flag:
        pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: while loop has no progress evidence") {
		t.Fatalf("expected progress evidence outside loop not to discharge loop obligation, got:\n%s", all)
	}
}

func TestProgressSafetyAllowsTrustedIntentionalNonProgressLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_trusted_nonprogress_while.elisa", `
def spin(flag: bool) -> void:
    trusted Unsafe.NonProgress:
        while flag:
            pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected trusted Unsafe.NonProgress to discharge loop locally, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("spin")
	if !ok {
		t.Fatal("expected spin symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected spin function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted non-progress implementation detail not to infer caller permissions, got %q", got)
	}
}

func TestProgressSafetyAllowsTrustedAssumeProgressLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_trusted_assume_progress_while.elisa", `
def walk(flag: bool) -> void:
    trusted Unsafe.AssumeProgress:
        while flag:
            pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected trusted Unsafe.AssumeProgress to discharge loop locally, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("walk")
	if !ok {
		t.Fatal("expected walk symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected walk function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted progress proof not to infer caller permissions, got %q", got)
	}
}

func TestProgressSafetyWarnsForInfiniteRecursionPressureCase(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_recursive_cycle.elisa", `
def ping() -> void:
    pong()

def pong() -> void:
    ping()
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: recursive cycle") {
		t.Fatalf("expected recursive-cycle progress warning, got:\n%s", all)
	}
}

func TestProgressSafetyAllowsRecursiveCycleWithProgressEvidence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_recursive_cycle_budgeted.elisa", `
def ping() -> void:
    can Progress.EnterRecursion:
        signal Progress.EnterRecursion
    pong()

def pong() -> void:
    ping()
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected Progress.EnterRecursion to discharge recursive-cycle progress obligation, got:\n%s", all)
	}
}

func TestProgressSafetyWarnsForBlockingOperation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_blocking_call.elisa", `
extern wait_for_worker() -> void can[Blocking.Wait]

def on_click() -> void:
    wait_for_worker()
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: function may block via Blocking.* permission") {
		t.Fatalf("expected blocking-call progress warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("on_click")
	if !ok {
		t.Fatal("expected on_click symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected on_click function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Blocking.Wait]" {
		t.Fatalf("expected visible Blocking.Wait permission, got %q", got)
	}
}

func TestProgressSafetyErrorsForMainThreadBlockingOperation(t *testing.T) {
	result := analyzeProgressSafetyTestSourceAllowingErrors(t, "progress_main_thread_blocking_call.elisa", `
extern wait_for_worker() -> void can[Blocking.Wait]

@main_thread
def on_click() -> void:
    wait_for_worker()
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "progress error: @main_thread function may block via Blocking.* permission") {
		t.Fatalf("expected main-thread blocking progress error, got:\n%s", allErrors)
	}
	sym, ok := result.GlobalScope.Lookup("on_click")
	if !ok {
		t.Fatal("expected on_click symbol")
	}
	fn, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected on_click function declaration, got %T", sym.Node)
	}
	summary := result.ProgressSummaries[fn]
	if summary == nil || !summary.MainThread || !summary.HasBlocking || summary.HasUnsafeBlockMain {
		t.Fatalf("expected main-thread blocking summary without escape hatch, got %#v", summary)
	}
}

func TestProgressSafetyReportsTransitiveBlockingPath(t *testing.T) {
	result := analyzeProgressSafetyTestSourceAllowingErrors(t, "progress_main_thread_transitive_blocking_call.elisa", `
extern wait_for_worker() -> void can[Blocking.Wait]

def wait_for_compile() -> void:
    wait_for_worker()

@main_thread
def on_click() -> void:
    wait_for_compile()
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "path: on_click -> wait_for_compile -> wait_for_worker") {
		t.Fatalf("expected transitive main-thread blocking path, got:\n%s", allErrors)
	}
	sym, ok := result.GlobalScope.Lookup("on_click")
	if !ok {
		t.Fatal("expected on_click symbol")
	}
	fn := sym.Node.(*ast.FuncDecl)
	summary := result.ProgressSummaries[fn]
	if summary == nil || strings.Join(summary.BlockingPath, " -> ") != "on_click -> wait_for_compile -> wait_for_worker" {
		t.Fatalf("expected transitive blocking path in summary, got %#v", summary)
	}
}

func TestProgressSafetyAllowsTrustedBlockMainEscapeHatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_blocking_call_trusted.elisa", `
extern wait_for_worker() -> void can[Blocking.Wait]

@main_thread
def on_click() -> void:
    trusted Unsafe.BlockMain:
        wait_for_worker()
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected trusted Unsafe.BlockMain to acknowledge blocking risk, got:\n%s", all)
	}
	allErrors := strings.Join(result.Errors(), "\n")
	if strings.Contains(allErrors, "progress error") {
		t.Fatalf("expected trusted Unsafe.BlockMain to avoid main-thread blocking error, got:\n%s", allErrors)
	}
	sym, ok := result.GlobalScope.Lookup("on_click")
	if !ok {
		t.Fatal("expected on_click symbol")
	}
	fn, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected on_click function declaration, got %T", sym.Node)
	}
	summary := result.ProgressSummaries[fn]
	if summary == nil || !summary.MainThread || !summary.HasBlocking || !summary.HasUnsafeBlockMain {
		t.Fatalf("expected blocking and Unsafe.BlockMain in progress summary, got %#v", summary)
	}
}

func TestProgressSafetyTreatsBlockingExternAnnotationAsBlocking(t *testing.T) {
	result := analyzeProgressSafetyTestSourceAllowingErrors(t, "progress_blocking_extern_annotation.elisa", `
@blocking
extern wait_for_worker() -> void

@main_thread
def on_click() -> void:
    wait_for_worker()
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "progress error: @main_thread function may block via Blocking.* permission") {
		t.Fatalf("expected @blocking extern to trigger main-thread blocking error, got:\n%s", allErrors)
	}
	if !strings.Contains(allErrors, "path: on_click -> wait_for_worker") {
		t.Fatalf("expected @blocking extern call path, got:\n%s", allErrors)
	}
	sym, ok := result.GlobalScope.Lookup("wait_for_worker")
	if !ok {
		t.Fatal("expected wait_for_worker symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected wait_for_worker function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Blocking.RawExtern]" {
		t.Fatalf("expected @blocking to add Blocking.RawExtern permission, got %q", got)
	}
}

func TestProgressSafetyAllowsNonblockingExternAnnotation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_nonblocking_extern_annotation.elisa", `
@nonblocking
extern monotonic_time() -> i64

@main_thread
def on_click() -> i64:
    return monotonic_time()
`, AnalyzeOptions{EnforceProgressSafety: true})

	if all := strings.Join(result.Errors(), "\n"); strings.Contains(all, "progress error") {
		t.Fatalf("expected @nonblocking extern to avoid main-thread blocking error, got:\n%s", all)
	}
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "progress warning") {
		t.Fatalf("expected @nonblocking extern to avoid progress warning, got:\n%s", all)
	}
}
