package main

import (
	"testing"
)

// docs/126 D1: `__drop__` destructors. Declaring one INDUCES affinity on the
// type, and the compiler inserts the call on every exit edge of the owning
// scope — fall-through, `return`, and `try` propagation — while a move
// suppresses it.
//
// Each case observes the destructor through a counter the dying value points
// at, shifted base-10 so the log records BOTH how many drops ran and in what
// order: a missing drop, a double drop and a wrong-order drop are all
// distinguishable from the outside.
const dropDestructorsBody = `
error DropErr:
    Bad

struct Tracked:
    log: mutable i64&
    mark: i64

def __drop__(self: Tracked) -> void:
    self.log[0] <- self.log[0] * 10 + self.mark

def may_fail(fail: bool) -> i64 error[DropErr]:
    if fail:
        raise DropErr.Bad
    return 5

def held_across_try(log: mutable i64&, fail: bool) -> i64 error[DropErr]:
    t: Tracked = Tracked{log: log, mark: 3}
    n: i64 = try may_fail(fail)
    return n

def consume(v: Tracked) -> void:
    _ = v.mark

def early_return(log: mutable i64&, bail: bool) -> i64:
    t: Tracked = Tracked{log: log, mark: 2}
    if bail:
        return 1
    return 0

@test
def drop_runs_at_scope_exit() -> void:
    can Abort.Panic:
        log: mutable i64 = 0
        if true:
            t: Tracked = Tracked{log: &log, mark: 1}
        if log != 1:
            panic("scope exit did not drop")

@test
def drop_runs_on_early_return() -> void:
    can Abort.Panic:
        taken: mutable i64 = 0
        _ = early_return(&taken, true)
        if taken != 2:
            panic("early return did not drop")

@test
def drop_runs_on_try_propagation() -> void:
    can Abort.Panic:
        # The error path leaves the scope with NO syntax at the drop point.
        propagated: mutable i64 = 0
        _ = try held_across_try(&propagated, true) else 99
        if propagated != 3:
            panic("try propagation did not drop")

@test
def drop_runs_once_on_the_ok_path() -> void:
    can Abort.Panic:
        settled: mutable i64 = 0
        _ = try held_across_try(&settled, false) else 99
        if settled != 3:
            panic("ok path dropped the wrong number of times")

@test
def moved_value_is_not_dropped_twice() -> void:
    can Abort.Panic:
        log: mutable i64 = 0
        if true:
            t: Tracked = Tracked{log: &log, mark: 4}
            consume(move t)
        # 4, not 44: the obligation moved with the value.
        if log != 4:
            panic("moved value dropped twice")

@test
def drops_run_in_reverse_declaration_order() -> void:
    can Abort.Panic:
        log: mutable i64 = 0
        if true:
            first: Tracked = Tracked{log: &log, mark: 1}
            second: Tracked = Tracked{log: &log, mark: 2}
            third: Tracked = Tracked{log: &log, mark: 3}
        if log != 321:
            panic("drops ran out of order")
`

func TestDropDestructorsRuntime(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "drop_destructors", dropDestructorsBody)
	assertAllPassed(t, exit, stdout, stderr,
		"drop_runs_at_scope_exit",
		"drop_runs_on_early_return",
		"drop_runs_on_try_propagation",
		"drop_runs_once_on_the_ok_path",
		"moved_value_is_not_dropped_twice",
		"drops_run_in_reverse_declaration_order")
}
