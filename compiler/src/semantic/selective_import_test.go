package semantic

import (
	"strings"
	"testing"
)

// `from Module import name` brings only the named members into scope unqualified,
// resolving functions and types the same way `using Module` does, but selectively.
func TestSelectiveImportBringsNamedFunctionAndTypeIntoScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "selective_import_basic.elisa", `
module Geo:
    struct Point:
        x: mutable i64
    def origin() -> Point:
        return Point{x: 0}

from Geo import Point, origin

def first(p: Point&) -> i64:
    return p.x

def use() -> i64:
    return origin().x
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

// Selective import only exposes the listed names — an unimported sibling stays
// out of scope (so it cannot clash with the importer's own declarations).
func TestSelectiveImportDoesNotExposeUnlistedNames(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "selective_import_unlisted.elisa", `
module Foo:
    def bar() -> i64:
        return 1
    def baz() -> i64:
        return 2

from Foo import bar

def call_unimported() -> i64:
    return baz()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "baz") {
		t.Fatalf("expected unimported `baz` to be unresolved, got:\n%s", all)
	}
}

// An imported name coexists with the importer's own same-named declaration of a
// SIBLING (Foo.baz is not imported, so a local `baz` does not clash), while the
// imported `bar` resolves to Foo.bar.
func TestSelectiveImportCoexistsWithLocalSiblingDecl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "selective_import_coexist.elisa", `
module Foo:
    def bar() -> i64:
        return 10
    def baz() -> i64:
        return 99

from Foo import bar

def baz() -> i64:
    return bar() + 5
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

// `Foo.bar(...)` (dot) on a namespace points the user at `::` rather than the
// cryptic "undefined identifier Foo" — `.` is value member access, `::` qualifies
// namespaces.
func TestNamespaceDotAccessSuggestsScopeOperator(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ns_dot_hint.elisa", `
module Foo:
    def bar() -> i64:
        return 1

def run() -> i64:
    return Foo.bar()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is a namespace") || !strings.Contains(all, "Foo::bar") {
		t.Fatalf("expected namespace `::` suggestion, got:\n%s", all)
	}
}

// Importing the same bare name from two different modules is a conflict.
func TestSelectiveImportConflictingNamesReported(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "selective_import_conflict.elisa", `
module A:
    def thing() -> i64:
        return 1

module B:
    def thing() -> i64:
        return 2

from A import thing
from B import thing

def run() -> i64:
    return thing()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "conflicting import") {
		t.Fatalf("expected conflicting-import diagnostic, got:\n%s", all)
	}
}
