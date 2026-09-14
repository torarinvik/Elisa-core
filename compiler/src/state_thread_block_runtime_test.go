package main

import "testing"

func TestStateThreadBlock(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "state_thread_block", `# Field updates thread a large state through scoped branches without copying it.
struct LargeState:
    buffer: i64[256]
    depth: mutable i64
    generation: mutable i64

global mutable cleanups: i64 = 0

def record_cleanup() -> void:
    cleanups <- cleanups + 1

def update(state: mutable LargeState&) -> void:
    state <-
        defer block:
            record_cleanup()
        step: i64 = 1
        state.depth <- state.depth - step
        if state.depth == 0:
            state with {generation <- state.generation + 10}
        else:
            state

def check_state_thread() -> i64:
    state: mutable LargeState = zeroed
    state.depth <- 2
    update(&state)
    return 1 if state.depth != 1 or state.generation != 0
    update(&state)
    return 2 if state.depth != 0 or state.generation != 10
    state <-
        state.depth <- 3
        state with {generation <- 20}
    return 3 if state.depth != 3 or state.generation != 20
    state <-
        state: mutable LargeState = zeroed
        state.generation <- 31
        state
    return 5 if state.generation != 31 or state.depth != 0
    return 4 if cleanups != 2
    return 0

@test
def state_thread() -> void:
    can Abort.Panic:
        assert check_state_thread() == 0
`)
	assertAllPassed(t, exit, stdout, stderr, "state_thread")
}
