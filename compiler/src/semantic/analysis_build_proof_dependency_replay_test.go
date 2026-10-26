//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"elisacore/src/ast"
)

// Actual same declaration/call graph as the fresh-process disk edit oracle.
const analysisBuildProofDependencySource = `
module Lib:
    struct Pair:
        a: i64
        b: i64

    def contains(xs: sview, x: u8) -> bool:
        for v in xs:
            if v == x:
                return true
        return false

    def at(b: u8, n: usize, i: usize) -> u8:
        requires i < n
        ensure result == b
        return b

    def width(p: Pair) -> i64:
        return p.a + p.b

    def collect(xs: mutable darray[Pair]&, c: bool) -> i64:
        xs <- xs.push(Pair{a: 1, b: 2}) if c
        seen: bool = any q in xs where q.b == 2
        return 1 if seen else 0

    def record(xs: mutable darray[Pair]&, c: bool) -> void:
        xs <- xs.push(Pair{a: 1, b: 2}) if c

    def tag(x: i64) -> i64 can[Abort.Panic]:
        return x

def main() -> i32:
    xs: sview = "abc"
    p: Lib::Pair = Lib::Pair{a: 1, b: 2}
    if Lib::contains(xs, 98) and Lib::at(98, 3, 1) == 98:
        return i32(Lib::width(p) + Lib::tag(1))
    return 0
`

func analysisBuildProofDependencyRows(state *RetainedAnalysis, changed string) []string {
	var rows []string
	name := func(node ast.Node) string {
		if node == nil {
			return "<nil>"
		}
		if fn, ok := node.(*ast.FuncDecl); ok {
			return fn.Name
		}
		return fmt.Sprintf("%T@%v", node, node.Pos())
	}
	for key, row := range state.a.proofLifecycle.Segments {
		if key.Phase == proofPhasePostDefault || name(key.Owner) == changed {
			continue
		}
		for origin := range row.Origins {
			if name(origin) != changed {
				continue
			}
			facts := []string{}
			for _, fact := range row.Facts {
				facts = append(facts, fmt.Sprintf("%v %s is %s", fact.Pos, fact.Subject, fact.Predicate))
			}
			rows = append(rows, fmt.Sprintf("owner=%s phase=%d site=%s instance=%q origin=%s facts=%q", name(key.Owner), key.Phase, name(key.Site), key.Instance, name(origin), facts))
		}
	}
	sort.Strings(rows)
	return rows
}

func TestAnalysisBuildProofDependentBodyReplayMatchesLegacy(t *testing.T) {
	for _, options := range []AnalyzeOptions{{}, {EnableSMT: true, EnforceStrictProofs: true, TargetDebug: true}} {
		t.Run(fmt.Sprintf("strict-%v-smt-%v", options.EnforceStrictProofs, options.EnableSMT), func(t *testing.T) {
			for _, replacement := range []string{"return p.a + p.b + 1", "return missing_body_value"} {
				t.Run(replacement, func(t *testing.T) {
					source := analysisBuildProofDependencySource
					legacyResult, legacy := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), options)
					if legacy == nil || len(legacyResult.Errors()) != 0 {
						t.Fatal("legacy fixture failed", legacyResult.Errors())
					}
					build, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), options)
					if err != nil {
						t.Fatal(err)
					}
					result, state, err := build.Advance(0, time.Minute)
					if err != nil || result != nil || state != nil {
						t.Fatalf("expected actual cold body/post boundary: %v", err)
					}
					encoded, err := build.MarshalState()
					if err != nil {
						t.Fatal(err)
					}
					build, err = RestoreAnalysisBuild(encoded)
					if err != nil {
						t.Fatal(err)
					}
					result, state, err = build.Advance(0, time.Minute)
					if err != nil || result == nil || state == nil || len(result.Errors()) != 0 {
						t.Fatalf("actual restored cold post completion failed: %v", err)
					}
					// Match the real fresh-process edit path for BOTH baselines,
					// including comparison projection and decoded identity rebinding.
					legacyBytes, err := legacy.MarshalState()
					if err != nil {
						t.Fatal(err)
					}
					legacy, err = RestoreAnalysisState(legacyBytes)
					if err != nil {
						t.Fatal(err)
					}
					resumedBytes, err := state.MarshalState()
					if err != nil {
						t.Fatal(err)
					}
					state, err = RestoreAnalysisState(resumedBytes)
					if err != nil {
						t.Fatal(err)
					}
					legacyRows := analysisBuildProofDependencyRows(legacy, "width")
					resumedRows := analysisBuildProofDependencyRows(state, "width")
					t.Logf("legacy dependent rows: %q; resumed dependent rows: %q", legacyRows, resumedRows)
					edited := strings.Replace(source, "return p.a + p.b", replacement, 1)
					want, werr := legacy.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
					if werr != nil {
						t.Fatalf("legacy replay declined: %v rows=%q", werr, legacyRows)
					}
					got, gerr := state.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
					if gerr != nil {
						t.Fatalf("resumable baseline replay declined: %v; exact prior journal dependent rows=%q (legacy=%q)", gerr, resumedRows, legacyRows)
					}
					cold := AnalyzeWithOptions(retainedParse(t, edited), options)
					for label, result := range map[string]*Result{"legacy": want, "resumed": got} {
						if !reflect.DeepEqual(result.ProofReport, cold.ProofReport) || !reflect.DeepEqual(result.Diagnostics, cold.Diagnostics) {
							t.Fatalf("%s edit proof/diagnostics differ from independently cold edited analysis", label)
						}
					}
				})
			}
		})
	}
}
