//go:build cgo

package backend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This fixture deliberately uses a synchronous pool: pool_submit1 invokes the
// actual callback, rather than returning a zeroed task without executing it.
// The C shim below only supplies fake packed-store state and observable results.
// A worker that receives the wrong captured store produces the wrong value.
func partitionSubmitRuntimeSource() string {
	names := [2]string{}
	for i := 0; names[0] == "" || names[1] == ""; i++ {
		name := fmt.Sprintf("submit_caller%d", i)
		part := partitionOwner(name, 2)
		if names[part] == "" {
			names[part] = name
		}
	}
	return fmt.Sprintf(`packed enum AExpr:
    Lit(value: i64)

packed enum BExpr:
    Lit(value: i64)

def classify_a() -> i64:
    region r(4096):
        value: AExpr = new[auto] AExpr.Lit(1)
        return 0

def classify_b() -> i64:
    region r(4096):
        value: BExpr = new[auto] BExpr.Lit(1)
        return 0

struct ATicket:
    node: AExpr
    bias: i64

struct BTicket:
    node: BExpr
    bias: i64

extern fixture_a_node() -> AExpr
extern fixture_b_node() -> BExpr
extern fixture_record(value: i64) -> void
extern fixture_result() -> i64

def pool_new(workers: usize) -> ThreadPool can[Pool.Create]:
    pool: ThreadPool = zeroed
    return move pool

def pool_shutdown(pool: ThreadPool&) -> void can[Pool.Shutdown]:
    pass

def pool_submit1[A, R, permission P](pool: mutable ThreadPool&, fn: fn(A) -> R can[P], arg: A) -> Task[R, Pending] can[Pool.Submit, Memory.Allocate, Abort.Panic]:
    can P:
        _ = fn(arg)
    task: Task[R, Pending] = zeroed
    return move task

def worker_a(ticket: ATicket) -> i64:
    value: i64 = match ticket.node:
        AExpr.Lit(value): value
        _: 0
    fixture_record(value + ticket.bias)
    return value + ticket.bias

def worker_b(ticket: BTicket) -> i64:
    value: i64 = match ticket.node:
        BExpr.Lit(value): value
        _: 0
    fixture_record(value + ticket.bias)
    return value + ticket.bias

def %s() -> i64:
    can Memory.Allocate, Pool{Create,Submit,Shutdown}, Abort.Panic, Unsafe:
        owner: mutable Arena = zeroed
        store: AExpr.Store[Local] = AExpr.Store(owner)
        in store:
            pool workers(1):
                node: AExpr = fixture_a_node()
                task: Task[i64, Pending] = submit[workers] worker_a(ATicket{node: node, bias: 31})
                _ = move task
    return 0

def %s() -> i64:
    can Memory.Allocate, Pool{Create,Submit,Shutdown}, Abort.Panic, Unsafe:
        owner: mutable Arena = zeroed
        store: BExpr.Store[Local] = BExpr.Store(owner)
        in store:
            pool workers(1):
                node: BExpr = fixture_b_node()
                task: Task[i64, Pending] = submit[workers] worker_b(BTicket{node: node, bias: 30})
                _ = move task
    return 0

def main() -> i64:
    _ = %s()
    _ = %s()
    return fixture_result()
`, names[0], names[1], names[0], names[1])
}

// Both callers instantiate the same generic body, so the weak parent and its
// capture-specific submit graph are emitted independently in each partition.
func partitionSubmitGenericRuntimeSource() string {
	source := partitionSubmitRuntimeSource()
	bodyA := `pool workers(1):
                node: AExpr = fixture_a_node()
                task: Task[i64, Pending] = submit[workers] worker_a(ATicket{node: node, bias: 31})
                _ = move task`
	bodyB := `pool workers(1):
                node: BExpr = fixture_b_node()
                task: Task[i64, Pending] = submit[workers] worker_b(BTicket{node: node, bias: 30})
                _ = move task`
	if !strings.Contains(source, bodyA) || !strings.Contains(source, bodyB) {
		panic("submit fixture changed")
	}
	source = strings.Replace(source, bodyA, `node: AExpr = fixture_a_node()
            _ = submit_generic[i64](0, ATicket{node: node, bias: 31})`, 1)
	source = strings.Replace(source, "store: BExpr.Store[Local] = BExpr.Store(owner)", "store: AExpr.Store[Local] = AExpr.Store(owner)", 1)
	source = strings.Replace(source, bodyB, `node: AExpr = fixture_a_node()
            _ = submit_generic[i64](0, ATicket{node: node, bias: 30})`, 1)
	generic := `def submit_generic[T](marker: T, ticket: ATicket) -> i64:
    can Memory.Allocate, Pool{Create,Submit,Shutdown}, Abort.Panic, Unsafe:
        pool workers(1):
            task: Task[i64, Pending] = submit[workers] worker_a(ticket)
            _ = move task
    return 0

`
	return strings.Replace(source, "def worker_a(ticket: ATicket) -> i64:", generic+"def worker_a(ticket: ATicket) -> i64:", 1)
}

const partitionSubmitRuntimeShim = `#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <stdio.h>
struct fake_state { uint64_t payload; };
#ifdef GENERIC_FIXTURE
static struct fake_state stores[] = {{0}, {11}, {23}};
#else
static struct fake_state stores[] = {{11}, {23}};
#endif
#define STORE_COUNT (sizeof(stores) / sizeof(stores[0]))
static int next_store;
static int64_t seen[2];
static int seen_count;
// The classification-only witness functions are never executed.
void arena_free(void *arena) { (void)arena; }
void arena_profile_allocation_event(uint32_t a, uint64_t b, uint64_t c, uint64_t d, uint64_t e, uint64_t f, uint64_t g) { (void)a; (void)b; (void)c; (void)d; (void)e; (void)f; (void)g; fputs("unexpected arena_profile\n", stderr); abort(); }
struct allocation_result { void *ptr; uint32_t index; };
struct allocation_result ctx_packed_store_alloc_fixed_tagged_variant_sparse_result(void *a, void *b, uint32_t c) { (void)a; (void)b; (void)c; fputs("unexpected alloc_fixed\n", stderr); abort(); }
void *new_region_backend(uint64_t size, uint64_t parent) { (void)size; (void)parent; fputs("unexpected new_region\n", stderr); abort(); }
void *ctx_packed_store_state_new_variant_sparse(void *arena, uintptr_t bytes) {
    (void)arena; (void)bytes;
    if ((size_t)next_store >= STORE_COUNT) { fprintf(stderr, "too many stores: %d\n", next_store); abort(); }
    return &stores[next_store++];
}
uint32_t ctx_packed_store_read_variant_sparse_tag(void *state, uint32_t index) {
    if (!state || index != 1) { fprintf(stderr, "bad store %p index %u\n", state, index); abort(); }
    return 0;
}
uintptr_t ctx_packed_store_read_variant_sparse_word(uint32_t index, void *state, uintptr_t word) {
    (void)word;
    if (!state || index != 1) { fprintf(stderr, "bad store %p index %u\n", state, index); abort(); }
    return ((struct fake_state *)state)->payload;
}
uint32_t fixture_a_node(void) { return 1; }
uint32_t fixture_b_node(void) { return 1; }
void fixture_record(int64_t value) { if (seen_count < 2) seen[seen_count++] = value; }
int64_t fixture_result(void) {
    return (size_t)next_store == STORE_COUNT && seen_count == 2 && seen[0] == 42 && seen[1] == 53 ? 0 : 99;
}
`

// The children have separate Go/LLVM processes and share only the disk cache.
// Both the backend ABI and the default parallel semantic analysis are exercised.
func TestPartitionedStoreCapturingSubmitRuntime(t *testing.T) {
	if os.Getenv("ELISA_TEST_SUBMIT_RUNTIME_CHILD") == "1" {
		mode := os.Getenv("ELISA_TEST_SUBMIT_RUNTIME_MODE")
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		if strings.HasPrefix(mode, "experiment-") {
			mode = strings.TrimPrefix(mode, "experiment-")
			t.Setenv("ELISACORE_EXPERIMENT_PARTITION_SUBMIT", "1")
		}
		source := partitionSubmitRuntimeSource()
		if strings.HasPrefix(mode, "generic-") {
			mode = strings.TrimPrefix(mode, "generic-")
			source = partitionSubmitGenericRuntimeSource()
		}
		result := parseAndAnalyzeBackendTest(t, "submit_runtime_fixture.elisa", source)
		ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(ir, "ctx_packed_store_read_variant_sparse_word") || (!strings.Contains(os.Getenv("ELISA_TEST_SUBMIT_RUNTIME_MODE"), "generic-") && !strings.Contains(ir, "__submit_capture_")) {
			t.Fatalf("fixture missing expected code: submit=%v payload=%v\n%s", strings.Contains(ir, "__submit_capture_"), strings.Contains(ir, "ctx_packed_store_read_variant_sparse_word"), ir)
		}
		dir := t.TempDir()
		object := filepath.Join(dir, "program.o")
		if mode == "serial" {
			err = WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true})
		} else {
			session := NewIncrementalObjectSession(2, 32<<20)
			session.minWeight = 0
			session.EnableDiskCache(os.Getenv("ELISA_TEST_SUBMIT_RUNTIME_CACHE"), "submit-runtime-fixture-v1", 64<<20)
			err = session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0})
		}
		if err != nil {
			t.Fatal(err)
		}
		shim := filepath.Join(dir, "shim.c")
		if err := os.WriteFile(shim, []byte(partitionSubmitRuntimeShim), 0600); err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(dir, "program")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		args := []string{shim, object, "-o", executable}
		if strings.Contains(os.Getenv("ELISA_TEST_SUBMIT_RUNTIME_MODE"), "generic-") {
			args = append([]string{"-DGENERIC_FIXTURE"}, args...)
		}
		if output, err := exec.CommandContext(ctx, "clang", args...).CombinedOutput(); err != nil {
			t.Fatalf("link submit fixture: %v\n%s", err, output)
		}
		if output, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
			t.Fatalf("captured store values or execution differ: %v\n%s", err, output)
		}
		return
	}
	root := t.TempDir()
	for _, mode := range []string{"cold", "warm", "serial", "generic-cold", "generic-warm", "generic-serial", "experiment-cold", "experiment-warm", "experiment-generic-cold", "experiment-generic-warm"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPartitionedStoreCapturingSubmitRuntime$")
			cmd.Env = append(os.Environ(), "ELISA_TEST_SUBMIT_RUNTIME_CHILD=1", "ELISA_TEST_SUBMIT_RUNTIME_MODE="+mode, "ELISA_TEST_SUBMIT_RUNTIME_CACHE="+root)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, output)
			}
		})
	}
}
