package parser

import (
	"strings"
	"testing"
)

func TestParallelFoldRejectsAccumulatorDependentTransform(t *testing.T) {
	for _, transform := range []string{"acc", "(value + acc)", "[value, acc][0]", "twice(value, acc)"} {
		source := "def f(values: darray[i64]) -> i64:\n    return (acc + " + transform + " for value in values with acc = 1 by par)\n"
		_, errors := parseSourceFile(t, source)
		if !strings.Contains(strings.Join(errors, "\n"), "transform must not reference the accumulator") {
			t.Fatalf("%s: missing dependency diagnostic: %v", transform, errors)
		}
		source = strings.Replace(source, " by par", "", 1)
		if _, errors = parseSourceFile(t, source); len(errors) != 0 {
			t.Fatalf("%s: sequential fold rejected: %v", transform, errors)
		}
	}
}
