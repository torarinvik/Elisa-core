package main

import "testing"

// A machine state is an ordinary payload enum, so its payload ABI must support the
// fixed two-word StringView value in both a constructor and a match dispatch. This is
// also the shape used by StructPy's bounded cross-module lookup walk.
const machineSViewPayloadBody = `
enum Lookup:
    Search(sview, sview, usize)
    Done(u32)

def probe(root: sview, member: sview) -> u32:
    limit: usize = 3
    remaining: mutable usize = limit + 1
    result: u32 = machine from Lookup.Search(root, member, 0) decreases remaining:
        Lookup.Search(module, symbol, steps):
            remaining <- remaining - 1
            next Lookup.Done(0xffffffff) if steps >= limit
            next Lookup.Done(0xffffffff) if module == root and symbol == member
            next Lookup.Search(module, symbol, steps + 1)
        Lookup.Done(value):
            done value
    return result

@test
def machine_sview_payload() -> void:
    can Abort.Panic:
        if probe("root", "member") != 0xffffffff:
            panic("machine sview payload result")
`

func TestMachineSViewPayload(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "machine_sview_payload", machineSViewPayloadBody)
	assertAllPassed(t, exit, stdout, stderr, "machine_sview_payload")
}
