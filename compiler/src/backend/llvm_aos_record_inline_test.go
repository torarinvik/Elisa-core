//go:build cgo

package backend

import "testing"

// ctx_aos_store_record (packed AoS store: recursive-enum node index -> record address) is a pure
// address computation and must not be forced out of line; see
// TestRunCLIAoSStoreRecordInlinedIntoTreeWalk for the end-to-end -O3 check. The allocation
// entry points stay never-inline.
func TestAoSStoreRecordLookupNotForcedNoinline(t *testing.T) {
	g := &llvmGenerator{}
	if g.shouldNeverInlineDefinedFunction("ctx_aos_store_record") {
		t.Fatal("ctx_aos_store_record must stay inlinable")
	}
	for _, name := range []string{"ctx_aos_store_new", "ctx_aos_store_alloc"} {
		if !g.shouldNeverInlineDefinedFunction(name) {
			t.Fatalf("%s must stay never-inline", name)
		}
	}
}
