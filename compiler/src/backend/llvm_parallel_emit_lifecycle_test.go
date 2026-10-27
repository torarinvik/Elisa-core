//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	partitionWorkerTestModeEnv  = "ELISA_TEST_PARTITION_WORKER_MODE"
	partitionWorkerTestReadyEnv = "ELISA_TEST_PARTITION_WORKER_READY"
)

// This test is also re-executed as a subprocess by the lifecycle tests below.
func TestPartitionWorkerLifecycleChild(t *testing.T) {
	mode := os.Getenv(partitionWorkerTestModeEnv)
	if mode == "" {
		return
	}
	ready := os.Getenv(partitionWorkerTestReadyEnv)
	switch mode {
	case "blocked":
		if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
			os.Exit(31)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "fail":
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				os.Exit(23)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(24)
	default:
		os.Exit(32)
	}
}

func partitionWorkerTestCommand(ctx context.Context, mode, ready string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPartitionWorkerLifecycleChild$")
	cmd.Env = append(os.Environ(),
		partitionWorkerTestModeEnv+"="+mode,
		partitionWorkerTestReadyEnv+"="+ready,
	)
	return cmd
}

func waitForPartitionWorkerReady(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestRunPartitionWorkersCancelsBlockedSiblingOnFailure(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "blocked-worker-ready")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 2)
	failure := runPartitionWorkers(ctx, len(commands), func(workerCtx context.Context, index int) *exec.Cmd {
		mode := "blocked"
		if index == 1 {
			mode = "fail"
		}
		commands[index] = partitionWorkerTestCommand(workerCtx, mode, ready)
		return commands[index]
	})
	if ctx.Err() != nil {
		t.Fatalf("worker lifecycle exceeded its test deadline: %v", ctx.Err())
	}
	if failure == nil || failure.index != 1 || failure.startup || failure.err == nil {
		t.Fatalf("failure = %+v, want worker 1 execution failure", failure)
	}
	if !waitForPartitionWorkerReady(ready, time.Second) {
		t.Fatal("blocked sibling never reached its blocking state")
	}
	if commands[0].ProcessState == nil {
		t.Fatal("blocked sibling was not waited after failure cancellation")
	}
}

func TestRunPartitionWorkersCancelsAndReapsOnLaterStartFailure(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "blocked-worker-ready")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 2)
	readyBeforeFailure := false
	failure := runPartitionWorkers(ctx, len(commands), func(workerCtx context.Context, index int) *exec.Cmd {
		if index == 0 {
			commands[index] = partitionWorkerTestCommand(workerCtx, "blocked", ready)
			return commands[index]
		}
		readyBeforeFailure = waitForPartitionWorkerReady(ready, 3*time.Second)
		commands[index] = exec.CommandContext(workerCtx, filepath.Join(t.TempDir(), "missing-worker-executable"))
		return commands[index]
	})
	if ctx.Err() != nil {
		t.Fatalf("worker lifecycle exceeded its test deadline: %v", ctx.Err())
	}
	if failure == nil || failure.index != 1 || !failure.startup || failure.err == nil {
		t.Fatalf("failure = %+v, want worker 1 startup failure", failure)
	}
	if !readyBeforeFailure {
		t.Fatal("start failure was attempted before the earlier worker reached its blocking state")
	}
	if commands[0].ProcessState == nil {
		t.Fatal("earlier worker was not waited after start failure cancellation")
	}
}
