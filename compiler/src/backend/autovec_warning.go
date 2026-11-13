package backend

import "fmt"

// autovecPerfWarning renders the -Wperf message for a marked-but-unvectorized loop, naming the
// construct (from the embedded reason) and the most likely blocker for that construct. The loop was
// only marked because its body is call-free and its shape was lowered to be vectorizer-legal, so a
// real failure is almost always a memory dependency the vectorizer could not disprove.
func autovecPerfWarning(pos, reason string, optLevel int, enforce bool) string {
	construct := reason
	if construct == "" {
		construct = "comprehension"
	}
	hint := "check for an aliasing or loop-carried dependency, or a body the vectorizer cost model rejected"
	switch reason {
	case "fold reduction":
		hint = "the reduction did not re-bracket into vector lanes — check for an aliasing source or a " +
			"loop-carried dependency other than the accumulator"
	case "comprehension map":
		hint = "the element store did not vectorize — check whether the source and destination may alias, " +
			"or for a loop-carried dependency in the element transform"
	case "loop":
		hint = "the element store did not vectorize — check for aliasing between the arrays, a loop-carried " +
			"dependency, or a shape the vectorizer cost model rejected"
	}
	severity := "warning"
	if enforce {
		severity = "error"
	}
	return fmt.Sprintf("%s: %s [-Wperf]: %s was lowered for auto-vectorization but did not vectorize "+
		"at -O%d; %s. If scalar execution is intended, wrap it in a `can Scalar:` block (or grant "+
		"`can[Scalar]` on the function)", pos, severity, construct, optLevel, hint)
}
