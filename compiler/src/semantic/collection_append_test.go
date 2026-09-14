package semantic

import "testing"

func TestStaticCollectionAppend(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_collection_append.elisa", `static def score() -> i64:
    values: mutable darray[i64] = []
    values += 4
    values += 9
    return values[0] + values[1]

def keep() -> void:
    static assert score() == 13
`)
}
