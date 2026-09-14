package semantic

import (
	"strings"
	"testing"
)

func TestMergeNullWithOptionalYieldsOptional(t *testing.T) {
	opt := &OptionalType{Value: &BuiltinType{Name: "i64"}}
	for _, merged := range []Type{MergeTypes(nullType, opt), MergeTypes(opt, nullType)} {
		if !SameType(merged, opt) {
			t.Fatalf("expected %s, got %s", opt, merged)
		}
	}
}

func TestOptionalMatchExpressionWildcardCoversAbsence(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "optional_match_wildcard.elisa", `struct Box:
    kind: i64

def pick(v: Box?, k: i64) -> Box?:
    r: Box? =
        match v:
            b if b.kind != k:
                null
            _:
                v
    return r
`)
}

func TestOptionalMatchExpressionWithoutCoverageIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "optional_match_hole.elisa", `struct Box:
    kind: i64

def pick(v: Box?) -> i64:
    return match v:
        b if b.kind > 0:
            b.kind
        b:
            0
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "non-exhaustive match expression over Box?") {
		t.Fatalf("expected coverage error, got %v", result.Errors())
	}
}
