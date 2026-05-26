package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProvidesDefaultNativeRuntimeHelpersForSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "native_runtime_helpers_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

struct ProbeToken:
	kind: i64

extern ctx_string_index(value: u8&, index: i64) -> i64
extern ctx_string_slice(value: u8&, start: i64, end: i64) -> u8&

def probe_keyword_hit(text: cstr) -> bool:
	return text == "program"

def probe_first_scalar() -> i64:
	can Memory.Allocate:
		region scratch(4096)
		in scratch:
			values: darray[i64] = [11, 22]
			return values[0]

@test
def keyword_compare_test() -> void:
	can Abort.Panic:
		assert_eq(probe_keyword_hit("program"), true)

@test
def scalar_array_index_test() -> void:
	can Abort.Panic, Memory.Allocate:
		assert_eq(probe_first_scalar(), 11)

@test
def string_view_empty_slice_test() -> void:
	can Abort.Panic:
		assert_eq(ctx_string_index("program", 99), 0)
		assert_eq(ctx_string_slice("program", 99, 123), "")
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write native runtime helpers fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected native runtime helper test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] keyword_compare_test",
		"[       OK ] keyword_compare_test",
		"[ RUN      ] scalar_array_index_test",
		"[       OK ] scalar_array_index_test",
		"[ RUN      ] string_view_empty_slice_test",
		"[       OK ] string_view_empty_slice_test",
		"[ SUMMARY  ] 3 test(s) selected; passed=3 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected native runtime helper output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIDebugRefereeRuntimeChecksAndTrace(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "debug_referee_runtime_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

@test
def debug_referee_records_bad_callable() -> void:
    can Abort.Panic, Debug.Referee:
        debug_referee_reset()
        debug_referee_set_trace_enabled(true)
        debug_referee_checkpoint("before-call")
        ok: bool = debug_referee_check_callable("bad-call", 0x1f4.uintptr())
        assert_false(ok)
        assert_eq(debug_referee_last_kind(), DEBUG_REFEREE_KIND_CHECK_CALLABLE)
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_NEAR_NULL)
        assert_eq(debug_referee_last_value(), 0x1f4.uintptr())
        assert_eq(debug_referee_trace_count(), 2.usize())
        first_event: DebugRefereeEvent = debug_referee_trace_event_at(0.usize())
        assert_eq(first_event.kind, DEBUG_REFEREE_KIND_CHECKPOINT)

@test
def debug_referee_accepts_zero_and_canonical_values() -> void:
    can Abort.Panic, Debug.Referee:
        debug_referee_reset()
        assert_true(debug_referee_check_pointer("null-sentinel", 0.uintptr()))
        assert_true(debug_referee_check_callable("normal", 0x100000.uintptr()))
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_OK)

@test
def debug_referee_records_poison_and_noncanonical() -> void:
    can Abort.Panic, Debug.Referee:
        debug_referee_reset()
        assert_false(debug_referee_check_pointer("got", DEBUG_REFEREE_POISON_GOT))
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_POISON)
        assert_false(debug_referee_check_callable("wild", DEBUG_REFEREE_POISON_GENERIC))
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_POISON)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write debug referee runtime fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected debug referee runtime test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] debug_referee_records_bad_callable",
		"[       OK ] debug_referee_records_bad_callable",
		"[ RUN      ] debug_referee_accepts_zero_and_canonical_values",
		"[       OK ] debug_referee_accepts_zero_and_canonical_values",
		"[ RUN      ] debug_referee_records_poison_and_noncanonical",
		"[       OK ] debug_referee_records_poison_and_noncanonical",
		"[ SUMMARY  ] 3 test(s) selected; passed=3 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected debug referee runtime output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIDebugTraceTapeFingerprint(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "debug_trace_tape_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

def record_trace_variant(value: uintptr) -> u64:
    can Debug.Trace:
        debug_trace_reset()
        debug_trace_set_enabled(true)
        debug_trace_checkpoint("entry")
        debug_trace_value("rdi", value)
        debug_trace_boundary("hle", value, 7.uintptr(), 9.uintptr())
        return debug_trace_fingerprint()

@test
def debug_trace_identical_tapes_have_identical_fingerprints() -> void:
    can Abort.Panic, Debug.Trace:
        first: u64 = record_trace_variant(0x100000.uintptr())
        second: u64 = record_trace_variant(0x100000.uintptr())
        assert_eq(first, second)
        assert_eq(debug_trace_count(), 3.usize())
        assert_eq(debug_trace_event_kind_at(0.usize()), DEBUG_TRACE_KIND_CHECKPOINT)
        assert_eq(debug_trace_event_kind_at(2.usize()), DEBUG_TRACE_KIND_BOUNDARY)
        assert_eq(debug_trace_event_a_at(2.usize()), 0x100000.uintptr())

@test
def debug_trace_value_divergence_changes_fingerprint() -> void:
    can Abort.Panic, Debug.Trace:
        first: u64 = record_trace_variant(0x100000.uintptr())
        second: u64 = record_trace_variant(0x100008.uintptr())
        assert_ne(first, second)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write debug trace tape fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected debug trace tape test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] debug_trace_identical_tapes_have_identical_fingerprints",
		"[       OK ] debug_trace_identical_tapes_have_identical_fingerprints",
		"[ RUN      ] debug_trace_value_divergence_changes_fingerprint",
		"[       OK ] debug_trace_value_divergence_changes_fingerprint",
		"[ SUMMARY  ] 2 test(s) selected; passed=2 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected debug trace tape output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLINativeRuntimeStringBuilderShortStringRegression(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "native_runtime_string_builder_short_string_regression.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

def make_queue_label(vqid: int) -> cstr:
	can Memory.Allocate, Abort.Panic, Console.Format:
		queue: cstr = "GFX" if vqid > 254 else "ASC"
		builder: mutable heap StringBuilder& = rt_string_builder_new(queue)
		builder <- rt_string_builder_append(builder, "[")
		builder <- rt_string_builder_append(builder, rt_int_to_string((vqid %% 260).i64()))
		builder <- rt_string_builder_append(builder, "]")
		return rt_string_builder_finish(builder)

def make_queue_label_with_suffix(vqid: int, suffix: cstr) -> cstr:
	can Memory.Allocate, Abort.Panic, Console.Format:
		builder: mutable heap StringBuilder& = rt_string_builder_new(make_queue_label(vqid))
		builder <- rt_string_builder_append(builder, suffix)
		return rt_string_builder_finish(builder)

@test
def short_string_builder_finish_regression() -> void:
	can Abort.Panic, Memory.Allocate, Console.Format:
		for i in 0..<20000:
			label: cstr = make_queue_label((i %% 260).int())
			assert ctx_strlen(label) >= 6
			assert ctx_strlen(label) <= 8

@test
def short_string_builder_mixed_inputs_regression() -> void:
	can Abort.Panic, Memory.Allocate, Console.Format:
		for i in 0..<24000:
			slot: int = (i %% 5).int()
			vqid: mutable int = 0
			expected: mutable cstr = ""
			if slot == 0:
				vqid <- 0
				expected <- "ASC[0]"
			if slot == 1:
				vqid <- 9
				expected <- "ASC[9]"
			if slot == 2:
				vqid <- 99
				expected <- "ASC[99]"
			if slot == 3:
				vqid <- 100
				expected <- "ASC[100]"
			if slot == 4:
				vqid <- 259
				expected <- "GFX[259]"

			plain: cstr = make_queue_label_with_suffix(vqid, "")
			assert plain == expected
			assert ctx_strlen(plain) >= 6
			assert ctx_strlen(plain) <= 8

@test
def short_string_builder_cross_path_stress_regression() -> void:
	can Abort.Panic, Memory.Allocate, Console.Format:
		total: mutable i64 = 0
		for i in 0..<16000:
			base: cstr = make_queue_label((i %% 260).int())
			builder: mutable heap StringBuilder& = rt_string_builder_new(base)
			builder <- rt_string_builder_append(builder, ":")
			builder <- rt_string_builder_append(builder, rt_int_to_string((ctx_strlen(base) + (i %% 11).i64()).i64()))
			label: cstr = rt_string_builder_finish(builder)
			assert ctx_strlen(label) >= 8
			assert ctx_strlen(label) <= 12
			total += ctx_strlen(label)
		assert total > 0
	`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write string builder regression fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected string builder regression test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] short_string_builder_finish_regression",
		"[       OK ] short_string_builder_finish_regression",
		"[ RUN      ] short_string_builder_mixed_inputs_regression",
		"[       OK ] short_string_builder_mixed_inputs_regression",
		"[ RUN      ] short_string_builder_cross_path_stress_regression",
		"[       OK ] short_string_builder_cross_path_stress_regression",
		"[ SUMMARY  ] 3 test(s) selected; passed=3 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected string builder regression output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesCategoryUnionTreeNativeSmoke(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "category_union_tree_native_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def eval(store: Lua.Store[Local], node: Lua.Expr) -> i64:
	in store:
		if node is Lua.Expr.Int:
			return node.value + node.span
		if node is Lua.Expr.Binary:
			return eval(store, node.left) + eval(store, node.right) + node.span
		return 0

def flip(store: Lua.Store[Local], node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in store:
		return node{left = right, right = left}

@test
def category_union_tree_roundtrip_test() -> void:
	can Abort.Panic, Memory.Allocate:
		region scratch(8192)
		store = Lua.Store(scratch)
		in store:
			left: Lua.Expr = Lua.Expr.Int(span: 1, value: 10)
			right: Lua.Expr = Lua.Expr.Int(span: 2, value: 20)
			root: Lua.Expr = Lua.Expr.Binary(span: 3, left: left, right: right)
			assert_eq(eval(store, root), 36)
			if root is Lua.Expr.Binary:
				flipped: Lua.Expr = flip(store, root, left, right)
				assert_eq(eval(store, flipped), 36)
				copied: Lua.Expr = clone[Lua.Expr](flipped)
				assert_eq(eval(store, copied), 36)
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write category_union native fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected category_union native tree test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] category_union_tree_roundtrip_test",
		"[       OK ] category_union_tree_roundtrip_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union native test output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesPerVariantTreeFoldRewriteNativeSmoke(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "per_variant_tree_fold_rewrite_native_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def score(node: Lua.Expr) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Int(expr, children) when expr.value > 0:
			expr.value + expr.span + children.len.i64()
		Lua.Expr.Int(expr, children):
			expr.span
		Lua.Expr.Binary(expr, left, right):
			left + right + expr.span

def rewrite_spans(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr default:
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span + 10, left, right}

@test
def per_variant_tree_fold_rewrite_test() -> void:
	can Abort.Panic, Memory.Allocate:
		region scratch(8192)
		owner: mutable Arena& = scratch.ref[mutable Arena&]
		in owner:
			left: Lua.Expr = Lua.Expr.Int(span: 1, value: 10)
			right: Lua.Expr = Lua.Expr.Int(span: 2, value: -5)
			root: Lua.Expr = Lua.Expr.Binary(span: 3, left: left, right: right)
			assert_eq(score(root), 16)
			rewritten: Lua.Expr = rewrite_spans(root)
			assert_eq(score(rewritten), 26)
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write per_variant native tree fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected per_variant native tree test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] per_variant_tree_fold_rewrite_test",
		"[       OK ] per_variant_tree_fold_rewrite_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected per_variant native tree test output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesTreeAttributeNativeSmoke(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "tree_attribute_native_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

@layout(per_variant_rows)
tree Sparse:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

attribute Sparse.Expr.checksum -> i64:
	Sparse.Expr.Int(expr):
		return expr.value
	Sparse.Expr.Binary(expr, left, right):
		return left.checksum + right.checksum + 1

tree Dense:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

attribute Dense.Expr.checksum -> i64:
	Dense.Expr.Int(expr):
		return expr.value
	Dense.Expr.Binary(expr, left, right):
		return left.checksum + right.checksum + 2

@test
def tree_attribute_native_test() -> void:
	can Abort.Panic, Memory.Allocate:
		region scratch(8192)
		owner: mutable Arena& = scratch.ref[mutable Arena&]
		in owner:
			sleft: Sparse.Expr = Sparse.Expr.Int(value: 10)
			sright: Sparse.Expr = Sparse.Expr.Int(value: 20)
			sroot: Sparse.Expr = Sparse.Expr.Binary(left: sleft, right: sright)
			assert_eq(sroot.checksum, 31)
		store = Dense.Store(scratch)
		in store:
			dleft: Dense.Expr = Dense.Expr.Int(value: 3)
			dright: Dense.Expr = Dense.Expr.Int(value: 4)
			droot: Dense.Expr = Dense.Expr.Binary(left: dleft, right: dright)
			assert_eq(droot.checksum, 9)
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write tree attribute native fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected tree attribute native test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] tree_attribute_native_test",
		"[       OK ] tree_attribute_native_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree attribute native test output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExecutesMixedTreeChildrenCloneRewriteNativeSmoke(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "mixed_tree_children_clone_rewrite_native_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

tree Flow:
	@role(stmt)
	node Stmt:
		IfStmt(condition: Flow.Expr, body: Flow.Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Flow.Stmt]

def count_stmt_children(store: Flow.Store[Local], stmt: Flow.Stmt) -> i64:
	in store:
		total: mutable i64 = 0
		for child in children(stmt as Flow.Node):
			_ = child.kind
			total <- total + 1
		return total

tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def eval_lua(store: Lua.Store[Local], node: Lua.Expr) -> i64:
	in store:
		if node is Lua.Expr.Int:
			return node.value
		if node is Lua.Expr.Binary:
			return eval_lua(store, node.left) + eval_lua(store, node.right) + 1
		return 0

def rewrite_same(store: Lua.Store[Local], node: Lua.Expr) -> Lua.Expr:
	in store:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default

@test
def mixed_tree_children_clone_rewrite_test() -> void:
	can Abort.Panic, Memory.Allocate:
		region scratch(12288)
		flow_store = Flow.Store(scratch)
		in flow_store:
			condition: Flow.Expr = Flow.Expr.Name(name_index: 7.u32())
			stmts: darray[Flow.Stmt] = []
			body: Flow.Block = Flow.Block(stmts: stmts)
			stmt: Flow.Stmt = Flow.Stmt.IfStmt(condition: condition, body: body)
			assert_eq(count_stmt_children(flow_store, stmt), 2)
		lua_store = Lua.Store(scratch)
		in lua_store:
			left: Lua.Expr = Lua.Expr.Int(value: 10)
			right: Lua.Expr = Lua.Expr.Int(value: 20)
			root: Lua.Expr = Lua.Expr.Binary(left: left, right: right)
			assert_eq(eval_lua(lua_store, root), 31)
			copied: Lua.Expr = clone[Lua.Expr](root)
			assert_eq(eval_lua(lua_store, copied), 31)
			rewritten: Lua.Expr = rewrite_same(lua_store, copied)
			assert_eq(eval_lua(lua_store, rewritten), 31)
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write mixed tree native fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected mixed tree native test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] mixed_tree_children_clone_rewrite_test",
		"[       OK ] mixed_tree_children_clone_rewrite_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected mixed tree native test output to contain %q, got:\n%s", check, output)
		}
	}
}
