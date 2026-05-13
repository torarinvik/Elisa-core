package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeFlagsRequiresConstEnumElementType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "flags_requires_const_enum.elisa", `struct Flags[T]:
    bits: u64

def bad() -> Flags[i64]:
    return zeroed
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "Flags[T] expects a const enum type argument, got i64") {
		t.Fatalf("expected Flags const-enum diagnostic, got:\n%s", got)
	}
}

func TestAnalyzeFlagsRejectsPayloadEnumElementType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "flags_rejects_payload_enum.elisa", `struct Flags[T]:
    bits: u64

enum Expr:
    Int(value: i64)
    Missing

def bad() -> Flags[Expr]:
    return zeroed
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "Flags[T] expects a const enum type argument, got Expr") {
		t.Fatalf("expected Flags payload-enum diagnostic, got:\n%s", got)
	}
}

func TestAnalyzeFlagsAllowsGenericHelperDefinitions(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "flags_allows_generic_helpers.elisa", `struct Flags[T]:
    bits: u64

def flags_empty[T]() -> Flags[T]:
    result: Flags[T] = zeroed
    return result
`)
}

func TestAnalyzeFlagsIndexRejectsWrongEnumTypeAndFallback(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "flags_index_rejections.elisa", `struct Flags[T]:
    bits: u64

const enum RoutineFlag of u8:
    External
    Forward

const enum OtherFlag of u8:
    Other

def bad(value: Flags[RoutineFlag]&) -> bool:
    first: bool = value[OtherFlag.Other]
    return value[RoutineFlag.External] else false
`)
	all := strings.Join(result.Errors(), "\n")
	for _, check := range []string{
		"flags index expects RoutineFlag, got OtherFlag",
		"flags membership indexing does not support fallback values",
	} {
		if !strings.Contains(all, check) {
			t.Fatalf("expected diagnostic %q, got:\n%s", check, all)
		}
	}
}
