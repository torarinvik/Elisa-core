package semantic_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func parseAndAnalyze(t *testing.T, filename string, src string) (*semantic.Result, []string) {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		return nil, errs
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		return nil, errs
	}
	result := semantic.Analyze(file)
	return result, result.Errors()
}

func requireNoErrors(t *testing.T, errs []string) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

func requireNoWarnings(t *testing.T, result *semantic.Result) {
	t.Helper()
	if warns := result.Warnings(); len(warns) != 0 {
		t.Fatalf("expected no warnings, got:\n%s", strings.Join(warns, "\n"))
	}
}

func requireDeclaredFunctionPermissionRefs(t *testing.T, result *semantic.Result, name string, expected ...string) {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected %s to be a function, got %T", name, sym.Type)
	}
	got := make([]string, 0, len(fn.DeclaredPermissionRefs))
	for _, ref := range fn.DeclaredPermissionRefs {
		got = append(got, semantic.PermissionRefString(ref))
	}
	want := append([]string(nil), expected...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %s declared permissions %v, got %v", name, expected, got)
	}
}

func requireFunctionReturnTypeString(t *testing.T, result *semantic.Result, name string, expected string) {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected %s to be a function, got %T", name, sym.Type)
	}
	if got := fn.Return.String(); got != expected {
		t.Fatalf("expected %s return type %q, got %q", name, expected, got)
	}
}

func requireFuncDecl(t *testing.T, result *semantic.Result, name string) *ast.FuncDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected %s to resolve to a function declaration, got %T", name, sym.Node)
	}
	return decl
}

func requireConstDecl(t *testing.T, result *semantic.Result, name string) *ast.ConstDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected %s to resolve to a const declaration, got %T", name, sym.Node)
	}
	return decl
}

func requireGlobalDecl(t *testing.T, result *semantic.Result, name string) *ast.GlobalDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.GlobalDecl)
	if !ok {
		t.Fatalf("expected %s to resolve to a global declaration, got %T", name, sym.Node)
	}
	return decl
}

func requireExprTypeString(t *testing.T, result *semantic.Result, expr ast.Expr, expected string) {
	t.Helper()
	typ, ok := result.ExprTypes[expr]
	if !ok || typ == nil {
		t.Fatalf("expected expression %T to have resolved type %q", expr, expected)
	}
	if got := typ.String(); got != expected {
		t.Fatalf("expected expression %T type %q, got %q", expr, expected, got)
	}
}

func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

func loadSourceWithIncludes(t *testing.T, filename string, seen map[string]bool) string {
	t.Helper()
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", filename, err)
	}
	if seen[abs] {
		t.Fatalf("cyclic include detected for %s", abs)
	}
	seen[abs] = true
	defer delete(seen, abs)

	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("failed to read %s: %v", abs, err)
	}

	lines := strings.Split(string(raw), "\n")
	var out strings.Builder
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if includePath, ok := parseIncludeDirective(trimmed); ok {
			out.WriteString(loadSourceWithIncludes(t, filepath.Join(filepath.Dir(abs), includePath), seen))
			if out.Len() == 0 || !strings.HasSuffix(out.String(), "\n") {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func parseIncludeDirective(line string) (string, bool) {
	if !strings.HasPrefix(line, "# include ") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "# include "))
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", false
	}
	return rest[1 : len(rest)-1], true
}

func TestAnalyzeValidInlineProgram(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern make_box() -> any Box&?

def read_box() -> int:
	box: mutable any Box&? = make_box()
    if box == null:
        return 0
    box.value <- 7
    return box.value
`
	_, errs := parseAndAnalyze(t, "inline_valid.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeUndefinedIdentifier(t *testing.T) {
	src := `def bad() -> int:
    return missing
`
	_, errs := parseAndAnalyze(t, "undefined_ident.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(errs[0], "undefined identifier") {
		t.Fatalf("expected undefined identifier error, got %q", errs[0])
	}
}

func TestAnalyzeWrongCallArity(t *testing.T) {
	src := `extern alloc(size: usize) -> int

def use_alloc() -> int:
    return alloc()
`
	_, errs := parseAndAnalyze(t, "wrong_arity.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects 1 arguments, got 0") {
		t.Fatalf("expected arity diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsFunctionTypeParametersAndHigherOrderCalls(t *testing.T) {
	src := `def apply_identity[T](fn: func(T) -> T, value: T) -> T:
    return fn(value)

def bump(value: int) -> int:
    return value + 1

def run() -> int:
	return apply_identity(bump, 41)
`
	result, errs := parseAndAnalyze(t, "higher_order_function_types.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "apply_identity", "T")
	requireFunctionReturnTypeString(t, result, "run", "int")
}

func TestAnalyzeAcceptsAggregateStateStructTypes(t *testing.T) {
	src := `struct Holder[?]:
    value: i32

def widen(value: Holder[&]) -> Holder:
    return value

def read(value: Holder[&]) -> i32:
    return value.value
`
	result, errs := parseAndAnalyze(t, "aggregate_state_structs.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "widen", "Holder[?]")
	requireFunctionReturnTypeString(t, result, "read", "i32")
}

func TestAnalyzeAcceptsMultiAggregateStateStructTypes(t *testing.T) {
	src := `struct Holder[?, ?]:
    value: i32

def widen(value: Holder[&, !]) -> Holder:
    return value

def read(value: Holder[&, ?]) -> i32:
    return value.value
`
	result, errs := parseAndAnalyze(t, "aggregate_state_structs_multi.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "widen", "Holder[?, ?]")
	requireFunctionReturnTypeString(t, result, "read", "i32")
}

func TestAnalyzeRejectsAggregateStateArityMismatch(t *testing.T) {
	src := `struct Pair[?, ?]:
    value: i32

def bad(value: Pair[&]) -> Pair[&]:
    return value
`
	_, errs := parseAndAnalyze(t, "aggregate_state_arity_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects 2 aggregate state arguments, got 1") {
		t.Fatalf("expected aggregate state arity diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAggregateStateOnPlainStruct(t *testing.T) {
	src := `struct Plain:
    value: i32

def bad(value: Plain[&]) -> Plain[&]:
    return value
`
	_, errs := parseAndAnalyze(t, "aggregate_state_plain_struct_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "does not declare an aggregate state parameter") {
		t.Fatalf("expected aggregate state diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeFunctionTypePermissionsParticipateInMatching(t *testing.T) {
	src := `extern puts(text: any u8&) -> int can[Console.Write]

def invoke_writer(fn: func(any u8&) -> int can[Console.Write], text: any u8&) -> int can[Console.Write]:
    return fn(text)

def run() -> int can[Console.Write]:
    return invoke_writer(puts, "hello".cast[any u8&]())
`
	result, errs := parseAndAnalyze(t, "function_type_permissions.llcontext", src)
	requireNoErrors(t, errs)
	requireFunctionReturnTypeString(t, result, "invoke_writer", "int")
}

func TestAnalyzeAcceptsPermissionPolymorphicFunctionWrappers(t *testing.T) {
	src := `extern puts(text: any u8&) -> int can[Console.Write]

def invoke_writer[permission P](fn: func(any u8&) -> int can[P], text: any u8&) -> int can[P]:
    return fn(text)

def run() -> int can[Console.Write]:
    return invoke_writer(puts, "hello".cast[any u8&]())
`
	result, errs := parseAndAnalyze(t, "permission_polymorphic_function_wrapper.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "invoke_writer", "int")
}

func TestAnalyzeRejectsPermissionParamMemberAccess(t *testing.T) {
	src := `def bad[permission P](fn: func() -> void can[P.Write]) -> void:
    fn()
`
	_, errs := parseAndAnalyze(t, "permission_param_member_access_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "permission parameter \"P\" does not support member access") {
		t.Fatalf("expected permission-parameter member-access diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsFunctionValueErasureCasts(t *testing.T) {
	src := `def inc(value: i64) -> i64:
    return value + 1

def call_erased(raw: any void&, value: i64) -> i64:
    fn: func(i64) -> i64 = raw.cast[func(i64) -> i64]()
    return fn(value)

def run() -> i64:
    raw: any void& = inc.cast[any void&]()
    bits: uintptr = raw.cast[uintptr]()
    fn: func(i64) -> i64 = bits.cast[func(i64) -> i64]()
    return call_erased(fn.cast[any void&](), 40)
`
	result, errs := parseAndAnalyze(t, "function_value_erasure_casts.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "call_erased", "i64")
	requireFunctionReturnTypeString(t, result, "run", "i64")
}

func TestAnalyzeAcceptsExplicitGenericFunctionSpecializationValues(t *testing.T) {
	src := `def id[T](value: T) -> T:
    return value

def apply_i64(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run() -> i64:
    fn: func(i64) -> i64 = id.specialize[i64]()
    return apply_i64(fn, 7)
`
	result, errs := parseAndAnalyze(t, "explicit_generic_function_specialization.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "apply_i64", "i64")
	requireFunctionReturnTypeString(t, result, "run", "i64")
}

func TestAnalyzeAcceptsRefQualifierGenericFunctionInference(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32

struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

def keep_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

def unwrap_handle(value: Handle[heap, &]) -> heap Node&:
	kept: Handle[heap, &] = keep_handle(value)
	return kept.ptr
`
	result, errs := parseAndAnalyze(t, "ref_qualifier_generic_inference.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "keep_handle", "Handle[Store, State]")
	requireFunctionReturnTypeString(t, result, "unwrap_handle", "heap Node&")
}

func TestAnalyzeRejectsSpecializationOfNonGenericFunction(t *testing.T) {
	src := `def id(value: i64) -> i64:
    return value

def run() -> i64:
    fn: func(i64) -> i64 = id.specialize[i64]()
    return fn(7)
`
	_, errs := parseAndAnalyze(t, "specialize_non_generic_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "function \"id\" is not generic") {
		t.Fatalf("expected non-generic specialization diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsReusingConsumedThreadHandle(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern detach(thread: Thread[i64, Joinable]) -> void

def bad(thread: Thread[i64, Joinable]) -> void:
    value: i64 = join(thread)
    _ = value
    detach(thread)
`
	_, errs := parseAndAnalyze(t, "consumed_thread_handle_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" must be moved explicitly before argument to call \"join\"") {
		t.Fatalf("expected explicit-move diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsAffineThreadMovesAcrossBranches(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def move_then_join(thread: Thread[i64, Joinable]) -> i64:
    moved: Thread[i64, Joinable] = move thread
    return join(move moved)

def branch_join(cond: bool, thread: Thread[i64, Joinable]) -> i64:
    if cond:
        return join(move thread)
    return join(move thread)
`
	result, errs := parseAndAnalyze(t, "affine_thread_moves.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "move_then_join", "i64")
	requireFunctionReturnTypeString(t, result, "branch_join", "i64")
}

func TestAnalyzeAcceptsMoveAsStructDestructure(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]
    count: mutable i64

def run(holder: Holder) -> i64:
    move holder as Holder(thread, count)
    _ = count
    return join(move thread)
`
	result, errs := parseAndAnalyze(t, "move_as_struct_destructure.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}

func TestAnalyzeAcceptsMoveAsRebind(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def run(thread: Thread[i64, Joinable]) -> i64:
    move thread as worker
    return join(move worker)
`
	result, errs := parseAndAnalyze(t, "move_as_rebind.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}

func TestAnalyzeRejectsMoveAsStructPatternArityMismatch(t *testing.T) {
	src := `repr(c) struct Pair:
    left: mutable i64
    right: mutable i64

def bad(pair: Pair) -> void:
    move pair as Pair(left)
`
	_, errs := parseAndAnalyze(t, "move_as_arity_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "move-as pattern \"Pair\" expects 2 bindings, got 1") {
		t.Fatalf("expected move-as arity diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsMoveAsEnumVariantDestructure(t *testing.T) {
	src := `enum MaybeInt:
	None
	Pair(left: int, right: int)

def sum(value: MaybeInt) -> int:
	move value as MaybeInt.Pair(left, right)
	return left + right
`
	result, errs := parseAndAnalyze(t, "move_as_enum_variant_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "sum", "int")
}

func TestAnalyzeRejectsAwaitAfterTaskGroupTransfer(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern pool_await(task: Task[i64, Pending]) -> i64

def bad(group: mutable TaskGroup, task: Task[i64, Pending]) -> i64:
    task_group_add((&group).cast[any TaskGroup&](), move task)
    return await task
`
	_, errs := parseAndAnalyze(t, "consumed_task_handle_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "task handle \"task\" cannot be used after argument to call \"task_group_add\"") {
		t.Fatalf("expected consumed-task diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsAwaitSyntax(t *testing.T) {
	src := `extern pool_await(task: Task[i64, Pending]) -> i64

def ok(task: Task[i64, Pending]) -> i64:
    return await task
`
	result, errs := parseAndAnalyze(t, "await_task_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}

func TestAnalyzeRejectsDroppedJoinableThreadAtScopeExit(t *testing.T) {
	src := `def bad(thread: Thread[i64, Joinable]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_joinable_thread_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "joinable thread handle \"thread\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed thread diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedPendingTaskAtScopeExit(t *testing.T) {
	src := `def bad(task: Task[i64, Pending]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_pending_task_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "pending task handle \"task\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed task diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedHeldMutexGuardAtScopeExit(t *testing.T) {
	src := `def bad(guard: MutexGuard[Held]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_held_mutex_guard_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "held mutex guard \"guard\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed held-guard diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedJoinableThreadInsideAggregateAtScopeExit(t *testing.T) {
	src := `repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: Holder) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_joinable_holder_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "joinable thread handle \"holder.thread\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-thread diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedHeldMutexGuardInsideAggregateAtScopeExit(t *testing.T) {
	src := `repr(c) struct Holder:
    guard: mutable MutexGuard[Held]

def bad(holder: Holder) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_held_mutex_guard_holder_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "held mutex guard \"holder.guard\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-guard diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedTaskGroupWithPendingTasksAtScopeExit(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void

def bad(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
    task_group_add((&group).cast[any TaskGroup&](), move task)
`
	_, errs := parseAndAnalyze(t, "drop_task_group_with_pending_tasks_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "task group with pending tasks \"group\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed task-group diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedTaskGroupInsideAggregateWithPendingTasksAtScopeExit(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void

repr(c) struct Holder:
    group: mutable TaskGroup

def bad(holder: mutable Holder, task: Task[i64, Pending]) -> void:
    task_group_add((&holder.group).cast[any TaskGroup&](), move task)
`
	_, errs := parseAndAnalyze(t, "drop_task_group_holder_with_pending_tasks_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "task group with pending tasks \"holder.group\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-task-group diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAdd(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

def ok(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
    task_group_add((&group).cast[any TaskGroup&](), move task)
    wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaBorrowedAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

def ok(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
	group_ref: any TaskGroup& = (&group).cast[any TaskGroup&]()
	task_group_add(group_ref, move task)
	wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaProjectedBorrowedAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

def ok(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
	holder: GroupHolder = GroupHolder((&group).cast[any TaskGroup&]())
	task_group_add(holder.group_ref, move task)
	wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_projected_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateParamAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	task_group_add(holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_param_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsDroppedThreadPoolRequiringShutdownAtScopeExit(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool

def bad() -> void:
	pool: ThreadPool = pool_new(2u)
`
	_, errs := parseAndAnalyze(t, "drop_thread_pool_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool requiring shutdown \"pool\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed thread-pool diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDroppedThreadPoolInsideAggregateAtScopeExit(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool

repr(c) struct Holder:
	pool: mutable ThreadPool

def bad(holder: mutable Holder) -> void:
	holder.pool <- pool_new(2u)
`
	_, errs := parseAndAnalyze(t, "drop_thread_pool_holder_scope_exit_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool requiring shutdown \"holder.pool\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-thread-pool diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsPoolShutdownAfterPoolNew(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def ok() -> void:
	pool: ThreadPool = pool_new(2u)
	pool_shutdown((&pool).cast[any ThreadPool&]())
`
	result, errs := parseAndAnalyze(t, "pool_shutdown_after_pool_new_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsSubmittingToThreadPoolAfterShutdown(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	pool: ThreadPool = pool_new(2u)
	pool_shutdown((&pool).cast[any ThreadPool&]())
	_ = pool_submit1((&pool).cast[any ThreadPool&](), work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_submit_after_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected closed-thread-pool submit diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsSubmittingToThreadPoolAfterShutdownViaBorrowedAlias(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	pool: ThreadPool = pool_new(2u)
	pool_ref: any ThreadPool& = (&pool).cast[any ThreadPool&]()
	pool_shutdown(pool_ref)
	_ = pool_submit1((&pool).cast[any ThreadPool&](), work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_submit_after_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected underlying-owner shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolParamAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def bad(pool: any ThreadPool&) -> void:
	pool_shutdown(pool)
	_ = pool_submit1(pool, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_ref_param_reuse_after_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected borrowed-param shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsSubmittingToThreadPoolAfterShutdownViaProjectedBorrowedAlias(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: mutable any ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	pool: ThreadPool = pool_new(2u)
	holder: PoolHolder = PoolHolder((&pool).cast[any ThreadPool&]())
	pool_shutdown(holder.pool_ref)
	_ = pool_submit1((&pool).cast[any ThreadPool&](), work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_submit_after_projected_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected projected-alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingReassignedProjectedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: mutable any ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(left: any ThreadPool&, right: any ThreadPool&) -> void:
	holder: mutable PoolHolder = PoolHolder(left)
	holder.pool_ref <- right
	_ = pool_submit1(left, work, 1)
	pool_shutdown(holder.pool_ref)
	_ = pool_submit1(right, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_reassigned_projected_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"right\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected reassigned projected-alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingAggregateThreadPoolParamFieldAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_shutdown(holder.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_aggregate_param_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate-param alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingMoveBoundBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	move holder as PoolHolder(pool_ref)
	pool_shutdown(pool_ref)
	_ = pool_submit1(pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_movebind_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected move-bind alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingHelperReturnedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = get_pool_ref(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_helper_return_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected helper-returned alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingHelperReturnedAggregateBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def keep_holder(holder: PoolHolder) -> PoolHolder:
	return holder

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	alias_holder: PoolHolder = keep_holder(holder)
	pool_shutdown(alias_holder.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_helper_return_aggregate_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate helper-returned alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHelperReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = keep_holder(holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_helper_returned_aggregate_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def apply_getter(fn: func(PoolHolder) -> any ThreadPool&, holder: PoolHolder) -> any ThreadPool&:
	return fn(holder)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(get_pool_ref, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_return_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected higher-order helper-returned alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

def apply_keeper(fn: func(GroupHolder) -> GroupHolder, holder: GroupHolder) -> GroupHolder:
	return fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(keep_holder, holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_returned_aggregate_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterLocalCallbackBinding(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def apply_getter(fn: func(PoolHolder) -> any ThreadPool&, holder: PoolHolder) -> any ThreadPool&:
	local_fn: func(PoolHolder) -> any ThreadPool& = fn
	return local_fn(holder)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(get_pool_ref, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_local_callback_binding_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected local callback binding shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperLocalCallbackBinding(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

def apply_keeper(fn: func(GroupHolder) -> GroupHolder, holder: GroupHolder) -> GroupHolder:
	local_fn: func(GroupHolder) -> GroupHolder = fn
	return local_fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(keep_holder, holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_local_callback_binding_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaAggregateHeldCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter(get_pool_ref)
	pool_ref: any ThreadPool& = getter.fn(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_aggregate_held_callback_return_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate-held callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateHeldCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper(keep_holder)
	alias_holder: GroupHolder = keeper.fn(holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_held_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaMoveAsDestructuredCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter(get_pool_ref)
	move getter as PoolGetter(callback_fn)
	pool_ref: any ThreadPool& = callback_fn(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_move_as_destructured_callback_return_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected move-as destructured callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaMoveAsDestructuredCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper(keep_holder)
	move keeper as GroupKeeper(callback_fn)
	alias_holder: GroupHolder = callback_fn(holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_move_as_destructured_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaMoveAsVariantDestructuredCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

enum PoolGetter:
	Wrap(fn: func(PoolHolder) -> any ThreadPool&)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter.Wrap(get_pool_ref)
	move getter as PoolGetter.Wrap(callback_fn)
	pool_ref: any ThreadPool& = callback_fn(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_move_as_variant_destructured_callback_return_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected move-as variant destructured callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaMoveAsVariantDestructuredCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

enum GroupKeeper:
	Wrap(fn: func(GroupHolder) -> GroupHolder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper.Wrap(keep_holder)
	move keeper as GroupKeeper.Wrap(callback_fn)
	alias_holder: GroupHolder = callback_fn(holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_move_as_variant_destructured_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaEnumMatchBoundCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

enum PoolGetter:
	Wrap(fn: func(PoolHolder) -> any ThreadPool&)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter.Wrap(get_pool_ref)
	match getter:
		PoolGetter.Wrap(callback_fn):
			pool_ref: any ThreadPool& = callback_fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_enum_match_bound_callback_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected enum match bound callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaEnumMatchBoundCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

enum GroupKeeper:
	Wrap(fn: func(GroupHolder) -> GroupHolder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper.Wrap(keep_holder)
	match keeper:
		GroupKeeper.Wrap(callback_fn):
			alias_holder: GroupHolder = callback_fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_enum_match_bound_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaPackedEnumMatchBoundCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

packed enum PoolGetter:
	Wrap(fn: func(PoolHolder) -> any ThreadPool&)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	region store_owner
	store: PoolGetter.Store[Local] = PoolGetter.Store(store_owner)
	getter: PoolGetter = new[store] PoolGetter.Wrap(fn: get_pool_ref)
	match getter in store:
		PoolGetter.Wrap(callback_fn):
			pool_ref: any ThreadPool& = callback_fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_packed_enum_match_bound_callback_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected packed enum match bound callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaPackedEnumMatchBoundCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

packed enum GroupKeeper:
	Wrap(fn: func(GroupHolder) -> GroupHolder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	region store_owner
	store: GroupKeeper.Store[Local] = GroupKeeper.Store(store_owner)
	keeper: GroupKeeper = new[store] GroupKeeper.Wrap(fn: keep_holder)
	match keeper in store:
		GroupKeeper.Wrap(callback_fn):
			alias_holder: GroupHolder = callback_fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_packed_enum_match_bound_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaEnumMatchBoundAggregateProjectedCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

enum GetterBox:
	Wrap(getter: PoolGetter)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	boxed: GetterBox = GetterBox.Wrap(getter: PoolGetter(get_pool_ref))
	match boxed:
		GetterBox.Wrap(wrapper):
			pool_ref: any ThreadPool& = wrapper.fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_enum_match_bound_aggregate_projected_callback_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected enum match bound aggregate projected callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaEnumMatchBoundAggregateProjectedCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

enum KeeperBox:
	Wrap(keeper: GroupKeeper)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	boxed: KeeperBox = KeeperBox.Wrap(keeper: GroupKeeper(keep_holder))
	match boxed:
		KeeperBox.Wrap(wrapper):
			alias_holder: GroupHolder = wrapper.fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_enum_match_bound_aggregate_projected_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaPackedEnumMatchBoundAggregateProjectedCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

packed enum GetterBox:
	Wrap(getter: PoolGetter)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	region store_owner
	store: GetterBox.Store[Local] = GetterBox.Store(store_owner)
	boxed: GetterBox = new[store] GetterBox.Wrap(getter: PoolGetter(get_pool_ref))
	match boxed in store:
		GetterBox.Wrap(wrapper):
			pool_ref: any ThreadPool& = wrapper.fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_packed_enum_match_bound_aggregate_projected_callback_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected packed enum match bound aggregate projected callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaPackedEnumMatchBoundAggregateProjectedCallback(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

packed enum KeeperBox:
	Wrap(keeper: GroupKeeper)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	region store_owner
	store: KeeperBox.Store[Local] = KeeperBox.Store(store_owner)
	boxed: KeeperBox = new[store] KeeperBox.Wrap(keeper: GroupKeeper(keep_holder))
	match boxed in store:
		KeeperBox.Wrap(wrapper):
			alias_holder: GroupHolder = wrapper.fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_packed_enum_match_bound_aggregate_projected_callback_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterAggregateCallbackParamProjection(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

def apply_getter(wrapper: PoolGetter, holder: PoolHolder) -> any ThreadPool&:
	return wrapper.fn(holder)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(PoolGetter(get_pool_ref), holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_aggregate_callback_param_projection_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate callback param projection shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateCallbackParamProjection(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def apply_keeper(wrapper: GroupKeeper, holder: GroupHolder) -> GroupHolder:
	return wrapper.fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(GroupKeeper(keep_holder), holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_callback_param_projection_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterAggregateCallbackParamLocalAliasProjection(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

def apply_getter(wrapper: PoolGetter, holder: PoolHolder) -> any ThreadPool&:
	local_wrapper: PoolGetter = wrapper
	alias_wrapper: PoolGetter = local_wrapper
	return alias_wrapper.fn(holder)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(PoolGetter(get_pool_ref), holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_aggregate_callback_param_local_alias_projection_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate callback param local alias projection shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateCallbackParamLocalAliasProjection(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def apply_keeper(wrapper: GroupKeeper, holder: GroupHolder) -> GroupHolder:
	local_wrapper: GroupKeeper = wrapper
	alias_wrapper: GroupKeeper = local_wrapper
	return alias_wrapper.fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(GroupKeeper(keep_holder), holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_callback_param_local_alias_projection_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterMutableAggregateCallbackWrapperRebinding(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolGetter:
	fn: func(PoolHolder) -> any ThreadPool&

def apply_getter(primary: PoolGetter, fallback: PoolGetter, holder: PoolHolder) -> any ThreadPool&:
	local_wrapper: mutable PoolGetter = fallback
	local_wrapper <- primary
	return local_wrapper.fn(holder)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def fallback_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(PoolGetter(get_pool_ref), PoolGetter(fallback_pool_ref), holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_mutable_aggregate_callback_wrapper_rebinding_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected mutable aggregate callback wrapper rebinding shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaMutableAggregateCallbackWrapperRebinding(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def apply_keeper(primary: GroupKeeper, fallback: GroupKeeper, holder: GroupHolder) -> GroupHolder:
	local_wrapper: mutable GroupKeeper = fallback
	local_wrapper <- primary
	return local_wrapper.fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def fallback_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(GroupKeeper(keep_holder), GroupKeeper(fallback_holder), holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_mutable_aggregate_callback_wrapper_rebinding_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterMutableCallbackRebinding(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

def apply_getter(primary: func(PoolHolder) -> any ThreadPool&, fallback: func(PoolHolder) -> any ThreadPool&, holder: PoolHolder) -> any ThreadPool&:
	local_fn: mutable func(PoolHolder) -> any ThreadPool& = fallback
	local_fn <- primary
	return local_fn(holder)

def get_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def get_pool_ref_fallback(holder: PoolHolder) -> any ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(get_pool_ref, get_pool_ref_fallback, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_mutable_callback_rebinding_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected mutable callback rebinding shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperMutableCallbackRebinding(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

def apply_keeper(primary: func(GroupHolder) -> GroupHolder, fallback: func(GroupHolder) -> GroupHolder, holder: GroupHolder) -> GroupHolder:
	local_fn: mutable func(GroupHolder) -> GroupHolder = fallback
	local_fn <- primary
	return local_fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def keep_holder_fallback(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(keep_holder, keep_holder_fallback, holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_mutable_callback_rebinding_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterBranchMergedCallbackRebinding(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	left_pool_ref: any ThreadPool&
	right_pool_ref: any ThreadPool&

def apply_getter(flag: bool, primary: func(PoolHolder) -> any ThreadPool&, fallback: func(PoolHolder) -> any ThreadPool&, holder: PoolHolder) -> any ThreadPool&:
	local_fn: mutable func(PoolHolder) -> any ThreadPool& = fallback
	if flag:
		local_fn <- primary
	else:
		local_fn <- primary
	return local_fn(holder)

def get_left_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.left_pool_ref

def get_right_pool_ref(holder: PoolHolder) -> any ThreadPool&:
	return holder.right_pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = apply_getter(true, get_left_pool_ref, get_right_pool_ref, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.left_pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_branch_merged_callback_rebinding_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.left_pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected branch-merged callback shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperBranchMergedCallbackRebinding(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	primary_group_ref: any TaskGroup&
	fallback_group_ref: any TaskGroup&

def apply_getter(flag: bool, primary: func(GroupHolder) -> any TaskGroup&, fallback: func(GroupHolder) -> any TaskGroup&, holder: GroupHolder) -> any TaskGroup&:
	local_fn: mutable func(GroupHolder) -> any TaskGroup& = fallback
	if flag:
		local_fn <- primary
	else:
		local_fn <- primary
	return local_fn(holder)

def get_primary_group_ref(holder: GroupHolder) -> any TaskGroup&:
	return holder.primary_group_ref

def get_fallback_group_ref(holder: GroupHolder) -> any TaskGroup&:
	return holder.fallback_group_ref

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	group_ref: any TaskGroup& = apply_getter(true, get_primary_group_ref, get_fallback_group_ref, holder)
	task_group_add(group_ref, move task)
	wait all holder.primary_group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_branch_merged_callback_rebinding_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsReusingExternReturnedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

@borrows_return(holder.pool_ref)
extern get_pool_ref(holder: PoolHolder) -> any ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: any ThreadPool& = get_pool_ref(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_extern_return_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected extern-returned alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingExternReturnedAggregateBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

@borrows_return(holder)
extern keep_holder(holder: PoolHolder) -> PoolHolder

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	alias_holder: PoolHolder = keep_holder(holder)
	pool_shutdown(alias_holder.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_extern_return_aggregate_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected extern aggregate-returned alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingExternReturnedNestedBorrowedThreadPoolFieldAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

repr(c) struct PoolHolder:
	pool_ref: any ThreadPool&

repr(c) struct PoolWrapper:
	inner: PoolHolder

@borrows_return_field(inner.pool_ref, holder.pool_ref)
extern wrap_holder(holder: PoolHolder) -> PoolWrapper

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	wrapped: PoolWrapper = wrap_holder(holder)
	pool_shutdown(wrapped.inner.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_extern_return_nested_field_alias_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected extern nested-field alias shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaExternReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

@borrows_return(holder)
extern keep_holder(holder: GroupHolder) -> GroupHolder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = keep_holder(holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_extern_returned_aggregate_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaExternRebasedReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

@borrows_return_rebased(items)
extern sub_items(items: view[GroupHolder], start: usize, end: usize) -> view[GroupHolder]

def ok(items: view[GroupHolder], task: Task[i64, Pending]) -> void:
	sub: view[GroupHolder] = sub_items(items, 1u, 2u)
	task_group_add(sub[0u].group_ref, move task)
	wait all items[1u].group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_extern_rebased_aggregate_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaExternFieldRebasedReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: any TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: any TaskGroup&) -> void

repr(c) struct GroupHolder:
	group_ref: any TaskGroup&

repr(c) struct GroupWindow:
	items: view[GroupHolder]

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[GroupHolder], start: usize, end: usize) -> GroupWindow

def ok(items: view[GroupHolder], task: Task[i64, Pending]) -> void:
	window: GroupWindow = wrap_sub(items, 1u, 2u)
	task_group_add(window.items[0u].group_ref, move task)
	wait all items[1u].group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_extern_field_rebased_aggregate_alias_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsDoubleThreadPoolShutdown(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def bad() -> void:
	pool: ThreadPool = pool_new(2u)
	pool_shutdown((&pool).cast[any ThreadPool&]())
	pool_shutdown((&pool).cast[any ThreadPool&]())
`
	_, errs := parseAndAnalyze(t, "thread_pool_double_shutdown_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used after argument to call \"pool_shutdown\"") {
		t.Fatalf("expected double-shutdown diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsReinitializingThreadPoolAfterShutdown(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def ok() -> void:
	pool: mutable ThreadPool = pool_new(2u)
	pool_shutdown((&pool).cast[any ThreadPool&]())
	pool <- pool_new(1u)
	pool_shutdown((&pool).cast[any ThreadPool&]())
`
	result, errs := parseAndAnalyze(t, "thread_pool_reinitialize_after_shutdown_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeRejectsCopyingThreadPoolOwnerWithoutMove(t *testing.T) {
	src := `def bad(pool: ThreadPool) -> void:
	copy: ThreadPool = pool
`
	_, errs := parseAndAnalyze(t, "thread_pool_owner_copy_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" must be moved explicitly before move into local \"copy\"") {
		t.Fatalf("expected thread-pool owner move diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingMovedTaskGroupOwner(t *testing.T) {
	src := `def bad(group: TaskGroup) -> void:
	moved: TaskGroup = move group
	_ = move group
`
	_, errs := parseAndAnalyze(t, "task_group_owner_reuse_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "task group owner \"group\" cannot be used after move into local \"moved\"") {
		t.Fatalf("expected task-group owner reuse diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsWaitAllSyntax(t *testing.T) {
	src := `extern task_group_wait_all(group: any TaskGroup&) -> void

def ok(group: mutable TaskGroup) -> void:
    wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_group_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsNotifySyntax(t *testing.T) {
	src := `extern notify_one(cv: any CondVar&) -> void
extern notify_all(cv: any CondVar&) -> void

def ok(cv: mutable CondVar, broadcast: bool) -> void:
    if broadcast:
        notify all cv
    else:
        notify one cv
`
	result, errs := parseAndAnalyze(t, "notify_syntax_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsAtomicRmwBuiltins(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_add(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_sub(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_or(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_and(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_xor(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]

def ok(slot: mutable atomic[i64]) -> i64 can[Atomics.Rmw]:
	slot_ref: any atomic[i64]& = (&slot).cast[any atomic[i64]&]()
	add: i64 = fetch_add(slot_ref, 1, MemoryOrder.AcqRel)
	sub: i64 = fetch_sub(slot_ref, 2, MemoryOrder.AcqRel)
	or_bits: i64 = fetch_or(slot_ref, 4, MemoryOrder.AcqRel)
	and_bits: i64 = fetch_and(slot_ref, 8, MemoryOrder.AcqRel)
	xor_bits: i64 = fetch_xor(slot_ref, 16, MemoryOrder.AcqRel)
	return add + sub + or_bits + and_bits + xor_bits
`
	result, errs := parseAndAnalyze(t, "atomic_rmw_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}

func TestAnalyzeRejectsAtomicRmwOnBoolPayload(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_or(slot: any atomic[bool]&, value: bool, order: MemoryOrder) -> bool can[Atomics.Rmw]

def bad(slot: mutable atomic[bool]) -> bool can[Atomics.Rmw]:
	slot_ref: any atomic[bool]& = (&slot).cast[any atomic[bool]&]()
	return fetch_or(slot_ref, true, MemoryOrder.AcqRel)
`
	_, errs := parseAndAnalyze(t, "atomic_rmw_bool_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"fetch_or\" requires atomic_numeric(T), got atomic[bool]") {
		t.Fatalf("expected atomic_numeric bool diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsAtomicRmwOnPointerPayload(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_xor(slot: any atomic[any u8&]&, value: any u8&, order: MemoryOrder) -> any u8& can[Atomics.Rmw]

def bad(slot: mutable atomic[any u8&], value: any u8&) -> any u8& can[Atomics.Rmw]:
	slot_ref: any atomic[any u8&]& = (&slot).cast[any atomic[any u8&]&]()
	return fetch_xor(slot_ref, value, MemoryOrder.AcqRel)
`
	_, errs := parseAndAnalyze(t, "atomic_rmw_pointer_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"fetch_xor\" requires atomic_numeric(T), got atomic[any u8&]") {
		t.Fatalf("expected atomic_numeric pointer diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsLockSyntax(t *testing.T) {
	src := `extern mutex_lock(mu: any Mutex&) -> MutexGuard[Held]
extern mutex_unlock(g: MutexGuard[Held]) -> void
extern cond_wait(cv: any CondVar&, g: MutexGuard[Held]) -> MutexGuard[Held]

def ok(mu: mutable Mutex, cv: mutable CondVar, ready: bool) -> void can[Sync.Lock, Sync.Unlock, Sync.Wait]:
    lock mu as g:
        while not ready:
			g <- cond_wait((&cv).cast[any CondVar&](), move g)
`
	result, errs := parseAndAnalyze(t, "lock_scope_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}

func TestAnalyzeAcceptsPoolScopeSyntax(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def ok() -> bool can[Pool.Create, Pool.Shutdown]:
	pool workers(2u):
		return workers.handle != null
`
	result, errs := parseAndAnalyze(t, "pool_scope_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "bool")
}

func TestAnalyzeAcceptsSubmitSyntaxInsidePoolScope(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void
extern pool_await(task: Task[i64, Pending]) -> i64

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def ok() -> i64 can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.Await]:
	pool workers(2u):
		task: Task[i64, Pending] = submit work(7)
		return await task
`
	result, errs := parseAndAnalyze(t, "submit_syntax_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}

func TestAnalyzeAcceptsExplicitSubmitSyntaxOutsidePoolScope(t *testing.T) {
	src := `extern pool_await(task: Task[i64, Pending]) -> i64

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def ok(pool: any ThreadPool&) -> i64 can[Pool.Submit, Pool.Await]:
	task: Task[i64, Pending] = submit[pool] work(7)
	return await task
`
	result, errs := parseAndAnalyze(t, "submit_explicit_pool_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}

func TestParseRejectsSubmitOutsidePoolScope(t *testing.T) {
	src := `def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	_ = submit work(7)
`
	_, errs := parseAndAnalyze(t, "submit_outside_pool_parse_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parse error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "submit requires an active pool scope") {
		t.Fatalf("expected active-pool submit parse diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsReusingConsumedThreadField(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern detach(thread: Thread[i64, Joinable]) -> void

repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: mutable Holder) -> void:
    value: i64 = join(move holder.thread)
    _ = value
    detach(move holder.thread)
`
	_, errs := parseAndAnalyze(t, "consumed_thread_field_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "thread handle \"holder.thread\" cannot be used after argument to call \"join\"") {
		t.Fatalf("expected consumed-thread-field diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAddressOfAffineHandleField(t *testing.T) {
	src := `repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: mutable Holder) -> void:
    borrow: stack Thread[i64, Joinable]& = &holder.thread
    _ = borrow
`
	_, errs := parseAndAnalyze(t, "affine_handle_addr_of_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot take address of thread handle") {
		t.Fatalf("expected affine-address diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAffineHandleGlobals(t *testing.T) {
	src := `repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

global current_thread: Thread[i64, Joinable] = zeroed
global current_holder: Holder = zeroed
extern foreign_task: Task[i64, Pending]
`
	_, errs := parseAndAnalyze(t, "affine_handle_globals_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "global \"current_thread\" cannot store affine handle values of type Thread[i64, Joinable]") {
		t.Fatalf("expected direct affine-global diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "global \"current_holder\" cannot store affine handle values of type Holder") {
		t.Fatalf("expected structural affine-global diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "extern var \"foreign_task\" cannot store affine handle values of type Task[i64, Pending]") {
		t.Fatalf("expected affine extern-global diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReferencesToAffineContainingValues(t *testing.T) {
	src := `repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad_param(holder: any Holder&) -> void:
    pass

def bad_local(holder: Holder) -> void:
    alias: any Holder& = &holder
    _ = alias
`
	_, errs := parseAndAnalyze(t, "affine_handle_refs_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "references to values containing affine handles are not supported; got Holder&") {
		t.Fatalf("expected affine-reference diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "cannot take address of value containing affine handles") {
		t.Fatalf("expected affine-address-of-container diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsCopyingAggregateAfterAffineFieldConsume(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]
    count: mutable i64

def bad(holder: Holder) -> void:
    value: i64 = join(move holder.thread)
    _ = value
    copy: Holder = holder
`
	_, errs := parseAndAnalyze(t, "affine_container_copy_after_field_move_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "value containing affine handles \"holder\" cannot be used after argument to call \"join\"") {
		t.Fatalf("expected aggregate-after-field-move diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingMovedAffineContainingAggregate(t *testing.T) {
	src := `repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: Holder) -> void:
    copy: Holder = move holder
    _ = move holder
`
	_, errs := parseAndAnalyze(t, "affine_container_move_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "value containing affine handles \"holder\" cannot be used after move into local \"copy\"") {
		t.Fatalf("expected moved-aggregate diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingAffineHandleAfterStructLiteralMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]
    count: mutable i64

def bad(thread: Thread[i64, Joinable]) -> i64:
    holder: Holder = Holder(move thread, 1)
    return join(move thread)
`
	_, errs := parseAndAnalyze(t, "affine_struct_literal_move_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" cannot be used after move into struct literal field \"thread\"") {
		t.Fatalf("expected struct-literal move diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingAffineHandleAfterArrayLiteralMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def bad(thread: Thread[i64, Joinable]) -> i64:
    items: array[Thread[i64, Joinable], 1] = [move thread]
    return join(move thread)
`
	_, errs := parseAndAnalyze(t, "affine_array_literal_move_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" cannot be used after move into array literal element") {
		t.Fatalf("expected array-literal move diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingAffineHandleAfterEnumConstructorMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

enum Job:
    Run(thread: Thread[i64, Joinable])

def bad(thread: Thread[i64, Joinable]) -> i64:
    job: Job = Job.Run(thread: move thread)
    return join(move thread)
`
	_, errs := parseAndAnalyze(t, "affine_enum_constructor_move_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" cannot be used after move into enum payload \"Job.Run.thread\"") {
		t.Fatalf("expected enum-constructor move diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsUsingAffineFieldAfterParentMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

repr(c) struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: Holder) -> i64:
    copy: Holder = move holder
    return join(move holder.thread)
`
	_, errs := parseAndAnalyze(t, "affine_parent_move_field_use_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"holder.thread\" cannot be used after move into local \"copy\"") {
		t.Fatalf("expected parent-move child-use diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReusingIndexedAffineValue(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def bad(items: array[Thread[i64, Joinable], 1]) -> i64:
    first: i64 = join(move items[0])
    _ = first
    return join(move items[0])
`
	_, errs := parseAndAnalyze(t, "affine_index_move_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "value containing affine handles \"items\" cannot be used after argument to call \"join\"") {
		t.Fatalf("expected indexed-affine diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTypeMismatchAssignment(t *testing.T) {
	src := `def mismatch() -> int:
    value: mutable int = true
    return value
`
	_, errs := parseAndAnalyze(t, "type_mismatch.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects int, got bool") {
		t.Fatalf("expected assignment mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsFloatArithmeticAndCastShorthand(t *testing.T) {
	src := `def combine(left: f32, right: f64) -> f64:
    total: f64 = left.f64() + right
    return total / 2.0

def narrow(value: f64) -> f32:
    return value.f32()
`
	result, errs := parseAndAnalyze(t, "float_arithmetic_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "combine", "f64")
	requireFunctionReturnTypeString(t, result, "narrow", "f32")
}

func TestAnalyzeRejectsFloatModulo(t *testing.T) {
	src := `def bad(value: f64) -> f64:
    return value % 2.0
`
	_, errs := parseAndAnalyze(t, "float_modulo_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "operator requires integral operands") {
		t.Fatalf("expected integral-operand diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsFloatArrayIndex(t *testing.T) {
	src := `def bad(values: i32[4], idx: f64) -> i32:
    return values[idx]
`
	_, errs := parseAndAnalyze(t, "float_index_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "index must be integral, got f64") {
		t.Fatalf("expected integral-index diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsConstFloatCastsInCompileTimeIntegerContexts(t *testing.T) {
	src := `const COUNT: i32 = 3.75.i32()

def sized() -> i32[COUNT]:
    return [1, 2, 3]
`
	result, errs := parseAndAnalyze(t, "const_float_cast_compile_time_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	count, ok := result.ConstValues["COUNT"]
	if !ok {
		t.Fatal("expected COUNT const value to be recorded")
	}
	if count.Kind != semantic.ConstInt || count.Int != 3 {
		t.Fatalf("expected COUNT const value to be int 3, got %#v", count)
	}
}

func TestAnalyzeAcceptsExtendedFloatCastMatrix(t *testing.T) {
	src := `def f64_to_i64(value: f64) -> i64:
	return value.i64()

def f64_to_u32(value: f64) -> u32:
	return value.u32()

def f64_to_u64(value: f64) -> u64:
	return value.u64()

def f32_to_f64(value: f32) -> f64:
	return value.f64()

def i64_to_f64(value: i64) -> f64:
	return value.f64()

def u32_to_f32(value: u32) -> f32:
	return value.f32()

def u64_to_f64(value: u64) -> f64:
	return value.f64()

def usize_to_f64(value: usize) -> f64:
	return value.f64()
`
	result, errs := parseAndAnalyze(t, "float_cast_matrix_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "f64_to_i64", "i64")
	requireFunctionReturnTypeString(t, result, "f64_to_u32", "u32")
	requireFunctionReturnTypeString(t, result, "f64_to_u64", "u64")
	requireFunctionReturnTypeString(t, result, "f32_to_f64", "f64")
	requireFunctionReturnTypeString(t, result, "i64_to_f64", "f64")
	requireFunctionReturnTypeString(t, result, "u32_to_f32", "f32")
	requireFunctionReturnTypeString(t, result, "u64_to_f64", "f64")
	requireFunctionReturnTypeString(t, result, "usize_to_f64", "f64")
}

func TestAnalyzeAcceptsConstExtendedFloatCastMatrix(t *testing.T) {
	src := `const I64_FROM_F64: i64 = 8.75.i64()
const U32_FROM_F64: u32 = 5.5.u32()
const U64_FROM_F64: u64 = 6.5.u64()
const F64_FROM_U32: f64 = 7.i32().u32().f64()
const F32_FROM_U64: f32 = 9.i32().u64().f32()

def total() -> f64:
	return I64_FROM_F64.f64() + U32_FROM_F64.f64() + U64_FROM_F64.f64() + F64_FROM_U32 + F32_FROM_U64.f64()
`
	result, errs := parseAndAnalyze(t, "const_float_cast_matrix_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	checks := map[string]semantic.ConstValue{
		"I64_FROM_F64": {Kind: semantic.ConstInt, Int: 8},
		"U32_FROM_F64": {Kind: semantic.ConstInt, Int: 5},
		"U64_FROM_F64": {Kind: semantic.ConstInt, Int: 6},
		"F64_FROM_U32": {Kind: semantic.ConstFloat, Float: 7},
		"F32_FROM_U64": {Kind: semantic.ConstFloat, Float: 9},
	}
	for name, want := range checks {
		got, ok := result.ConstValues[name]
		if !ok {
			t.Fatalf("expected %s const value to be recorded", name)
		}
		if got.Kind != want.Kind {
			t.Fatalf("expected %s const kind %v, got %#v", name, want.Kind, got)
		}
		switch want.Kind {
		case semantic.ConstInt:
			if got.Int != want.Int {
				t.Fatalf("expected %s const int %d, got %#v", name, want.Int, got)
			}
		case semantic.ConstFloat:
			if got.Float != want.Float {
				t.Fatalf("expected %s const float %v, got %#v", name, want.Float, got)
			}
		default:
			t.Fatalf("unexpected expected const kind for %s: %#v", name, want)
		}
	}
}

func TestAnalyzeRejectsConstFloatCastEdgeCases(t *testing.T) {
	src := `const NEG_TO_U32: u32 = (-1.0).u32()
const BIG_TO_I8: i8 = 200.0.i8()
const BIG_TO_I64: i64 = 9223372036854775808.0.i64()
const BIG_TO_U64: u64 = 9223372036854775808.0.u64()
`
	_, errs := parseAndAnalyze(t, "const_float_cast_edge_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "const \"NEG_TO_U32\" initializer must be a compile-time u32 value") {
		t.Fatalf("expected unsigned const-cast rejection, got:\n%s", all)
	}
	if !strings.Contains(all, "const \"BIG_TO_I8\" initializer must be a compile-time i8 value") {
		t.Fatalf("expected narrowing const-cast rejection, got:\n%s", all)
	}
	if !strings.Contains(all, "const \"BIG_TO_I64\" initializer must be a compile-time i64 value") {
		t.Fatalf("expected signed overflow const-cast rejection, got:\n%s", all)
	}
	if !strings.Contains(all, "const \"BIG_TO_U64\" initializer must be a compile-time u64 value") {
		t.Fatalf("expected unsigned overflow const-cast rejection, got:\n%s", all)
	}
}

func TestAnalyzeContextualFloatLiteralSites(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

repr(c) struct FloatPair:
	left: f32
	right: f32

def contextual_local() -> f32:
	local: f32 = 1.25
	return local

def contextual_return(flag: bool) -> f32:
	return (2.5 if flag else 3.5)

def contextual_call() -> f32:
	return passthrough(4.5)

def contextual_struct() -> FloatPair:
	return FloatPair(5.5, 6.5)

def contextual_array() -> f32[2]:
	return [7.5, 8.5]
`
	result, errs := parseAndAnalyze(t, "contextual_float_literals_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	localDecl := requireFuncDecl(t, result, "contextual_local")
	localInit, ok := localDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_local to start with a local declaration, got %T", localDecl.Body[0])
	}
	requireExprTypeString(t, result, localInit.Value, "f32")

	returnDecl := requireFuncDecl(t, result, "contextual_return")
	returnStmt, ok := returnDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_return to contain a return statement, got %T", returnDecl.Body[0])
	}
	parenExpr, ok := returnStmt.Value.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a parenthesized ternary, got %T", returnStmt.Value)
	}
	ternaryExpr, ok := parenExpr.Inner.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a ternary, got %T", parenExpr.Inner)
	}
	requireExprTypeString(t, result, ternaryExpr, "f32")
	requireExprTypeString(t, result, ternaryExpr.Value, "f32")
	requireExprTypeString(t, result, ternaryExpr.Alt, "f32")

	callDecl := requireFuncDecl(t, result, "contextual_call")
	callReturn, ok := callDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_call to contain a return statement, got %T", callDecl.Body[0])
	}
	callExpr, ok := callReturn.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected contextual_call to return a function call, got %T", callReturn.Value)
	}
	requireExprTypeString(t, result, callExpr.Args[0], "f32")

	structDecl := requireFuncDecl(t, result, "contextual_struct")
	structReturn, ok := structDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_struct to contain a return statement, got %T", structDecl.Body[0])
	}
	structLit, ok := structReturn.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected contextual_struct to return a struct literal, got %T", structReturn.Value)
	}
	for _, arg := range structLit.Args {
		requireExprTypeString(t, result, arg, "f32")
	}

	arrayDecl := requireFuncDecl(t, result, "contextual_array")
	arrayReturn, ok := arrayDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_array to contain a return statement, got %T", arrayDecl.Body[0])
	}
	arrayLit, ok := arrayReturn.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected contextual_array to return an array literal, got %T", arrayReturn.Value)
	}
	for _, elem := range arrayLit.Elems {
		requireExprTypeString(t, result, elem, "f32")
	}
}

func TestAnalyzeContextualFloatLiteralArithmeticSites(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

repr(c) struct FloatPair:
	left: f32
	right: f32

def contextual_local() -> f32:
	local: f32 = 1.25 + 2.25
	return local

def contextual_return(flag: bool) -> f32:
	return ((3.25 + 4.25) if flag else (5.25 + 6.25))

def contextual_call() -> f32:
	return passthrough(7.25 + 8.25)

def contextual_struct() -> FloatPair:
	return FloatPair(9.25 + 10.25, 11.25 + 12.25)

def contextual_array() -> f32[2]:
	return [13.25 + 14.25, 15.25 + 16.25]
`
	result, errs := parseAndAnalyze(t, "contextual_float_literal_arithmetic_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	localDecl := requireFuncDecl(t, result, "contextual_local")
	localInit, ok := localDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_local to start with a local declaration, got %T", localDecl.Body[0])
	}
	localBinary, ok := localInit.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_local initializer to be a binary expression, got %T", localInit.Value)
	}
	requireExprTypeString(t, result, localBinary, "f32")
	requireExprTypeString(t, result, localBinary.Left, "f32")
	requireExprTypeString(t, result, localBinary.Right, "f32")

	returnDecl := requireFuncDecl(t, result, "contextual_return")
	returnStmt, ok := returnDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_return to contain a return statement, got %T", returnDecl.Body[0])
	}
	parenExpr, ok := returnStmt.Value.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a parenthesized ternary, got %T", returnStmt.Value)
	}
	ternaryExpr, ok := parenExpr.Inner.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a ternary, got %T", parenExpr.Inner)
	}
	thenParen, ok := ternaryExpr.Value.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return true branch to be parenthesized, got %T", ternaryExpr.Value)
	}
	thenBinary, ok := thenParen.Inner.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return true branch to be a binary expression, got %T", thenParen.Inner)
	}
	elseParen, ok := ternaryExpr.Alt.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return false branch to be parenthesized, got %T", ternaryExpr.Alt)
	}
	elseBinary, ok := elseParen.Inner.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return false branch to be a binary expression, got %T", elseParen.Inner)
	}
	requireExprTypeString(t, result, ternaryExpr, "f32")
	for _, expr := range []ast.Expr{thenBinary, thenBinary.Left, thenBinary.Right, elseBinary, elseBinary.Left, elseBinary.Right} {
		requireExprTypeString(t, result, expr, "f32")
	}

	callDecl := requireFuncDecl(t, result, "contextual_call")
	callReturn, ok := callDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_call to contain a return statement, got %T", callDecl.Body[0])
	}
	callExpr, ok := callReturn.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected contextual_call to return a function call, got %T", callReturn.Value)
	}
	callBinary, ok := callExpr.Args[0].(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_call argument to be a binary expression, got %T", callExpr.Args[0])
	}
	requireExprTypeString(t, result, callBinary, "f32")
	requireExprTypeString(t, result, callBinary.Left, "f32")
	requireExprTypeString(t, result, callBinary.Right, "f32")

	structDecl := requireFuncDecl(t, result, "contextual_struct")
	structReturn, ok := structDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_struct to contain a return statement, got %T", structDecl.Body[0])
	}
	structLit, ok := structReturn.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected contextual_struct to return a struct literal, got %T", structReturn.Value)
	}
	for _, arg := range structLit.Args {
		binary, ok := arg.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected contextual_struct field to be a binary expression, got %T", arg)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}

	arrayDecl := requireFuncDecl(t, result, "contextual_array")
	arrayReturn, ok := arrayDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_array to contain a return statement, got %T", arrayDecl.Body[0])
	}
	arrayLit, ok := arrayReturn.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected contextual_array to return an array literal, got %T", arrayReturn.Value)
	}
	for _, elem := range arrayLit.Elems {
		binary, ok := elem.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected contextual_array element to be a binary expression, got %T", elem)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}
}

func TestAnalyzeContextualFloatLiteralArithmeticTopLevelSites(t *testing.T) {
	src := `repr(c) struct FloatPair:
	left: f32
	right: f32

const F32_TOTAL: f32 = 1.25 + 2.25
const F64_TOTAL: f64 = 3.25 + 4.25

global g_f32: f32 = 5.25 + 6.25
global g_f64: f64 = 7.25 + 8.25
global g_pair: FloatPair = FloatPair(9.25 + 10.25, 11.25 + 12.25)
global g_values: f32[2] = [13.25 + 14.25, 15.25 + 16.25]
`
	result, errs := parseAndAnalyze(t, "contextual_float_literal_arithmetic_toplevel_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	checks := map[string]semantic.ConstValue{
		"F32_TOTAL": {Kind: semantic.ConstFloat, Float: 3.5},
		"F64_TOTAL": {Kind: semantic.ConstFloat, Float: 7.5},
	}
	for name, want := range checks {
		got, ok := result.ConstValues[name]
		if !ok {
			t.Fatalf("expected %s const value to be recorded", name)
		}
		if got.Kind != want.Kind || got.Float != want.Float {
			t.Fatalf("expected %s const value %#v, got %#v", name, want, got)
		}
	}

	constF32 := requireConstDecl(t, result, "F32_TOTAL")
	constF32Binary, ok := constF32.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected F32_TOTAL initializer to be a binary expression, got %T", constF32.Value)
	}
	requireExprTypeString(t, result, constF32Binary, "f32")
	requireExprTypeString(t, result, constF32Binary.Left, "f32")
	requireExprTypeString(t, result, constF32Binary.Right, "f32")

	constF64 := requireConstDecl(t, result, "F64_TOTAL")
	constF64Binary, ok := constF64.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected F64_TOTAL initializer to be a binary expression, got %T", constF64.Value)
	}
	requireExprTypeString(t, result, constF64Binary, "f64")
	requireExprTypeString(t, result, constF64Binary.Left, "f64")
	requireExprTypeString(t, result, constF64Binary.Right, "f64")

	globalF32 := requireGlobalDecl(t, result, "g_f32")
	globalF32Binary, ok := globalF32.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected g_f32 initializer to be a binary expression, got %T", globalF32.Value)
	}
	requireExprTypeString(t, result, globalF32Binary, "f32")
	requireExprTypeString(t, result, globalF32Binary.Left, "f32")
	requireExprTypeString(t, result, globalF32Binary.Right, "f32")

	globalF64 := requireGlobalDecl(t, result, "g_f64")
	globalF64Binary, ok := globalF64.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected g_f64 initializer to be a binary expression, got %T", globalF64.Value)
	}
	requireExprTypeString(t, result, globalF64Binary, "f64")
	requireExprTypeString(t, result, globalF64Binary.Left, "f64")
	requireExprTypeString(t, result, globalF64Binary.Right, "f64")

	globalPair := requireGlobalDecl(t, result, "g_pair")
	pairLit, ok := globalPair.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected g_pair initializer to be a struct literal, got %T", globalPair.Value)
	}
	for _, arg := range pairLit.Args {
		binary, ok := arg.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected g_pair field to be a binary expression, got %T", arg)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}

	globalValues := requireGlobalDecl(t, result, "g_values")
	listLit, ok := globalValues.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected g_values initializer to be a list literal, got %T", globalValues.Value)
	}
	for _, elem := range listLit.Elems {
		binary, ok := elem.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected g_values element to be a binary expression, got %T", elem)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}
}

func TestAnalyzeRejectsNullIntoNonNullRef(t *testing.T) {
	src := `repr(c) struct Box:
    value: int

def bad() -> void:
    box: any Box& = null
`
	_, errs := parseAndAnalyze(t, "nonnull_ref_rejects_null.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects any Box&, got null") {
		t.Fatalf("expected non-null ref rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzePointerTypestateBranches(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern alloc_box() -> any Box&?
extern sfree_box(box: any Box&) -> any Box!

def release_box() -> void:
	box: mutable any Box&? = alloc_box()
    if box != null:
        box as ! <- sfree_box(box)

def missing_box() -> any Box!:
    return null
`
	_, errs := parseAndAnalyze(t, "pointer_typestate.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNullableFieldAccessWithoutProof(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern maybe_box() -> any Box&?

def bad() -> int:
	box: any Box&? = maybe_box()
    return box.value
`
	_, errs := parseAndAnalyze(t, "nullable_field_access.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field access requires proven non-null reference") {
		t.Fatalf("expected nullable field access rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeGuardClauseRefinesAfterReturn(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern maybe_box() -> any Box&?

def read_box() -> int:
	box: any Box&? = maybe_box()
    if box == null:
        return 0
    return box.value
`
	_, errs := parseAndAnalyze(t, "guard_clause_refinement.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsBuiltinSurfaceCollectionTypes(t *testing.T) {
	src := `extern take_array(values: array[i32, 4]) -> void
extern take_darray(values: darray[i32, row]) -> void
extern take_dstr(text: dstr[row]) -> void
extern take_view(values: view[i32, 0u, 2u]) -> void
extern take_sview(text: sview[1, 4]) -> void

def use(values: array[i32, 4], dyn: darray[i32, row], text: str[5], dyn_text: dstr[row]) -> char:
	sub_array: view[i32, 0u, 2u] = values[0u:2u]
	sub_text: sview[1, 4] = text[1:4]
	dyn_sub: sview[0, 1] = dyn_text[0:1]
	take_array(values)
	take_darray(dyn)
	take_dstr(dyn_text)
	take_view(sub_array)
	take_sview(sub_text)
	take_sview(dyn_sub)
	return text[0]
`
	_, errs := parseAndAnalyze(t, "builtin_surface_types.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsCharCastAndComparisonFromStringIndexing(t *testing.T) {
	src := `def first_code(text: str[4]) -> i64:
	ch: char = text[0]
	return ch.i64()

def same_head(left: dstr[row], right: dstr[col]) -> bool:
	return left[0] == right[0]
`
	_, errs := parseAndAnalyze(t, "char_cast_and_compare.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsStandaloneCharLocalsParamsAndCasts(t *testing.T) {
	src := `def normalize(code: i64) -> char:
	ch: char = code.char()
	if ch == 0.char():
		return 65.char()
	return ch

def bump(ch: char) -> i64:
	return (ch + 1).i64()
`
	_, errs := parseAndAnalyze(t, "standalone_char_values.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsExportedConcreteWrappers(t *testing.T) {
	src := `struct Vec[T]:
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export func vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result, errs := parseAndAnalyze(t, "export_wrappers.llcontext", src)
	requireNoErrors(t, errs)
	if len(result.ExportedTypes) != 1 {
		t.Fatalf("expected 1 exported type, got %d", len(result.ExportedTypes))
	}
	if len(result.ExportedFuncs) != 2 {
		t.Fatalf("expected 2 exported funcs, got %d", len(result.ExportedFuncs))
	}
	if len(result.ExportedGlobals) != 1 {
		t.Fatalf("expected 1 exported global, got %d", len(result.ExportedGlobals))
	}
	if _, ok := result.NamedTypes["Vec2i"]; !ok {
		t.Fatal("expected Vec2i type alias to be available")
	}
	if result.ExportedGlobals[0].PublicName != "ctx_seed" {
		t.Fatalf("expected exported global name ctx_seed, got %s", result.ExportedGlobals[0].PublicName)
	}
	if result.ExportedFuncs[1].TargetGenericDecl == nil {
		t.Fatal("expected generic export target metadata for vec2i_keep_left")
	}
	if result.ExportedFuncs[1].TargetBindings["T"] == nil {
		t.Fatal("expected generic export binding for keep_left")
	}
	if result.ExportedFuncs[0].Signature.Return.String() != "Vec[i32]" {
		t.Fatalf("expected exported wrapper return to resolve concretely, got %s", result.ExportedFuncs[0].Signature.Return.String())
	}
}

func TestAnalyzeAcceptsConcreteRefQualifierExports(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32

repr(c) struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

export type Node as CtxNode
export type Handle[heap, &] as HeapHandle

def keep_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

export func keep_heap_handle(value: HeapHandle) -> HeapHandle = keep_handle[heap, &]
`
	result, errs := parseAndAnalyze(t, "export_ref_qualifier_wrappers.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	if len(result.ExportedTypes) != 2 {
		t.Fatalf("expected 2 exported types, got %d", len(result.ExportedTypes))
	}
	if len(result.ExportedFuncs) != 1 {
		t.Fatalf("expected 1 exported func, got %d", len(result.ExportedFuncs))
	}
	if got := result.ExportedTypes[1].Type.String(); got != "Handle[heap, &]" {
		t.Fatalf("expected concrete exported handle type, got %s", got)
	}
	bindings := result.ExportedFuncs[0].TargetBindings
	storageBinding, ok := bindings["Store"].(*semantic.RefStorageValueType)
	if !ok {
		t.Fatalf("expected concrete refstorage binding, got %#v", bindings["Store"])
	}
	if storageBinding.Storage != semantic.RefStorageHeap {
		t.Fatalf("expected heap refstorage binding, got %v", storageBinding.Storage)
	}
	stateBinding, ok := bindings["State"].(*semantic.RefStateValueType)
	if !ok {
		t.Fatalf("expected concrete refstate binding, got %#v", bindings["State"])
	}
	if stateBinding.State != semantic.RefStateNonNull {
		t.Fatalf("expected non-null refstate binding, got %v", stateBinding.State)
	}
	if got := result.ExportedFuncs[0].Signature.Return.String(); got != "Handle[heap, &]" {
		t.Fatalf("expected concrete exported handle return type, got %s", got)
	}
}

func TestAnalyzeRejectsInvalidRefQualifierExportTypeArgs(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32

repr(c) struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

export type Handle[i32, &] as BadHandle
`
	_, errs := parseAndAnalyze(t, "export_ref_qualifier_non_concrete_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "generic argument \"i32\" for refstorage parameter \"Store\" must be a refstorage literal or parameter") {
		t.Fatalf("expected refstorage export-type argument diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCollectsKnownFunctionAnnotations(t *testing.T) {
	src := `@test
def sample_case() -> void:
	pass

@fixture
def shared_seed() -> int:
	return 7

@bench
def hot_loop() -> void:
	pass
`
	result, errs := parseAndAnalyze(t, "function_annotations_ok.llcontext", src)
	requireNoErrors(t, errs)
	if len(result.AnnotatedFuncs) != 3 {
		t.Fatalf("expected 3 annotated funcs, got %d", len(result.AnnotatedFuncs))
	}
	if result.AnnotatedFuncs[0].Name != "sample_case" {
		t.Fatalf("expected first annotated func to be sample_case, got %q", result.AnnotatedFuncs[0].Name)
	}
	if len(result.AnnotatedFuncs[0].Annotations) != 1 {
		t.Fatalf("expected sample_case to carry 1 annotation, got %d", len(result.AnnotatedFuncs[0].Annotations))
	}
	if got := result.AnnotatedFuncs[0].Annotations[0].Name; got != "test" {
		t.Fatalf("expected first annotation to be test, got %q", got)
	}
	if result.AnnotatedFuncs[0].Signature == nil || result.AnnotatedFuncs[0].Signature.Return.String() != "void" {
		t.Fatalf("expected sample_case signature to resolve to void return, got %#v", result.AnnotatedFuncs[0].Signature)
	}
	if result.AnnotatedFuncs[1].Name != "shared_seed" {
		t.Fatalf("expected second annotated func to be shared_seed, got %q", result.AnnotatedFuncs[1].Name)
	}
	if len(result.AnnotatedFuncs[1].Annotations) != 1 || result.AnnotatedFuncs[1].Annotations[0].Name != "fixture" {
		t.Fatalf("expected shared_seed to carry a single fixture annotation, got %#v", result.AnnotatedFuncs[1].Annotations)
	}
	if result.AnnotatedFuncs[1].Signature == nil || result.AnnotatedFuncs[1].Signature.Return.String() != "int" {
		t.Fatalf("expected shared_seed signature to resolve to int return, got %#v", result.AnnotatedFuncs[1].Signature)
	}
	if result.AnnotatedFuncs[2].Name != "hot_loop" {
		t.Fatalf("expected third annotated func to be hot_loop, got %q", result.AnnotatedFuncs[2].Name)
	}
	if len(result.AnnotatedFuncs[2].Annotations) != 1 || result.AnnotatedFuncs[2].Annotations[0].Name != "bench" {
		t.Fatalf("expected hot_loop to carry a single bench annotation, got %#v", result.AnnotatedFuncs[2].Annotations)
	}
}

func TestAnalyzeRejectsUnknownFunctionAnnotation(t *testing.T) {
	src := `@smoke
def sample_case() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_unknown.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unknown function annotation @smoke") {
		t.Fatalf("expected unknown-annotation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUnknownExternFunctionAnnotation(t *testing.T) {
	src := `@smoke
extern borrow_value(holder: any i32&) -> any i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_annotations_unknown.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unknown extern function annotation @smoke on \"borrow_value\"") {
		t.Fatalf("expected unknown-extern-annotation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsExternBorrowsReturnUnknownParam(t *testing.T) {
	src := `@borrows_return(missing)
extern borrow_value(holder: any i32&) -> any i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_annotations_unknown_param.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return on extern function \"borrow_value\" references unknown parameter \"missing\"") {
		t.Fatalf("expected borrows_return unknown-param diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsExternBorrowsReturnOnNonProvenanceParam(t *testing.T) {
	src := `@borrows_return(count)
extern borrow_value(count: i32) -> any i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_annotations_bad_param.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return on extern function \"borrow_value\" cannot borrow from parameter \"count\" of type i32") {
		t.Fatalf("expected borrows_return bad-param diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsDuplicateFunctionAnnotation(t *testing.T) {
	src := `@test
@test
def sample_case() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_duplicate.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "duplicate @test annotation on function \"sample_case\"") {
		t.Fatalf("expected duplicate-annotation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsTestFunctionParameters(t *testing.T) {
	src := `@test
def sample_case(value: int) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "function_annotations_test_params.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@test function \"sample_case\" must not take parameters") {
		t.Fatalf("expected test-parameter diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsBenchFunctionNonVoidReturn(t *testing.T) {
	src := `@bench
def hot_loop() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_bench_return.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@bench function \"hot_loop\" must return void, got int") {
		t.Fatalf("expected bench-return diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsGenericFixtureFunction(t *testing.T) {
	src := `@fixture
def shared_seed[T]() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_fixture_generic.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@fixture function \"shared_seed\" must not have type or shape parameters") {
		t.Fatalf("expected fixture-generic diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsExportedNonGlobalSymbol(t *testing.T) {
	src := `const MAGIC = 1337

export global MAGIC as ctx_magic
`
	_, errs := parseAndAnalyze(t, "export_non_global_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "must target a global") {
		t.Fatalf("expected exported-global target rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsExportedArrayBoundaryTypes(t *testing.T) {
	src := `def pass_array(value: i32[4]) -> i32[4]:
	return value

export func pass_array_c(value: i32[4]) -> i32[4] = pass_array
`
	_, errs := parseAndAnalyze(t, "export_array_boundary_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "export func \"pass_array_c\" is not C-ABI-compatible") {
		t.Fatalf("expected export array boundary rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeTernaryRefinesNullablePointerBranch(t *testing.T) {
	src := `def choose_text(value: any u8&?) -> any u8&:
	return value if value != null else "".cast[any u8&]()
`
	_, errs := parseAndAnalyze(t, "ternary_refinement.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNullableToNonNullCastWithoutProof(t *testing.T) {
	src := `repr(c) struct Box:
    value: int

extern maybe_box() -> any Box&?

def bad() -> any Box&:
    box: any Box&? = maybe_box()
    return box.cast[any Box&]()
`
	_, errs := parseAndAnalyze(t, "nonnull_cast_rejection.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "invalid cast from any Box&? to any Box&") {
		t.Fatalf("expected invalid cast diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsReferenceComparisons(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

extern maybe_box() -> any Box&?

def is_missing() -> bool:
	return maybe_box() == null

def is_present() -> bool:
	return maybe_box() != null

def same_box(left: any Box&, right: any Box&) -> bool:
	return left == right
`
	_, errs := parseAndAnalyze(t, "reference_comparisons.llcontext", src)
	requireNoErrors(t, errs)
}

func TestParseRejectsBareReferenceTypeSyntax(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

def read(box: Box&) -> int:
	return box.value
`
	_, errs := parseAndAnalyze(t, "bare_reference_type_parse_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parse error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference types require an explicit storage qualifier") {
		t.Fatalf("expected explicit-storage parse diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestParseRejectsLegacyDotReferenceCastSyntax(t *testing.T) {
	src := `def bits_ptr(bits: uintptr) -> any u8&:
	return bits.u8&()
`
	_, errs := parseAndAnalyze(t, "legacy_reference_cast_parse_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parse error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "legacy reference cast syntax is no longer supported") {
		t.Fatalf("expected legacy reference cast parse diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsStorageQualifiedPointersAndCastSyntax(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

extern maybe_heap_box() -> heap Box&?

def widen(box: heap Box&?) -> any Box&?:
	return box.cast[any Box&?]()

def keep_heap(box: heap Box&?) -> heap Box&?:
	return box.cast[heap Box&?]()

def coerce_text() -> any u8&:
	return "hello".cast[any u8&]()

def use_source() -> any Box&?:
	return maybe_heap_box().cast[any Box&?]()
`
	_, errs := parseAndAnalyze(t, "storage_cast_syntax.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsImplicitStorageMismatchWithoutCast(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

def bad(box: heap Box&) -> any Box&:
	return box
`
	_, errs := parseAndAnalyze(t, "storage_mismatch_without_cast.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects any Box&, got heap Box&") {
		t.Fatalf("expected storage-mismatch diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsEquivalentConstArrayShapes(t *testing.T) {
	src := `const N: usize = 4

def same_shape(buf: u8[N]) -> u8[2 + 2]:
    return buf
`
	_, errs := parseAndAnalyze(t, "equivalent_const_array_shapes.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsMismatchedFixedArrayShapes(t *testing.T) {
	src := `def bad(buf: u8[4]) -> u8[5]:
    return buf
`
	_, errs := parseAndAnalyze(t, "mismatched_fixed_array_shapes.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects u8[5], got u8[4]") {
		t.Fatalf("expected fixed-array mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsRuntimeArraySizeExpression(t *testing.T) {
	src := `def bad(n: usize) -> void:
    buf: u8[n] = zeroed
`
	_, errs := parseAndAnalyze(t, "runtime_array_size.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "array size must be a compile-time integer") {
		t.Fatalf("expected compile-time array size diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsConstantOutOfBoundsArrayIndex(t *testing.T) {
	src := `const IDX: usize = 4

def bad() -> u8:
    buf: u8[4] = zeroed
    return buf[IDX]
`
	_, errs := parseAndAnalyze(t, "constant_oob_array_index.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "constant index 4 out of bounds for u8[4]") {
		t.Fatalf("expected out-of-bounds diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsConstantInBoundsArrayIndex(t *testing.T) {
	src := `const IDX: usize = 3

def read_last() -> u8:
    buf: u8[4] = zeroed
    return buf[IDX]
`
	_, errs := parseAndAnalyze(t, "constant_in_bounds_array_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsModuloExpressionsAndConstModulo(t *testing.T) {
	src := `const IDX: usize = 10 % 3

def remainder(left: i32, right: i32) -> i32:
    return left % right

def read_second() -> u8:
    buf: u8[4] = zeroed
    return buf[IDX]
`
	_, errs := parseAndAnalyze(t, "modulo_expr_and_const.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPointerArithmetic(t *testing.T) {
	src := `def advance(ptr: any u8&, offset: usize) -> any u8&:
	return ptr + offset

def advance_commutative(offset: usize, ptr: any u8&) -> any u8&:
	return offset + ptr

def rewind(ptr: any u8&, offset: usize) -> any u8&:
	return ptr - offset
`
	_, errs := parseAndAnalyze(t, "pointer_arithmetic.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsManualRegions(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024u)
	value: any i32& = new[scratch] seed + 1
	return value[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsExplicitRegionQualifiedRefs(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024u)
	value: scratch i32& = new[scratch] seed + 1
	alias: scratch i32& = value
	return value[0u] + alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_explicit_ref_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsExplicitRegionParamsOnFunctions(t *testing.T) {
	src := `def id[T, region r](value: r T&) -> r T&:
	alias: r T& = value
	return alias

def use(seed: i32) -> i32:
	region scratch(1024u)
	value: scratch i32& = new[scratch] seed + 1
	alias: scratch i32& = id(value)
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "function_region_params_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsExplicitRegionParamsOnExternFunctions(t *testing.T) {
	src := `extern borrow[region r](value: r i32&) -> r i32&

def use(seed: i32) -> i32:
	region scratch(1024u)
	value: scratch i32& = new[scratch] seed + 1
	alias: scratch i32& = borrow(value)
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "extern_function_region_params_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsExplicitAndInferredPermissions(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console.Write]

def explicit() -> void can[Console]:
	emit(1) can Console.Write

def inferred() -> void:
	emit(2) can Console.Write

def scoped() -> void:
	can Console.Write:
		emit(3)
`
	result, errs := parseAndAnalyze(t, "permissions_explicit_and_inferred_ok.llcontext", src)
	requireNoErrors(t, errs)

	for _, name := range []string{"explicit", "inferred", "scoped"} {
		sym, ok := result.GlobalScope.Lookup(name)
		if !ok {
			t.Fatalf("expected %s symbol", name)
		}
		fn, ok := sym.Type.(*semantic.FuncType)
		if !ok {
			t.Fatalf("expected %s to have function type, got %T", name, sym.Type)
		}
		if len(fn.Permissions) != 1 || fn.Permissions[0] != "Console" {
			t.Fatalf("expected %s to infer can[Console], got %#v", name, fn.Permissions)
		}
		if len(fn.PermissionRefs) == 0 {
			t.Fatalf("expected %s to preserve permission refs, got none", name)
		}
	}
	if warns := result.Warnings(); len(warns) == 0 {
		t.Fatal("expected inferred-permission warnings, got none")
	}
}

func TestAnalyzeAcceptsBuiltinConcurrencyPermissionFamilies(t *testing.T) {
	src := `def use() -> void can[Thread.Spawn, Thread.Join, Thread.Detach, Pool.Create, Pool.Submit, Pool.Await, Pool.WaitAll, Pool.Shutdown, Sync.Lock, Sync.Unlock, Sync.Wait, Sync.Notify, Atomics.Load, Atomics.Store, Atomics.Exchange, Atomics.CompareExchange, Atomics.Rmw, Atomics.Fence]:
	pass
`
	result, errs := parseAndAnalyze(t, "builtin_concurrency_permissions_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "use",
		"Atomics.CompareExchange",
		"Atomics.Exchange",
		"Atomics.Fence",
		"Atomics.Load",
		"Atomics.Rmw",
		"Atomics.Store",
		"Pool.Await",
		"Pool.Create",
		"Pool.Shutdown",
		"Pool.Submit",
		"Pool.WaitAll",
		"Sync.Lock",
		"Sync.Notify",
		"Sync.Unlock",
		"Sync.Wait",
		"Thread.Detach",
		"Thread.Join",
		"Thread.Spawn",
	)
}

func TestAnalyzeAcceptsBuiltinConcurrencyCarrierTypes(t *testing.T) {
	src := `extern detach(thread: Thread[i64, Joinable]) -> void
extern mutex_unlock(g: MutexGuard[Held]) -> void
extern pool_await(task: Task[i64, Pending]) -> i64

def touch(thread: Thread[i64, Joinable], task: Task[i64, Pending], pool: ThreadPool, group: TaskGroup, mu: Mutex, guard: MutexGuard[Held], cv: CondVar, slot: atomic[i64]) -> void:
	_ = thread.handle
	_ = task.handle
	_ = pool.handle
	_ = group.handle
	_ = mu.handle
	_ = guard.handle
	_ = cv.handle
	copy: atomic[i64] = slot
	_ = copy
	detach(move thread)
	mutex_unlock(move guard)
	_ = pool_await(move task)
`
	result, errs := parseAndAnalyze(t, "builtin_concurrency_carriers_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeAcceptsAtomicSafePayloadTypes(t *testing.T) {
	src := `def touch(counter: atomic[i64], ready: atomic[bool], ptrs: atomic[any u8&]) -> void:
	_ = counter.value
	_ = ready.value
	_ = ptrs.value
`
	result, errs := parseAndAnalyze(t, "atomic_safe_payloads_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeRejectsAtomicPayloadOfAggregateStruct(t *testing.T) {
	src := `repr(c) struct Pair:
	left: i64
	right: i64

def bad(slot: atomic[Pair]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "atomic_aggregate_payload_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "atomic payload type must satisfy atomic_safe(T), got Pair") {
		t.Fatalf("expected atomic_safe aggregate diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsAtomicPayloadOfAffineHandle(t *testing.T) {
	src := `def bad(slot: atomic[Thread[i64, Joinable]]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "atomic_affine_payload_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "atomic payload type must satisfy atomic_safe(T), got Thread[i64, Joinable]") {
		t.Fatalf("expected atomic_safe affine diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsMissingProtocolStateTypeArguments(t *testing.T) {
	src := `def bad(thread: Thread[i64]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "concurrency_protocol_arity_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "type \"Thread\" expects 2 type arguments, got 1") {
		t.Fatalf("expected protocol-state arity diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsProtocolStateMismatchAtCallSite(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def bad(thread: Thread[i64, Pending]) -> i64:
	return join(move thread)
`
	_, errs := parseAndAnalyze(t, "concurrency_protocol_state_mismatch_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 1 to \"join\" expects Thread[i64, Joinable], got Thread[i64, Pending]") {
		t.Fatalf("expected protocol-state mismatch diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReferencesToUserDeclaredAffineStruct(t *testing.T) {
	src := `affine repr(c) struct Handle:
	raw: mutable uintptr

def bad(handle: Handle) -> void:
	borrow: any Handle& = &handle
	_ = borrow
`
	_, errs := parseAndAnalyze(t, "user_affine_ref_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot take address of affine value") {
		t.Fatalf("expected user-affine address diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsGlobalUserDeclaredAffineStruct(t *testing.T) {
	src := `affine repr(c) struct Handle:
	raw: mutable uintptr

global current: Handle = zeroed
`
	_, errs := parseAndAnalyze(t, "user_affine_global_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "global \"current\" cannot store affine handle values of type Handle") {
		t.Fatalf("expected user-affine global diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWarnsOnMissingPermissionGrant(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console.Write]

def bad() -> void:
	emit(1)
`
	result, errs := parseAndAnalyze(t, "permissions_missing_grant_warn.llcontext", src)
	requireNoErrors(t, errs)
	warns := strings.Join(result.Warnings(), "\n")
	if warns == "" {
		t.Fatal("expected semantic warning, got none")
	}
	if !strings.Contains(warns, "call to \"emit\" requires can[Console]") {
		t.Fatalf("expected missing-permission warning, got:\n%s", warns)
	}
}

func TestAnalyzePropagatesForwardReferencedInferredPermissionCalls(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console.Write]

def caller() -> void:
	callee()

def callee() -> void:
	emit(1) can Console.Write
`
	result, errs := parseAndAnalyze(t, "permissions_forward_reference_propagates.llcontext", src)
	requireNoErrors(t, errs)
	sym, ok := result.GlobalScope.Lookup("caller")
	if !ok {
		t.Fatal("expected caller symbol")
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected caller function type, got %T", sym.Type)
	}
	if len(fn.Permissions) != 1 || fn.Permissions[0] != "Console" {
		t.Fatalf("expected caller to infer can[Console], got %#v", fn.Permissions)
	}
	if !strings.Contains(strings.Join(result.Warnings(), "\n"), "call to \"callee\" requires can[Console]") {
		t.Fatalf("expected forward-reference permission warning, got:\n%s", strings.Join(result.Warnings(), "\n"))
	}
}

func TestAnalyzeRejectsUnknownPermissionMember(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console]

def bad() -> void:
	emit(1) can Console.Read
`
	_, errs := parseAndAnalyze(t, "permissions_unknown_member_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "permission \"Console\" has no member \"Read\"") {
		t.Fatalf("expected unknown-member diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeInfersBuiltinAbortPermissionFromPanic(t *testing.T) {
	src := `def fail_fast() -> void:
	panic("boom")
`
	result, errs := parseAndAnalyze(t, "builtin_abort_from_panic.llcontext", src)
	requireNoErrors(t, errs)
	sym, ok := result.GlobalScope.Lookup("fail_fast")
	if !ok {
		t.Fatal("expected fail_fast symbol")
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected fail_fast function type, got %T", sym.Type)
	}
	if len(fn.Permissions) != 1 || fn.Permissions[0] != "Abort" {
		t.Fatalf("expected fail_fast to infer can[Abort], got %#v", fn.Permissions)
	}
	warns := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warns, "function \"fail_fast\" infers can[Abort]") || !strings.Contains(warns, "can[Abort.Panic]") {
		t.Fatalf("expected implicit-abort warning, got:\n%s", warns)
	}
}

func TestAnalyzeRejectsCallsWhenRegionParamCannotBeInferred(t *testing.T) {
	src := `def id[region r](value: r i32&) -> r i32&:
	return value

def use(value: any i32&) -> any i32&:
	return id(value)
`
	_, errs := parseAndAnalyze(t, "function_region_params_inference_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot infer region parameter \"r\" for call to \"id\"") {
		t.Fatalf("expected region inference diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsMismatchedRegionQualifiedRefs(t *testing.T) {
	src := `def bad() -> i32:
	region left(1024u)
	region right(1024u)
	value: left i32& = new[left] 1
	other: right i32& = value
	return other[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_mismatched_ref_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "variable \"other\" expects right i32&, got left i32&") {
		t.Fatalf("expected region-qualified mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUnknownRegionQualifiedRef(t *testing.T) {
	src := `def bad() -> void:
	value: scratch i32&? = null
`
	_, errs := parseAndAnalyze(t, "manual_regions_unknown_ref_qualifier_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unknown region qualifier \"scratch\"") {
		t.Fatalf("expected unknown-region qualifier diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsReturningReferenceAllocatedFromLocalRegion(t *testing.T) {
	src := `def bad() -> any i32&:
	region scratch(1024u)
	value: any i32& = new[scratch] 1
	return value
`
	_, errs := parseAndAnalyze(t, "manual_regions_return_ref_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference allocated from local region \"scratch\"") {
		t.Fatalf("expected region-escape return diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsReturningCastedReferenceAllocatedFromLocalRegion(t *testing.T) {
	src := `def bad() -> any i32&:
	region scratch(1024u)
	value: any i32& = new[scratch] 1
	return value.cast[any i32&]()
`
	_, errs := parseAndAnalyze(t, "manual_regions_return_cast_ref_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference allocated from local region \"scratch\"") {
		t.Fatalf("expected region-escape return diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsRegionCheckpoints(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024u)
	mark scratch as cp
	temp: any i32& = new[scratch] seed + 1
	restore scratch from cp
	reused: any i32& = new[scratch] seed + 2
	value: i32 = reused[0u]
	reset scratch
	final: any i32& = new[scratch] seed + 3
	return value + final[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_checkpoints_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsNestedRegionCheckpoints(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024u)
	base: any i32& = new[scratch] seed
	baseline: i32 = base[0u]
	mark scratch as outer
	stable: any i32& = new[scratch] seed + 1
	mark scratch as inner
	temp: any i32& = new[scratch] seed + 2
	restore scratch from inner
	kept: i32 = stable[0u]
	restore scratch from outer
	final: any i32& = new[scratch] seed + 3
	return baseline + kept + final[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_nested_checkpoints_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsEnumConstructorsAndMatch(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	match value:
		MaybeInt.None:
			return fallback
		MaybeInt.Some(inner):
			return inner
		MaybeInt.Pair(left, right):
			return left + right

def make_pair() -> MaybeInt:
	return MaybeInt.Pair(3, 4)

def make_none() -> MaybeInt:
	return MaybeInt.None
`
	_, errs := parseAndAnalyze(t, "enum_match_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsMatchExpressionsAndNestedPatterns(t *testing.T) {
	src := `enum Inner:
	A(int)
	B

enum Outer:
	Wrap(Inner)
	Empty

def score(value: Outer) -> int:
	return match value:
		Outer.Wrap(Inner.A(inner)):
			inner
		Outer.Wrap(Inner.B):
			0
		Outer.Empty:
			-1
`
	_, errs := parseAndAnalyze(t, "enum_match_expr_nested_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsNamedEnumPayloadFieldsAndPatterns(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(3, 4)

def score(value: PairOrInt) -> int:
	return match value:
		PairOrInt.Just(value: inner):
			inner
		PairOrInt.Pair(right: r, left: l):
			l + r
`
	_, errs := parseAndAnalyze(t, "enum_named_payloads_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsNamedEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(right: 4, left: 3)
`
	_, errs := parseAndAnalyze(t, "enum_named_ctor_args_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNamedPatternsForUnnamedEnumPayloads(t *testing.T) {
	src := `enum MaybeInt:
	Some(int)

def unwrap(value: MaybeInt) -> int:
	return match value:
		MaybeInt.Some(value: inner):
			inner
`
	_, errs := parseAndAnalyze(t, "enum_named_payloads_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "match arm \"MaybeInt.Some\" does not declare named payload fields") {
		t.Fatalf("expected named-payload diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsNamedConstructorArgsForUnnamedEnumPayloads(t *testing.T) {
	src := `enum MaybeInt:
	Some(int)

def make_some() -> MaybeInt:
	return MaybeInt.Some(value: 1)
`
	_, errs := parseAndAnalyze(t, "enum_named_ctor_args_unnamed_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum constructor \"MaybeInt.Some\" does not declare named payload fields") {
		t.Fatalf("expected named-constructor diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsMixedNamedAndPositionalEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(left: 3, 4)
`
	_, errs := parseAndAnalyze(t, "enum_named_ctor_args_mixed_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum constructor \"PairOrInt.Pair\" cannot mix positional and named arguments") {
		t.Fatalf("expected mixed-argument diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsNamedArgumentsForNonEnumCalls(t *testing.T) {
	src := `extern add(left: int, right: int) -> int

def bad() -> int:
	return add(left: 1, right: 2)
`
	_, errs := parseAndAnalyze(t, "named_args_non_enum_call_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "named arguments are only supported for enum constructors") {
		t.Fatalf("expected non-enum named-argument diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsShadowedStatementMatchArms(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)

def unwrap(value: MaybeInt) -> int:
	match value:
		MaybeInt.Some(first):
			return first
		MaybeInt.Some(second):
			return second
		MaybeInt.None:
			return 0
	return 0
`
	_, errs := parseAndAnalyze(t, "enum_match_shadowed_stmt.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "match arm \"MaybeInt.Some(second)\" is unreachable because an earlier arm already matches it") {
		t.Fatalf("expected shadowed-arm diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsNonExhaustiveMatchesWithMissingVariants(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def unwrap(value: MaybeInt) -> int:
	return match value:
		MaybeInt.Some(inner):
			inner
`
	_, errs := parseAndAnalyze(t, "enum_match_non_exhaustive.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "non-exhaustive match over \"MaybeInt\"; missing variants: MaybeInt.None, MaybeInt.Pair") {
		t.Fatalf("expected non-exhaustive match diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsRecursiveEnumPayloadByValue(t *testing.T) {
	src := `enum Expr:
	Int(int)
	Add(Expr, Expr)
`
	_, errs := parseAndAnalyze(t, "enum_recursive_by_value_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum \"Expr\" variant \"Add\" cannot contain \"Expr\" by value; use a reference type instead") {
		t.Fatalf("expected recursive-enum by-value diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsRecursiveEnumPayloadByReference(t *testing.T) {
	src := `enum Expr:
	Int(value: int)
	Add(left: any Expr&, right: any Expr&)

def eval(node: any Expr&) -> int:
	return match node[0u]:
		Expr.Int(value: value):
			value
		Expr.Add(left: left, right: right):
			eval(left) + eval(right)
`
	_, errs := parseAndAnalyze(t, "enum_recursive_by_ref_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPackedEnumsWithExplicitStoreCore(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def build(store_owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(store_owner)
	one: Expr = new[store] Expr.Int(value: 1)
	two: Expr = new[store] Expr.Int(value: 2)
	return new[store] Expr.Add(left: one, right: two)

def eval(node: Expr, store: Expr.Store[Local]) -> int:
	return match node in store:
		Expr.Int(value: value):
			value
		Expr.Add(left: left, right: right):
			eval(left, store) + eval(right, store)
`
	_, errs := parseAndAnalyze(t, "packed_enum_explicit_store_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPackedEnumsWithinInStoreBlocks(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def build(store_owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(store_owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 1)
		right: Expr = new Expr.Int(span: 2, value: 2)
		return new Expr.Add(span: 3, left: left, right: right)

def eval(node: Expr, store: Expr.Store[Local]) -> int:
	in store:
		return match node:
			Expr.Int(value: value):
				value + node.span
			Expr.Add(left: left, right: right):
				node.span + eval(left, store) + eval(right, store)
`
	_, errs := parseAndAnalyze(t, "packed_enum_in_store_block_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPackedEnumTailPayloadsAsDynamicViews(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def build() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Block(count: 3u, items: [1, 2, 3])
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Expr.Block(count: count, items: items):
			if items.len == count:
				items[0u] + items[2u]
			0
`
	result, errs := parseAndAnalyze(t, "packed_enum_tail_payload_dview_ok.llcontext", src)
	requireNoErrors(t, errs)

	enumType, ok := result.NamedTypes["Expr"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Expr enum type, got %T", result.NamedTypes["Expr"])
	}
	variant, ok := enumType.Variant("Block")
	if !ok || variant == nil {
		t.Fatal("expected Expr.Block variant metadata")
	}
	if !variant.HasTailPayload() {
		t.Fatal("expected Expr.Block to record a tail payload")
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok || tailIndex != 1 {
		t.Fatalf("expected Expr.Block tail payload index 1, got %d (ok=%v)", tailIndex, ok)
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		t.Fatal("expected Expr.Block tail payload to lower as dview")
	}
	if viewType.String() != "dview[int]" {
		t.Fatalf("expected Expr.Block tail payload type dview[int], got %s", viewType.String())
	}
}

func TestAnalyzeRejectsTailPayloadsOnOrdinaryEnums(t *testing.T) {
	src := `enum Expr:
	Block(items: tail int)
`
	_, errs := parseAndAnalyze(t, "enum_tail_payload_non_packed_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "enum \"Expr\" variant \"Block\" tail payloads are only supported for packed enums") {
		t.Fatalf("expected ordinary-enum tail payload diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsNonFinalPackedEnumTailPayloads(t *testing.T) {
	src := `packed enum Expr:
	Block(items: tail int, count: usize)
`
	_, errs := parseAndAnalyze(t, "packed_enum_tail_payload_not_final_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum \"Expr\" variant \"Block\" tail payload \"items\" must be the final payload field") {
		t.Fatalf("expected non-final tail payload diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsFreezeOfLocalPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def freeze_store(owner: Arena) -> Expr.Store[Frozen]:
	store: Expr.Store[Local] = Expr.Store(owner)
	return freeze(move store)
`
	result, errs := parseAndAnalyze(t, "packed_enum_store_freeze_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "freeze_store", "Expr.Store[Frozen]")
}

func TestAnalyzeRejectsAllocatingPackedEnumIntoFrozenStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(owner)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return new[frozen] Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_frozen_alloc_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum allocation requires local store type \"Expr.Store[Local]\", got Expr.Store[Frozen]") {
		t.Fatalf("expected frozen-store allocation diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsPackedMatchWithFrozenStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	return match node in store:
		Expr.Int(value: value):
			value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_frozen_store_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsBarePackedEnumConstructorCall(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad() -> Expr:
	return Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_ctor_without_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum constructor \"Expr.Int\" must be allocated with new[Expr.Store]") {
		t.Fatalf("expected packed constructor diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsBarePackedAllocOutsideInStoreScope(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad() -> Expr:
	return new Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_alloc_without_scope_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum constructor \"Expr.Int\" requires an active in Expr.Store: scope or explicit new[Expr.Store]") {
		t.Fatalf("expected in-store allocation diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsPackedMatchWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(node: Expr) -> int:
	return match node:
		Expr.Int(value: value):
			value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_missing_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum match over \"Expr\" requires an in Expr.Store clause") {
		t.Fatalf("expected packed match-store diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsOrdinaryEnumMatchWithStoreClause(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

packed enum PackedExpr:
	Int(value: int)

def bad(node: Expr, store: PackedExpr.Store[Local]) -> int:
	return match node in store:
		Expr.Int(value: value):
			value
`
	_, errs := parseAndAnalyze(t, "ordinary_enum_match_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "ordinary enum match over \"Expr\" does not take an in-store clause") {
		t.Fatalf("expected ordinary enum in-store rejection, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsPackedCommonFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr) -> int:
	return node.span
`
	_, errs := parseAndAnalyze(t, "packed_enum_common_field_access_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPackedCommonFieldInitializationWithNamedArgs(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def build(store_owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(store_owner)
	return new[store] Expr.Int(span: 7, value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_common_field_init_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPayloadlessPackedCommonFieldInitialization(t *testing.T) {
	src := `packed enum Token:
	common:
		span: int
	Region

def build(store_owner: Arena) -> Token:
	store: Token.Store[Local] = Token.Store(store_owner)
	return new[store] Token.Region(span: 4)
`
	_, errs := parseAndAnalyze(t, "packed_enum_payloadless_common_field_init_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPayloadlessPackedConstructorInAlloc(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build(store_owner: Arena) -> Token:
	store: Token.Store[Local] = Token.Store(store_owner)
	return new[store] Token.Region
`
	_, errs := parseAndAnalyze(t, "packed_enum_payloadless_alloc_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsAffinePayloadsInPackedEnums(t *testing.T) {
	src := `affine repr(c) struct Handle:
	raw: mutable uintptr

packed enum Expr:
	common:
		handle: Handle
	Run(value: Thread[i64, Joinable])
`
	_, errs := parseAndAnalyze(t, "packed_enum_affine_payload_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum \"Expr\" common field \"handle\" cannot contain affine payload type Handle") {
		t.Fatalf("expected packed common-field affine diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "packed enum \"Expr\" variant \"Run\" cannot contain affine payload type Thread[i64, Joinable]") {
		t.Fatalf("expected packed payload affine diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsSpawnOfValueDependingOnUnpublishedPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(node: Expr) -> i64:
	return 0

def bad(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	return spawn1(worker, node)
`
	_, errs := parseAndAnalyze(t, "spawn1_unpublished_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" cannot depend on unpublished packed store \"Expr.Store[Local]\"") {
		t.Fatalf("expected unpublished-store spawn diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsSpawnAndPoolTransferOfValueDependingOnlyOnFrozenStore(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern pool_await(task: Task[i64, Pending]) -> i64

packed enum Expr:
	Int(value: int)

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(node: Expr) -> i64:
	return 0

def ok(owner: Arena, pool: any ThreadPool&) -> i64:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	thread: Thread[i64, Joinable] = spawn1(worker, node)
	task: Task[i64, Pending] = pool_submit1(pool, worker, node)
	_ = join(move thread)
	_ = pool_await(move task)
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_frozen_store_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeAcceptsThreadTransferOfBlessedRuntimeCarriers(t *testing.T) {
	src := `repr(c) struct SharedGate:
	mu: Mutex
	cv: CondVar

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(gate: SharedGate) -> i64:
	return 1

def ok(pool_ref: any ThreadPool&, mu: Mutex, cv: CondVar) -> i64:
	_ = spawn1(worker, SharedGate(mu, cv))
	_ = pool_submit1(pool_ref, worker, SharedGate(mu, cv))
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_runtime_carriers_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeAcceptsSpawnTransferOfStaticRef(t *testing.T) {
	src := `extern shared_cell() -> static i32&

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(cell: static i32&) -> i64:
	return cell[0u].i64()

def ok() -> Thread[i64, Joinable]:
	return spawn1(worker, shared_cell())
`
	result, errs := parseAndAnalyze(t, "spawn1_static_ref_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeRejectsThreadTransferOfNonStaticRef(t *testing.T) {
	src := `def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(cell: any i32&) -> i64:
	return cell[0u].i64()

def bad(pool: any ThreadPool&, cell: any i32&) -> i64:
	_ = spawn1(worker, cell)
	_ = pool_submit1(pool, worker, cell)
	return 0
`
	_, errs := parseAndAnalyze(t, "thread_transfer_non_static_ref_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" is not structurally shareable across threads: any i32&") {
		t.Fatalf("expected non-static-ref spawn diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "argument to \"pool_submit1\" is not structurally shareable across threads: any i32&") {
		t.Fatalf("expected non-static-ref pool diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsThreadTransferOfBlessedRuntimeCarrierResults(t *testing.T) {
	src := `repr(c) struct SharedGate:
	mu: Mutex
	cv: CondVar

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def echo(gate: SharedGate) -> SharedGate:
	return gate

def ok(pool_ref: any ThreadPool&, mu: Mutex, cv: CondVar) -> i64:
	gate: SharedGate = SharedGate(mu, cv)
	_ = spawn1(echo, gate)
	_ = pool_submit1(pool_ref, echo, gate)
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_runtime_carrier_result_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeAcceptsThreadTransferOfStaticRefResult(t *testing.T) {
	src := `extern shared_cell() -> static i32&

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(value: i64) -> static i32&:
	_ = value
	return shared_cell()

def ok(pool: any ThreadPool&) -> i64:
	_ = spawn1(worker, 0)
	_ = pool_submit1(pool, worker, 0)
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_static_ref_result_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeRejectsThreadTransferOfNonStaticRefResult(t *testing.T) {
	src := `extern borrowed_cell() -> any i32&

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(value: i64) -> any i32&:
	_ = value
	return borrowed_cell()

def bad(pool: any ThreadPool&) -> i64:
	_ = spawn1(worker, 0)
	_ = pool_submit1(pool, worker, 0)
	return 0
`
	_, errs := parseAndAnalyze(t, "thread_transfer_non_static_ref_result_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "result of \"spawn1\" is not structurally shareable across threads: any i32&") {
		t.Fatalf("expected non-static-ref spawn-result diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "result of \"pool_submit1\" is not structurally shareable across threads: any i32&") {
		t.Fatalf("expected non-static-ref pool-result diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsSpawnOfNestedValueDependingOnUnpublishedPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

repr(c) struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(box: Box) -> i64:
	_ = box
	return 0

def bad(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	box: Box = wrap_node(node)
	return spawn1(worker, box)
`
	_, errs := parseAndAnalyze(t, "spawn1_nested_unpublished_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" cannot depend on unpublished packed store \"Expr.Store[Local]\"") {
		t.Fatalf("expected nested unpublished-store spawn diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsSpawnOfNestedValueAfterFreezeRemapsPackedStoreRecursively(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

repr(c) struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(box: Box) -> i64:
	_ = box
	return 0

def ok(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	box: Box = wrap_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	_ = frozen
	return spawn1(worker, box)
`
	result, errs := parseAndAnalyze(t, "spawn1_nested_frozen_store_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeRejectsSpawnOfNestedViewDependingOnUnpublishedPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

repr(c) struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(items: view[Box]) -> i64:
	_ = items
	return 0

def bad(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 1] = [wrap_node(node)]
	return spawn1(worker, items[0u:1u])
`
	_, errs := parseAndAnalyze(t, "spawn1_nested_view_unpublished_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" cannot depend on unpublished packed store \"Expr.Store[Local]\"") {
		t.Fatalf("expected nested view unpublished-store spawn diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsSpawnOfNestedViewAfterFreezeRemapsPackedStoreRecursively(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

repr(c) struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(items: view[Box]) -> i64:
	_ = items
	return 0

def ok(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 1] = [wrap_node(node)]
	frozen: Expr.Store[Frozen] = freeze(move store)
	_ = frozen
	return spawn1(worker, items[0u:1u])
`
	result, errs := parseAndAnalyze(t, "spawn1_nested_view_frozen_store_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}

func TestAnalyzeRejectsPoolTransferOfValueDependingOnLocalRegion(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: any i32&)

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(node: Expr) -> i64:
	return 0

def bad(owner: Arena, pool: any ThreadPool&) -> Task[i64, Pending]:
	region scratch
	store: Expr.Store[Local] = Expr.Store(owner)
	cell: any i32& = new[scratch] 1
	node: Expr = new[store] Expr.Hold(value: cell)
	frozen: Expr.Store[Frozen] = freeze(move store)
	_ = frozen
	return pool_submit1(pool, worker, node)
`
	_, errs := parseAndAnalyze(t, "pool_submit_local_region_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"pool_submit1\" cannot depend on local region \"scratch\"") {
		t.Fatalf("expected local-region pool diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsDictSurfaceTypesAndRuntimeBridge(t *testing.T) {
	src := `extern take_runtime(values: DynDict[i32]) -> void
extern make_runtime() -> DynDict[i32]

def id[V](values: dict[dstr, V]) -> dict[dstr, V]:
	return values

def keep(values: dict[dstr, i32]) -> dict[dstr, i32]:
	return id(values)

def use(values: dict[dstr[row], i32]) -> dict[dstr, i32]:
	take_runtime(values)
	return make_runtime()
`
	_, errs := parseAndAnalyze(t, "dict_surface_and_bridge_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUnsupportedDictKeyTypes(t *testing.T) {
	src := `def bad(values: dict[i32, i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "dict_bad_key.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "dict currently only supports dstr keys") {
		t.Fatalf("expected dict-key diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAllocatingFromDestroyedRegion(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	destroy scratch
	value: any i32& = new[scratch] 1
`
	_, errs := parseAndAnalyze(t, "manual_regions_destroyed_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot allocate from destroyed region \"scratch\"") {
		t.Fatalf("expected destroyed-region diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingReferenceInvalidatedByRestore(t *testing.T) {
	src := `def bad() -> i32:
	region scratch
	mark scratch as cp
	value: any i32& = new[scratch] 1
	restore scratch from cp
	return value[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_ref.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"value\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingMoveBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `def bad() -> i32:
	region scratch
	mark scratch as cp
	value: any i32& = new[scratch] 1i32
	move value as alias
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for move-bound alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingStructFieldAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	alias: any i32& = holder.value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_struct_field_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for struct field alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingMoveAsStructBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	move holder as Holder(alias, count)
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_struct_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for move-as struct alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsMoveAsStructScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def ok() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	move holder as Holder(alias, count)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_struct_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsMoveAsPackedVariantDestructure(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left(node: Expr, store: Expr.Store[Frozen]) -> Expr:
	move node in store as Expr.Add(lhs, rhs)
	_ = rhs
	return lhs
`
	result, errs := parseAndAnalyze(t, "move_as_packed_variant_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "left", "Expr")
}

func TestAnalyzeRejectsPackedMoveAsWithWrongStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

packed enum Token:
	Ident

def bad(node: Expr, store: Token.Store[Local]) -> int:
	move node in store as Expr.Int(value)
	return value
`
	_, errs := parseAndAnalyze(t, "move_as_packed_wrong_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum move-as over \"Expr\" requires store type \"Expr.Store\", got Token.Store[Local]") {
		t.Fatalf("expected packed move-as wrong-store diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsUsingMoveAsEnumBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `enum Holder:
	Keep(value: any i32&, count: i32)

def bad() -> i32:
	region scratch
	mark scratch as cp
	value: Holder = Holder.Keep(new[scratch] 1i32, 7i32)
	move value as Holder.Keep(alias, count)
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_enum_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for enum move-as alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsMoveAsEnumScalarAfterRestore(t *testing.T) {
	src := `enum Holder:
	Keep(value: any i32&, count: i32)

def ok() -> i32:
	region scratch
	mark scratch as cp
	value: Holder = Holder.Keep(new[scratch] 1i32, 7i32)
	move value as Holder.Keep(alias, count)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_enum_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingHelperReturnedReferenceInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def borrow_value(holder: Holder) -> any i32&:
	return holder.value

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	alias: any i32& = borrow_value(holder)
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_helper_returned_ref_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for helper-returned reference, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsHelperReturnedScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def borrow_count(holder: Holder) -> i32:
	return holder.count

def ok() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	count: i32 = borrow_count(holder)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_helper_returned_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingHelperReturnedNestedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]

def keep_window(window: Window) -> Window:
	return window

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = keep_window(Window(items[0u:2u]))
	which: usize = 1u
	alias: any i32& = window.items[which].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_helper_nested_view_alias_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for helper-returned nested view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsFreshHelperReturnAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&?
	count: i32

def copy_count(holder: Holder) -> Holder:
	return Holder(null, holder.count)

def ok() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	copy: Holder = copy_count(holder)
	restore scratch from cp
	return copy.count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_fresh_helper_return_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingExternBorrowReturnedReferenceInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

@borrows_return(holder)
extern borrow_value(holder: Holder) -> any i32&

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1i32, 7i32)
	alias: any i32& = borrow_value(holder)
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_borrowed_ref_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern borrowed reference, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingExternBorrowReturnedNestedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]

@borrows_return(window)
extern keep_window(window: Window) -> Window

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = keep_window(Window(items[0u:2u]))
	which: usize = 1u
	alias: any i32& = window.items[which].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_borrowed_nested_view_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern borrowed nested view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingExternPathBorrowReturnedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window) -> view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = Window(items[0u:2u], 9i32)
	selected: view[Holder] = get_items(window)
	which: usize = 1u
	alias: any i32& = selected[which].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_path_borrowed_view_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern path-borrowed view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsExternPathBorrowReturnedViewScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window) -> view[Holder]

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = Window(items[0u:2u], 9i32)
	selected: view[Holder] = get_items(window)
	which: usize = 1u
	count: i32 = selected[which].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_path_borrowed_view_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingExternRefParamBorrowReturnedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: any Window&) -> view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = Window(items[0u:2u], 9i32)
	selected: view[Holder] = get_items((&window).cast[any Window&]())
	which: usize = 1u
	alias: any i32& = selected[which].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_ref_param_view_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern ref-param borrowed view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingExternRefParamBorrowReturnedElementAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items[*])
extern get_item(window: any Window&, which: usize) -> Holder

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = Window(items[0u:2u], 9i32)
	which: usize = 1u
	item: Holder = get_item((&window).cast[any Window&](), which)
	alias: any i32& = item.value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_ref_param_elem_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern ref-param borrowed element alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingExternFieldBorrowReturnedStructFieldInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Box:
	value: any i32&

repr(c) struct Pair:
	left: any i32&
	right: any i32&

@borrows_return_field(left, left_src.value, right, right_src.value)
extern pair_refs(left_src: Box, right_src: Box) -> Pair

def bad() -> i32:
	region left_r
	region right_r
	mark left_r as left_cp
	mark right_r as right_cp
	left_box: Box = Box(new[left_r] 1i32)
	right_box: Box = Box(new[right_r] 2i32)
	pair: Pair = pair_refs(left_box, right_box)
	restore left_r from left_cp
	return pair.left[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_borrow_left_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"pair.left\" is invalid after restore of region \"left_r\" from checkpoint \"left_cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern field-borrowed left field, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsUsingExternFieldBorrowReturnedSiblingFieldAfterUnrelatedRestore(t *testing.T) {
	src := `repr(c) struct Box:
	value: any i32&

repr(c) struct Pair:
	left: any i32&
	right: any i32&

@borrows_return_field(left, left_src.value, right, right_src.value)
extern pair_refs(left_src: Box, right_src: Box) -> Pair

def ok() -> i32:
	region left_r
	region right_r
	mark left_r as left_cp
	mark right_r as right_cp
	left_box: Box = Box(new[left_r] 1i32)
	right_box: Box = Box(new[right_r] 2i32)
	pair: Pair = pair_refs(left_box, right_box)
	restore left_r from left_cp
	return pair.right[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_borrow_right_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingExternRebasedBorrowReturnedSubviewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

@borrows_return_rebased(items)
extern sub_items(items: view[Holder], start: usize, end: usize) -> view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	sub: view[Holder] = sub_items(view, 1u, 2u)
	alias: any i32& = sub[0u].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_rebased_subview_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected rebased subview invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsExternRebasedBorrowReturnedSubviewScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

@borrows_return_rebased(items)
extern sub_items(items: view[Holder], start: usize, end: usize) -> view[Holder]

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	sub: view[Holder] = sub_items(view, 1u, 2u)
	count: i32 = sub[0u].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_rebased_subview_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingExternFieldRebasedBorrowReturnedStructFieldInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct SlicePair:
	items: view[Holder]
	total: i32

@borrows_return_field_rebased(items, src)
extern slice_pair(src: view[Holder], start: usize, end: usize, total: i32) -> SlicePair

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	pair: SlicePair = slice_pair(view, 1u, 2u, 9i32)
	alias: any i32& = pair.items[0u].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_rebased_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected field-rebased invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsExternFieldRebasedSiblingScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct SlicePair:
	items: view[Holder]
	total: i32

@borrows_return_field_rebased(items, src)
extern slice_pair(src: view[Holder], start: usize, end: usize, total: i32) -> SlicePair

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	pair: SlicePair = slice_pair(view, 1u, 2u, 9i32)
	restore scratch from cp
	return pair.total
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_rebased_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsExternBorrowsReturnFieldRebasedOnNonStructReturn(t *testing.T) {
	src := `@borrows_return_field_rebased(items, source)
extern bad(source: any i32&) -> any i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_field_rebased_non_struct_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return_field_rebased on extern function \"bad\" requires a concrete struct return type, got any i32&") {
		t.Fatalf("expected field-rebased non-struct diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingExternNestedFieldBorrowReturnedAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Meta:
	items: view[Holder]
	total: i32

repr(c) struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field(meta.items, src)
extern wrap_meta(src: view[Holder], total: i32, tag: i32) -> Wrapper

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	wrapped: Wrapper = wrap_meta(view, 9i32, 5i32)
	alias: any i32& = wrapped.meta.items[0u].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected nested field-path invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsExternNestedFieldBorrowReturnedSiblingScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Meta:
	items: view[Holder]
	total: i32

repr(c) struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field(meta.items, src)
extern wrap_meta(src: view[Holder], total: i32, tag: i32) -> Wrapper

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	wrapped: Wrapper = wrap_meta(view, 9i32, 5i32)
	restore scratch from cp
	return wrapped.meta.total + wrapped.tag
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingExternNestedFieldRebasedBorrowReturnedAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Meta:
	items: view[Holder]
	total: i32

repr(c) struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Holder], start: usize, end: usize, total: i32, tag: i32) -> Wrapper

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	wrapped: Wrapper = wrap_submeta(view, 1u, 2u, 9i32, 5i32)
	alias: any i32& = wrapped.meta.items[0u].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_rebased_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected nested field-path rebased invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsExternNestedFieldRebasedSiblingScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Meta:
	items: view[Holder]
	total: i32

repr(c) struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Holder], start: usize, end: usize, total: i32, tag: i32) -> Wrapper

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	view: view[Holder] = items[0u:2u]
	wrapped: Wrapper = wrap_submeta(view, 1u, 2u, 9i32, 5i32)
	restore scratch from cp
	return wrapped.meta.total + wrapped.tag
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_rebased_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsExternBorrowsReturnFieldOnUnknownNestedReturnPath(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Meta:
	items: view[Holder]
	total: i32

repr(c) struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field(meta.missing, src)
extern wrap_meta(src: view[Holder]) -> Wrapper
`
	_, errs := parseAndAnalyze(t, "extern_function_nested_field_path_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return_field on extern function \"wrap_meta\" references unknown return field path \"meta.missing\" in Wrapper") {
		t.Fatalf("expected nested return-field-path diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingMoveAsPackedVariantBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: any i32&, count: i32)

def bad(owner: Arena) -> i32:
	region scratch
	mark scratch as cp
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Hold(value: new[scratch] 1i32, count: 7i32)
	move node in store as Expr.Hold(alias, count)
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_packed_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for packed move-as alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsMoveAsPackedVariantScalarAfterRestore(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: any i32&, count: i32)

def ok(owner: Arena) -> i32:
	region scratch
	mark scratch as cp
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Hold(value: new[scratch] 1i32, count: 7i32)
	move node in store as Expr.Hold(alias, count)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_packed_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingIndexedStructFieldAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	alias: any i32& = items[0u].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_indexed_struct_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for indexed struct alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsIndexedStructScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	count: i32 = items[0u].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_indexed_struct_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingSlicedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: view[Holder] = items[0u:2u]
	alias: any i32& = window[0u].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_sliced_view_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for sliced-view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsSlicedViewScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: view[Holder] = items[0u:2u]
	count: i32 = window[0u].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_sliced_view_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingNestedStructViewAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = Window(items[0u:2u])
	which: usize = 1u
	alias: any i32& = window.items[which].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_nested_struct_view_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for nested struct view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsNestedStructViewScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

repr(c) struct Window:
	items: view[Holder]

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)]
	window: Window = Window(items[0u:2u])
	which: usize = 1u
	count: i32 = window.items[which].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_nested_struct_view_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUsingMoveAsEnumBoundIndexedAliasInvalidatedByRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

enum Bucket:
	Keep(items: array[Holder, 2])

def bad() -> i32:
	region scratch
	mark scratch as cp
	value: Bucket = Bucket.Keep([Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)])
	move value as Bucket.Keep(items)
	which: usize = 1u
	alias: any i32& = items[which].value
	restore scratch from cp
	return alias[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_enum_indexed_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" is invalid after restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for move-as enum indexed alias, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsMoveAsEnumBoundIndexedScalarAfterRestore(t *testing.T) {
	src := `repr(c) struct Holder:
	value: any i32&
	count: i32

enum Bucket:
	Keep(items: array[Holder, 2])

def ok() -> i32:
	region scratch
	mark scratch as cp
	value: Bucket = Bucket.Keep([Holder(new[scratch] 1i32, 7i32), Holder(new[scratch] 2i32, 8i32)])
	move value as Bucket.Keep(items)
	which: usize = 1u
	count: i32 = items[which].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_enum_indexed_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsRestoringCheckpointFromWrongRegion(t *testing.T) {
	src := `def bad() -> void:
	region left
	region right
	mark left as cp
	restore right from cp
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_wrong_region.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "checkpoint \"cp\" belongs to region \"left\", not \"right\"") {
		t.Fatalf("expected wrong-region checkpoint diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsRestoringResetCheckpoint(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	mark scratch as cp
	reset scratch
	restore scratch from cp
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_reset_checkpoint.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "checkpoint \"cp\" is invalid after reset of region \"scratch\"") {
		t.Fatalf("expected invalid-checkpoint diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUsingReferenceInvalidatedByReset(t *testing.T) {
	src := `def bad() -> i32:
	region scratch
	value: any i32& = new[scratch] 1
	reset scratch
	return value[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_reset_invalid_ref.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"value\" is invalid after reset of region \"scratch\"") {
		t.Fatalf("expected reset invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsRestoringCheckpointInvalidatedByEarlierRestore(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	mark scratch as outer
	mark scratch as inner
	restore scratch from outer
	restore scratch from inner
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalidated_checkpoint.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "checkpoint \"inner\" is invalid after restore of region \"scratch\" from checkpoint \"outer\"") {
		t.Fatalf("expected nested-checkpoint invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsFrontendStressFixture(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "test_programs", "frontend_stress.llcontext"), map[string]bool{})
	_, errs := parseAndAnalyze(t, "frontend_stress.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsErrorDeclarationsAndTryRecovery(t *testing.T) {
	src := `error MemoryError:
	OutOfMemory

error IoError:
	NotFound

extern alloc(size: usize) -> heap void&?
extern read_file(path: any u8&) -> dstr[file_text] error[IoError.NotFound, ...]

def checked_alloc(size: usize) -> heap void& error[MemoryError.OutOfMemory, ...]:
	ptr: heap void& = alloc(size) else raise MemoryError.OutOfMemory
	return ptr

def load_text(path: any u8&) -> dstr[file_text] error[IoError.NotFound, ...]:
	text: dstr[file_text] = try read_file(path)
	return text

def load_with_fallback(path: any u8&) -> any u8&:
	text: any u8& = try read_file(path) else "".cast[any u8&]()
	return text
`
	_, errs := parseAndAnalyze(t, "error_handling_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsErrorSetWideningAndTagRaise(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error AppError:
	PermissionDenied
	Busy
	NotFound

error OneTagError:
	NotFound

extern read_value() -> int error[FileError.NotFound, ...]

def bubble() -> int error[AppError.NotFound, ...]:
	return try read_value()

def fail_now() -> int error[OneTagError.NotFound]:
	raise FileError.NotFound
`
	_, errs := parseAndAnalyze(t, "error_set_widening_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsWildcardErrorSetShorthand(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

extern read_value() -> int error[FileError]

def bubble() -> int error[FileError, ...]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_set_wildcard_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsMultiFamilyErrorComposition(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	NotFound

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError, NetworkError]:
	if use_disk:
		return try read_disk()
	return try read_network()
`
	_, errs := parseAndAnalyze(t, "error_multi_family_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsMixedRowStyleFamilyExpansion(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	if use_disk:
		return try read_disk()
	return try read_network()

def fail_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	raise FileError.PermissionDenied
`
	_, errs := parseAndAnalyze(t, "error_mixed_row_expansion_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeCanonicalizesEquivalentErrorSetSpellings(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

def by_full_subset() -> int error[FileError.NotFound, FileError.PermissionDenied]:
	return 1

def by_reverse_family_order() -> int error[NetworkError, FileError]:
	return 2
`
	result, errs := parseAndAnalyze(t, "error_canonicalization.llcontext", src)
	requireNoErrors(t, errs)

	fullSubset, ok := result.GlobalScope.Lookup("by_full_subset")
	if !ok {
		t.Fatal("expected by_full_subset symbol")
	}
	fullSubsetFn, ok := fullSubset.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fullSubset.Type)
	}
	if fullSubsetFn.Return.String() != "int | FileError" {
		t.Fatalf("expected canonical single-family return type, got %s", fullSubsetFn.Return.String())
	}

	reversed, ok := result.GlobalScope.Lookup("by_reverse_family_order")
	if !ok {
		t.Fatal("expected by_reverse_family_order symbol")
	}
	reversedFn, ok := reversed.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", reversed.Type)
	}
	if reversedFn.Return.String() != "int | error[FileError, NetworkError]" {
		t.Fatalf("expected canonical multi-family return type, got %s", reversedFn.Return.String())
	}
}

func TestAnalyzeRejectsAmbiguousCrossFamilyRaiseIntoMultiFamilySet(t *testing.T) {
	src := `error LegacyError:
	NotFound

error FileError:
	NotFound

error NetworkError:
	NotFound

def fail() -> int error[FileError, NetworkError]:
	raise LegacyError.NotFound
`
	_, errs := parseAndAnalyze(t, "error_multi_family_ambiguous_raise.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise cannot propagate tag \"LegacyError.NotFound\" into error[FileError, NetworkError]") {
		t.Fatalf("expected ambiguous multi-family raise diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeCanonicalizesRaiseDestinationInDiagnostics(t *testing.T) {
	src := `error LegacyError:
	Busy

error FileError:
	NotFound

error NetworkError:
	Disconnected

def fail() -> int error[NetworkError, FileError]:
	raise LegacyError.Busy
`
	_, errs := parseAndAnalyze(t, "error_canonical_raise_diag.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise cannot propagate tag \"LegacyError.Busy\" into error[FileError, NetworkError]") {
		t.Fatalf("expected canonical raise destination diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsLegacyWildcardErrorSetShorthand(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

extern read_value() -> int error[FileError.*]
`
	_, errs := parseAndAnalyze(t, "error_set_wildcard_mixed.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "error[Set.*] is no longer supported; use error[Set] instead") {
		t.Fatalf("expected wildcard migration diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsTryPropagationWhenDestinationMissesErrorTags(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error AppError:
	NotFound

extern read_value() -> int error[FileError.NotFound, ...]

def bubble() -> int error[AppError.NotFound, ...]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_set_widening_rejects_missing_tags.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot propagate FileError from a function returning AppError") {
		t.Fatalf("expected propagation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeCanonicalizesTryDestinationInDiagnostics(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout

error AppError:
	Busy

extern read_value() -> int error[AppError]

def bubble() -> int error[NetworkError, FileError]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_canonical_try_diag.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot propagate AppError from a function returning error[FileError, NetworkError]") {
		t.Fatalf("expected canonical try destination diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsRaiseOutsideErrorUnionFunction(t *testing.T) {
	src := `error IoError:
	NotFound

def bad() -> int:
	raise IoError.NotFound
	return 0
`
	_, errs := parseAndAnalyze(t, "raise_outside_error_union.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise requires the current function to return an error union") {
		t.Fatalf("expected raise diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsLegacyPipeErrorSyntax(t *testing.T) {
	src := `error IoError:
	NotFound

extern read_file(path: any u8&) -> int | IoError
`
	_, errs := parseAndAnalyze(t, "legacy_pipe_error_syntax.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parser error, got none")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "legacy fallible return syntax `T | ErrorSet` is no longer supported") {
		t.Fatalf("expected legacy syntax migration diagnostic, got:\n%s", joined)
	}
	if !strings.Contains(joined, "use `T error[SomeSet]` instead") {
		t.Fatalf("expected migration guidance, got:\n%s", joined)
	}
}

func TestAnalyzeRejectsTryOnNonFallibleExpression(t *testing.T) {
	src := `def bad() -> int:
	value: int = try 7
	return value
`
	_, errs := parseAndAnalyze(t, "try_on_non_fallible.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "try requires a fallible expression") {
		t.Fatalf("expected try diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsElseOnNonNullableReference(t *testing.T) {
	src := `def bad(value: any u8&) -> any u8&:
	return value else "".cast[any u8&]()
`
	_, errs := parseAndAnalyze(t, "else_on_nonnullable_ref.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "else recovery requires a nullable reference") {
		t.Fatalf("expected else recovery diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsRuntimeBackedArrayAndViewIndexing(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable any void&?
	count: mutable usize

def read_array(values: darray[i32, row]) -> i32:
	return values[0]

def read_view(view: dview[i32]) -> i32:
	return view[0]
`
	_, errs := parseAndAnalyze(t, "runtime_backed_array_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDStrIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(text: dstr[row]) -> char:
	return text[0]
`
	result, errs := parseAndAnalyze(t, "runtime_backed_dstr_index.llcontext", src)
	requireNoErrors(t, errs)
	fn, ok := result.GlobalScope.Lookup("read_codepoint")
	if !ok {
		t.Fatal("expected read_codepoint symbol")
	}
	ft, ok := fn.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fn.Type)
	}
	if ft.Return.String() != "char" {
		t.Fatalf("expected return type char, got %s", ft.Return.String())
	}
}

func TestAnalyzeRejectsAssigningToDStrIndex(t *testing.T) {
	src := `def bad(text: dstr[row]) -> void:
	text[0] <- 1
`
	_, errs := parseAndAnalyze(t, "dstr_index_assignment.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to string index") {
		t.Fatalf("expected string index assignment diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsStringViewIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(view: StringView) -> char:
	return view[0]
`
	result, errs := parseAndAnalyze(t, "ctx_string_view_index.llcontext", src)
	requireNoErrors(t, errs)
	fn, ok := result.GlobalScope.Lookup("read_codepoint")
	if !ok {
		t.Fatal("expected read_codepoint symbol")
	}
	ft, ok := fn.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fn.Type)
	}
	if ft.Return.String() != "char" {
		t.Fatalf("expected return type char, got %s", ft.Return.String())
	}
}

func TestAnalyzeAcceptsRuntimeStringEqualityOperators(t *testing.T) {
	src := `def same_text(left: dstr[row], right: dstr[col]) -> bool:
	return left == right

def same_view_text(view: StringView, text: dstr[row]) -> bool:
	return view == text

def same_text_view(text: dstr[row], view: StringView) -> bool:
	return text == view

def different_views(left: StringView, right: StringView) -> bool:
	return left != right

def same_literal(text: dstr[row]) -> bool:
	return text == "hello"
`
	_, errs := parseAndAnalyze(t, "runtime_string_equality.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDStrLenField(t *testing.T) {
	src := `def text_len(text: dstr[row]) -> i64:
	return text.len
`
	_, errs := parseAndAnalyze(t, "dstr_len_field.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsViewAliasForArraySlices(t *testing.T) {
	src := `def middle(values: i32[4]) -> view[i32]:
	part: view[i32] = values[1u:3u]
	return part
`
	_, errs := parseAndAnalyze(t, "view_alias_and_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsArrayAndArrayViewSliceSyntax(t *testing.T) {
	src := `def middle(values: darray[i32, row], view: dview[i32]) -> i32:
	part: dview[i32] = values[1u:3u]
	sub: dview[i32] = view[0u:1u]
	return part[0u] + sub[0u]
`
	_, errs := parseAndAnalyze(t, "array_and_array_view_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsFixedArraySliceSyntax(t *testing.T) {
	src := `def middle(values: i32[4], view: any i32[4]&) -> i32:
	part: view[i32] = values[1u:3u]
	sub: view[i32] = view[0u:2u]
	return part[0u] + sub[1u]
`
	_, errs := parseAndAnalyze(t, "fixed_array_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `extern make_array() -> darray[i32, row]
extern make_array_view() -> dview[i32]

def read_array_index() -> i32:
	return make_array()[1u]

def read_array_slice_index() -> i32:
	return make_array()[1u:3u][0u]

def read_array_view_index() -> i32:
	return make_array_view()[0u]
`
	_, errs := parseAndAnalyze(t, "nested_collection_access_returns.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsArrayLiteralWithInferredLocalAndViewSlice(t *testing.T) {
	src := `def middle() -> int:
	values = [1, 2, 3, 4]
	part: view[int] = values[1:3]
	return part[0]
`
	_, errs := parseAndAnalyze(t, "array_literal_inferred_local.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzePreservesStackStorageForAddressableLocalSubobjects(t *testing.T) {
	src := `repr(c) struct ScratchPair:
	left: mutable int
	right: mutable int


repr(c) struct ScratchHolder:
	pair: mutable ScratchPair


error ProbeError:
	Zero


def checked_pair(slot: stack ScratchPair&) -> int error[ProbeError]:
		if slot.left == 0:
			raise ProbeError.Zero
		return slot.left + slot.right


def from_local_field() -> int:
		holder: ScratchHolder = ScratchHolder(ScratchPair(1, 2))
		return try checked_pair(&holder.pair) else 0


def from_local_array_elem() -> int:
		values: ScratchPair[2] = [ScratchPair(1, 2), ScratchPair(5, 6)]
		return try checked_pair(&values[1u]) else 0
`
	_, errs := parseAndAnalyze(t, "stack_storage_local_subobjects.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsAllocatorOwnershipFixturePatterns(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "test_programs", "allocator_ownership.llcontext"), map[string]bool{})
	_, errs := parseAndAnalyze(t, "allocator_ownership.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzePinsArenaBuiltinPermissionContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "arena.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "arena.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "malloc", "Memory.Allocate")
	requireDeclaredFunctionPermissionRefs(t, result, "free", "Memory.Release")
	requireDeclaredFunctionPermissionRefs(t, result, "assert", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "sfree", "Memory.Release")
	requireDeclaredFunctionPermissionRefs(t, result, "new_region_with_owner", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "new_region", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "free_region", "Memory.Release", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "arena_alloc", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "arena_realloc", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "arena_free", "Memory.Release", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "arena_trim", "Memory.Release", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "arena_vsprintf", "Memory.Allocate", "Console.Format", "Abort.Panic")
}

func TestAnalyzePinsArenaHeapPointerContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "arena.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "arena.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "malloc", "heap void&?")
	requireFunctionReturnTypeString(t, result, "sfree", "heap T!")
	requireFunctionReturnTypeString(t, result, "new_region_with_owner", "heap Region&")
	requireFunctionReturnTypeString(t, result, "new_region", "heap Region&")
	requireFunctionReturnTypeString(t, result, "arena_alloc", "heap void&")
	requireFunctionReturnTypeString(t, result, "arena_realloc", "heap void&")
	requireFunctionReturnTypeString(t, result, "arena_strdup", "heap u8&")
	requireFunctionReturnTypeString(t, result, "arena_memdup", "heap void&")
	requireFunctionReturnTypeString(t, result, "ctx_packed_store_state_new", "heap void&")
	requireFunctionReturnTypeString(t, result, "arena_dict_copy_key", "heap u8&")
	requireFunctionReturnTypeString(t, result, "arena_vsprintf", "heap u8&")
}

func TestAnalyzePinsRuntimePreludeBuiltinExternPermissionContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "runtime_llcontext", "contextlang_runtime_prelude.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "contextlang_runtime_prelude.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "snprintf", "Console.Format")
	requireDeclaredFunctionPermissionRefs(t, result, "puts", "Console.Write")
	requireDeclaredFunctionPermissionRefs(t, result, "fprintf", "Console")
	requireDeclaredFunctionPermissionRefs(t, result, "exit", "Abort.Exit")
}

func TestAnalyzePinsRuntimePreludeHeapPointerContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "runtime_llcontext", "contextlang_runtime_prelude.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "contextlang_runtime_prelude.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "alloc_perm", "heap void&")
	requireFunctionReturnTypeString(t, result, "alloc_scratch", "heap void&")
	requireFunctionReturnTypeString(t, result, "intern_small_string", "heap u8&")
	requireFunctionReturnTypeString(t, result, "int_to_string_into", "heap u8&")
	requireFunctionReturnTypeString(t, result, "char_to_string_into", "heap u8&")
	requireFunctionReturnTypeString(t, result, "string_builder_new", "heap StringBuilder&")
	requireFunctionReturnTypeString(t, result, "string_builder_append", "heap StringBuilder&")
	requireFunctionReturnTypeString(t, result, "string_builder_finish", "heap u8&")
}

func TestAnalyzePinsRuntimeStage1BuiltinPermissionContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "contextlang_runtime.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "contextlang_runtime.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "int_to_string", "Memory.Allocate", "Console.Format", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "int_to_string_scratch", "Memory.Allocate", "Console.Format", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "char_to_string", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "char_to_string_scratch", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_concat2", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_string_builder_new", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_string_builder_append", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_string_builder_finish", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_int_to_string", "Memory.Allocate", "Console.Format", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_char_to_string", "Memory.Allocate", "Abort.Panic")
	requireDeclaredFunctionPermissionRefs(t, result, "rt_puts", "Console.Write")
	requireFunctionReturnTypeString(t, result, "int_to_string", "heap u8&")
	requireFunctionReturnTypeString(t, result, "int_to_string_scratch", "heap u8&")
	requireFunctionReturnTypeString(t, result, "char_to_string", "heap u8&")
	requireFunctionReturnTypeString(t, result, "char_to_string_scratch", "heap u8&")
	requireFunctionReturnTypeString(t, result, "rt_string_builder_new", "heap StringBuilder&")
	requireFunctionReturnTypeString(t, result, "rt_string_builder_append", "heap StringBuilder&")
	requireFunctionReturnTypeString(t, result, "string_view_copy", "heap u8&")
}

func TestAnalyzeAcceptsValueOptionalsAndTryElse(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def fallback_value(flag: bool) -> int:
	value: int? = maybe_value(flag)
	return try value else 11
`
	result, errs := parseAndAnalyze(t, "value_optionals_try_else.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "maybe_value", "int?")
	requireFunctionReturnTypeString(t, result, "fallback_value", "int")
}

func TestAnalyzeRejectsTryOptionalWithoutElse(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def bad(flag: bool) -> int:
	return try maybe_value(flag)
`
	_, errs := parseAndAnalyze(t, "value_optionals_try_without_else.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "try without else requires an error union") {
		t.Fatalf("expected optional try-without-else diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsOptionalNullChecksAndSmartCastUse(t *testing.T) {
	src := `repr(c) struct Box:
	value: int


def maybe_box(flag: bool) -> Box?:
	if flag:
		return Box(7)
	return null


def unwrap_or(flag: bool) -> int:
	value: Box? = maybe_box(flag)
	if value == null:
		return 11
	return value.value
`
	result, errs := parseAndAnalyze(t, "value_optionals_smart_cast.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "unwrap_or", "int")
}

func TestAnalyzeAcceptsTypedFixedArrayLiteralInitialization(t *testing.T) {
	src := `def first() -> i32:
	values: i32[3] = [1, 2, 3]
	return values[0]
`
	_, errs := parseAndAnalyze(t, "typed_array_literal_init.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsEmptyArrayLiteralWithoutContext(t *testing.T) {
	src := `def bad() -> void:
	values = []
`
	_, errs := parseAndAnalyze(t, "empty_array_literal_needs_context.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "empty array literal requires an expected array type") {
		t.Fatalf("expected empty-array context diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsMismatchedFixedArrayLiteralLength(t *testing.T) {
	src := `def bad() -> void:
	values: i32[2] = [1, 2, 3]
`
	_, errs := parseAndAnalyze(t, "fixed_array_literal_length_mismatch.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "array literal expects 2 elements, got 3") {
		t.Fatalf("expected array-length diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsStringSliceSyntax(t *testing.T) {
	src := `def middle(text: dstr[row]) -> StringView:
	return text[1:3]
`
	_, errs := parseAndAnalyze(t, "string_slice_syntax.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsAssigningToDStrLenField(t *testing.T) {
	src := `def bad(text: dstr[row]) -> void:
	text.len <- 1
`
	_, errs := parseAndAnalyze(t, "dstr_len_assign.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field \"len\" is immutable") {
		t.Fatalf("expected immutable len-field diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAssigningToStringViewIndex(t *testing.T) {
	src := `def bad(view: StringView) -> void:
	view[0] <- 1
`
	_, errs := parseAndAnalyze(t, "ctx_string_view_index_assignment.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to string view index") {
		t.Fatalf("expected string view index assignment diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsImplicitDArrayShapeParams(t *testing.T) {
	src := `def identity[T](array: darray[T, shape_in]) -> darray[T, shape_in]:
	return array

def keep(array: darray[i32, row]) -> darray[i32, row]:
	return identity(array)
`
	_, errs := parseAndAnalyze(t, "implicit_darray_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsShapeErasingDArrayShorthand(t *testing.T) {
	src := `def keep_surface(values: darray[i32]) -> darray[i32]:
	return values

def erase_explicit(values: darray[i32, row]) -> darray[i32]:
	return values
`
	_, errs := parseAndAnalyze(t, "darray_shorthand_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsRecoveringExplicitShapeFromShorthand(t *testing.T) {
	src := `def bad(values: darray[i32]) -> darray[i32, row]:
	return values
`
	_, errs := parseAndAnalyze(t, "darray_shorthand_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, row], got darray[i32]") {
		t.Fatalf("expected omitted-shape to explicit-shape rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeDArrayUsesDynArrayRuntimeFields(t *testing.T) {
	src := `def needs_grow[T](array: any darray[T, row]&) -> bool:
	return array.count >= array.capacity
`
	_, errs := parseAndAnalyze(t, "darray_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDynArrayRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `def take_raw[T](values: DynArray[T]) -> void:
	pass

def take_logical[T](values: darray[T, shape_in]) -> void:
	pass

def roundtrip(values: darray[i32, row], raw: DynArray[i32]) -> darray[i32, row]:
	take_raw(values)
	take_logical(raw)
	bridged: DynArray[i32] = values
	return bridged
`
	_, errs := parseAndAnalyze(t, "dynarray_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsMismatchedDArrayShapes(t *testing.T) {
	src := `def bad(array: darray[i32, row]) -> darray[i32, col]:
	return array
`
	_, errs := parseAndAnalyze(t, "mismatched_darray_shapes.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, col], got darray[i32, row]") {
		t.Fatalf("expected dynamic shape mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsImplicitDStrShapeParams(t *testing.T) {
	src := `def echo(text: dstr[shape_text]) -> dstr[shape_text]:
	return text

def keep(text: dstr[row]) -> dstr[row]:
	return echo(text)
`
	_, errs := parseAndAnalyze(t, "implicit_dstr_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsShapeErasingDStrShorthand(t *testing.T) {
	src := `def keep_surface(text: dstr) -> dstr:
	return text

def erase_explicit(text: dstr[row]) -> dstr:
	return text
`
	_, errs := parseAndAnalyze(t, "dstr_shorthand_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsRecoveringExplicitShapeFromDStrShorthand(t *testing.T) {
	src := `def bad(text: dstr) -> dstr[row]:
	return text
`
	_, errs := parseAndAnalyze(t, "dstr_shorthand_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects dstr[row], got dstr") {
		t.Fatalf("expected omitted-shape DStr to explicit-shape rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeDStrRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `def take_raw(text: any u8&) -> void:
	pass

def take_logical(text: dstr[shape_text]) -> void:
	pass

def roundtrip(text: dstr[row], raw: any u8&) -> dstr[row]:
	take_raw(text)
	take_logical(raw)
	bridged: dstr[row] = raw
	raw_value: any u8& = text
	return raw_value
`
	_, errs := parseAndAnalyze(t, "dstr_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsLegacyUppercaseBuiltinTypes(t *testing.T) {
	src := `def bad_array(values: DArray[i32]) -> void:
	pass

def bad_array_view(values: DArrayView[i32]) -> void:
	pass

def bad_str(text: DStr[row]) -> void:
	pass

def bad_dict(values: Dict[dstr, i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "dynamic_shape_arity.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"legacy built-in \"DArray\" has been replaced; use \"darray\" instead",
		"legacy built-in \"DArrayView\" has been replaced; use \"dview\" instead",
		"legacy built-in \"DStr\" has been replaced; use \"dstr\" instead",
		"legacy built-in \"Dict\" has been replaced; use \"dict\" instead",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected legacy-builtin diagnostic containing %q, got:\n%s", want, all)
		}
	}
}

func TestAnalyzeRejectsLegacyStringBuiltinAliases(t *testing.T) {
	src := `def bad_fixed(text: string[5]) -> void:
	pass

def bad_dynamic(text: dstring[row]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "legacy_string_aliases.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "legacy built-in \"string\" has been replaced; use \"str\" instead") || !strings.Contains(all, "legacy built-in \"dstring\" has been replaced; use \"dstr\" instead") {
		t.Fatalf("expected legacy string alias diagnostics, got:\n%s", all)
	}
}

func TestAnalyzeRejectsRemovedDListTypes(t *testing.T) {
	src := `def bad_list(values: DList[i32, row]) -> void:
	pass

def bad_list_view(view: DListView[i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "removed_dlist_surface.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "DList has been removed from the language") || !strings.Contains(all, "DListView has been removed from the language") {
		t.Fatalf("expected removed-DList diagnostics, got:\n%s", all)
	}
}

func TestAnalyzeDArrayViewUsesDynArrayViewRuntimeFields(t *testing.T) {
	src := `def non_empty[T](view: dview[T]) -> bool:
	return view.len > 0u and view.elem_size > 0u
`
	_, errs := parseAndAnalyze(t, "darray_view_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}
