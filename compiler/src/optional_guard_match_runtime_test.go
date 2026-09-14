package main

import "testing"

// A guarded binder arm, a null arm, and a wildcard over an optional scrutinee: the
// wildcard must cover absence, and the guard must only run on a present payload.
func TestOptionalGuardMatchExpression(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "optional_guard_match", `# A guarded optional match unwraps before its guard runs; _ covers absence and
# a present value whose guard was false. The style guide's validated_condition form.
struct Handle:
    kind: i64
    identity: i64

global mutable guard_calls: i64 = 0

def kind_of(value: Handle) -> i64:
    guard_calls <- guard_calls + 1
    return value.kind

def validate(condition_value: Handle?, wanted: i64) -> Handle?:
    validated_condition: Handle? =
        match condition_value:
            emitted_condition if kind_of(emitted_condition) != wanted:
                null
            _:
                condition_value
    return validated_condition

def describe(value: Handle?) -> i64:
    return match value:
        null:
            -1
        held if held.identity > 100:
            held.identity - 100
        held:
            held.identity

def check_optional_guard_match() -> i64:
    absent: Handle? = validate(null, 1)
    return 1 if absent != null
    return 2 if guard_calls != 0
    valid: Handle? = validate(Handle{kind: 1, identity: 42}, 1)
    if valid is held:
        return 3 if held.identity != 42 or held.kind != 1
    else:
        return 4
    return 5 if guard_calls != 1
    invalid: Handle? = validate(Handle{kind: 64, identity: 99}, 1)
    return 6 if invalid != null
    return 7 if guard_calls != 2
    return 8 if describe(null) != -1
    return 9 if describe(Handle{kind: 0, identity: 142}) != 42
    return 10 if describe(Handle{kind: 0, identity: 7}) != 7
    return 0

@test
def optional_guard_match() -> void:
    can Abort.Panic:
        assert check_optional_guard_match() == 0
`)
	assertAllPassed(t, exit, stdout, stderr, "optional_guard_match")
}
