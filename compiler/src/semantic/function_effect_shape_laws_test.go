package semantic

import (
	"elisacore/src/ast"
	"testing"
)

// docs/85 §4 Stage 4: an effect law forbids effects; a conforming function that uses none is clean.
func TestEffectLawFulfillsClean(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def pure_add(x: i64, y: i64) -> i64 fulfills NoAlloc:
    return x + y
`
	result := analyzeTreeTestSource(t, "effect_law_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a function using no forbidden effect should fulfill the effect law cleanly, got: %v", errs)
	}
}

// The effect law is discharged against the inferred effect set: a function that allocates while
// `fulfills NoAlloc` is a compile error.
func TestEffectLawFulfillsViolationErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def grow() -> i64 fulfills NoAlloc:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)
        return xs[0]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "effect_law_violation.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "uses the `Memory.Allocate` effect") {
		t.Fatalf("allocating under `fulfills NoAlloc` must error, got:\n%s", allDiagnostics(result))
	}
}

// Applying an effect law with a subject (`fulfills f is NoAlloc`) is a wrong-form error — effect
// laws are function-level.
func TestEffectLawWithSubjectErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def f(x: i64) -> i64 fulfills x is NoAlloc:
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "effect_law_subject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "constrains the whole function") {
		t.Fatalf("applying an effect law with a subject must error, got:\n%s", allDiagnostics(result))
	}
}

// An effect law used in value `is` position is a wrong-class error.
func TestEffectLawInValueIsErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def f(x: i64) -> bool:
    return x is NoAlloc
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "effect_law_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is an effect law") {
		t.Fatalf("using an effect law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/89 Stage 4 (shape): a function whose every index access is statically proven in-bounds
// fulfills the built-in `NoBoundsChecks` shape law cleanly.
func TestShapeLawNoBoundsChecksClean(t *testing.T) {
	src := `
def sum(xs: darray[i64]) -> i64 fulfills NoBoundsChecks:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i]
    return total
`
	result := analyzeTreeTestSource(t, "shape_nobounds_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a function with only proven index accesses should fulfill NoBoundsChecks, got: %v", errs)
	}
}

// An unproven, bounds-requiring index access would emit a runtime bounds check → violates the law.
func TestShapeLawNoBoundsChecksViolationErrors(t *testing.T) {
	src := `
def third(xs: darray[i64]) -> i64 fulfills NoBoundsChecks:
    return xs[2]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shape_nobounds_violation.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "fulfills NoBoundsChecks") {
		t.Fatalf("an unproven index access under `fulfills NoBoundsChecks` must error, got:\n%s", allDiagnostics(result))
	}
}

// Applying a shape law with a subject is a wrong-form error — shape laws are function-level.
func TestShapeLawWithSubjectErrors(t *testing.T) {
	src := `
def f(xs: darray[i64]) -> i64 fulfills xs is NoBoundsChecks:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shape_subject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "constrains the whole function") {
		t.Fatalf("applying a shape law with a subject must error, got:\n%s", allDiagnostics(result))
	}
}

// A shape law used in value `is` position is a wrong-class error.
func TestShapeLawInValueIsErrors(t *testing.T) {
	src := `
def f(xs: darray[i64]) -> bool:
    return xs is NoBoundsChecks
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shape_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is a shape law") {
		t.Fatalf("using a shape law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/85 §6: a composite law unions its members — a function clean against every member fulfills it.
func TestCompositeLawFulfillsClean(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law NoPanic forbids Abort.Panic
law Leaf includes NoAlloc, NoPanic

def add(x: i64, y: i64) -> i64 fulfills Leaf:
    return x + y
`
	result := analyzeTreeTestSource(t, "composite_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a function clean against all composite members should fulfill it, got: %v", errs)
	}
}

// A composite law is violated if the function uses an effect any member forbids (here Memory.Allocate
// via the included NoAlloc).
func TestCompositeLawFulfillsViolationErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law NoPanic forbids Abort.Panic
law Leaf includes NoAlloc, NoPanic

def grow() -> i64 fulfills Leaf:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)
        return xs[0]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_violation.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "fulfills Leaf") || !contains(allDiagnostics(result), "Memory.Allocate") {
		t.Fatalf("a composite member's forbidden effect must be enforced, got:\n%s", allDiagnostics(result))
	}
}

// A composite may union an effect law and a built-in shape law; both obligations are discharged.
func TestCompositeLawMixesEffectAndShape(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law HotKernel includes NoAlloc, NoBoundsChecks

def get_third(xs: darray[i64]) -> i64 fulfills HotKernel:
    return xs[2]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_shape.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "requires NoBoundsChecks") {
		t.Fatalf("a composite's shape member must be discharged, got:\n%s", allDiagnostics(result))
	}
}

// A composite that includes a value law is malformed — `includes` composes only function-level laws.
func TestCompositeLawRejectsValueMember(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law Bad includes Positive

def f() -> i64 fulfills Bad:
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_value_member.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "composes only function-level laws") {
		t.Fatalf("a composite including a value law must error, got:\n%s", allDiagnostics(result))
	}
}

// A cyclic include graph is rejected.
func TestCompositeLawRejectsCycle(t *testing.T) {
	src := `
law A includes B
law B includes A

def f() -> i64 fulfills A:
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_cycle.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "cyclic") {
		t.Fatalf("a cyclic composite must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/89 Stage 5 (measure): `fulfills Vectorizes` compiles with no static obligation and tags the
// function's range loops expected-to-vectorize, opting them into the post-codegen autovec verifier.
func TestMeasureLawVectorizesTagsLoops(t *testing.T) {
	src := `
def scale(xs: darray[i64]) -> i64 fulfills Vectorizes:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i] * 2
    return total
`
	result := analyzeTreeTestSource(t, "measure_vectorizes.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a measure law is verify-only and should compile cleanly, got: %v", errs)
	}
	fn, ok := result.File.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", result.File.Decls[0])
	}
	for _, stmt := range fn.Body {
		if forStmt, ok := stmt.(*ast.ForStmt); ok {
			if !forStmt.AutovecExpected {
				t.Fatalf("a range loop in a `fulfills Vectorizes` function must be tagged AutovecExpected")
			}
			return
		}
	}
	t.Fatalf("did not find the range loop to check its AutovecExpected tag")
}

// Applying a measure law with a subject is a wrong-form error.
func TestMeasureLawWithSubjectErrors(t *testing.T) {
	src := `
def f(xs: darray[i64]) -> i64 fulfills xs is Vectorizes:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "measure_subject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "constrains the whole function") {
		t.Fatalf("applying a measure law with a subject must error, got:\n%s", allDiagnostics(result))
	}
}

// A measure law used in value `is` position is a wrong-class error.
func TestMeasureLawInValueIsErrors(t *testing.T) {
	src := `
def f(x: i64) -> bool:
    return x is Vectorizes
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "measure_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is a measure law") {
		t.Fatalf("using a measure law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/85 §8 hard rule: a measure law is NOT composable — including it in a law is a class error.
func TestMeasureLawNotComposable(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law Bad includes NoAlloc, Vectorizes

def f() -> i64 fulfills Bad:
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "measure_composable.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "not composable") {
		t.Fatalf("including a measure law in a composite must error, got:\n%s", allDiagnostics(result))
	}
}
