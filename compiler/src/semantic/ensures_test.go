package semantic

import (
	"strings"
	"testing"
)

func TestSemanticEnsuresNamedStateCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_named_state.llcontext", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_ready(job: any ParseJob[Ready]&) -> void:
	pass

def finish_ok(mutable job: any ParseJob[Pending]&) -> void can[Abort] ensures job => Ready:
	job.checksum <- 7
	job.stage <- 1

def use(job: any ParseJob[Pending]&) -> void can[Abort]:
	finish_ok(job)
	expect_ready(job)
`)
	sym, ok := result.GlobalScope.Lookup("finish_ok")
	if !ok {
		t.Fatal("expected finish_ok symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected finish_ok function type, got %T", sym.Type)
	}
	if len(fnType.Poststates) != 1 {
		t.Fatalf("expected finish_ok to record one poststate, got %#v", fnType.Poststates)
	}
	if fnType.Poststates[0].Kind != FuncPoststateKindNamedState || len(fnType.Poststates[0].StateCases) != 1 || fnType.Poststates[0].StateCases[0] != "Ready" {
		t.Fatalf("expected finish_ok poststate to record Ready, got %#v", fnType.Poststates[0])
	}
}

func TestSemanticEnsuresRejectsWrongNamedStateProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_bad_named_state.llcontext", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def bad_finish(mutable job: any ParseJob[Pending]&) -> void can[Abort] ensures job => Ready:
	job.stage <- 2
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures job => Ready") {
		t.Fatalf("expected named-state ensures proof failure, got:\n%s", errText)
	}
}

func TestSemanticEnsuresRefStateCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_refstate.llcontext", `repr(c) struct HeapPairNode:
	value: i32

def expect_null(node: heap HeapPairNode!) -> void:
	pass

extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void can[Abort] ensures node => !

def use(node: heap HeapPairNode&) -> void can[Abort]:
	sfree_heap_pair_node(node)
	expect_null(node)
`)
	sym, ok := result.GlobalScope.Lookup("sfree_heap_pair_node")
	if !ok {
		t.Fatal("expected extern sfree_heap_pair_node symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected sfree_heap_pair_node function type, got %T", sym.Type)
	}
	if len(fnType.Poststates) != 1 || fnType.Poststates[0].Kind != FuncPoststateKindRefState || fnType.Poststates[0].RefState != RefStateNull {
		t.Fatalf("expected extern refstate poststate to record node => !, got %#v", fnType.Poststates)
	}
}

func TestSemanticEnsuresDefRefStateProofAndCallRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ensures_require_non_null.llcontext", `repr(c) struct HeapPairNode:
	value: i32

def expect_non_null(node: heap HeapPairNode&) -> void:
	pass

def require_non_null(node: heap HeapPairNode&?) -> void can[Abort] ensures node => &:
	assert node != null

def use(node: heap HeapPairNode&?) -> void can[Abort]:
	require_non_null(node)
	expect_non_null(node)
`)
}

func TestSemanticEnsuresPreserveCallRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ensures_preserve.llcontext", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def expect_alive(player: any Player[Alive]&) -> void:
	pass

def bump_score(mutable player: any Player[Alive]&) -> void can[Abort] ensures player => preserve:
	player.score <- player.score + 1

def use(player: any Player[Alive]&) -> void can[Abort]:
	bump_score(player)
	expect_alive(player)
`)
}

func TestSemanticEnsuresRejectsInvalidPreserveAfterRelevantMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_bad_preserve.llcontext", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad_preserve(mutable player: any Player[Alive]&) -> void can[Abort] ensures player => preserve:
	player.health <- 0
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures player => preserve") {
		t.Fatalf("expected preserve proof failure, got:\n%s", errText)
	}
}

func TestSemanticEnsuresNestedFieldCallRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ensures_nested_fields.llcontext", `repr(c) struct HeapPairNode:
	value: i32

struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

repr(c) struct Team:
	player: mutable Player[Alive]
	slot: mutable heap HeapPairNode&?

def expect_dead(player: Player[Dead]) -> void:
	pass

def expect_null(node: heap HeapPairNode!) -> void:
	pass

def kill_team(mutable team: any Team&) -> void can[Abort] ensures team.player => Dead:
	team.player.health <- 0

def clear_slot(mutable team: any Team&) -> void can[Abort] ensures team.slot => !:
	team.slot <- null

def use(team: any Team&) -> void can[Abort]:
	kill_team(team)
	expect_dead(team.player)
	clear_slot(team)
	expect_null(team.slot)
`)
}

func TestSemanticEnsuresRejectsInvalidTargets(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_invalid_targets.llcontext", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad_ref(count: int) -> void can[Abort] ensures count => !:
	pass

def bad_state(player: any Player[Alive]&) -> void can[Abort] ensures player => Closed:
	pass
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "requires \"count\" to resolve to a ref target") {
		t.Fatalf("expected invalid ref-target diagnostic, got:\n%s", errText)
	}
	if !strings.Contains(errText, "uses unknown state \"Closed\"") {
		t.Fatalf("expected invalid named-state diagnostic, got:\n%s", errText)
	}
}
