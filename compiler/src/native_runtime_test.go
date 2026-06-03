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
			out: i64 = values[0]
			destroy scratch
			return out

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

func TestRunCLIPostfixSviewCastEnablesStringContentEquality(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "sview_cast_string_equality_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A raw C-string (u8&?) gains content equality against string literals via the
	// postfix-shorthand cast to a borrowed view (__cast__(u8&?) -> sview), instead of
	// needing streq(). The == lowers to a length + byte content comparison.
	src := fmt.Sprintf(`# include %q

def sview_cast_matches_program(p: u8&?) -> bool:
    return p.sview() == "program"

@test
def sview_cast_string_equality_test() -> void:
    can Abort.Panic:
        assert_true(sview_cast_matches_program("program"))
        assert_false(sview_cast_matches_program("different"))
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write sview cast fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected sview cast test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	// Allow benign runtime-prelude lint warnings on stderr; fail only on errors.
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] sview_cast_string_equality_test",
		"[       OK ] sview_cast_string_equality_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected sview cast output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIPostfixShorthandCallsPascalCaseUFCSMethod(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_pascalcase_ufcs_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// `recv.Name()` reads as `Name(recv)`: when `Name` is a type it is a
	// constructor/cast (the postfix-shorthand cast, e.g. `x.u64()`), and otherwise
	// it is a function — including a UFCS ``. So a PascalCase method like
	// `Bump`/`Value`/`IsZero` is callable in method position the same as the
	// lowercase / multi-arg forms already are. `x.u64()` must keep casting.
	src := fmt.Sprintf(`# include %q

struct Counter:
    n: mutable i64

def Counter() -> Counter:
    return Counter{n: 0}


def Bump(self: mutable Counter&, by: i64) -> void:
    self.n <- self.n + by


def Value(self: Counter&) -> i64:
    return self.n


def IsZero(self: Counter&) -> bool:
    return self.n == 0

@test
def postfix_pascalcase_ufcs_test() -> void:
    can Abort.Panic:
        c: mutable Counter = Counter()
        assert_true(c.IsZero())
        c.Bump(40)
        c.Bump(2)
        assert_false(c.IsZero())
        assert_true(c.Value() == 42)
        # postfix cast-to-type shorthand still casts (not a method call)
        assert_true(c.Value().u64() == 42)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix pascalcase UFCS fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected postfix pascalcase UFCS test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] postfix_pascalcase_ufcs_test",
		"[       OK ] postfix_pascalcase_ufcs_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected postfix pascalcase UFCS output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIUniformCallSyntaxOverloadResolvesByArgumentType(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "uniform_call_overload_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A function overloaded by first-parameter type resolves to the right overload
	// in BOTH free-call and dot positions (uniform call syntax). `act(d)` selects
	// the Door& overload (not the first-declared cstr one); `act("x")` and
	// `"x".act()` select the cstr overload. Each overload has observable behavior.
	src := fmt.Sprintf(`# include %q

struct Door:
    state: mutable i64

def act(s: cstr) -> i64:
    _ = s
    return 7

def act(d: mutable Door&) -> i64:
    d.state <- d.state + 100
    return d.state

@test
def uniform_call_overload_test() -> void:
    can Abort.Panic:
        d: mutable Door = Door{state: 5}
        assert_true(act(d) == 105)          # free call, Door overload
        assert_true(d.act() == 205)         # dot call, Door overload
        assert_true(act("hello") == 7)      # free call, cstr overload
        assert_true("hello".act() == 7)     # dot call, cstr overload
        assert_true(d.state == 205)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write uniform call overload fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected uniform call overload test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] uniform_call_overload_test",
		"[       OK ] uniform_call_overload_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected uniform call overload output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIOverloadResolutionByArityAndMostConcrete(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "overload_arity_concrete_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// Overloads resolve by arity AND specificity ("most concrete wins"):
	//   - a concrete first-param (f64) outranks a generic wildcard (T) at the same
	//     arity, so Flush(1.5, 5) selects Flush(f64, i64);
	//   - a receiver whose first-param type only the generic matches (Widget) falls
	//     to the generic Flush[T];
	//   - arity disambiguates a 1-arg receiver call (g.Tag()) from the 2-arg generic
	//     Tag[T](a, b), which the wildcard T would otherwise also match.
	src := fmt.Sprintf(`# include %q

struct Widget:
    n: mutable i64

def Flush[T](a: T, b: i64) -> i64:
    _ = a
    return b

def Flush(a: f64, b: i64) -> i64:
    _ = a
    return b + 1000

def Tag(self: Widget&) -> i64:
    return self.n

def Tag[T](a: T, b: i64) -> i64:
    _ = a
    return b + 5

@test
def overload_arity_concrete_test() -> void:
    can Abort.Panic:
        w: Widget = Widget{n: 9}
        assert_true(Flush(1.5, 5) == 1005)    # concrete f64 beats generic T
        assert_true(Flush(w, 7) == 7)         # only generic T matches Widget
        assert_true(w.Tag() == 9)             # 1-arg method, not the 2-arg generic
        assert_true(Tag(w, 3) == 8)           # 2-arg generic
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write overload arity/concrete fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected overload arity/concrete test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] overload_arity_concrete_test",
		"[       OK ] overload_arity_concrete_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected overload arity/concrete output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIZeroParamFreeFunctionOverloadsWithReceiverMethod(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "zero_param_overload_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	// A zero-param free function `Flush()` and a same-named receiver method
	// `Flush(IOFile&)` form a legal overload set, disambiguated by arity: a 0-arg
	// call resolves to the free function (which owns the bare global name), while a
	// 1-arg call — as UFCS dot `f.Flush()` or free `Flush(f)` — resolves to the
	// receiver method. This is what dropping @method relies on (e.g. the emulator's
	// `Flush()` in logging coexisting with `IOFile.Flush()`).
	src := fmt.Sprintf(`# include %q

struct IOFile:
    fd: mutable i64

def Flush() -> i64:
    return 100

def Flush(self: IOFile&) -> i64:
    return self.fd

@test
def zero_param_overload_test() -> void:
    can Abort.Panic:
        f: IOFile = IOFile{fd: 7}
        assert_true(Flush() == 100)       # 0-arg free function
        assert_true(f.Flush() == 7)       # 1-arg method via UFCS dot
        assert_true(Flush(f) == 7)        # 1-arg method via free call
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write zero-param overload fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected zero-param overload test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] zero_param_overload_test",
		"[       OK ] zero_param_overload_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected zero-param overload output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIExplicitTypeArgsOverloadResolvesByReceiverType(t *testing.T) {
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
	// struct type, got func[T](...)"). This is the local-vs-global clash that blocks
	// dropping  (prelude's `string_builder_append(builder: StringBuilder&)`
	// vs the global `def builder[T](Arena&)`).
	src := fmt.Sprintf(`# include %q

struct Wid[T]:
    v: mutable T

def widget[T](owner: mutable Arena&) -> Wid[T]:
    _ = owner
    w: mutable Wid[T] = zeroed
    return w

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
        immutable_cell: AutoRefCell = AutoRefCell(value: 5)
        assert_eq(autoref_read(immutable_cell), 5)
        mutable_cell: mutable AutoRefCell = AutoRefCell(value: 1)
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
    immutable_cell: AutoRefCell = AutoRefCell(value: 5)
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
		destroy scratch
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
		destroy scratch
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
		destroy scratch
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
		for child in children(stmt.cast[Flow.Node]):
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
		destroy scratch
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
