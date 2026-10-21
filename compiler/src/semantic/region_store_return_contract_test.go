package semantic

import (
	"strings"
	"testing"
)

func TestStoreScanProgramReturnContractBeforeBodyTyping(t *testing.T) {
	source := `global region source
extern persistent_copy(bytes: darray[u8]&) -> sview @source
extern opaque_view(bytes: darray[u8]&) -> sview

struct StableRecord:
    name: sview @source
struct BorrowedRecord:
    name: sview

def stable_record(bytes: darray[u8]&) -> StableRecord:
    return StableRecord{name: persistent_copy(bytes)}
def unknown_record(bytes: darray[u8]&) -> BorrowedRecord:
    return BorrowedRecord{name: opaque_view(bytes)}

def stable(bytes: darray[u8]&) -> sview @source:
    return persistent_copy(bytes)

def unknown(bytes: darray[u8]&) -> sview:
    return opaque_view(bytes)
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "return_contract_scan.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		a.storeScanCache = nil
		for _, name := range []string{"stable", "stable_record"} {
			stable := storeScanFindFunc(t, result, name)
			if got := a.calleeReturnModeOf(stable, 0, map[storeScanKey]bool{}); got != storeNone {
				t.Errorf("%s cold=%v program-return contract lost: %v", name, cold, got)
			}
		}
		for _, name := range []string{"unknown", "unknown_record"} {
			unknown := storeScanFindFunc(t, result, name)
			if got := a.calleeReturnModeOf(unknown, 0, map[storeScanKey]bool{}); got == storeNone {
				t.Errorf("%s cold=%v opaque return lost retention", name, cold)
			}
		}
	}
}

func TestStoreScanProgramReturnStillChecksBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bad_program_return.elisa", `global region source
def bad(bytes: darray[u8]&) -> sview @source:
    return bytes.as_sview()
`, AnalyzeOptions{})
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, `is not known to outlive region "source"`) {
		t.Fatalf("missing declared-return lifetime rejection: %s", joined)
	}
}
