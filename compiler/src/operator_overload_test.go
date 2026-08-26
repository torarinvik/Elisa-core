//go:build cgo

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/semantic"
)

// Operator overloading (Stage A: `+` -> `__add__`, value types first). `a + b` on a type that impls a
// protocol declaring `__add__` desugars to the static-impl method call `T.__add__(a, b)` (recorded as
// BinaryExpr.LoweredCall), so the backend emits the call and the effect/region collectors thread the
// callee's obligations to the `+` site — while a type WITHOUT `__add__` keeps the numeric-operands error.

func analyzeOverloadSource(t *testing.T, src string) *semantic.Result {
	t.Helper()
	return semantic.Analyze(parseFStringSource(t, src)) // reuses the parse helper from fstring_test.go
}

func TestOperatorOverloadTypesAndRegression(t *testing.T) {
	ok := analyzeOverloadSource(t, `
struct Vec3:
    x: i64
    y: i64
    z: i64

protocol Add:
    def __add__(self: Self, other: Self) -> Self

impl Add for Vec3:
    def __add__(self: Vec3, other: Vec3) -> Vec3:
        return Vec3{x: self.x + other.x, y: self.y + other.y, z: self.z + other.z}

def use(a: Vec3, b: Vec3) -> Vec3:
    return a + b + a
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("Vec3 with an __add__ impl must accept `a + b` (and chaining), got: %v", errs)
	}

	// A struct WITHOUT __add__ must still be rejected — no accidental blanket overloading.
	bad := analyzeOverloadSource(t, `
struct P:
    x: i64

def use(a: P, b: P) -> i64:
    c: P = a + b
    return c.x
`)
	if !strings.Contains(strings.Join(bad.Errors(), "\n"), "operator requires numeric operands") {
		t.Fatalf("a struct without __add__ must keep the numeric-operands error, got: %v", bad.Errors())
	}
}

// Unary `-` overloading: `-x` on a type impl-ing a protocol declaring `__neg__` desugars to
// `T.__neg__(x)` (recorded as UnaryExpr.LoweredCall), while a type without `__neg__` keeps the
// numeric-operand error — no accidental blanket unary overloading.
func TestUnaryNegOverloadTypesAndRegression(t *testing.T) {
	ok := analyzeOverloadSource(t, `
struct V:
    x: i64

protocol Neg:
    def __neg__(self: Self) -> Self

impl Neg for V:
    def __neg__(self: V) -> V:
        return V{x: 0 - self.x}

def flip(a: V) -> V:
    return -a

def gen[T: Neg](a: T) -> T:
    return -a
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("V with a __neg__ impl must accept `-a` (concrete and generic), got: %v", errs)
	}

	bad := analyzeOverloadSource(t, `
struct P:
    x: i64

def use(a: P) -> i64:
    b: P = -a
    return b.x
`)
	if !strings.Contains(strings.Join(bad.Errors(), "\n"), "unary operator requires numeric operand") {
		t.Fatalf("a struct without __neg__ must keep the numeric-operand error, got: %v", bad.Errors())
	}
}

// End-to-end: compile and RUN `-x` on a value type, both directly and through a `[T: Neg]` bound.
func TestRunCLIUnaryNegNative(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "neg.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("rel runtime path: %v", err)
	}
	src := "# include \"" + filepath.ToSlash(runtimeInclude) + "\"\n" + `
struct V:
    x: i64

impl Neg for V:
    def __neg__(self: V) -> V:
        return V{x: 0 - self.x}

def negate[T: Neg](a: T) -> T:
    return -a

def main() -> i64:
    a: V = V{x: 7}
    r: mutable i64 = 0
    if (-a).x == 0 - 7:
        r <- r + 1
    b: V = negate(a)
    if b.x == 0 - 7:
        r <- r + 2
    return r
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	objPath := filepath.Join(fixtureDir, "neg.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objPath, fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("compile failed (exit %d):\n%s", code, stderr.String())
	}
	exePath := filepath.Join(fixtureDir, "neg")
	if out, err := exec.Command("clang", objPath, "-o", exePath).CombinedOutput(); err != nil {
		t.Fatalf("link failed: %v\n%s", err, out)
	}
	got := 0
	if ee, ok := exec.Command(exePath).Run().(*exec.ExitError); ok {
		got = ee.ExitCode()
	}
	if got != 3 {
		t.Fatalf("expected direct `-a` and generic negate[T:Neg] both correct (3), got %d", got)
	}
}

// Regression: a protocol impl may RENAME the protocol's declared parameters — names are not part of a
// function's type. `Sub` declares `__sub__(self, other)`; an impl naming the second param `o` must
// still conform (previously rejected with a baffling "expects fn(T,T)->T, got fn(T,T)->T" because
// SameType compared parameter names). Found dogfooding operator overloading in the wolf3d port.
func TestProtocolImplRenamedParamConforms(t *testing.T) {
	ok := analyzeOverloadSource(t, `
struct Vec2:
    x: i32
    y: i32

protocol Sub:
    def __sub__(self: Self, other: Self) -> Self

impl Sub for Vec2:
    def __sub__(self: Vec2, o: Vec2) -> Vec2:
        return Vec2{x: self.x - o.x, y: self.y - o.y}

def use(a: Vec2, b: Vec2) -> Vec2:
    return a - b
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("impl renaming the protocol's `other` param to `o` must conform, got: %v", errs)
	}
}

// End-to-end: compile and RUN a value-type `a + b`, asserting the componentwise result.
func TestRunCLIOperatorOverloadNative(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "operator_overload.elisa")
	// Each of the 5 operators contributes a distinct bit; a fully-correct run returns 255.
	src := `
struct V:
    x: i64

protocol Ops:
    def __add__(self: Self, other: Self) -> Self
    def __sub__(self: Self, other: Self) -> Self
    def __mul__(self: Self, other: Self) -> Self
    def __eq__(self: Self, other: Self) -> bool
    def __cmp__(self: Self, other: Self) -> i64

impl Ops for V:
    def __add__(self: V, other: V) -> V:
        return V{x: self.x + other.x}
    def __sub__(self: V, other: V) -> V:
        return V{x: self.x - other.x}
    def __mul__(self: V, other: V) -> V:
        return V{x: self.x * other.x}
    def __eq__(self: V, other: V) -> bool:
        return self.x == other.x
    def __cmp__(self: V, other: V) -> i64:
        return self.x - other.x

def main() -> i64:
    a: V = V{x: 7}
    b: V = V{x: 3}
    r: mutable i64 = 0
    if (a + b).x == 10:
        r <- r + 1
    if (a - b).x == 4:
        r <- r + 2
    if (a * b).x == 21:
        r <- r + 4
    if a == V{x: 7}:
        r <- r + 8
    if a != b:
        r <- r + 16
    if b < a:
        r <- r + 32
    if a <= a:
        r <- r + 64
    if a > b:
        r <- r + 128
    return r
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	objPath := filepath.Join(fixtureDir, "op.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objPath, fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("compile failed (exit %d):\n%s", code, stderr.String())
	}
	exePath := filepath.Join(fixtureDir, "op")
	if out, err := exec.Command("clang", objPath, "-o", exePath).CombinedOutput(); err != nil {
		t.Fatalf("link failed: %v\n%s", err, out)
	}
	// All 5 operators correct -> each contributes a distinct bit -> 255.
	err := exec.Command(exePath).Run()
	got := 0
	if ee, ok := err.(*exec.ExitError); ok {
		got = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != 255 {
		t.Fatalf("expected all of +,-,*,==,!=,<,<=,> correct (255), got exit %d", got)
	}
}

// The canonical std operator protocols (Add/Sub/Mul/Div/Eq/Ord in runtime.elisa) drive all operators,
// including `/` via Div; `Eq`'s single __eq__ derives ==/!=, `Ord`'s single __cmp__ derives </></<=/>=;
// and the protocols double as generic bounds (`[T: Add]`). Compiled+run: each op contributes a bit,
// all correct -> 255.
func TestRunCLICanonicalOperatorProtocols(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "canon_ops.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("rel runtime path: %v", err)
	}
	src := "# include \"" + filepath.ToSlash(runtimeInclude) + "\"\n" + `
struct Q:
    n: i64

impl Add for Q:
    def __add__(self: Q, other: Q) -> Q:
        return Q{n: self.n + other.n}
impl Sub for Q:
    def __sub__(self: Q, other: Q) -> Q:
        return Q{n: self.n - other.n}
impl Mul for Q:
    def __mul__(self: Q, other: Q) -> Q:
        return Q{n: self.n * other.n}
impl Div for Q:
    def __div__(self: Q, other: Q) -> Q:
        return Q{n: self.n / other.n}
impl Eq for Q:
    def __eq__(self: Q, other: Q) -> bool:
        return self.n == other.n
impl Ord for Q:
    def __cmp__(self: Q, other: Q) -> i64:
        return self.n - other.n

def add3[T: Add](a: T, b: T, c: T) -> T:
    return a + b + c

def main() -> i64:
    a: Q = Q{n: 12}
    b: Q = Q{n: 4}
    r: mutable i64 = 0
    sum: Q = a + b
    dif: Q = a - b
    prod: Q = a * b
    quot: Q = a / b
    if sum.n == 16:
        r <- r + 1
    if dif.n == 8:
        r <- r + 2
    if prod.n == 48:
        r <- r + 4
    if quot.n == 3:
        r <- r + 8
    if a == Q{n: 12}:
        r <- r + 16
    if a != b:
        r <- r + 32
    if b < a:
        r <- r + 64
    g: Q = add3(a, b, b)
    if g.n == 20:
        r <- r + 128
    return r
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	objPath := filepath.Join(fixtureDir, "canon.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objPath, fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("compile failed (exit %d):\n%s", code, stderr.String())
	}
	exePath := filepath.Join(fixtureDir, "canon")
	if out, err := exec.Command("clang", objPath, "-o", exePath).CombinedOutput(); err != nil {
		t.Fatalf("link failed: %v\n%s", err, out)
	}
	got := 0
	if ee, ok := exec.Command(exePath).Run().(*exec.ExitError); ok {
		got = ee.ExitCode()
	}
	if got != 255 {
		t.Fatalf("expected canonical protocols + Div + generic [T:Add] all correct (255), got %d", got)
	}
}

// A struct VALUE with no `__eq__` must be REJECTED at the comparison.
//
// AssignableTo(P, P) made `typesComparableForEquality` say yes, so the checker passed it
// through to a backend that has no lowering for it: emitBinaryExpr builds an `icmp` for
// every non-float operand type, aggregates included. The module then failed LLVM's own
// verifier with "Invalid operand types for ICmp instruction" naming an internal temp — a
// crash report about compiler internals, for an ordinary mistake in user code.
func TestStructValueEqualityWithoutEqImplIsRejected(t *testing.T) {
	result := analyzeOverloadSource(t, `
struct P:
    a: i64

def same(x: P, y: P) -> bool:
    return x == y
`)
	errs := result.Errors()
	found := false
	for _, err := range errs {
		if strings.Contains(err, "struct P has no == operator") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a source-level diagnostic naming the missing == operator, got: %v", errs)
	}
}

// The same comparison with an `__eq__` impl still resolves through the overload — the
// rejection above must not shadow `analyzeComparisonOverload`, which runs before it.
func TestStructValueEqualityWithEqImplStillResolves(t *testing.T) {
	result := analyzeOverloadSource(t, `
struct P:
    a: i64

protocol Eq:
    def __eq__(self: Self, other: Self) -> bool

impl Eq for P:
    def __eq__(self: P, other: P) -> bool:
        return self.a == other.a

def same(x: P, y: P) -> bool:
    return x == y
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a struct with an __eq__ impl must still compare, got: %v", errs)
	}
}

// Built-in aggregate values have no scalar equality lowering. They must be
// diagnosed before the backend can attempt an invalid LLVM `icmp` over the
// aggregate representation.
func TestAggregateValueEqualityWithoutLoweringIsRejected(t *testing.T) {
	result := analyzeOverloadSource(t, `
def same(xs: darray[i64], ys: darray[i64]) -> bool:
    return xs == ys
`)
	errs := result.Errors()
	found := false
	for _, err := range errs {
		if strings.Contains(err, "aggregate values do not support ==") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected aggregate equality diagnostic before LLVM lowering, got: %v", errs)
	}
}
