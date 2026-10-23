package semantic

import (
	"bytes"
	"os"
	"testing"
	"time"
)

// Qualify the complete graph of an existing real-source checkpoint without
// rerunning its expensive body analysis. Never publishes into a CLI cache.
// The caller must apply an external 30-second timeout to this opt-in test.
func TestAnalysisBuildCompilerCheckpoint(t *testing.T) {
	path := os.Getenv("ELISACORE_STATE_QUALIFY_CHECKPOINT")
	if path == "" {
		t.Skip("set ELISACORE_STATE_QUALIFY_CHECKPOINT")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	envelope := bytes.SplitN(data, []byte{'\n'}, 3)
	if len(envelope) != 3 || string(envelope[0]) != "ELISACSTATE1" || len(envelope[1]) != 64 {
		t.Fatal("invalid checkpoint envelope")
	}
	start := time.Now()
	build, err := RestoreAnalysisBuild(envelope[2])
	if err != nil {
		t.Fatal(err)
	}
	done, total := build.Progress()
	t.Logf("restore %d/%d: %v", done, total, time.Since(start))
	if os.Getenv("ELISACORE_STATE_QUALIFY_FINISH") == "1" {
		start = time.Now()
		result, state, err := build.Advance(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || state == nil {
			t.Fatal("checkpoint was not at completion boundary")
		}
		t.Logf("finish checks: %v; errors=%d notices=%d", time.Since(start), len(result.Errors()), len(result.Notices()))
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		start = time.Now()
		encoded, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("encode finished state: %v bytes=%d", time.Since(start), len(encoded))
	} else {
		start = time.Now()
		encoded, err := build.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("encode checkpoint: %v bytes=%d", time.Since(start), len(encoded))
		start = time.Now()
		again, err := RestoreAnalysisBuild(encoded)
		if err != nil {
			t.Fatal(err)
		}
		now, all := again.Progress()
		if now != done || all != total {
			t.Fatal("checkpoint progress changed")
		}
		t.Logf("restore encoded checkpoint: %v", time.Since(start))
	}
}
