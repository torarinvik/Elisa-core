package semantic

import (
	"strings"
	"testing"
)

const moduleOwnErrorSetSource = `module Ids:
    error IdError:
        TooSmall
        TooBig

    def check(v: i64) -> i64 error[IdError]:
        if v < 0:
            raise IdError.TooSmall
        if v > 10:
            raise IdError.TooBig
        v * 2
`

// A module's own error set is reachable under the name the author wrote. Its tags are
// registered under the set's QUALIFIED name (`Ids.IdError.TooSmall`), so a key built from
// the spelled qualifier (`IdError.TooSmall`) matched nothing and the set was reported to
// have no tag it plainly declares — from inside the very module that declares it.
func TestErrorSetTagResolvesInsideItsOwnModule(t *testing.T) {
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "module_error_tag.elisa", moduleOwnErrorSetSource, AnalyzeOptions{})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("a module's own error set must resolve unqualified, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The qualified spelling kept working throughout; it must keep working.
func TestErrorSetTagResolvesQualified(t *testing.T) {
	src := strings.Replace(moduleOwnErrorSetSource, "raise IdError.", "raise Ids::IdError.", -1)
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "module_error_tag_qualified.elisa", src, AnalyzeOptions{})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("the qualified spelling must still resolve, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A tag that really is absent is still reported — the fix widens resolution, not acceptance.
func TestErrorSetAbsentTagStillReported(t *testing.T) {
	src := strings.Replace(moduleOwnErrorSetSource, "raise IdError.TooBig", "raise IdError.Missing", 1)
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "module_error_tag_absent.elisa", src, AnalyzeOptions{})
	// Only the tag half is asserted: the set half renders through ErrorSetDiagnosticName,
	// whose qualified-vs-bare spelling is not what this test is pinning.
	if !strings.Contains(strings.Join(r.Errors(), "\n"), `has no tag "Missing"`) {
		t.Fatalf("an absent tag must still be reported, got:\n%s", strings.Join(r.Errors(), "\n"))
	}
}

func TestErrorSetSubsetThroughNestedModuleAlias(t *testing.T) {
	src := `module Outer:
    module Right:
        module Nested:
            error Fault:
                Available
                Busy
using Outer as First
using First::Right as Second
def available() -> i64 error[Second::Nested::Fault{Available}]:
    return 1
def full() -> i64 error[Second::Nested::Fault]:
    return available()
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nested_error_alias.elisa", src, AnalyzeOptions{})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("valid nested alias rejected: %s", strings.Join(errs, "\n"))
	}
	invalid := strings.Replace(src, "Fault{Available}", "Fault{Missing}", 1)
	r = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nested_error_alias_missing.elisa", invalid, AnalyzeOptions{})
	if errs := strings.Join(r.Errors(), "\n"); !strings.Contains(errs, `has no tag "Missing"`) {
		t.Fatalf("missing tag accepted through alias: %s", errs)
	}
	mismatch := src + `
def wrap[errorset R](callback: fn() -> i64 error[R]) -> i64 error[R]:
    return try callback()
def wrong() -> i64 error[Second::Nested::Fault{Busy}]:
    return wrap(available)
`
	r = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nested_error_alias_mismatch.elisa", mismatch, AnalyzeOptions{})
	errs := strings.Join(r.Errors(), "\n")
	if !strings.Contains(errs, "return type expects") || strings.Contains(errs, "has no tag") {
		t.Fatalf("wrong subset was not rejected after alias resolution: %s", errs)
	}
}
