package semantic_test

import (
	"strings"
	"testing"

	"llcontext/src/semantic"
)

func requireNamedType(t *testing.T, result *semantic.Result, name string) semantic.Type {
	t.Helper()
	typ, ok := result.NamedTypes[name]
	if !ok || typ == nil {
		t.Fatalf("expected named type %q", name)
	}
	return typ
}

func TestAnalyzeAcceptsNamespaceAndUsingForTypesAndFunctions(t *testing.T) {
	src := `namespace math:
	struct Box:
		value: int

	def make_box(value: int) -> Box:
		return Box(value)

	def read(box: Box) -> int:
		return box.value

using math

def run() -> int:
	box: math.Box = make_box(7)
	return read(box)
`
	result, errs := parseAndAnalyze(t, "namespace_using_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireNamedType(t, result, "math.Box")
	requireFunctionReturnTypeString(t, result, "math.make_box", "math.Box")
	requireFunctionReturnTypeString(t, result, "math.read", "int")
	requireFunctionReturnTypeString(t, result, "run", "int")
}

func TestAnalyzeAcceptsTopLevelUsingInsideAnotherNamespace(t *testing.T) {
	src := `namespace math:
	def inc(value: int) -> int:
		return value + 1

using math

namespace app:
	def run() -> int:
		return inc(41)
`
	result, errs := parseAndAnalyze(t, "namespace_top_level_using_ok.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "math.inc", "int")
	requireFunctionReturnTypeString(t, result, "app.run", "int")
}

func TestAnalyzeNamespacesDoNotLeakWithoutUsing(t *testing.T) {
	src := `namespace math:
	def inc(value: int) -> int:
		return value + 1

def run() -> int:
	return inc(41)
`
	_, errs := parseAndAnalyze(t, "namespace_no_using_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, semantic.UndefinedIdentifierMessage("inc")) {
		t.Fatalf("expected undefined identifier diagnostic, got:\n%s", all)
	}
}
