package semantic

import (
	"reflect"
	"testing"
)

func TestStoreFlowSummaryCachesStayOutOfParallelUnitExports(t *testing.T) {
	if !holdsLazyCache(reflect.TypeOf((*storeFlowSummary)(nil))) {
		t.Fatal("store-flow summaries must remain lazy-cache values while they contain mutable edge-kind slices")
	}
	for _, path := range exportDeclMapPaths() {
		name := reflect.TypeOf(Analyzer{}).Field(path[0]).Name
		if name == "storeFlowSummaries" || name == "storeFlowInProgress" {
			t.Errorf("mutable store-flow cache %s must not be published between body units", name)
		}
	}
	if !parallelBodyWorkerLocalOnlyMap("storeFlowInProgress") {
		t.Fatal("transient in-progress summaries must not be merged back from a body worker")
	}
}
