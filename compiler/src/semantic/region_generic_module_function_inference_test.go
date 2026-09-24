package semantic

import (
	"strings"
	"testing"
)

func TestRegionGenericFunctionInfersModuleTypeRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_generic_module_inference.elisa", `module Json:
    enum JsonValue:
        Null

    struct JsonValueHandle[@r]:
        private:
            node: JsonValue

    struct JsonParseResult[@r]:
        code: mutable i64
        pos: mutable i64
        value: mutable JsonValueHandle[r]

    def json_parse_checked[@r](arena: mutable Arena& @r) -> JsonParseResult[r]:
        return JsonParseResult{code: 0, pos: 0, value: JsonValueHandle{node: JsonValue.Null}}

    def json_handle_from_result[@r](result: JsonParseResult[r]) -> JsonValueHandle[r]:
        return result.value

using Json

def consume_pair[@r](left: JsonValueHandle[r], right: JsonValueHandle[r]) -> i64:
    return 0

def main() -> i64:
    can Memory.Allocate:
        arena: mutable Arena = zeroed
        parsed: JsonParseResult[arena] = json_parse_checked(&arena)
        return consume_pair(json_handle_from_result(parsed), json_handle_from_result(parsed))
`)
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("qualified generic-region call should infer the concrete module type region:\n%s", errors)
	}
}
