//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersTypedFlagsBuilderAndMembership(t *testing.T) {
	src := `struct Flags[T]:
    bits: mutable u64

struct FlagsNamespace:
    _marker: u8

global flags: FlagsNamespace = zeroed

def new[T](api: FlagsNamespace) -> Flags[T]:
    _ = api
    result: Flags[T] = zeroed
    return result

def flags_mask[T](value: T) -> u64:
    index: u64 = value.u64()
    return 1.u64() << index.u32()

def add[T](items: mutable Flags[T]&, value: T):
    items.bits <- items.bits | flags_mask[T](value)

const enum RoutineFlag of u8:
    Imported
    Exported
    VarArgs

def build_flags(imported: bool, exported: bool, varargs: bool) -> Flags[RoutineFlag]:
    result: mutable Flags[RoutineFlag] = flags.new()
    if imported:
        result.add(.Imported)
    if exported:
        result.add(.Exported)
    if varargs:
        result.add(.VarArgs)
    return result

def has_imported(value: Flags[RoutineFlag]&) -> bool:
    return value[RoutineFlag.Imported]
`

	result := parseAndAnalyzeBackendTest(t, "backend_typed_flags_builder.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Flags__RoutineFlag = type { i64 }",
		"define %Flags__RoutineFlag @build_flags",
		"define i1 @has_imported",
		"flags.has",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected typed flags lowering to include %q, got:\n%s", check, output)
		}
	}
}
