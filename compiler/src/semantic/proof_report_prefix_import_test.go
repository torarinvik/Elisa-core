//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

// This accepted edit already has a semantic caller closure. The report adapter
// must import the new declaration prefix without importing scratch body rows,
// and the real live caller analysis must replace the body contributions.
func TestLiveProofReportConstContractEditMatchesColdAcrossDisk(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const base = `const LIMIT: i64 = 1

def accept(value: i64) -> i64:
    requires value == LIMIT
    return value

def bridge() -> i64:
    return accept(1)

def client() -> i64:
    return bridge()
`
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), options)
	for _, value := range []string{"2", "1", "3", "1"} {
		disk, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(disk)
		if err != nil {
			t.Fatal(err)
		}
		next := strings.Replace(base, "LIMIT: i64 = 1", "LIMIT: i64 = "+value, 1)
		got, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
		if err != nil {
			t.Fatalf("value %s: %v", value, err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, next), options)
		assertExactLiveProofReport(t, cold, got)
		if state.LastStats.ConstUsers != 3 {
			t.Fatalf("caller closure not exercised: %+v", state.LastStats)
		}
	}
}

// Rebinding is an all-or-nothing operation. In particular a missing origin must
// leave the old journal intact instead of publishing the subset already bound.
func TestLiveProofReportPrefixRebindingFailureIsTransactional(t *testing.T) {
	const source = `law Positive(self: i64) = self > 0

def need_pos(value: i64 is Positive) -> i64:
    return value

def consume(value: i64 = need_pos(3)) -> i64:
    return value
`
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	pre := scratchPreBody(retainedParse(t, source), AnalyzeOptions{})
	if pre == nil || len(pre.proofPrefix) == 0 {
		t.Fatal("fixture emitted no declaration contribution")
	}
	before := make(map[proofContributionKey]proofContribution, len(state.a.proofLifecycle.Segments))
	for key, row := range state.a.proofLifecycle.Segments {
		before[key] = row
	}
	row := pre.proofPrefix[0]
	row.Origins = map[ast.Node]bool{&ast.FuncDecl{}: true}
	pre.proofPrefix = append([]proofContribution{row}, pre.proofPrefix[1:]...)
	if why := state.a.importProofPrefix(pre); why == "" {
		t.Fatal("unbound origin accepted")
	}
	if !reflect.DeepEqual(before, state.a.proofLifecycle.Segments) {
		t.Fatal("failed import mutated journal")
	}
}
