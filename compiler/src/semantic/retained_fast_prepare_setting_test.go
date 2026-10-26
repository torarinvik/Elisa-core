package semantic

import (
	"os"
	"testing"
)

func TestRetainedFastPrepareDefaultPreservesExplicitSelections(t *testing.T) {
	before, hadBefore := os.LookupEnv("ELISACORE_RETAINED_FAST_PREPARE")
	t.Cleanup(func() {
		if hadBefore {
			_ = os.Setenv("ELISACORE_RETAINED_FAST_PREPARE", before)
		} else {
			_ = os.Unsetenv("ELISACORE_RETAINED_FAST_PREPARE")
		}
	})
	if err := os.Unsetenv("ELISACORE_RETAINED_FAST_PREPARE"); err != nil {
		t.Fatal(err)
	}
	if RetainedFastPrepareSetting() != "on" {
		t.Fatal("ordinary unset invocation did not select verified fast preparation")
	}
	for _, explicit := range []string{"", "off", "shadow", "on", "unknown-mode"} {
		if err := os.Setenv("ELISACORE_RETAINED_FAST_PREPARE", explicit); err != nil {
			t.Fatal(err)
		}
		if got := RetainedFastPrepareSetting(); got != explicit {
			t.Fatalf("explicit %q changed to %q", explicit, got)
		}
	}
}
