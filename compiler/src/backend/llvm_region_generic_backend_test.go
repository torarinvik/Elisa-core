package backend

import "testing"

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

def main() -> i64:
    return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_region_generic_field_try.elisa", src)
	if _, err := generateLLVMIRWithDefaultPackedLoweringForTest(result); err != nil {
		t.Fatalf("generic region annotations in forwarded result fields should lower: %v", err)
	}
}
