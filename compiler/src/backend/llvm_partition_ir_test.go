//go:build cgo

package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func partitionIRModuleTextForWorklistTest(t *testing.T, result *semantic.Result, part, parts int, declarations []ast.Decl, useWorklist bool) string {
	t.Helper()
	shared := newSharedCodegenLookups(result)
	g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
		g.partBody = func(name string) bool { return partitionOwner(name, parts) == part }
		g.partitionGlobalDeclarationsOnly = part != 0
		g.sharedResult = true
		if useWorklist {
			g.partitionModuleDecls = declarations
			g.hasPartitionModuleDecls = true
		}
	}, shared)
	if err != nil {
		t.Fatalf("compile partition %d: %v", part, err)
	}
	defer g.dispose()
	if !finishPartitionModule(g.module, part, parts, "worklist-equivalence-test") {
		t.Fatalf("partition %d declined module finalization", part)
	}
	return g.printModule()
}

func countPartitionWorklistFunctions(decls []ast.Decl) int {
	count := 0
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.FuncDecl:
			count++
		case *ast.NamespaceDecl:
			count += countPartitionWorklistFunctions(n.Decls)
		case *ast.StaticIfDecl:
			count += countPartitionWorklistFunctions(n.Then) + countPartitionWorklistFunctions(n.Else)
			for _, branch := range n.Elifs {
				count += countPartitionWorklistFunctions(branch.Body)
			}
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if _, ok := member.(*ast.FuncDecl); ok {
					count++
				}
			}
		}
	}
	return count
}

func TestPartitionedIRDeclarationWorklistsPreserveModuleText(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "partition_worklists.elisa", `
struct Pair:
    left: i64
    right: i64

global mutable counter: i64 = 17
export global counter as public_counter
const TABLE = [5, 7, 11]

protocol Identity:
    def identity(self: Self) -> Self

impl Identity for Pair:
    def identity(self: Self) -> Self:
        return self

def add(left: i64, right: i64) -> i64:
    return left + right
export fn add(left: i64, right: i64) -> i64 = add

def identity_pair(value: Pair) -> Pair:
    return value

def read_counter() -> i64:
    can Global.Read:
        return counter + TABLE[1]
export fn public_read_counter() -> i64 = read_counter

module Nested:
    struct Cell:
        value: i64

    global mutable count: i64 = 3

    def read_cell(value: Cell) -> i64:
        return value.value

    impl Cell:
        def doubled(self: Self) -> i64:
            return self.value + self.value

static if true:
    def selected() -> i64:
        return 1
static else:
    def not_selected() -> i64:
        return 2
`)
	const parts = 4
	symbols := codegenSymbolsByNode(result)
	worklists := partitionIRDeclarationWorklists(result, symbols, parts)
	originalFunctionCount := countPartitionWorklistFunctions(result.ActiveFile().Decls)
	plannedFunctionCount := 0
	for _, declarations := range worklists {
		plannedFunctionCount += countPartitionWorklistFunctions(declarations)
	}
	if plannedFunctionCount >= originalFunctionCount*parts {
		t.Fatalf("worklists retained all function declarations: original=%d planned=%d", originalFunctionCount, plannedFunctionCount)
	}
	for part := 0; part < parts; part++ {
		want := partitionIRModuleTextForWorklistTest(t, result, part, parts, nil, false)
		got := partitionIRModuleTextForWorklistTest(t, result, part, parts, worklists[part], true)
		if got != want {
			t.Fatalf("partition %d module changed after declaration filtering", part)
		}
	}
}

// Partitioned IR at -O0: a private constant reached from functions owned by
// different partitions keeps one address, an edit to it is seen everywhere,
// and a signature edit reaches every caller.
func TestPartitionedIRPrivateConstantAndSignatureEdits(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	dir := t.TempDir()
	for revision, value := range []int{20, 21, 20} {
		var b strings.Builder
		fmt.Fprintf(&b, "const XS = [10, %d, 30]\n\n", value)
		for i := 0; i < 16; i++ {
			fmt.Fprintf(&b, "@inline(never)\ndef addr%d(index: usize) -> usize:\n    return (&XS[index]).cast[usize]\n\n", i)
		}
		b.WriteString("@inline(never)\ndef read(index: usize) -> i64:\n    return XS[index]\n\ndef main() -> i64:\n")
		for i := 1; i < 16; i++ {
			fmt.Fprintf(&b, "    if addr0(1) != addr%d(1):\n        return 1\n", i)
		}
		b.WriteString("    return read(1)\n")
		result := parseAndAnalyzeBackendTest(t, "partitioned_constant.elisa", b.String())
		before := session.Stats()
		out := filepath.Join(dir, "partitioned.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		if after := session.Stats(); after.SerialFallbacks != before.SerialFallbacks {
			t.Fatalf("revision%d did not use partitioned IR: %+v", revision, after)
		}
		if got := incrementalObjectNativeStatus(t, out); got != value {
			t.Fatalf("revision%d: got %d want %d", revision, got, value)
		}
	}
}

// Fresh sessions model separate CLI invocations: no semantic or object map is
// retained. Native behavior must change after an edit and match after reverting.
func TestPartitionedIRDiskCacheFreshSessions(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cache")
	for revision, value := range []int{20, 20, 21, 20} {
		session := NewIncrementalObjectSession(4, 8<<20)
		session.minWeight = 0
		session.EnableDiskCache(root, "test-compiler", 16<<20)
		var source strings.Builder
		source.WriteString("def keep[T](x: T) -> T:\n    return x\n\n")
		for i := 0; i < 80; i++ {
			fmt.Fprintf(&source, "@inline(never)\ndef f%d() -> i64:\n    return keep[i64](%d)\n\n", i, i)
		}
		fmt.Fprintf(&source, "def main() -> i64:\n    return f0() + %d\n", value)
		result := parseAndAnalyzeBackendTest(t, "disk_partitions.elisa", source.String())
		out := filepath.Join(dir, "out.o")
		if err := session.WriteObject(result, out, LLVMObjectEmitOptions{}); err != nil {
			t.Fatal(err)
		}
		paths, err := filepath.Glob(filepath.Join(session.disk.root, "*.schedule"))
		if err != nil || len(paths) != 1 {
			t.Fatalf("missing emitted-membership record: %v %v", paths, err)
		}
		data, err := os.ReadFile(paths[0])
		var members [][]string
		if err != nil || len(data) < 32 || json.Unmarshal(data[32:], &members) != nil {
			t.Fatal("invalid emitted-membership record", err)
		}
		found := false
		for _, names := range members {
			for _, name := range names {
				found = found || name == "keep"
			}
		}
		if !found {
			t.Fatal("generic bodies missing from persisted scheduling record")
		}
		stats := session.Stats()
		if stats.SerialFallbacks != 0 {
			t.Fatalf("not partitioned: %+v", stats)
		}
		if revision > 0 && stats.DiskPartitionsReused == 0 {
			t.Fatalf("fresh process did not reuse disk partitions, revision %d: %+v", revision, stats)
		}
		if got := incrementalObjectNativeStatus(t, out); got != value {
			t.Fatalf("revision %d: native status %d want %d", revision, got, value)
		}
	}
}
