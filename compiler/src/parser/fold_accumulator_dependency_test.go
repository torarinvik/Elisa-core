package parser

import (
	"strings"
	"testing"
)

func TestParallelFoldRejectsAccumulatorDependentTransform(t *testing.T) {
	source := "def f(values: darray[i64]) -> i64:\n    return (acc + (value + acc) for value in values with acc = 1 by par)\n"
	_, errors := parseSourceFile(t, source)
	if !strings.Contains(strings.Join(errors, "\n"), "transform must not reference the accumulator") {
		t.Fatalf("missing dependency diagnostic: %v", errors)
	}
	source = strings.Replace(source, " by par", "", 1)
	if _, errors = parseSourceFile(t, source); len(errors) != 0 {
		t.Fatalf("sequential fold rejected: %v", errors)
	}
}
