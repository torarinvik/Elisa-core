package semantic

import (
	"strings"
	"testing"
)

const storeFlowPrelude = `enum Expr:
    Leaf(name: sview)
    Call(callee: sview, names: darray[sview])
    Absent

`

// A self-recursive walker whose recursive call passes comprehension-built view containers next to a
// writable Expr container it never stores them into is accepted.
func TestStoreFlowSummaryRecursiveWalkerKeepsArgumentsApart(t *testing.T) {
	src := storeFlowPrelude + `def walk(depth: u32, nesting: darray[sview]&, locals: mutable darray[sview]&, exprs: mutable darray[Expr]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        return if depth == 0
        exprs.push(Expr.Absent)
        child: mutable darray[sview] = [entry for entry in nesting]
        child.push("x")
        kid: mutable darray[sview] = [entry for entry in locals]
        walk(depth - 1, child, kid, exprs)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "store_flow_walker.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// A callee that really stores a view argument into the writable container stays rejected, also
// through a recursive hop.
func TestStoreFlowSummaryStillRejectsRealStore(t *testing.T) {
	src := storeFlowPrelude + `def put(depth: u32, first: Expr, exprs: mutable darray[Expr]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        return if depth == 0
        exprs.push(first)
        put(depth - 1, first, exprs)

def run(exprs: mutable darray[Expr]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        buf: mutable darray[u8] = []
        buf.push(65)
        put(2, Expr.Leaf(buf.as_sview()), exprs)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_real_store.elisa", src, AnalyzeOptions{})
	found := false
	for _, e := range result.Errors() {
		if strings.Contains(e, "call may store a value in region") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the store escape to be reported, got: %v", result.Errors())
	}
}

// A connected parameter pair is not necessarily connected in both directions.
// If the callee copies from a longer-lived source into a shorter-lived target,
// the reverse-only pair at the call site cannot store the short-lived value
// back into the longer-lived source and must not be rejected.
func TestStoreFlowSummaryCallArgumentDirectionAvoidsReverseOnlyEscape(t *testing.T) {
	src := `enum E:
    Text(value: sview)

def append_source_into_target(target: mutable darray[E]&, source: mutable darray[E]&):
    can Memory.Allocate, Abort.Panic:
        target.push(source[0])

def caller():
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            source: mutable darray[E] @outer = [E.Text("static")]
            region inner(2048):
                target: mutable darray[E] @inner = []
                append_source_into_target(target, source)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_direction_reverse_only.elisa", src)
	if errs := strings.Join(result.Errors(), "\n"); errs != "" {
		t.Fatalf("reverse-only flow must not be treated as a source-to-target store, got:\n%s", errs)
	}
}
