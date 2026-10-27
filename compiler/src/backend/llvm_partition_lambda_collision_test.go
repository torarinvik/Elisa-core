//go:build cgo

package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPartitionedDistinctLambdaBodies(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	names := [2]string{}
	for n := 0; names[0] == "" || names[1] == ""; n++ {
		candidate := fmt.Sprintf("worker%d", n)
		p := partitionOwner(candidate, 2)
		if names[p] == "" {
			names[p] = candidate
		}
	}
	source := fmt.Sprintf(`def %s(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)

def %s(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 2
    return f(n)

def main() -> i64:
    return %s(3) + %s(3)
`, names[0], names[1], names[0], names[1])
	result := parseAndAnalyzeBackendTest(t, "lambda_collision.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "lambda-collision", 64<<20)
	object := filepath.Join(t.TempDir(), "out.o")
	if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 {
		t.Fatalf("partitioned IR was not exercised: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, object); got != 9 {
		t.Fatalf("partitioned runtime got %d, want 9", got)
	}
	before := session.Stats()
	replayed := filepath.Join(t.TempDir(), "replayed.o")
	if err := session.WriteObject(result, replayed, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsReused-before.PartitionsReused != 2 {
		t.Fatalf("cache did not replay both partitions: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, replayed); got != 9 {
		t.Fatalf("cached runtime got %d, want 9", got)
	}
	cold := filepath.Join(t.TempDir(), "cold.o")
	if err := WriteLLVMObjectFileWithOptions(result, cold, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, cold); got != 9 {
		t.Fatalf("cold runtime got %d, want 9", got)
	}
}

func TestPartitionedGenericLambdaAcrossPartitions(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	names := [2]string{}
	for n := 0; names[0] == "" || names[1] == ""; n++ {
		candidate := fmt.Sprintf("caller%d", n)
		p := partitionOwner(candidate, 2)
		if names[p] == "" {
			names[p] = candidate
		}
	}
	source := fmt.Sprintf(`def invoke[T](value: T, n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)

def %s() -> i64:
    return invoke[i64](0, 3)

def %s() -> i64:
    return invoke[i64](0, 4)

def main() -> i64:
    return %s() + %s()
`, names[0], names[1], names[0], names[1])
	result := parseAndAnalyzeBackendTest(t, "generic_lambda.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "generic-lambda", 64<<20)
	for i := 0; i < 2; i++ {
		object := filepath.Join(t.TempDir(), "out.o")
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, object); got != 9 {
			t.Fatalf("iteration %d: got %d, want 9", i, got)
		}
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 || stats.PartitionsReused != 2 {
		t.Fatalf("partitions not emitted and reused: %+v", stats)
	}
	cold := filepath.Join(t.TempDir(), "cold.o")
	if err := WriteLLVMObjectFileWithOptions(result, cold, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, cold); got != 9 {
		t.Fatalf("cold got %d, want 9", got)
	}
}

func TestPartitionedParallelWorkerDeclinesUnsafeIdentity(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	source := parallelForConcurrencyPrelude + `
packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
    store: Expr.Store[Local] = Expr.Store(owner)
    in store:
        _ = new Expr.Int(span: 1, value: 3)
    frozen: Expr.Store[Frozen] = freeze(move store)
    pool workers(2):
        parallel for node in frozen:
            if node in frozen is Expr.Int(value: value):
                _ = value + node.span
    return 0
`
	result := parseAndAnalyzeBackendTest(t, "parallel_worker_decline.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	cacheRoot := t.TempDir()
	session.EnableDiskCache(cacheRoot, "worker-decline", 64<<20)
	func() {
		result.BeginSharedCodegen()
		defer result.EndSharedCodegen()
		shared := newSharedCodegenLookups(result)
		worklists := partitionIRDeclarationWorklists(result, shared.symbolsByNode, 2)
		var unit, target [32]byte
		var members []string
		_, err := session.buildIRPartition(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}, partitionOwner("visit", 2), 2, true, "test", unit, target, worklists[partitionOwner("visit", 2)], &members, shared)
		if !errors.Is(err, errPartitionDeclined) {
			t.Fatalf("parallel worker must decline partitioning, got %v", err)
		}
	}()
	// Exercise the complete fallback after all in-flight partition generators
	// release the shared semantic view. The fixture has no main to link and run.
	out := filepath.Join(t.TempDir(), "fallback.o")
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("missing fallback object: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(session.disk.root, "*.schedule"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("missing decline schedule: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || len(data) <= sha256.Size {
		t.Fatalf("invalid schedule: %v", err)
	}
	var saved [][]string
	if err := json.Unmarshal(data[sha256.Size:], &saved); err != nil {
		t.Fatal(err)
	}
	order := partitionedIRWorkOrder(result, 2, saved)
	found := false
	for _, name := range saved[order[0]] {
		if name == partitionScheduleDeclined {
			found = true
		}
	}
	if !found {
		t.Fatalf("declined partition was not stored and prioritized: %v", saved)
	}
	// A stale hint may only reorder work. Once the unsafe worker is removed,
	// every partition must complete and successful scheduling clears the hint.
	worker := "        parallel for node in frozen:\n            if node in frozen is Expr.Int(value: value):\n                _ = value + node.span\n"
	if !strings.Contains(source, worker) {
		t.Fatal("parallel fixture changed")
	}
	changed := strings.Replace(source, worker, "        pass\n", 1)
	updated := parseAndAnalyzeBackendTest(t, "parallel_worker_decline.elisa", changed)
	again := NewIncrementalObjectSession(2, 32<<20)
	again.minWeight = 0
	again.EnableDiskCache(cacheRoot, "worker-decline", 64<<20)
	if err := again.WriteObject(updated, filepath.Join(t.TempDir(), "after.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	stats := again.Stats()
	if stats.PartitionsEmitted+stats.PartitionsReused != 2 {
		t.Fatalf("stale hint skipped a partition: %+v", stats)
	}
	data, err = os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data[sha256.Size:], &saved); err != nil {
		t.Fatal(err)
	}
	for _, names := range saved {
		for _, name := range names {
			if name == partitionScheduleDeclined {
				t.Fatal("successful partition build retained stale decline hint")
			}
		}
	}
	// Disabled schedules and a no-disk session must still safely fall back.
	t.Setenv("ELISACORE_PARTITION_SCHEDULE", "0")
	disabled := NewIncrementalObjectSession(2, 32<<20)
	disabled.minWeight = 0
	disabled.EnableDiskCache(t.TempDir(), "disabled-schedule", 64<<20)
	if err := disabled.WriteObject(result, filepath.Join(t.TempDir(), "disabled.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if files, err := filepath.Glob(filepath.Join(disabled.disk.root, "*.schedule")); err != nil || len(files) != 0 {
		t.Fatalf("disabled scheduling published hints: %v %v", files, err)
	}
	uncached := NewIncrementalObjectSession(2, 32<<20)
	uncached.minWeight = 0
	if err := uncached.WriteObject(result, filepath.Join(t.TempDir(), "uncached.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
}

func TestPartitionedSubmitWorkerDeclinesUnsafeIdentity(t *testing.T) {
	source := parallelForConcurrencyPrelude + `
module Shapes:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

module Other:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

struct Ticket:
    node: Shapes::Expr?
    bias: i64

def worker(ticket: Ticket) -> i64:
    return ticket.bias

struct OtherTicket:
    node: Other::Expr?
    bias: i64

def other_worker(ticket: OtherTicket) -> i64:
    return ticket.bias

def main() -> i64:
    can Memory.Allocate, Pool{Create,Submit,Shutdown}, Abort.Panic:
        region storage(65536):
            pool workers(1):
                task: Task[i64, Pending] = submit[workers] worker(Ticket{node: null, bias: 42})
                other_task: Task[i64, Pending] = submit[workers] other_worker(OtherTicket{node: null, bias: 17})
                _ = move task
                _ = move other_task
                return 0
`
	result := parseAndAnalyzeBackendTest(t, "submit_worker_decline.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "worker-decline", 64<<20)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	worklists := partitionIRDeclarationWorklists(result, shared.symbolsByNode, 2)
	var unit, target [32]byte
	var members []string
	part := partitionOwner("main", 2)
	_, err := session.buildIRPartition(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}, part, 2, true, "test", unit, target, worklists[part], &members, shared)
	if !errors.Is(err, errPartitionDeclined) {
		t.Fatalf("submit worker must decline partitioning, got %v", err)
	}
}

func TestPartitionedLambdaDoesNotReuseUserLinkName(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	worker := "worker0"
	part := partitionOwner(worker, 2)
	var filename, helper string
	for i := 0; i < 1000; i++ {
		filename = fmt.Sprintf("lambda_user_collision_%d.elisa", i)
		abs, err := filepath.Abs(filename)
		if err != nil {
			t.Fatal(err)
		}
		unit := sha256.Sum256([]byte(abs))
		helper = fmt.Sprintf("elisa.partition.%x.%d.lambda_0", unit, part)
		if partitionOwner(helper, 2) == part {
			break
		}
	}
	source := fmt.Sprintf(`@link_name("%s")
def evil(n: i64) -> i64:
    return n + 9

def %s(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)

def main() -> i64:
    return %s(3) + evil(3)
`, helper, worker, worker)
	result := parseAndAnalyzeBackendTest(t, filename, source)
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatal(err)
	}
	unit := sha256.Sum256([]byte(abs))
	if !partitionHelperNamespaceConflicts(result, unit, nil) {
		t.Fatal("failed to detect reserved user link name")
	}
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "user-symbol", 64<<20)
	out := filepath.Join(t.TempDir(), "partitioned.o")
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, out); got != 16 {
		t.Fatalf("partitioned runtime got %d, want 16", got)
	}
	cold := filepath.Join(t.TempDir(), "cold.o")
	if err := WriteLLVMObjectFileWithOptions(result, cold, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, cold); got != 16 {
		t.Fatalf("cold runtime got %d, want 16", got)
	}
}

func TestPartitionLambdaHelperRemainsPrivate(t *testing.T) {
	source := `def worker(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)
`
	result := parseAndAnalyzeBackendTest(t, "private_lambda.elisa", source)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	const parts = 2
	part := partitionOwner("worker", parts)
	unit := sha256.Sum256([]byte("lambda-private-test"))
	shared := newSharedCodegenLookups(result)
	g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
		g.partBody = func(name string) bool { return partitionOwner(name, parts) == part }
		g.partSynthNS = fmt.Sprintf("elisa.partition.%x.%d.", unit, part)
		g.sharedResult = true
	}, shared)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if !finishPartitionModule(g.module, part, parts, hex.EncodeToString(unit[:]), g.partLocalHelpers) {
		t.Fatal("partition unexpectedly declined")
	}
	ir := g.printModule()
	if !strings.Contains(ir, "define private i64 @elisa.partition.") {
		t.Fatalf("generated helper is not private:\n%s", ir)
	}
}

func TestPartitionedGenericReturningLambda(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	names := [2]string{}
	for n := 0; names[0] == "" || names[1] == ""; n++ {
		candidate := fmt.Sprintf("returncaller%d", n)
		p := partitionOwner(candidate, 2)
		if names[p] == "" {
			names[p] = candidate
		}
	}
	source := fmt.Sprintf(`def make[T](value: T) -> fn(i64) -> i64:
    return fn(x: i64) => x + 1

def %s() -> i64:
    f: fn(i64) -> i64 = make[i64](0)
    return f(3)

def %s() -> i64:
    f: fn(i64) -> i64 = make[i64](0)
    return f(4)

def main() -> i64:
    return %s() + %s()
`, names[0], names[1], names[0], names[1])
	result := parseAndAnalyzeBackendTest(t, "return_lambda.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "return-lambda", 64<<20)
	for i := 0; i < 2; i++ {
		out := filepath.Join(t.TempDir(), "out.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, out); got != 9 {
			t.Fatalf("iteration %d got %d want 9", i, got)
		}
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 || stats.PartitionsReused != 2 {
		t.Fatalf("did not exercise partition and replay: %+v", stats)
	}
	cold := filepath.Join(t.TempDir(), "cold.o")
	if err := WriteLLVMObjectFileWithOptions(result, cold, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, cold); got != 9 {
		t.Fatalf("cold got %d want 9", got)
	}
}

func TestPartitionedLambdaCannotBeInterposedByForeignObject(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	filename := "foreign_lambda_interposition.elisa"
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatal(err)
	}
	unit := sha256.Sum256([]byte(abs))
	part := partitionOwner("worker", 2)
	helper := fmt.Sprintf("elisa.partition.%x.%d.lambda_0", unit, part)
	source := `def worker(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)

def main() -> i64:
    return worker(3)
`
	result := parseAndAnalyzeBackendTest(t, filename, source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "foreign-interpose", 64<<20)
	dir := t.TempDir()
	out := filepath.Join(dir, "elisa.o")
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if session.Stats().PartitionsEmitted != 2 {
		t.Fatal("partitioned IR was not exercised")
	}
	assemblerName := helper
	if runtime.GOOS == "darwin" {
		assemblerName = "_" + assemblerName
	}
	cfile := filepath.Join(dir, "foreign.c")
	csource := fmt.Sprintf("long hostile(long x) __asm__(%q);\nlong hostile(long x) { return 100; }\n", assemblerName)
	if err := os.WriteFile(cfile, []byte(csource), 0600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign.o")
	if output, err := exec.Command("clang", "-c", cfile, "-o", foreign).CombinedOutput(); err != nil {
		t.Fatalf("foreign object: %v %s", err, output)
	}
	exe := filepath.Join(dir, "exe")
	if output, err := exec.Command("clang", out, foreign, "-o", exe).CombinedOutput(); err != nil {
		t.Fatalf("link: %v %s", err, output)
	}
	err = exec.Command(exe).Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("interposed result: %v", err)
	}
}
