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

func TestRunCLIExplicitTypeArgsOverloadResolvesByReceiverType(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "explicit_typeargs_overload_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A generic function overloaded by first-parameter type, called with EXPLICIT
	// type args (`wipe[K, V](b)`), must disambiguate by receiver type the same way
	// the bare `wipe(b)` form does. Regression: explicit type args parse as a
	// SpecializeExpr wrapping the ident, which previously skipped the free-call
	// receiver-overload rewrite and latched onto the first-declared overload
	// (mismatched type-arity error). This is the bug that blocks dropping .
	src := fmt.Sprintf(`# include %q

struct Aaa[T]:
    v: mutable T

struct Bbb[K, V]:
    k: mutable K
    w: mutable V

def wipe[T](self: Aaa[T]&) -> i64:
    _ = self
    return 1

def wipe[K, V](self: Bbb[K, V]&) -> i64:
    _ = self
    return 2

@test
def explicit_typeargs_overload_test() -> void:
    can Abort.Panic:
        a: Aaa[i64] = Aaa[i64]{v: 0}
        b: Bbb[i64, i64] = Bbb[i64, i64]{k: 0, w: 0}
        assert_true(wipe[i64](a) == 1)          # explicit 1-arg form, Aaa overload
        assert_true(wipe[i64, i64](b) == 2)     # explicit 2-arg form, Bbb overload
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write explicit type-args overload fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected explicit type-args overload test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] explicit_typeargs_overload_test",
		"[       OK ] explicit_typeargs_overload_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected explicit type-args overload output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLILocalParamShadowsSameNamedGlobalFunction(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "param_shadows_global_fn_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A parameter `widget` whose type is NOT a function must shadow a same-named
	// global generic function `widget[T]`. Regression: binding the param into a
	// typed local (`g: Gadget& = widget`) recorded the init expr, and reading the
	// local re-resolved `widget` through functionValueTypeForExpr, which — after
	// finding the (non-function) local symbol — fell through to the global lookup
	// and mis-bound the local to the global function's type ("field access requires
	// struct type, got func[T](...)"). This is the local-vs-global clash that used
	// to block removing receiver-only method annotations.
	src := fmt.Sprintf(`# include %q

struct Wid[T]:
    v: mutable T&?

def widget[T](owner: mutable Arena&) -> Wid[T]:
    _ = owner
    return Wid[T]{v: null}

struct Gadget:
    n: mutable i64

def use_it(widget: Gadget&) -> i64:
    g: Gadget& = widget
    return g.n

@test
def param_shadows_global_fn_test() -> void:
    can Abort.Panic:
        gg: Gadget = Gadget{n: 42}
        assert_true(use_it(gg) == 42)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write param-shadow fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected param-shadow test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] param_shadows_global_fn_test",
		"[       OK ] param_shadows_global_fn_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected param-shadow output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLISelectiveImportBringsNamedModuleMemberIntoScope(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "selective_import_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// `from Foo import bar` brings only `bar` into scope unqualified; the importer's
	// own `baz` does not clash with the unimported `Foo.baz`. `bar()` resolves to
	// `Foo.bar` end-to-end (analysis + codegen).
	src := fmt.Sprintf(`# include %q

module Foo:
    def bar() -> i64:
        return 10
    def baz() -> i64:
        return 999

from Foo import bar

def baz() -> i64:
    return bar() + 5

@test
def selective_import_test() -> void:
    can Abort.Panic:
        assert_true(bar() == 10)
        assert_true(baz() == 15)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write selective import fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected selective import test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] selective_import_test",
		"[       OK ] selective_import_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected selective import output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLINamespacedTypeResolvesAtCodegen(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "namespaced_type_codegen_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A namespaced/imported type used unqualified (local annotation + struct literal)
	// must lower at codegen. The backend consumes the analyzer's recorded resolution
	// (Geo.Point) instead of re-resolving the bare name "Point" without namespace
	// context — previously this failed with "unknown type".
	src := fmt.Sprintf(`# include %q

module Geo:
    struct Point:
        x: mutable i64
        y: mutable i64
    def make(x: i64, y: i64) -> Point:
        return Point{x: x, y: y}

from Geo import Point, make

@test
def namespaced_type_codegen_test() -> void:
    can Abort.Panic:
        a: Point = make(3, 4)
        b: Point = Point{x: 1, y: 2}
        assert_true(a.x + a.y + b.x + b.y == 10)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write namespaced type fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected namespaced type test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] namespaced_type_codegen_test",
		"[       OK ] namespaced_type_codegen_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected namespaced type output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLINamespacedGenericMonomorphizes(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "namespaced_generic_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// An imported generic type AND generic function must monomorphize at codegen.
	// The generic instance is named by its canonical (qualified) name, so a bare
	// reference inside the module and an imported reference unify; the backend
	// resolves the call target through the analyzer's recorded canonical name so
	// the namespaced generic function is specialized (previously emitted as an
	// unsubstituted %Holder__T -> LLVM verifier error).
	src := fmt.Sprintf(`# include %q

module Box:
    struct Holder[T]:
        value: mutable T
    def make[T](v: T) -> Holder[T]:
        return Holder[T]{value: v}

from Box import Holder, make

@test
def namespaced_generic_test() -> void:
    can Abort.Panic:
        h: Holder[i64] = make(7)
        assert_true(h.value == 7)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write namespaced generic fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected namespaced generic test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] namespaced_generic_test",
		"[       OK ] namespaced_generic_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected namespaced generic output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIDotNotationAcceptsExplicitTypeArgs(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "dot_type_args_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A UFCS method call may carry explicit type arguments: `recv.method[T](args)`
	// resolves to `method[T](recv, args)`. Here T is supplied ONLY by the type
	// argument (not inferable from a value arg), so it must thread through.
	src := fmt.Sprintf(`# include %q

struct Box:
    n: mutable i64


def type_size[T](self: Box&) -> usize:
    _ = self
    return size_of(T)

@test
def dot_type_args_test() -> void:
    can Abort.Panic:
        b: Box = Box{n: 0}
        assert_true(b.type_size[i64]() == 8)
        assert_true(b.type_size[u8]() == 1)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write dot type args fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected dot type args test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] dot_type_args_test",
		"[       OK ] dot_type_args_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected dot type args output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIAutoRefCoercesValuesToReferenceParams(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "autoref_call_site_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A plain value passed where a reference parameter is expected is implicitly
	// address-of-coerced (auto-ref) at the call site, removing the need for the
	// `value.ref[T&]` ceremony. An immutable place coerces to an immutable `T&`;
	// a mutable place coerces to a `mutable T&` and writes through propagate.
	src := fmt.Sprintf(`# include %q

struct AutoRefCell:
    value: mutable i64

def autoref_read(cell: AutoRefCell&) -> i64:
    return cell.value

def autoref_write(cell: mutable AutoRefCell&) -> void:
    cell.value <- 99

@test
def autoref_value_to_reference_param_test() -> void:
    can Abort.Panic:
        immutable_cell: AutoRefCell = AutoRefCell{value: 5}
        assert_eq(autoref_read(immutable_cell), 5)
        mutable_cell: mutable AutoRefCell = AutoRefCell{value: 1}
        autoref_write(mutable_cell)
        assert_eq(autoref_read(mutable_cell), 99)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write autoref fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected autoref test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	// Allow benign runtime-prelude lint warnings on stderr; fail only on errors.
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] autoref_value_to_reference_param_test",
		"[       OK ] autoref_value_to_reference_param_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected autoref output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIAutoRefRejectsImmutableValueForMutableReferenceParam(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "autoref_immutable_reject_fixture.elisa")
	// Passing an immutable place where a `mutable T&` parameter is expected must
	// be rejected: auto-ref produces an immutable reference, which is not
	// assignable to a mutable reference. Forcing it would require an explicit
	// unsafe cast.
	src := `struct AutoRefCell:
    value: mutable i64

def autoref_write(cell: mutable AutoRefCell&) -> void:
    cell.value <- 99

@test
def autoref_immutable_reject_test() -> void:
    immutable_cell: AutoRefCell = AutoRefCell{value: 5}
    autoref_write(immutable_cell)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write autoref reject fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected immutable-to-mutable auto-ref to be rejected, but compilation succeeded; stdout:\n%s", stdout.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "expects mutable AutoRefCell&") {
		t.Fatalf("expected a mutable-reference mismatch diagnostic, got:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
