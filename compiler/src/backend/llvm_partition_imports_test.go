//go:build cgo

package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func crossPartitionImportLeaf(root string) string {
	for i := 0; ; i++ {
		name := fmt.Sprintf("import_leaf%d", i)
		if partitionOwner(name, 4) != partitionOwner(root, 4) {
			return name
		}
	}
}

func TestPartitionImportsHaveNoEmittedForeignDefinition(t *testing.T) {
	t.Setenv("ELISACORE_IR_IMPORTS", "1")
	leaf := crossPartitionImportLeaf("main")
	for _, annotation := range []string{"", "@inline(never)\n"} {
		source := fmt.Sprintf("%sdef %s(value: i64) -> i64:\n    return value + 1\n\ndef main() -> i64:\n    return %s(20)\n", annotation, leaf, leaf)
		result := parseAndAnalyzeBackendTest(t, "import-linkage.elisa", source)
		part := partitionOwner("main", 4)
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel3, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { g.partBody = func(name string) bool { return partitionOwner(name, 4) == part } })
		if err != nil {
			t.Fatal(err)
		}
		if !finishPartitionModule(g.module, part, 4, "import-test") {
			g.dispose()
			t.Fatal("declined")
		}
		ir := g.printModule()
		hasBody := strings.Contains(ir, "define available_externally i64 @"+leaf+"(")
		if hasBody != (annotation == "") {
			g.dispose()
			t.Fatalf("wrong import linkage:\n%s", ir)
		}
		if err := g.optimizeModule(OptimizationLevel3); err != nil {
			g.dispose()
			t.Fatal(err)
		}
		ir = g.printModule()
		g.dispose()
		if annotation == "" && (strings.Contains(ir, "call i64 @"+leaf+"(") || !strings.Contains(ir, "ret i64 21")) {
			t.Fatalf("import failed to fold:\n%s", ir)
		}
	}
}

func TestPartitionImportsPersistAcrossProcesses(t *testing.T) {
	if os.Getenv("ELISA_IMPORT_CHILD") == "1" {
		t.Setenv("ELISACORE_IR_IMPORTS", "1")
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "1")
		leaf := crossPartitionImportLeaf("main")
		value := 1
		wantEmit := 0
		switch os.Getenv("ELISA_IMPORT_REV") {
		case "initial":
			wantEmit = 4
		case "body":
			value = 2
			wantEmit = 2
		case "body-repeat":
			value = 2
		}
		source := fmt.Sprintf("def %s(value: i64) -> i64:\n    return value + %d\n\ndef main() -> i64:\n    return %s(20)\n", leaf, value, leaf)
		for i := 0; i < 32; i++ {
			source += fmt.Sprintf("\ndef spare%d(value: i64) -> i64:\n    return value + %d\n", i, i+3)
		}
		result := parseAndAnalyzeBackendTest(t, "import-cache.elisa", source)
		session := NewIncrementalObjectSession(4, 32<<20)
		session.minWeight = 0
		session.EnableDiskCache(os.Getenv("ELISA_IMPORT_ROOT"), "imports-test", 64<<20)
		object := filepath.Join(t.TempDir(), "module.o")
		debug := os.Getenv("ELISA_IMPORT_DEBUG") == "1"
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, DebugInfo: debug}); err != nil {
			t.Fatal(err)
		}
		stats := session.Stats()
		if stats.PartitionsEmitted != uint64(wantEmit) || stats.DiskPartitionsReused != uint64(4-wantEmit) || stats.OwnerMapBuilds != 0 || stats.SerialFallbacks != 0 {
			t.Fatalf("wrong invalidation/reuse: %+v want emitted %d", stats, wantEmit)
		}
		if got := incrementalObjectNativeStatus(t, object); got != 20+value {
			t.Fatalf("stale caller: %d want %d", got, 20+value)
		}
		return
	}
	for _, debug := range []string{"0", "1"} {
		root := t.TempDir()
		for _, revision := range []string{"initial", "body", "body-repeat", "revert", "repeat"} {
			cmd := exec.Command("timeout", "30s", os.Args[0], "-test.run=^TestPartitionImportsPersistAcrossProcesses$")
			cmd.Env = append(os.Environ(), "ELISACORE_TEST_NATIVE_WORKERS=1", "ELISA_IMPORT_CHILD=1", "ELISA_IMPORT_ROOT="+root, "ELISA_IMPORT_REV="+revision, "ELISA_IMPORT_DEBUG="+debug)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("debug=%s revision=%s: %v %s", debug, revision, err, out)
			}
		}
	}
}

func TestPartitionImportsPreserveFunctionPointerIdentity(t *testing.T) {
	t.Setenv("ELISACORE_IR_IMPORTS", "1")
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	t.Setenv("ELISACORE_PARALLEL_OPT", "1")
	t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "1")
	leaf := ""
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("address_leaf%d", i)
		if partitionOwner(name, 4) != partitionOwner("from_a", 4) && partitionOwner(name, 4) != partitionOwner("from_b", 4) {
			leaf = name
			break
		}
	}
	if leaf == "" {
		t.Fatal("no foreign leaf")
	}
	source := fmt.Sprintf(`def %s(value: i64) -> i64:
    return value + 7
export fn %s(value: i64) -> i64 = %s

def from_a() -> fn(i64) -> i64:
    return %s
export fn from_a() -> fn(i64) -> i64 = from_a

def from_b() -> fn(i64) -> i64:
    return %s
export fn from_b() -> fn(i64) -> i64 = from_b
`, leaf, leaf, leaf, leaf, leaf)
	result := parseAndAnalyzeBackendTest(t, "import-address.elisa", source)
	session := NewIncrementalObjectSession(4, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "import-address", 64<<20)
	dir := t.TempDir()
	object := filepath.Join(dir, "module.o")
	if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(dir, "client.c")
	data := fmt.Sprintf("#include <stdint.h>\ntypedef int64_t(*Fn)(int64_t);\nextern int64_t %s(int64_t);\nextern Fn from_a(void),from_b(void);\nint main(void){Fn a=from_a(),b=from_b();return a!=%s || b!=%s || a(17)!=24 || b(-31)!=-24;}\n", leaf, leaf, leaf)
	if err := os.WriteFile(client, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "client")
	cmd := exec.Command("timeout", "30s", "clang", object, client, "-o", binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("link %v %s", err, out)
	}
	if out, err := exec.Command("timeout", "30s", binary).CombinedOutput(); err != nil {
		t.Fatalf("identity %v %s", err, out)
	}
}
