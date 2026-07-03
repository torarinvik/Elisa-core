package semantic_test

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestAnalyzeAcceptsAggregateStateStructTypes(t *testing.T) {
	src := `struct Holder[?]:
    value: i32

def widen(value: Holder[&]) -> Holder:
    return value

def read(value: Holder[&]) -> i32:
    return value.value
`
	result, errs := parseAndAnalyze(t, "aggregate_state_structs.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "widen", "Holder[?]")
	requireFunctionReturnTypeString(t, result, "read", "i32")
}
func TestAnalyzeAcceptsMultiAggregateStateStructTypes(t *testing.T) {
	src := `struct Holder[?, ?]:
    value: i32

def widen(value: Holder[&, !]) -> Holder:
    return value

def read(value: Holder[&, ?]) -> i32:
    return value.value
`
	result, errs := parseAndAnalyze(t, "aggregate_state_structs_multi.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "widen", "Holder[?, ?]")
	requireFunctionReturnTypeString(t, result, "read", "i32")
}
func TestAnalyzeRejectsAggregateStateArityMismatch(t *testing.T) {
	src := `struct Pair[?, ?]:
    value: i32

def bad(value: Pair[&]) -> Pair[&]:
    return value
`
	_, errs := parseAndAnalyze(t, "aggregate_state_arity_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects 2 aggregate state arguments, got 1") {
		t.Fatalf("expected aggregate state arity diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsAggregateStateOnPlainStruct(t *testing.T) {
	src := `struct Plain:
    value: i32

def bad(value: Plain[&]) -> Plain[&]:
    return value
`
	_, errs := parseAndAnalyze(t, "aggregate_state_plain_struct_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "does not declare an aggregate state parameter") {
		t.Fatalf("expected aggregate state diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsDerivedStructStatesAndIsNarrowing(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
	return player.health

def take_dead(player: Player[Dead]) -> int:
	return player.health

def make_alive() -> Player:
	return Player{health: 5}

def route(player: Player) -> int:
	if player is Player[Alive]:
		return take_alive(player)
	return take_dead(player)
`
	result, errs := parseAndAnalyze(t, "derived_struct_states_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "make_alive", "Player[Alive | Dead]")
	makeAlive := requireFuncDecl(t, result, "make_alive")
	ret, ok := makeAlive.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected make_alive body to return a struct literal, got %T", makeAlive.Body[0])
	}
	requireExprTypeString(t, result, ret.Value, "Player[Alive]")
	requireFunctionReturnTypeString(t, result, "route", "int")
}
func TestAnalyzeRejectsExplicitDerivedStateConstructorMismatch(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad() -> Player[Alive]:
	return Player[Alive]{health: 0}
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_constructor_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "does not satisfy derived state Alive") {
		t.Fatalf("expected derived-state constructor diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingStaleDerivedStateAfterFieldMutation(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
	return player.health

def bad(player: mutable Player[Alive]) -> int:
	player.health <- 0
	return take_alive(player)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_field_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Player[Alive], got Player[Dead]") {
		t.Fatalf("expected post-mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsConditionalDerivedStateMutationFallbackToExactPrestate(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
	return player.health

def bad(player: mutable Player[Alive], cond: bool) -> int:
	if cond:
		player.health <- 0
	return take_alive(player)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_conditional_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Player[Alive], got Player[Alive | Dead]") {
		t.Fatalf("expected merged post-mutation state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzePreservesDerivedStateAcrossUnrelatedFieldMutation(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
	return player.health

def ok(player: mutable Player[Alive]) -> int:
	player.score <- 1
	return take_alive(player)
`
	result, errs := parseAndAnalyze(t, "derived_struct_state_unrelated_mutation_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "int")
}
func TestAnalyzeRejectsUsingStaleDerivedStateAfterNestedFieldMutation(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

struct Team:
	player: mutable Player[Alive]

def take_alive(player: Player[Alive]) -> int:
	return player.health

def bad(team: mutable Team) -> int:
	team.player.health <- 0
	return take_alive(team.player)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_nested_field_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Player[Alive], got Player[Dead]") {
		t.Fatalf("expected nested post-mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingStaleDerivedStateAfterRefAliasMutation(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
	return player.health

def bad(player: mutable Player[Alive]) -> int:
	alias: Player[Alive]& = (&player).cast[Player[Alive]&]
	alias.health <- 0
	return take_alive(player)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_ref_alias_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Player[Alive], got Player[Dead]") {
		t.Fatalf("expected ref-alias mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingStaleDerivedStateAfterRefCallMutation(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
	return player.health

def kill(player: Player[Alive]& ) -> void:
	player.health <- 0

def bad(player: mutable Player[Alive]) -> int:
	kill((&player).cast[Player[Alive]&])
	return take_alive(player)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_ref_call_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Player[Alive], got Player[Alive | Dead]") {
		t.Fatalf("expected ref-call mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingStaleDerivedStateAfterWrapperRefCallMutation(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

struct Team:
	player: mutable Player[Alive]

def take_alive(player: Player[Alive]) -> int:
	return player.health

def kill_team(team: Team&) -> void:
	team.player.health <- 0

def bad(team: mutable Team) -> int:
	kill_team((&team).cast[Team&])
	return take_alive(team.player)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_wrapper_ref_call_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Player[Alive], got Player[Alive | Dead]") {
		t.Fatalf("expected wrapper ref-call mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsDirectParserStateTransitionToReady(t *testing.T) {
	src := `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def take_ready(job: ParseJob[Ready]) -> int:
	return job.checksum

def ok(job: mutable ParseJob[Pending]) -> int:
	job.checksum <- 7
	job.stage <- 1
	return take_ready(job)
`
	result, errs := parseAndAnalyze(t, "derived_struct_state_parser_direct_transition_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "int")
}
func TestAnalyzeRejectsMutatingThroughReadonlyRefParam(t *testing.T) {
	src := `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def bad(job: ParseJob[Pending]&) -> void:
	job.checksum <- 7
	job.stage <- 1
`
	_, errs := parseAndAnalyze(t, "readonly_ref_param_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot mutate through readonly ref") {
		t.Fatalf("expected readonly ref mutation diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "note: plain refs T& are readonly; use mutable ParseJob[Pending]& if this reference should allow writes") {
		t.Fatalf("expected readonly ref mutation hint, got:\n%s", all)
	}
}
func TestAnalyzeRejectsPassingReadonlyRefToMutableRefParamWithHint(t *testing.T) {
	src := `struct ParseJob:
	stage: mutable int

def finish_ok(job: mutable ParseJob&) -> void:
	job.stage <- 1

def bad(job: ParseJob&) -> void:
	finish_ok(job)
`
	_, errs := parseAndAnalyze(t, "readonly_ref_arg_to_mutable_param_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 1 to \"finish_ok\" expects mutable ParseJob&, got ParseJob&") {
		t.Fatalf("expected mutable-ref call diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "note: use mutable ParseJob& here if the callee should be allowed to write through it") {
		t.Fatalf("expected mutable-ref call hint, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingStaleReadyStateAfterParserRefCallMutation(t *testing.T) {
	src := `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def take_ready(job: ParseJob[Ready]) -> int:
	return job.checksum

def finish_ok(job: mutable ParseJob[Pending]&) -> void ensures job => Failed:
	job.checksum <- 7
	job.stage <- 2

def bad(job: mutable ParseJob[Pending]) -> int:
	finish_ok((&job).cast[mutable ParseJob[Pending]&])
	return take_ready(job)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_parser_ref_call_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects ParseJob[Ready], got ParseJob[Failed]") {
		t.Fatalf("expected parser ref-call mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsReprovingReadyStateAfterParserRefCallMutation(t *testing.T) {
	src := `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def take_ready(job: ParseJob[Ready]) -> int:
	return job.checksum

def finish_ok(job: mutable ParseJob[Pending]&) -> void ensures job => Ready:
	job.checksum <- 7
	job.stage <- 1

def ok(job: mutable ParseJob[Pending]) -> int:
	finish_ok((&job).cast[mutable ParseJob[Pending]&])
	if job is ParseJob[Ready]:
		return take_ready(job)
	return 0
`
	result, errs := parseAndAnalyze(t, "derived_struct_state_parser_ref_call_reprove_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "int")
}
func TestAnalyzeRejectsUsingStaleOpenStateAfterSocketCloseRefCall(t *testing.T) {
	src := `struct Socket[state Open | Closed]:
	fd: mutable int
	bytes_sent: mutable int

	derive state:
		Open when self.fd >= 0
		Closed when self.fd < 0

def take_open(sock: Socket[Open]) -> int:
	return sock.bytes_sent

def close_socket(sock: mutable Socket[Open]&) -> void ensures sock => Closed:
	sock.fd <- -1

def bad(sock: mutable Socket[Open]) -> int:
	close_socket((&sock).cast[mutable Socket[Open]&])
	return take_open(sock)
`
	_, errs := parseAndAnalyze(t, "derived_struct_state_socket_ref_call_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expects Socket[Open], got Socket[Closed]") {
		t.Fatalf("expected socket ref-call mutation derived-state diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsInitializedStateAfterBufferInitRefCall(t *testing.T) {
	src := `struct ScratchBuffer[state Uninitialized | Initialized]:
	capacity: mutable int
	used: mutable int

	derive state:
		Uninitialized when self.capacity == 0
		Initialized when self.capacity > 0

def take_initialized(buf: ScratchBuffer[Initialized]) -> int:
	return buf.capacity - buf.used

def init_buffer(buf: mutable ScratchBuffer[Uninitialized]&) -> void ensures buf => Initialized:
	buf.capacity <- 64
	buf.used <- 0

def ok(buf: mutable ScratchBuffer[Uninitialized]) -> int:
	init_buffer((&buf).cast[mutable ScratchBuffer[Uninitialized]&])
	return take_initialized(buf)
`
	result, errs := parseAndAnalyze(t, "derived_struct_state_buffer_ref_call_init_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "int")
}
func TestAnalyzeFunctionTypePermissionsParticipateInMatching(t *testing.T) {
	src := `extern puts(text: u8&) -> int can[Console.Write]

def invoke_writer(fn: fn(u8&) -> int can[Console.Write], text: u8&) -> int can[Console.Write]:
    return fn(text)

def run() -> int can[Console.Write]:
	return invoke_writer(puts, "hello".cast[u8&])
`
	result, errs := parseAndAnalyze(t, "function_type_permissions.elisa", src)
	requireNoErrors(t, errs)
	requireFunctionReturnTypeString(t, result, "invoke_writer", "int")
}
func TestAnalyzeAcceptsPermissionPolymorphicFunctionWrappers(t *testing.T) {
	src := `extern puts(text: u8&) -> int can[Console.Write]

def invoke_writer[permission P](fn: fn(u8&) -> int can[P], text: u8&) -> int can[P]:
    return fn(text)

def run() -> int can[Console.Write]:
	return invoke_writer(puts, "hello".cast[u8&])
`
	result, errs := parseAndAnalyze(t, "permission_polymorphic_function_wrapper.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "invoke_writer", "int")
}
func TestAnalyzeRejectsPermissionParamMemberAccess(t *testing.T) {
	src := `def bad[permission P](fn: fn() -> void can[P.Write]) -> void:
    fn()
`
	_, errs := parseAndAnalyze(t, "permission_param_member_access_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "permission parameter \"P\" does not support member access") {
		t.Fatalf("expected permission-parameter member-access diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFunctionValueErasureCasts(t *testing.T) {
	src := `def inc(value: i64) -> i64:
    return value + 1

def call_erased(raw: void&, value: i64) -> i64:
	trusted Unsafe.PointerCast:
		fn: fn(i64) -> i64 = raw.cast[fn(i64) -> i64]
		return fn(value)

def run() -> i64:
	trusted Unsafe.PointerCast:
		raw: void& = inc.cast[void&]
		bits: uintptr = raw.cast[uintptr]
		fn: fn(i64) -> i64 = bits.cast[fn(i64) -> i64]
		return call_erased(fn.cast[void&], 40)
`
	result, errs := parseAndAnalyze(t, "function_value_erasure_casts.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "call_erased", "i64")
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeAcceptsExplicitGenericFunctionSpecializationValues(t *testing.T) {
	src := `def id[T](value: T) -> T:
    return value

def apply_i64(fn: fn(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run() -> i64:
    fn: fn(i64) -> i64 = id[i64]
    return apply_i64(fn, 7)
`
	result, errs := parseAndAnalyze(t, "explicit_generic_function_specialization.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "apply_i64", "i64")
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeAcceptsValueGenericStructAndFunction(t *testing.T) {
	src := `struct Fixed[T, N: usize]:
    items: T[N]

def first[T, N: usize](value: Fixed[T, N]) -> T:
    return value.items[0]

def run(values: i32[4]) -> i32:
    fixed: Fixed[i32, 4] = Fixed[i32, 4]{items: values}
    return first[i32, 4](fixed)
`
	result, errs := parseAndAnalyze(t, "value_generic_struct_function.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "first", "T")
	requireFunctionReturnTypeString(t, result, "run", "i32")
}
