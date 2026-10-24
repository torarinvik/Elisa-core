//go:build cgo

package backend

import (
	"context"
	"elisacore/src/ast"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestO3MergedFunctionsPreserveAddressesAndABI(t *testing.T) {
	source := `struct Pair layout(c):
    left: i64
    right: i64

def first(value: i64) -> i64:
    return value + 7
export fn first(value: i64) -> i64 = first

def second(value: i64) -> i64:
    return value + 7
export fn second(value: i64) -> i64 = second

def pair_first(value: Pair) -> Pair:
    return value
export fn pair_first(value: Pair) -> Pair = pair_first

def pair_second(value: Pair) -> Pair:
    return value
export fn pair_second(value: Pair) -> Pair = pair_second

@inline(never)
def identity[T](value: T) -> T:
    return value

def id_i64(value: i64) -> i64:
    return identity[i64](value)
export fn id_i64(value: i64) -> i64 = id_i64

def id_u64(value: u64) -> u64:
    return identity[u64](value)
export fn id_u64(value: u64) -> u64 = id_u64

@callconv(c)
extern consume(bytes: view[u8]) -> usize

def view_first() -> usize:
    text: mutable darray[u8] = []
    text.push(65)
    text.push(0)
    return consume(text[0:2])
export fn view_first() -> usize = view_first

def view_second() -> usize:
    text: mutable darray[u8] = []
    text.push(65)
    text.push(0)
    return consume(text[0:2])
export fn view_second() -> usize = view_second

`
	// Keep enough externally observable identical functions for every partition
	// to contain duplicates, even when the visible probes have different owners.
	for i := 0; i < 32; i++ {
		source += fmt.Sprintf("\ndef duplicate%d(value: i64) -> i64:\n    return value + 7\nexport fn duplicate%d(value: i64) -> i64 = duplicate%d\n", i, i, i)
	}
	client := `#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
struct Allocation { void *owner, *memory; struct Allocation *next; };
static struct Allocation *allocations;
void *arena_alloc(void *owner, size_t bytes) {
 struct Allocation *entry=malloc(sizeof(*entry)); if(!entry) abort();
 entry->owner=owner; entry->memory=calloc(1,bytes); if(!entry->memory) abort();
 entry->next=allocations; allocations=entry; return entry->memory;
}
void arena_free(void *owner) {
 struct Allocation **entry=&allocations;
 while(*entry) { if((*entry)->owner==owner) { struct Allocation *old=*entry; *entry=old->next; free(old->memory); free(old); } else { entry=&(*entry)->next; } }
}
typedef struct { int64_t left, right; } Pair;
extern int64_t first(int64_t), second(int64_t);
extern int64_t id_i64(int64_t);
extern uint64_t id_u64(uint64_t);
extern Pair pair_first(Pair), pair_second(Pair);
extern size_t view_first(void), view_second(void);
size_t consume(const unsigned char* bytes, size_t count) { return count==2 && bytes[0]==65 && bytes[1]==0 ? 67 : 999; }
int main(void) {
 int64_t (*volatile a)(int64_t)=first, (*volatile b)(int64_t)=second;
 if(a==b || a(6)!=13 || b(-2)!=5) return 11;
 Pair (*volatile c)(Pair)=pair_first, (*volatile d)(Pair)=pair_second;
 if(c==d) return 12;
 Pair value={17,-31}, left=c(value), right=d(value);
 if(left.left!=17 || left.right!=-31 || right.left!=17 || right.right!=-31) return 13;
 size_t (*volatile e)(void)=view_first, (*volatile f)(void)=view_second;
 if(e==f || e()!=67 || f()!=67) return 14;
 int64_t (*volatile id_a)(int64_t)=id_i64; uint64_t (*volatile id_b)(uint64_t)=id_u64;
 if((void(*)(void))id_a==(void(*)(void))id_b || id_a(-31)!=-31 || id_b(UINT64_MAX-7)!=UINT64_MAX-7) return 15;
 return 0;
}`
	for _, mode := range []struct {
		name                         string
		ir, parallel, debug, postOpt bool
	}{{"serial", false, false, false, false}, {"split", false, true, false, false}, {"independent-ir", true, true, false, false}, {"serial-debug", false, false, true, false}, {"independent-ir-debug", true, true, true, false}, {"post-opt-split", false, true, false, true}, {"post-opt-split-debug", false, true, true, true}} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("ELISACORE_PARALLEL_IR", fmt.Sprint(boolInt(mode.ir)))
			t.Setenv("ELISACORE_PARALLEL_OPT", fmt.Sprint(boolInt(mode.parallel && !mode.postOpt)))
			dir := t.TempDir()
			object := filepath.Join(dir, "module.o")
			result := parseAndAnalyzeBackendTest(t, "merged-addresses.elisa", source)
			options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, DebugInfo: mode.debug, Serial: !mode.parallel}
			session := NewIncrementalObjectSession(4, 32<<20)
			session.minWeight = 0
			if nativeFragmentsEnabled() && mode.ir {
				session.EnableDiskCache(t.TempDir(), "fragment-abi-regression", 64<<20)
			}
			if err := session.WriteObject(result, object, options); err != nil {
				t.Fatal(err)
			}
			if nativeFragmentsEnabled() && mode.ir && session.Stats().NativeFragmentsEmitted == 0 {
				t.Fatalf("fragment ABI path did not emit fragments: %+v", session.Stats())
			}
			if mode.parallel && !partitionWorkersAvailable {
				t.Fatal("test did not install native worker entry point")
			}
			if mode.postOpt && session.Stats().SerialFallbacks != 0 {
				t.Fatal("post-O3 alias partitioning declined")
			}
			if mode.ir && session.Stats().OwnerMapBuilds != 0 {
				t.Fatal("independent IR path declined")
			}
			path := filepath.Join(dir, "client.c")
			if err := os.WriteFile(path, []byte(client), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			executable := filepath.Join(dir, "client")
			if out, err := exec.CommandContext(ctx, "clang", path, object, "-o", executable).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			if out, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
				t.Fatalf("address/ABI/runtime mismatch: %v\n%s", err, out)
			}
		})
	}
}

func TestOptimizationPipelineKeepsLevelsAndMergesOnlyO3(t *testing.T) {
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel1, OptimizationLevel2, OptimizationLevel3} {
		got := optimizationPipeline(level)
		if !strings.HasPrefix(got, fmt.Sprintf("default<O%d>", int(level))) || strings.Contains(got, "mergefunc") != (level == OptimizationLevel3) {
			t.Fatalf("unexpected pipeline for %d: %s", level, got)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestO3MergePreservesEachPerformanceWarningOnCacheReplay(t *testing.T) {
	source := `@inline(never)
def leaf(value: i64) -> i64:
    return value * value

@inline(never)
def first_loop(count: i64) -> i64:
    total: mutable i64 = 0
    for index in 0..<count:
        total <- total + leaf(index)
    return total
export fn first_loop(count: i64) -> i64 = first_loop

@inline(never)
def second_loop(count: i64) -> i64:
    total: mutable i64 = 0
    for index in 0..<count:
        total <- total + leaf(index)
    return total
export fn second_loop(count: i64) -> i64 = second_loop
`
	result := parseAndAnalyzeBackendTest(t, "merged-loop-warnings.elisa", source)
	for _, decl := range result.ActiveFile().Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && (fn.Name == "first_loop" || fn.Name == "second_loop") {
			loop := fn.Body[1].(*ast.ForStmt)
			loop.AutovecExpected = true
			loop.AutovecReason = "loop"
		}
	}
	session := NewIncrementalObjectSession(2, 8<<20)
	session.EnableDiskCache(t.TempDir(), "merge-warning-test", 16<<20)
	for index := 0; index < 2; index++ {
		result.EnforcePerfLints = index == 1
		g, err := compileLLVMModule(result, OptimizationLevel3, DefaultPackedLoweringProfile())
		if err != nil {
			t.Fatal(err)
		}
		optimized, err := session.optimizeObjectModule(g, OptimizationLevel3)
		if err != nil {
			g.dispose()
			t.Fatal(err)
		}
		warnings := append([]string(nil), optimized.PerfWarnings()...)
		if optimized != g {
			optimized.dispose()
		}
		g.dispose()
		if len(warnings) != 2 {
			t.Fatalf("each source loop must retain its warning; got %v", warnings)
		}
		severity := "warning [-Wperf]"
		if index == 1 {
			severity = "error [-Wperf]"
		}
		for _, warning := range warnings {
			if !strings.Contains(warning, severity) {
				t.Fatalf("wrong warning policy: %s", warning)
			}
		}
	}
	if session.Stats().OptimizedModulesReused != 1 {
		t.Fatal("did not exercise exact optimized-module cache replay")
	}
}
