//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestCapabilityQuerySessionHistoryFullResultParity(t *testing.T) {
	mutable := `struct Box:
    borrow: mutable i64&

def transfer(value: Box) -> Box:
    return value

def relay(value: Box) -> Box:
    return transfer(value)
`
	readonly := strings.Replace(mutable, "mutable i64&", "i64&", 1)
	session := NewIncrementalAnalysisSession()
	for step, source := range []string{mutable, mutable, readonly, readonly, mutable} {
		before := session.Stats()
		cold := AnalyzeWithOptions(parseIncrementalSessionFile(t, source), AnalyzeOptions{EnforceGlobalPermissions: true})
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{EnforceGlobalPermissions: true})
		assertIncrementalResultParity(t, cold, warm)
		after := session.Stats()
		if after.PrimaryBodiesReused != 0 {
			t.Fatal("fixture accidentally exercised scalar body replay")
		}
		if step == 0 && after.CapabilityComputed == 0 {
			t.Fatal("fixture did not query resolved aggregate capability")
		}
		if (step == 1 || step == 3 || step == 4) && after.CapabilityProductsReused <= before.CapabilityProductsReused {
			t.Fatalf("step%d did not reuse current resolved type product", step)
		}
	}
}

func TestCapabilityQueryGlobalBorrowHistoryFullResultParity(t *testing.T) {
	mutable := `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update() -> State:
    can Global.Read:
        return state
`
	readonly := strings.ReplaceAll(mutable, "mutable view[i32]", "view[i32]")
	session := NewIncrementalAnalysisSession()
	for step, source := range []string{mutable, mutable, readonly, readonly, mutable} {
		before := session.Stats()
		cold := AnalyzeWithOptions(parseIncrementalSessionFile(t, source), AnalyzeOptions{EnforceGlobalPermissions: true})
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{EnforceGlobalPermissions: true})
		assertIncrementalResultParity(t, cold, warm)
		wantMutable := step != 2 && step != 3
		if got := strings.Contains(strings.Join(cold.Errors(), "\n"), "mutable global write"); got != wantMutable {
			t.Fatalf("step %d: missing authority distinction: %v", step, cold.Errors())
		}
		symbol, ok := cold.GlobalScope.Lookup("update")
		if !ok {
			t.Fatal("missing update")
		}
		fn, ok := symbol.Type.(*FuncType)
		if !ok {
			t.Fatalf("unexpected update type %T", symbol.Type)
		}
		wantRow := " can[Global.Read]"
		if wantMutable {
			wantRow = " can[Global.Read, Global.Write]"
		}
		if got := PermissionRefsString(fn.PermissionRefs); got != wantRow {
			t.Fatalf("step %d row %q, want %q", step, got, wantRow)
		}
		if (step == 1 || step == 3 || step == 4) && session.Stats().CapabilityProductsReused <= before.CapabilityProductsReused {
			t.Fatalf("step %d did not reuse products", step)
		}
	}
	if session.Stats().CapabilityProductsReused == 0 {
		t.Fatal("global aggregate history did not reuse capability products")
	}
}
