package main

import (
	"strings"
	"testing"
)

// Regression for the sview-via-reference equality codegen gap (FIXED).
//
// Comparing an `sview` obtained through a reference — e.g. a `dict.get` result bound with
// `is` (dict.get returns `sview&?`) — used to fail codegen two ways:
//   1. runtimeStringCompareInfo returned the operand type with its ref intact, so
//      ctx_string_views_eq was declared once as i64(ptr, StringView) and elsewhere as
//      i64(StringView, StringView) -> "conflicting LLVM function declaration".
//   2. Even peeling the type, emitExpr on an `sview&` operand loaded the POINTER, not the
//      StringView aggregate (value-context coercion loads scalars through a ref, not
//      aggregates) -> "call parameter type does not match function signature".
//
// Fix: stringCompareValueOperandType peels `sview&`->`sview` for the declared param types,
// and emitStringCompareOperandValue loads the StringView through the ref so the value
// matches. Both live in the runtime string-compare path.
//
// probe() finds the key and the stored view equals the query, returning 1; as a process
// exit code that is "exit status 1".
func TestSviewCompareThroughDictGetRuns(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def probe() -> usize can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        m: mutable dict[u64, sview] = {}
        m.put(1, sview("hello", 0, -1))
        hit: mutable usize = 0
        if m.get(1) is existing:
            if existing == sview("hello", 0, -1):
                hit <- hit + 1
        return hit

def main() -> int can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        return probe().int()
`)
	if status == "BUILD-FAIL" && (strings.Contains(out, "conflicting LLVM function declaration") ||
		strings.Contains(out, "does not match function signature")) {
		t.Fatalf("sview-via-ref equality codegen REGRESSED: %s", out)
	}
	// probe() returns 1 (the stored view equals the query) -> exit status 1.
	if status != "RUNERR" || !strings.Contains(out, "exit status 1") {
		t.Fatalf("sview-via-ref equality: expected exit 1 (match found), got status=%s out=%q", status, out)
	}
}
