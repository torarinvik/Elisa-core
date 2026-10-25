//go:build cgo

package backend

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// Production -O3 makes unexported unused functions private and may remove them.
// A real main root keeps this optimized comparison/runtime check observable
// without adding exports, changing linkage, or forcing partitioned optimization.
// argc=1 supplies the explicit parameter: this is NOT default invocation coverage.
func TestPlainDefaultLifecycleClosedOwnersDiskColdO3AndRuntime(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const prefix = "global values: i32[2] = [11, 22]\nglobal cursor: usize = 0\nglobal cursor2: usize = 1\n"
	const first = "def first(value: i32 = values[cursor]) -> i32:\n    return value\n"
	const firstChanged = "def first(value: i32 = values[cursor2]) -> i32:\n    return value\n"
	const entry = "def main(value: i32 = values[cursor]) -> i32:\n    return value\n"
	const entryDefaultChanged = "def main(value: i32 = values[cursor2]) -> i32:\n    return value\n"
	const entryBodyChanged = "def main(value: i32 = values[cursor2]) -> i32:\n    return 7\n"
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "plain-default-closed-o3.elisa", source)
	}
	initialSource := prefix + first + entry
	initial, state := semantic.AnalyzeRetained(parse(initialSource), parse(initialSource), []byte(initialSource), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	// Settle the real backend/catalog before the first disk transaction, too.
	_ = plainDefaultExactO3IR(t, initial, false)
	revisions := []struct {
		name, source string
		exit         int
	}{
		{"remove-shared-observer", prefix + entry, 1},
		{"re-add-observer", initialSource, 1},
		{"replace-default-with-survivor", prefix + firstChanged + entry, 1},
		{"replace-live-entry-default", prefix + firstChanged + entryDefaultChanged, 1},
		{"change-observable-entry-body", prefix + firstChanged + entryBodyChanged, 7},
		{"revert-defaults-and-body", initialSource, 1},
	}
	for _, revision := range revisions {
		t.Run(revision.name, func(t *testing.T) {
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = semantic.RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			warm, err := state.Update(parse(revision.source), parse(revision.source), []byte(revision.source))
			if err != nil {
				t.Fatal(err)
			}
			cold := semantic.AnalyzeWithOptions(parse(revision.source), semantic.AnalyzeOptions{})
			plainDefaultAssertRawReports(t, warm, cold)
			for _, debug := range []bool{false, true} {
				warmIR, coldIR := plainDefaultExactO3IR(t, warm, debug), plainDefaultExactO3IR(t, cold, debug)
				if warmIR != coldIR {
					t.Fatalf("debug=%v exact full-quality O3 IR differs without normalization\nwarm:\n%s\ncold:\n%s", debug, warmIR, coldIR)
				}
			}
			plainDefaultLinkedO3Exit(t, warm, revision.exit, "warm")
			plainDefaultLinkedO3Exit(t, cold, revision.exit, "cold")
		})
	}
}

// Actual default invocation lives outside the admitted B1 whole-program grammar:
// an Elisa caller has a call in its body. Preserve that explicit fallback and
// prove the real unchanged caller sees edited defaults in a fresh O3 program.
func TestPlainDefaultLifecycleUnchangedDefaultCallerDeclinesThenColdO3Runtime(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const prefix = "global values: i32[2] = [11, 22]\nglobal cursor: usize = 0\n"
	const first = "def first(value: i32 = values[cursor]) -> i32:\n    return value\n"
	const changed = "def first(value: i32 = 77) -> i32:\n    return value\n"
	const second = "def second(value: i32 = values[cursor]) -> i32:\n    return value\n"
	const caller = "def main() -> i32:\n    return first()\n"
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "plain-default-caller-fallback.elisa", source)
	}
	before := prefix + first + second + caller
	baseline, state := semantic.AnalyzeRetained(parse(before), parse(before), []byte(before), semantic.AnalyzeOptions{})
	if len(baseline.Errors()) != 0 {
		t.Fatal(baseline.Errors())
	}
	plainDefaultLinkedO3Exit(t, baseline, 11, "baseline-real-default")
	revisions := []struct {
		name, source string
		exit         int
	}{
		{"remove-shared-default-observer", prefix + first + caller, 11},
		{"replace-callee-default", prefix + changed + caller, 77},
		{"revert-default-and-observer", before, 11},
	}
	for _, revision := range revisions {
		t.Run(revision.name, func(t *testing.T) {
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = semantic.RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			if warm, err := state.Update(parse(revision.source), parse(revision.source), []byte(revision.source)); err == nil || warm != nil {
				t.Fatal("unowned unchanged caller/default transaction unexpectedly admitted")
			}
			// This is explicit caller fallback, not a retained success or reused product.
			cold, next := semantic.AnalyzeRetained(parse(revision.source), parse(revision.source), []byte(revision.source), semantic.AnalyzeOptions{})
			independent := semantic.AnalyzeWithOptions(parse(revision.source), semantic.AnalyzeOptions{})
			plainDefaultAssertRawReports(t, cold, independent)
			if plainDefaultExactO3IR(t, cold, false) != plainDefaultExactO3IR(t, independent, false) {
				t.Fatal("fresh caller fallback IR differs without normalization")
			}
			plainDefaultLinkedO3Exit(t, cold, revision.exit, "fallback-real-default")
			state = next
		})
	}
}

func plainDefaultAssertRawReports(t *testing.T, warm, cold *semantic.Result) {
	t.Helper()
	if len(warm.Errors()) != 0 || len(cold.Errors()) != 0 {
		t.Fatalf("analysis errors: warm=%v cold=%v", warm.Errors(), cold.Errors())
	}
	if !reflect.DeepEqual(warm.ProofReport, cold.ProofReport) || warm.ElisionSummary != cold.ElisionSummary || !reflect.DeepEqual(warm.Diagnostics, cold.Diagnostics) {
		t.Fatal("complete raw proof/elision/diagnostic outputs differ")
	}
}

func plainDefaultExactO3IR(t *testing.T, result *semantic.Result, debug bool) string {
	t.Helper()
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	ir, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(result, OptimizationLevel3, DefaultPackedLoweringProfile(), "", debug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ir, "@main(") || !strings.Contains(ir, "define ") {
		t.Fatal("optimized fixture lost its observable production entry")
	}
	return ir
}

func plainDefaultLinkedO3Exit(t *testing.T, result *semantic.Result, want int, label string) {
	t.Helper()
	dir := t.TempDir()
	object := filepath.Join(dir, label+".o")
	if err := WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, PackedProfile: DefaultPackedLoweringProfile(), Serial: true}); err != nil {
		t.Fatal(err)
	}
	// The same actual arena ABI/owner-tracking support used by the existing
	// retained bitgroup/runtime fixture. No fake ABI or noop free implementation.
	support := filepath.Join(dir, "runtime.c")
	if err := os.WriteFile(support, []byte(plainDefaultOwnerRuntimeC), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, label)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	output, err := exec.CommandContext(ctx, "clang", object, support, "-o", binary).CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("link %s: %v\n%s", label, err, output)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	output, err = exec.CommandContext(ctx, binary).CombinedOutput()
	cancel()
	got := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("execute %s: %v\n%s", label, err, output)
		}
		got = exit.ExitCode()
	}
	if got != want || len(output) != 0 {
		t.Fatalf("%s actual linked O3 exit=%d want=%d output=%q", label, got, want, output)
	}
}

const plainDefaultOwnerRuntimeC = `#include <stddef.h>
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
`
