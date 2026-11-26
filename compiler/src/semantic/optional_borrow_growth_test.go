package semantic

import (
	"strings"
	"testing"
)

// An optional borrow parameter (`edges: mutable darray[usize]&?`) is grown only through the name
// a refinement binds. That growth needs the caller's region exactly as `edges.push(x)` on a plain
// borrow does, and a call passing `null` binds the region to `perm`: a null borrow grows nothing.
func TestOptionalBorrowParameterGrowthThreadsCallerRegion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "optional_borrow_growth.elisa", `def enqueue(value: usize, edges: mutable darray[usize]&?) -> void can[Memory.Allocate]:
    if edges is e:
        e.push(value)

def enqueue_get(value: usize, edges: mutable darray[usize]&?) -> void can[Memory.Allocate]:
    e = get edges else return
    e.push(value)

def enqueue_typed(value: usize, edges: mutable darray[usize]&?) -> void can[Memory.Allocate]:
    e: mutable darray[usize]& = get edges else return
    e.push(value)

def push_alias(value: usize, edges: mutable darray[usize]&) -> void can[Memory.Allocate]:
    alias: mutable darray[usize]& = edges
    alias.push(value)

def walk(count: usize, edges: mutable darray[usize]&?) -> void can[Memory.Allocate]:
    for index in 0..<count |edges|:
        enqueue(index, edges)

def main() -> i32 can[Memory.Allocate, Abort.Panic]:
    buffer: mutable darray[usize] = []
    enqueue(3, &buffer)
    enqueue(4, null)
    enqueue_get(5, &buffer)
    enqueue_get(6, null)
    enqueue_typed(7, &buffer)
    enqueue_typed(8, null)
    push_alias(9, &buffer)
    walk(2, &buffer)
    walk(2, null)
    0
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("want optional-borrow growth accepted, got:\n%s", strings.Join(errors, "\n"))
	}
}

// Growing through an optional borrow keeps the plain borrow's escape rules: a locally built
// row pushed into the caller's container is rejected just as it is through `T&`.
func TestOptionalBorrowGrowthKeepsEscapeRules(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "optional_borrow_escape.elisa", `def add_row(width: usize, rows: mutable darray[darray[usize]]&?) -> void can[Memory.Allocate]:
    if rows is r:
        row: mutable darray[usize] = []
        for index in 0..<width |row|:
            row.push(index)
        r.push(row)
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "is stored into longer-lived region") {
		t.Fatalf("want the local row rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}
