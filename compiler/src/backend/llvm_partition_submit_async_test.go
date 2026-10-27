//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This pool copies a generic work item and defers its callback until shutdown.
// fixture_drain_all executes queued work on pthreads after both submitting
// functions return. The shim-backed stores remain alive until the drain.
func partitionSubmitAsyncSource(generic bool) string {
	source := partitionSubmitRuntimeSource()
	if generic {
		source = partitionSubmitGenericRuntimeSource()
	}
	source = strings.Replace(source, "extern fixture_result() -> i64", `extern fixture_result() -> i64
extern fixture_enqueue(entry: uintptr, arg: void&, size: usize) -> void
extern fixture_drain_all() -> void

struct AsyncFixtureWork[A, R]:
    callback: fn(A) -> R
    argument: A

def async_fixture_entry[A, R](bits: void&) -> void:
    can Unsafe.PointerCast:
        work: AsyncFixtureWork[A, R]& = bits.cast[AsyncFixtureWork[A, R]&]
        _ = work.callback(work.argument)
`, 1)
	oldSubmit := `def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending] can[Pool.Submit, Memory.Allocate, Abort.Panic]:
    _ = fn(arg)
    task: Task[R, Pending] = zeroed
    return move task`
	newSubmit := `def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending] can[Pool.Submit, Memory.Allocate, Abort.Panic, Unsafe.PointerCast]:
    work: AsyncFixtureWork[A, R] = AsyncFixtureWork[A, R]{callback: fn, argument: arg}
    fixture_enqueue(async_fixture_entry[A, R].cast[uintptr], (&work).cast[void&], size_of[AsyncFixtureWork[A, R]]())
    task: Task[R, Pending] = zeroed
    return move task`
	if !strings.Contains(source, oldSubmit) || !strings.Contains(source, "    return fixture_result()") {
		panic("submit runtime fixture changed")
	}
	source = strings.Replace(source, "    return fixture_result()", "    fixture_drain_all()\n    return fixture_result()", 1)
	return strings.Replace(source, oldSubmit, newSubmit, 1)
}

const partitionSubmitAsyncShim = `
#include <pthread.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
struct queued_work { void (*entry)(void *); void *work; };
static struct queued_work async_queue[2];
static int async_count;
static void *run_async_work(void *arg) {
    struct queued_work *item = arg;
    item->entry(item->work);
    return NULL;
}
void fixture_enqueue(uintptr_t entry, void *arg, uintptr_t size) {
    if (async_count >= 2 || !entry || !arg || size == 0 || size > (1u << 20)) { fputs("invalid enqueue\n", stderr); abort(); }
    struct queued_work *item = &async_queue[async_count++];
    item->work = malloc((size_t)size);
    if (!item->work) abort();
    memcpy(item->work, arg, (size_t)size);
    item->entry = (void (*)(void *))entry;
}
void fixture_drain_all(void) {
    if (async_count != 2) { fputs("missing work\n", stderr); abort(); }
    for (int i=0; i<async_count; i++) {
        pthread_t thread;
        if (pthread_create(&thread, NULL, run_async_work, &async_queue[i])) abort();
        if (pthread_join(thread, NULL)) abort();
        free(async_queue[i].work);
    }
}

`

// Every child has a fresh compiler process; cold and warm use the same disk
// cache. The test also checks the serial object with the same asynchronous shim.
func TestPartitionedStoreCaptureDeferredWorker(t *testing.T) {
	if os.Getenv("ELISACORE_TEST_ASYNC_SUBMIT") != "1" {
		t.Skip("opt in to the experimental async submit fixture")
	}
	if os.Getenv("ELISA_TEST_ASYNC_SUBMIT_CHILD") == "1" {
		mode := os.Getenv("ELISA_TEST_ASYNC_SUBMIT_MODE")
		generic := strings.HasPrefix(mode, "generic-")
		mode = strings.TrimPrefix(mode, "generic-")
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		result := parseAndAnalyzeBackendTest(t, "async_submit_fixture.elisa", partitionSubmitAsyncSource(generic))
		dir := t.TempDir()
		object := filepath.Join(dir, "program.o")
		var err error
		if mode == "serial" {
			err = WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true})
		} else {
			session := NewIncrementalObjectSession(2, 32<<20)
			session.minWeight = 0
			session.EnableDiskCache(os.Getenv("ELISA_TEST_ASYNC_SUBMIT_CACHE"), "async-submit-fixture-v1", 64<<20)
			err = session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0})
			if err == nil && os.Getenv("ELISACORE_TEST_REQUIRE_PARTITIONED_SUBMIT") == "1" {
				stats := session.Stats()
				if stats.PartitionsEmitted+stats.PartitionsReused != 2 {
					t.Fatalf("partitioned submit not exercised: %+v", stats)
				}
				if mode == "warm" && stats.DiskPartitionsReused != 2 {
					t.Fatalf("disk replay not exercised: %+v", stats)
				}
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		shim := filepath.Join(dir, "shim.c")
		if err := os.WriteFile(shim, []byte(partitionSubmitRuntimeShim+partitionSubmitAsyncShim), 0600); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "program")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		args := []string{"-pthread", shim, object, "-o", exe}
		if generic {
			args = append([]string{"-DGENERIC_FIXTURE"}, args...)
		}
		if out, err := exec.CommandContext(ctx, "clang", args...).CombinedOutput(); err != nil {
			t.Fatalf("link asynchronous fixture: %v\n%s", err, out)
		}
		if out, err := exec.CommandContext(ctx, exe).CombinedOutput(); err != nil {
			t.Fatalf("asynchronous capture failed: %v\n%s", err, out)
		}
		return
	}
	root := t.TempDir()
	for _, mode := range []string{"cold", "warm", "serial", "generic-cold", "generic-warm", "generic-serial"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPartitionedStoreCaptureDeferredWorker$")
			cmd.Env = append(os.Environ(), "ELISA_TEST_ASYNC_SUBMIT_CHILD=1", "ELISA_TEST_ASYNC_SUBMIT_MODE="+mode, "ELISA_TEST_ASYNC_SUBMIT_CACHE="+root)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
		})
	}
}
