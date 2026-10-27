//go:build cgo

package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/semantic"
)

// This independently lowers the same worklists and finalizes the same
// partition module as the experimental emitter. It observes text with local
// SSA names retained; the production path discards those names and caches its
// exact bitcode, so the replay assertion below also checks the production path.
func submitReplayPartitionText(t *testing.T, result *semantic.Result, session *IncrementalObjectSession, part int, unit [32]byte) string {
	t.Helper()
	const parts = 2
	shared := newSharedCodegenLookups(result)
	declarations := partitionIRDeclarationWorklists(result, shared.symbolsByNode, parts)
	g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
		g.partSynthNS = fmt.Sprintf("elisa.partition.%x.%d.", unit, part)
		g.partBody = func(name string) bool { return partitionOwner(name, parts) == part }
		g.partitionGlobalDeclarationsOnly = part != 0
		g.sharedResult = true
		g.partitionModuleDecls = declarations[part]
		g.hasPartitionModuleDecls = true
		g.partitionImportDefaults = session.diskDefaultsActive()
	}, shared)
	if err != nil {
		t.Fatalf("compile partition %d: %v", part, err)
	}
	defer g.dispose()
	if g.partUnsafeHelpers {
		t.Fatalf("partition %d declined experimental submit", part)
	}
	if !finishPartitionModule(g.module, part, parts, hex.EncodeToString(unit[:]), g.partLocalHelpers) {
		t.Fatalf("partition %d declined finalization", part)
	}
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	return g.printModule()
}

// Every mode executes in a new process with the same source path, compiler
// options, partition count, and persistent object cache. Reuse is asserted
// independently from the exact textual comparisons of freshly lowered modules.
func TestExperimentalSubmitPartitionIRDiskReplay(t *testing.T) {
	if os.Getenv("ELISA_SUBMIT_IR_REPLAY_CHILD") == "1" {
		root := os.Getenv("ELISA_SUBMIT_IR_REPLAY_DIR")
		mode := os.Getenv("ELISA_SUBMIT_IR_REPLAY_MODE")
		t.Setenv("ELISACORE_EXPERIMENT_PARTITION_SUBMIT", "1")
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		filename := "submit_ir_replay.elisa"
		result := parseAndAnalyzeBackendTest(t, filename, partitionSubmitGenericRuntimeSource())
		abs, err := filepath.Abs(filename)
		if err != nil {
			t.Fatal(err)
		}
		unit := sha256.Sum256([]byte(abs))
		session := NewIncrementalObjectSession(2, 32<<20)
		session.minWeight = 0
		session.EnableDiskCache(filepath.Join(root, "cache"), "submit-ir-replay-v1", 64<<20)
		result.BeginSharedCodegen()
		var texts [2]string
		for part := range texts {
			texts[part] = submitReplayPartitionText(t, result, session, part, unit)
		}
		result.EndSharedCodegen()
		if !strings.Contains(strings.Join(texts[:], "\n"), "__submit_capture_") || !strings.Contains(strings.Join(texts[:], "\n"), "pool_submit1$generic_v1$") {
			t.Fatal("fixture did not emit capture and generic submit")
		}
		for part, text := range texts {
			path := filepath.Join(root, fmt.Sprintf("%s-%d.ll", mode, part))
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := session.WriteObject(result, filepath.Join(root, mode+".o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		stats := session.Stats()
		if mode == "cold" && (stats.PartitionsEmitted != 2 || stats.SerialFallbacks != 0) {
			t.Fatalf("cold did not partition: %+v", stats)
		}
		if mode == "warm" && (stats.DiskPartitionsReused != 2 || stats.SerialFallbacks != 0) {
			t.Fatalf("warm did not reuse both disk partitions: %+v", stats)
		}
		return
	}
	root := t.TempDir()
	for _, mode := range []string{"cold", "warm"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExperimentalSubmitPartitionIRDiskReplay$")
		cmd.Env = append(os.Environ(), "ELISA_SUBMIT_IR_REPLAY_CHILD=1", "ELISA_SUBMIT_IR_REPLAY_MODE="+mode, "ELISA_SUBMIT_IR_REPLAY_DIR="+root)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s child: %v\n%s", mode, err, out)
		}
	}
	for part := 0; part < 2; part++ {
		cold, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("cold-%d.ll", part)))
		if err != nil {
			t.Fatal(err)
		}
		warm, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("warm-%d.ll", part)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cold, warm) {
			t.Fatalf("partition %d freshly lowered IR changed on disk replay", part)
		}
	}
}
