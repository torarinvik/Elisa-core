package main

import "testing"

func TestCollectionAppend(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "collection_append", `# += appends one element; with yields the receiver without copying the record.
global mutable trace: i64 = 0

def note(digit: i64) -> i64:
    trace <- trace * 10 + digit
    return digit

def choose() -> usize:
    _ = note(9)
    return 0

struct State:
    padding: i64[256]
    depth: mutable i64
    names: mutable darray[i64]
    flags: mutable darray[bool]

def update(state: mutable State&, value: i64) -> void can[Abort.Panic, Memory.Allocate]:
    state <-
        if state.depth > 0:
            state with {
                names += value,
                flags += true,
                depth += 1,
            }
        else:
            state

def append_scalar(values: mutable darray[i64]&, value: i64) -> void can[Abort.Panic, Memory.Allocate]:
    values += value

def check_collection_append() -> i64 can[Abort.Panic, Memory.Allocate]:
    state: mutable State = State{padding: zeroed, depth: 1, names: [], flags: []}
    update(&state, 42)
    return 1 if state.names.count != 1 or state.names[0] != 42
    return 2 if state.flags.count != 1 or not state.flags[0]
    return 3 if state.depth != 2
    values: mutable darray[i64] = []
    values += 17
    return 4 if values.count != 1 or values[0] != 17
    append_scalar(&values, 18)
    return 9 if values.count != 2 or values[1] != 18
    batches: mutable darray[darray[i64]] = []
    inner: darray[i64] = [8, 9]
    batches += inner
    return 5 if batches.count != 1 or batches[0].count != 2 or batches[0][1] != 9
    state.depth <- 0
    update(&state, 99)
    return 6 if state.names.count != 1 or state.flags.count != 1
    rows: mutable State[1] = [State{padding: zeroed, depth: 0, names: [], flags: []}]
    rows[choose()] with {
        names += note(2),
        names += note(3),
        depth += 1,
    }
    return 7 if trace != 923
    return 8 if rows[0].names.count != 2 or rows[0].names[0] != 2 or rows[0].names[1] != 3 or rows[0].depth != 1
    return 0

@test
def collection_append() -> void:
    can Abort.Panic, Memory.Allocate:
        assert check_collection_append() == 0
`)
	assertAllPassed(t, exit, stdout, stderr, "collection_append")
}
