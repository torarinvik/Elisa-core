//go:build cgo

package backend

import (
	"regexp"
	"testing"
)

func TestQualifiedStoreCapturingSubmitLLVM(t *testing.T) {
	source := parallelForConcurrencyPrelude + `
module Shapes:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

module Other:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

struct Ticket:
    node: Shapes::Expr?
    bias: i64

def worker(ticket: Ticket) -> i64:
    return ticket.bias

struct OtherTicket:
    node: Other::Expr?
    bias: i64

def other_worker(ticket: OtherTicket) -> i64:
    return ticket.bias

def main() -> i64:
    can Memory.Allocate, Pool{Create,Submit,Shutdown}, Abort.Panic:
        region storage(65536):
            pool workers(1):
                task: Task[i64, Pending] = submit[workers] worker(Ticket{node: null, bias: 42})
                other_task: Task[i64, Pending] = submit[workers] other_worker(OtherTicket{node: null, bias: 17})
                _ = move task
                _ = move other_task
                return 0
`
	result := parseAndAnalyzeBackendTest(t, "qualified_submit_store.elisa", source)
	output, err := GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("qualified store capture did not lower: %v", err)
	}
	if len(regexp.MustCompile(`%submit\.capture\.store[^ ]* =`).FindAllStringIndex(output, -1)) != 2 {
		t.Fatal("fixture did not capture both independent qualified stores")
	}
}
