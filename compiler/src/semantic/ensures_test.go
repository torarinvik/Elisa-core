package semantic

import (
	"strings"
	"testing"
)

func TestSemanticEnsuresNamedStateCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_named_state.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_ready(job: ParseJob[Ready]&) -> void:
	pass

def finish_ok(mutable job: ParseJob[Pending]&) -> void can[Abort] ensures job => Ready:
	job.checksum <- 7
	job.stage <- 1

def use(job: ParseJob[Pending]&) -> void can[Abort]:
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
	analysis, ok := result.FunctionAnalysisByName("finish_ok")
	if !ok || analysis == nil {
		t.Fatal("expected finish_ok function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformEnsure, FactTypestate, "job", "ensures typestate Ready") {
		t.Fatalf("expected function analysis to expose named-state ensure transform, got %#v", analysis.FactTransforms)
	}
}

func TestSemanticEnsuresRejectsWrongNamedStateProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_bad_named_state.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def bad_finish(mutable job: ParseJob[Pending]&) -> void can[Abort] ensures job => Ready:
	job.stage <- 2
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures job => Ready") {
		t.Fatalf("expected named-state ensures proof failure, got:\n%s", errText)
	}
	if !strings.Contains(errText, "current tracked facts are ParseJob[Failed]") {
		t.Fatalf("expected named-state ensures fact diagnostic, got:\n%s", errText)
	}
}

func TestSemanticEnsuresRefStateCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_refstate.elisa", `struct HeapPairNode:
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
	result := analyzeFunctionAnalysisTestSource(t, "ensures_require_non_null.elisa", `struct HeapPairNode:
	value: i32

def expect_non_null(node: heap HeapPairNode&) -> void:
	pass

def require_non_null(node: heap HeapPairNode&?) -> void can[Abort] ensures node => &:
	assert node != null

def use(node: heap HeapPairNode&?) -> void can[Abort]:
	require_non_null(node)
	expect_non_null(node)
`)
	analysis, ok := result.FunctionAnalysisByName("require_non_null")
	if !ok || analysis == nil {
		t.Fatal("expected require_non_null function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformEnsure, FactRefState, "node", "ensures refstate &") {
		t.Fatalf("expected function analysis to expose refstate ensure transform, got %#v", analysis.FactTransforms)
	}
}

func TestSemanticEnsuresPreserveCallRecovery(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ensures_preserve.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def expect_alive(player: Player[Alive]&) -> void:
	pass

def bump_score(mutable player: Player[Alive]&) -> void can[Abort] ensures player => preserve:
	player.score <- player.score + 1

def use(player: Player[Alive]&) -> void can[Abort]:
	bump_score(player)
	expect_alive(player)
`)
	analysis, ok := result.FunctionAnalysisByName("bump_score")
	if !ok || analysis == nil {
		t.Fatal("expected bump_score function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformEnsure, FactTypestate, "player", "ensures preserve") {
		t.Fatalf("expected function analysis to expose preserve ensure transform, got %#v", analysis.FactTransforms)
	}
}

func TestSemanticEnsuresRejectsInvalidPreserveAfterRelevantMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_bad_preserve.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad_preserve(mutable player: Player[Alive]&) -> void can[Abort] ensures player => preserve:
	player.health <- 0
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures player => preserve") {
		t.Fatalf("expected preserve proof failure, got:\n%s", errText)
	}
	if !strings.Contains(errText, "current tracked facts are Player[Dead]&") {
		t.Fatalf("expected preserve tracked-facts diagnostic, got:\n%s", errText)
	}
}

func TestSemanticEnsuresNestedFieldCallRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ensures_nested_fields.elisa", `struct HeapPairNode:
	value: i32

struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

struct Team:
	player: mutable Player[Alive]
	slot: mutable heap HeapPairNode&?

def expect_dead(player: Player[Dead]) -> void:
	pass

def expect_null(node: heap HeapPairNode!) -> void:
	pass

def kill_team(mutable team: Team&) -> void can[Abort] ensures team.player => Dead:
	team.player.health <- 0

def clear_slot(mutable team: Team&) -> void can[Abort] ensures team.slot => !:
	team.slot <- null

def use(team: Team&) -> void can[Abort]:
	kill_team(team)
	expect_dead(team.player)
	clear_slot(team)
	expect_null(team.slot)
`)
}

func TestSemanticEnsuresRejectsInvalidTargets(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ensures_invalid_targets.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad_ref(count: int) -> void can[Abort] ensures count => !:
	pass

def bad_state(player: Player[Alive]&) -> void can[Abort] ensures player => Closed:
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

func TestSemanticConditionalEnsuresBranchRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "conditional_ensures_branch.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int
	checksum: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_ready(job: ParseJob[Ready]&) -> void:
	pass

def expect_failed(job: ParseJob[Failed]&) -> void:
	pass

def finish(mutable job: ParseJob[Pending]&, ok: bool) -> bool can[Abort] ensures return true => job => Ready, return false => job => Failed:
	if ok:
		job.stage <- 1
		job.checksum <- 7
		return true
	job.stage <- 2
	return false

def use(mutable job: ParseJob[Pending]&, ok: bool) -> void can[Abort]:
	if finish(job, ok):
		expect_ready(job)
	else:
		expect_failed(job)
`)
}

func TestSemanticConditionalEnsuresJoinOnPlainCall(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "conditional_ensures_join.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def expect_done(job: ParseJob[Ready | Failed]&) -> void:
	pass

def finish(mutable job: ParseJob[Pending]&, ok: bool) -> bool can[Abort] ensures return true => job => Ready, return false => job => Failed:
	if ok:
		job.stage <- 1
		return true
	job.stage <- 2
	return false

def use(mutable job: ParseJob[Pending]&, ok: bool) -> void can[Abort]:
	finish(job, ok)
	expect_done(job)
`)
}

func TestSemanticConditionalEnsuresPreserveBranchRecovery(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "conditional_ensures_preserve.elisa", `struct Player[state Alive | Dead]:
	health: mutable int
	score: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def expect_alive(player: Player[Alive]&) -> void:
	pass

def expect_dead(player: Player[Dead]&) -> void:
	pass

def maybe_update(mutable player: Player[Alive]&, ok: bool) -> bool can[Abort] ensures return true => player => preserve, return false => player => Dead:
	if ok:
		player.score <- player.score + 1
		return true
	player.health <- 0
	return false

def use(mutable player: Player[Alive]&, ok: bool) -> void can[Abort]:
	if maybe_update(player, ok):
		expect_alive(player)
	else:
		expect_dead(player)
`)
}

func TestSemanticConditionalEnsuresRejectWrongReturnBranchProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "conditional_ensures_wrong_branch.elisa", `struct ParseJob[state Pending | Ready | Failed]:
	stage: mutable int

	derive state:
		Pending when self.stage == 0
		Ready when self.stage == 1
		Failed when self.stage == 2

def bad_finish(mutable job: ParseJob[Pending]&, ok: bool) -> bool can[Abort] ensures return true => job => Ready, return false => job => Failed:
	if ok:
		job.stage <- 1
		return false
	job.stage <- 2
	return true
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove ensures job => Failed") {
		t.Fatalf("expected false-branch conditional ensures proof failure, got:\n%s", errText)
	}
	if !strings.Contains(errText, "cannot prove ensures job => Ready") {
		t.Fatalf("expected true-branch conditional ensures proof failure, got:\n%s", errText)
	}
}

func TestSemanticConditionalEnsuresRejectNonBoolReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "conditional_ensures_non_bool.elisa", `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def bad(player: Player[Alive]&) -> void can[Abort] ensures return true => player => preserve:
	pass
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "requires a bool return type") {
		t.Fatalf("expected non-bool conditional ensures diagnostic, got:\n%s", errText)
	}
}
