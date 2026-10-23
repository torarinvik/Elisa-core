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

func TestPartitionedExportedGlobalAliasDiskEditAndRevert(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	t.Setenv("ELISACORE_PARALLEL_OPT", "1")
	t.Setenv("ELISACORE_TEST_NATIVE_WORKERS", "1")
	previous := partitionWorkersAvailable
	RunPartitionWorkerIfRequested()
	defer func() { partitionWorkersAvailable = previous }()
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			root := t.TempDir()
			for revision, value := range []int{17, 23, 17} {
				source := fmt.Sprintf(`global mutable counter: i64 = %d
export global counter as public_counter

def get_counter() -> i64:
    can Global.Read:
        return counter
export fn get_counter() -> i64 = get_counter
`, value)
				result := parseAndAnalyzeBackendTest(t, "global_alias.elisa", source)
				session := NewIncrementalObjectSession(4, 1<<20)
				session.minWeight = 0
				session.EnableDiskCache(root, "global-alias-fixture", 8<<20)
				dir := t.TempDir()
				object := filepath.Join(dir, "library.o")
				if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: level}); err != nil {
					t.Fatal(err)
				}
				stats := session.Stats()
				if stats.OwnerMapBuilds != 0 || stats.SerialFallbacks != 0 {
					t.Fatalf("alias fixture declined independent IR: %+v", stats)
				}
				if revision == 1 && stats.PartitionsEmitted != 1 || revision == 2 && stats.PartitionsEmitted != 0 {
					t.Fatalf("alias initializer edit/revert lost locality: %+v", stats)
				}
				client := fmt.Sprintf(`#include <stdint.h>
extern int64_t counter, public_counter;
extern int64_t get_counter(void);
int main(void) {
    volatile uintptr_t original = (uintptr_t)&counter;
    volatile uintptr_t alias = (uintptr_t)&public_counter;
    if (original != alias) return 101;
    if (public_counter != %d || get_counter() != %d) return 102;
    public_counter = %d;
    if (counter != %d || get_counter() != %d) return 103;
    return 0;
}
`, value, value, value+1, value+1, value+1)
				path := filepath.Join(dir, "client.c")
				if err := os.WriteFile(path, []byte(client), 0600); err != nil {
					t.Fatal(err)
				}
				executable := filepath.Join(dir, "client")
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if out, err := exec.CommandContext(ctx, "clang", path, object, "-o", executable).CombinedOutput(); err != nil {
					t.Fatalf("alias link: %v\n%s", err, out)
				}
				if out, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
					t.Fatalf("alias address/value mismatch: %v\n%s", err, out)
				}
			}
		})
	}
}

func TestPartitionedSourceGlobalsKeepOneInitializer(t *testing.T) {
	source := `global mutable counter: i64 = 17
const TABLE = [5, 7, 11]

def read(index: usize) -> i64:
    can Global.Read:
        return counter + TABLE[index]
`
	for _, tc := range []struct {
		name                             string
		partitioned, declarations, alias bool
	}{
		{"ordinary", false, false, false}, {"owner", true, false, false}, {"reader", true, true, false}, {"aliased-export-owner", true, false, true}, {"aliased-export-reader", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := source
			if tc.alias {
				input += "\nexport global counter as public_counter\n"
			}
			result := parseAndAnalyzeBackendTest(t, "global_owner.elisa", input)
			g, err := compileLLVMModuleConfigured(result, OptimizationLevel3, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
				if tc.partitioned {
					g.partBody = func(string) bool { return true }
				}
				g.partitionGlobalDeclarationsOnly = tc.declarations
			})
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			ir := g.printModule()
			want := "@counter = global i64 17"
			if tc.declarations {
				want = "@counter = external global i64"
			}
			if !strings.Contains(ir, want) {
				t.Fatalf("expected %s:\n%s", want, ir)
			}
			if tc.alias && strings.Contains(ir, "@public_counter = alias") != !tc.declarations {
				t.Fatalf("export alias was not confined to the data owner:\n%s", ir)
			}
			if !strings.Contains(ir, "@TABLE = internal constant [3 x i64]") {
				t.Fatalf("aggregate constants lost their local initializer:\n%s", ir)
			}
		})
	}
}

func TestPartitionedSourceGlobalDiskEditAndRevert(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	nameInPart := func(prefix string, part int) string {
		for i := 0; ; i++ {
			name := fmt.Sprintf("%s%d", prefix, i)
			if partitionOwner(name, 4) == part {
				return name
			}
		}
	}
	writer, reader := nameInPart("writer", 1), nameInPart("reader", 2)
	root := t.TempDir()
	for revision, value := range []int{17, 23, 17} {
		source := fmt.Sprintf(`global mutable counter: i64 = %d

def %s() -> void:
    can Global{Read,Write}:
        counter <- counter + 1

def %s() -> i64:
    can Global.Read:
        return counter

def main() -> i64:
    can Global{Read,Write}:
        %s()
        return %s()
`, value, writer, reader, writer, reader)
		result := parseAndAnalyzeBackendTest(t, "global_edit.elisa", source)
		// A fresh session must read disk; no previous session can supply data.
		session := NewIncrementalObjectSession(4, 1<<20)
		session.minWeight = 0
		session.EnableDiskCache(root, "global-owner-fixture", 8<<20)
		out := filepath.Join(t.TempDir(), "partitioned.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		stats := session.Stats()
		if stats.OwnerMapBuilds != 0 || stats.SerialFallbacks != 0 {
			t.Fatalf("independent partition generation declined: %+v", stats)
		}
		if revision == 1 && (stats.PartitionsEmitted != 1 || stats.DiskPartitionsReused != 3) {
			t.Fatalf("initializer edit invalidated more than the owner: %+v", stats)
		}
		if revision == 2 && (stats.PartitionsEmitted != 0 || stats.DiskPartitionsReused != 4) {
			t.Fatalf("revert failed to read exact original disk products: %+v", stats)
		}
		if got := incrementalObjectNativeStatus(t, out); got != value+1 {
			t.Fatalf("shared global status=%d want=%d", got, value+1)
		}
	}
}
