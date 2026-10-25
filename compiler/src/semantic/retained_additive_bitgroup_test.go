//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// Keep Packet plain so the defaulted-field optimization is eligible. Change
// only its declaration: frozen projected readers must observe the new u5 type.
func TestRetainedAdditiveScalarMustNotHideBitGroupChange(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := strings.Replace(retainedBitGroupFixture, "struct Packet layout(c):", "struct Packet:", 1)
	after := strings.Replace(before, "    prefix: u32", "    added: u64 = 0\n    prefix: u32", 1)
	after = strings.Replace(after, "        mode: u4", "        mode: u5", 1)
	initial, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) || len(warm.Errors()) != 0 {
		t.Fatalf("diagnostics: retained=%v cold=%v", warm.Diagnostics, cold.Diagnostics)
	}
	for _, decl := range retainedWalkDecls(warm.File) {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		walkStoreScanNodes(funcScanStmts(fn), func(node any) {
			field, ok := node.(*ast.FieldExpr)
			if !ok || field.Field != "mode" {
				return
			}
			group, ok := warm.ExprTypes[field.Object].(*BitGroupType)
			if !ok || group.Name != "Packet.bits" {
				return
			}
			if result := warm.ExprTypes[field]; !SameType(result, group.MemberMap["mode"].Type) {
				t.Fatalf("%s: retained live member result %s, current group member %s; reader was skipped by additive-field optimization", fn.Name, result, group.MemberMap["mode"].Type)
			}
		})
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
}
