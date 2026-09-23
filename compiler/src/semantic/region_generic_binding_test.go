package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeRegionGenericBindingsAcrossGenericValues(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "region_generic_binding.elisa", `struct Box[@owner]:
	value: i64

def build[@r](arena: mutable Arena& @r) -> Box[r]:
	return Box{value: 7}

def read[@r](box: Box[r]) -> i64:
	return box.value

def relay[@r](box: Box[r]) -> i64:
	return read(box)

def main() -> i64:
	can Memory.Allocate:
		arena: mutable Arena = zeroed
		built: Box[arena] = build(&arena)
		return relay(built)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected region parameters to flow through Arena references and generic values, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeRegionGenericBindingPreservesForwardedArenaRegionThroughTry(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "region_generic_arena_try.elisa", `error RegionProbeError:
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
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected a forwarded Arena region to remain the caller's region through `try`, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeRegionGenericBindingRejectsMismatchedArenaAndValueRegions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "region_generic_arena_mismatch.elisa", `struct RegionHandle[@owner]:
	value: i64

def consume[@r](value: RegionHandle[r], arena: mutable Arena& @r) -> RegionHandle[r]:
	return value

def forward[@outer, @inner](value: RegionHandle[outer], arena: mutable Arena& @inner) -> RegionHandle[outer]:
	return consume(value, arena)
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "Arena selects") {
		t.Fatalf("expected incompatible value and Arena regions to be rejected, got:\n%s", joined)
	}
}

func TestAnalyzeRegionGenericBindingRejectsMixedRegionArguments(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "region_generic_mixed_args.elisa", `struct Box[@owner]:
	value: i64

def same_region[@r](left: Box[r], right: Box[r]) -> i64:
	return left.value + right.value

def main() -> i64:
	region first(64):
		left: Box[first] = Box{value: 1}
		region second(64):
			right: Box[second] = Box{value: 2}
			return same_region(left, right)
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, `expects Box[first], got Box[second]`) {
		t.Fatalf("expected the second argument to be rejected against the first argument's region binding, got:\n%s", joined)
	}
}
