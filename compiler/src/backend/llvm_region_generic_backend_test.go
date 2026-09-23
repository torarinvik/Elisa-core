package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRPreservesForwardedRegionParameterInGenericField(t *testing.T) {
	src := `error RegionProbeError:
    Failed

struct RegionHandle[@owner]:
    value: i64

struct ParseResult[@owner]:
    value: RegionHandle[owner]

def parse_checked[@r](arena: mutable Arena& @r) -> ParseResult[r]:
    handle: RegionHandle[r] = RegionHandle{value: 7}
    return ParseResult{value: handle}

def parse[@r](arena: mutable Arena& @r) -> RegionHandle[r] error[RegionProbeError]:
    result: ParseResult[r] = parse_checked(arena)
    return result.value

def parse_forwarded[@r](arena: mutable Arena& @r) -> RegionHandle[r] error[RegionProbeError]:
    can Abort.Panic:
        return try parse(arena)

def extract[@r](result: ParseResult[r]) -> RegionHandle[r]:
    return result.value

def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        arena: mutable Arena = zeroed
        parsed: ParseResult[arena] = parse_checked(&arena)
        handle: RegionHandle[arena] = extract(parsed)
        return handle.value - 7
`
	result := parseAndAnalyzeBackendTest(t, "backend_region_generic_field_try.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generic region annotations in forwarded result fields should lower: %v", err)
	}
	if strings.Contains(output, "<null operand!") {
		t.Fatalf("region-generic calls must carry the matching Arena argument, got invalid LLVM IR:\n%s", output)
	}
	if !strings.Contains(output, "@extract__arena") {
		t.Fatalf("region-generic function should be specialized for the concrete result region:\n%s", output)
	}
}
