package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPerformanceFrictionDocsCoverConcurrencyRemediation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRootFromMainTest(t), "docs", "70-performance-friction.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docs/70: %v", err)
	}
	doc := string(data)
	for _, heading := range []string{
		"### How To Satisfy `-Wperf`",
		"#### Thread Spawn Loop",
		"#### Pool Or Task-Group Creation Loop",
		"#### Lock Loop",
		"#### Atomic Hot Loop",
		"#### Await Or Join Loop",
		"#### Wait-All Loop",
		"trusted Perf.HotLoop:",
	} {
		if !strings.Contains(doc, heading) {
			t.Fatalf("docs/70 should include %q", heading)
		}
	}
}
