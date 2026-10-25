//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func actualBackendConcurrencyOwnerRewrapSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source path")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../runtime/elisacore_std/elisacore_runtime_concurrency.elisa"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	var out strings.Builder
	for _, name := range []string{"ctx_thread_from_raw", "ctx_thread_to_raw", "ctx_task_from_raw", "ctx_task_to_raw"} {
		start := strings.Index(source, "def "+name+"[")
		if start < 0 {
			t.Fatal("missing actual runtime helper", name)
		}
		end := strings.Index(source[start+1:], "\ndef ")
		if end < 0 {
			t.Fatal("missing helper boundary", name)
		}
		out.WriteString(source[start : start+1+end])
		out.WriteString("\n\n")
	}
	return out.String()
}

func TestActualConcurrencyOwnerRewrapPreservesNativeRepresentation(t *testing.T) {
	source := actualBackendConcurrencyOwnerRewrapSource(t) + `def main() -> int:
    task: Task[i64, Pending] = Task[i64, Pending]{handle: 37, state: null}
    raw_task: Task[void&?, Pending] = ctx_task_to_raw(move task)
    restored_task: Task[i64, Pending] = ctx_task_from_raw[i64, Pending](move raw_task)
    move restored_task as {handle: task_handle, state: task_state}
    thread: Thread[i64, Joinable] = Thread[i64, Joinable]{handle: 91, state: null}
    raw_thread: Thread[void&?, Joinable] = ctx_thread_to_raw(move thread)
    restored_thread: Thread[i64, Joinable] = ctx_thread_from_raw[i64, Joinable](move raw_thread)
    move restored_thread as {handle: thread_handle, state: thread_state}
    if task_handle != 37 or thread_handle != 91:
        return 1
    if task_state != null or thread_state != null:
        return 2
    return 0
`
	result := parseAndAnalyzeBackendTest(t, "owner-rewrap-native.elisa", source)
	ir := retainedDisjointIR(t, result)
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable; LLVM verification completed")
	}
	directory := t.TempDir()
	input := filepath.Join(directory, "rewrap.ll")
	binary := filepath.Join(directory, "rewrap")
	if err := os.WriteFile(input, []byte(ir), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, clang, "-O0", "-x", "ir", input, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile ownership rewrap: %v\n%s", err, output)
	}
	if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("ownership rewrap changed carrier bits: %v\n%s", err, output)
	}
}
