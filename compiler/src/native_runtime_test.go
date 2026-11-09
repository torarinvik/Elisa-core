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
	t.Parallel()
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
	t.Parallel()
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
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test helper include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q
# include %q

@test
def debug_referee_records_bad_callable() -> void:
    can Abort.Panic, Debug.Referee, Global.Read, Global.Write:
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
    can Abort.Panic, Debug.Referee, Global.Read, Global.Write:
        debug_referee_reset()
        assert_true(debug_referee_check_pointer("null-sentinel", 0.uintptr()))
        assert_true(debug_referee_check_callable("normal", 0x100000.uintptr()))
        assert_false(debug_referee_is_noncanonical_x86_64(0.uintptr()))
        assert_false(debug_referee_is_noncanonical_x86_64(0x100000.uintptr()))
        if size_of(uintptr) >= 8:
            assert_true(debug_referee_is_noncanonical_x86_64(0x0000800000000000.uintptr()))
            assert_true(debug_referee_is_noncanonical_x86_64(0xffff000000000000.uintptr()))
            assert_false(debug_referee_is_noncanonical_x86_64(0xffff800000000000.uintptr()))
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_OK)

@test
def debug_referee_records_poison_and_noncanonical() -> void:
    can Abort.Panic, Debug.Referee, Global.Read, Global.Write:
        debug_referee_reset()
        assert_false(debug_referee_check_pointer("got", DEBUG_REFEREE_POISON_GOT))
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_POISON)
        assert_false(debug_referee_check_callable("wild", DEBUG_REFEREE_POISON_GENERIC))
        assert_eq(debug_referee_last_reason(), DEBUG_REFEREE_REASON_POISON)
`, runtimeInclude, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write debug referee runtime fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected debug referee runtime test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), fixturePath+":") {
		t.Fatalf("expected no fixture diagnostics, got:\n%s", stderr.String())
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
	t.Parallel()
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
	// A valid C string gains content equality against string literals via the
	// postfix-shorthand cast to a borrowed view (__cast__(cstr) -> sview), instead
	// of needing streq(). Arbitrary u8 references are not C strings.
	src := fmt.Sprintf(`# include %q

def sview_cast_matches_program(p: cstr?) -> bool:
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

struct Box[A, B]:
    a: mutable A
    b: mutable B

def graded[A, B](x: Box[A, B]&) -> i64:
    _ = x
    return 1

def graded[B](x: Box[i64, B]&) -> i64:
    _ = x
    return 2

def graded(x: Box[i64, i64]&) -> i64:
    _ = x
    return 3

@test
def overload_arity_concrete_test() -> void:
    can Abort.Panic:
        w: Widget = Widget{n: 9}
        assert_true(Flush(1.5, 5) == 1005)    # concrete f64 beats generic T
        assert_true(Flush(w, 7) == 7)         # only generic T matches Widget
        assert_true(w.Tag() == 9)             # 1-arg method, not the 2-arg generic
        assert_true(Tag(w, 3) == 8)           # 2-arg generic
        b_ii: Box[i64, i64] = Box[i64, i64]{a: 0, b: 0}
        b_if: Box[i64, f64] = Box[i64, f64]{a: 0, b: 0.0}
        b_ff: Box[f64, f64] = Box[f64, f64]{a: 0.0, b: 0.0}
        assert_true(b_ii.graded() == 3)       # fully bound Box[i64,i64] most specific
        assert_true(b_if.graded() == 2)       # partially bound Box[i64,B]
        assert_true(b_ff.graded() == 1)       # fully generic Box[A,B]
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
	t.Parallel()
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
	// receiver method. This is what the removed @method path relied on (e.g. the emulator's
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
