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

def probe_first_scalar(owner: mutable Arena&) -> i64:
	in owner:
		values: darray[i64] = [11, 22]
		return values[0u]

@test
def keyword_compare_test() -> void:
	can Abort.Panic:
		assert_eq(probe_keyword_hit("program"), true)

@test
def scalar_array_index_test() -> void:
	can Abort.Panic:
		region scratch(4096)
		assert_eq(probe_first_scalar(scratch.ref[mutable Arena&]), 11)

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

def count_stmt_children(stmt: Flow.Stmt) -> i64:
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

def eval_lua(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Int:
		return node.value
	if node is Lua.Expr.Binary:
		return eval_lua(node.left) + eval_lua(node.right) + 1
	return 0

def rewrite_same(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default

@test
def mixed_tree_children_clone_rewrite_test() -> void:
	can Abort.Panic, Memory.Allocate:
		region scratch(12288)
		owner: mutable Arena& = scratch.ref[mutable Arena&]
		in owner:
			condition: Flow.Expr = Flow.Expr.Name(name_index: 7u32)
			stmts: darray[Flow.Stmt] = []
			body: Flow.Block = Flow.Block(stmts: stmts)
			stmt: Flow.Stmt = Flow.Stmt.IfStmt(condition: condition, body: body)
			assert_eq(count_stmt_children(stmt), 2)
			left: Lua.Expr = Lua.Expr.Int(value: 10)
			right: Lua.Expr = Lua.Expr.Int(value: 20)
			root: Lua.Expr = Lua.Expr.Binary(left: left, right: right)
			assert_eq(eval_lua(root), 31)
			copied: Lua.Expr = clone[Lua.Expr](root)
			assert_eq(eval_lua(copied), 31)
			rewritten: Lua.Expr = rewrite_same(copied)
			assert_eq(eval_lua(rewritten), 31)
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
