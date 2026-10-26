//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const partitionO3QualityParts = 4

func partitionO3QualityBoundarySource() (source, leaf string) {
	leaf = crossPartitionImportLeaf("main")
	expression := "value"
	for n := 1; n <= 32; n++ {
		expression += fmt.Sprintf(" + %d", n)
	}
	source = fmt.Sprintf(`def %s(value: i64) -> i64:
    return %s
export fn %s(value: i64) -> i64 = %s

def main() -> i32:
    if %s(9) == 537:
        return 0
    return 1
`, leaf, expression, leaf, leaf, leaf)
	return source, leaf
}

func partitionO3QualityBoundaryResult(t *testing.T, source, leaf string) *semantic.Result {
	t.Helper()
	result := parseAndAnalyzeBackendTest(t, "o3-import-weight-boundary.elisa", source)
	var leafDecl *ast.FuncDecl
	for _, decl := range result.ActiveFile().Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == leaf {
			leafDecl = fn
			break
		}
	}
	if leafDecl == nil {
		t.Fatalf("could not find AST declaration for %s", leaf)
	}
	if weight := partitionImportWeight(leafDecl); weight <= 64 {
		t.Fatalf("counterexample leaf AST weight=%d, want above import cap 64", weight)
	}
	if partitionOwner("main", partitionO3QualityParts) == partitionOwner(leaf, partitionO3QualityParts) {
		t.Fatalf("counterexample caller %q and leaf %q share a partition", "main", leaf)
	}
	t.Logf("leaf import weight=%d; main owner=%d, leaf owner=%d", partitionImportWeight(leafDecl), partitionOwner("main", partitionO3QualityParts), partitionOwner(leaf, partitionO3QualityParts))
	return result
}

func TestExplicitPartitionO3ImportBoundaryRetainsCrossOwnerCall(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	t.Setenv("ELISACORE_PARALLEL_OPT", "1")
	t.Setenv("ELISACORE_IR_IMPORTS", "1")
	source, leaf := partitionO3QualityBoundarySource()
	result := partitionO3QualityBoundaryResult(t, source, leaf)
	callerOwner := partitionOwner("main", partitionO3QualityParts)
	partitionIR := partitionO3QualityIR(t, result, OptimizationLevel3, "main", partitionO3QualityParts, func(g *llvmGenerator) {
		g.partBody = func(name string) bool { return partitionOwner(name, partitionO3QualityParts) == callerOwner }
	})
	mainBody, ok := partitionQualityFunctionBody(partitionIR, "main")
	if !ok {
		t.Fatalf("explicit partitioned O3 IR has no main definition:\n%s", partitionIR)
	}
	if strings.Contains(partitionIR, "define available_externally i64 @"+leaf+"(") {
		t.Fatalf("over-cap leaf was unexpectedly imported into the caller module:\n%s", partitionIR)
	}
	if !strings.Contains(mainBody, "call") || !strings.Contains(mainBody, "@"+leaf+"(") || strings.Contains(mainBody, "ret i32 0") {
		t.Fatalf("explicit partitioned O3 did not retain the cross-owner call:\n%s", mainBody)
	}
}

func TestDiskO3DefaultsPreserveWholeModuleOptimizationAndPostO3Cache(t *testing.T) {
	unsetDiskPresetFlags(t)
	t.Setenv("ELISACORE_TEST_NATIVE_WORKERS", "1")
	t.Setenv("ELISACORE_OBJECT_PARTITIONS", "4")
	previousWorkers := partitionWorkersAvailable
	RunPartitionWorkerIfRequested()
	t.Cleanup(func() { partitionWorkersAvailable = previousWorkers })

	options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}
	source, leaf := partitionO3QualityBoundarySource()
	serialResult := partitionO3QualityBoundaryResult(t, source, leaf)
	serialIR := partitionO3QualityIR(t, serialResult, OptimizationLevel3, "main", partitionO3QualityParts, nil)
	serialMain, ok := partitionQualityFunctionBody(serialIR, "main")
	if !ok || !strings.Contains(serialMain, "ret i32 0") || strings.Contains(serialMain, "@"+leaf+"(") {
		t.Fatalf("serial whole-module O3 did not fold main():\n%s", serialMain)
	}
	t.Logf("serial whole-module O3 main body:\n%s", serialMain)

	diskResult := partitionO3QualityBoundaryResult(t, source, leaf)
	diskIR := diskFullModuleO3QualityIR(t, diskResult, options.OptLevel)
	diskMain, ok := partitionQualityFunctionBody(diskIR, "main")
	if !ok || !strings.Contains(diskMain, "ret i32 0") || strings.Contains(diskMain, "@"+leaf+"(") {
		t.Fatalf("disk-profile whole-module O3 did not fold main():\n%s", diskMain)
	}
	t.Logf("disk-profile shared whole-module O3 main body:\n%s", diskMain)

	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	profile := NewIncrementalObjectSession(partitionO3QualityParts, 32<<20)
	profile.minWeight = 0
	profile.UseDiskIncrementalDefaults()
	profile.EnableDiskCache(cache, "o3-quality-boundary", 64<<20)
	if profile.partitionedIRApplies(diskResult, options) || profile.diskCodegenFlag("ELISACORE_PARALLEL_IR") || profile.diskCodegenFlag("ELISACORE_PARALLEL_OPT") {
		t.Fatal("disk-profile O3 selected a pre-optimization partition path")
	}

	serialOptions := options
	serialOptions.Serial = true
	serialObject := filepath.Join(dir, "serial.o")
	if err := WriteLLVMObjectFileWithOptions(serialResult, serialObject, serialOptions); err != nil {
		t.Fatalf("write serial O3 object: %v", err)
	}
	profileObject := filepath.Join(dir, "profile.o")
	if err := profile.WriteObject(diskResult, profileObject, options); err != nil {
		t.Fatalf("write disk-profile O3 object: %v", err)
	}
	if stats := profile.Stats(); stats.PartitionsEmitted == 0 || stats.DiskPartitionsReused != 0 || stats.NativeFragmentsEmitted != 0 || stats.SerialFallbacks != 0 {
		t.Fatalf("disk profile did not emit post-O3 native partitions: %+v", stats)
	}
	if serialStatus, diskStatus := incrementalObjectNativeStatus(t, serialObject), incrementalObjectNativeStatus(t, profileObject); serialStatus != 0 || diskStatus != serialStatus {
		t.Fatalf("serial executable status=%d, disk-profile status=%d; both should preserve the folded result", serialStatus, diskStatus)
	}

	repeat := NewIncrementalObjectSession(partitionO3QualityParts, 32<<20)
	repeat.minWeight = 0
	repeat.UseDiskIncrementalDefaults()
	repeat.EnableDiskCache(cache, "o3-quality-boundary", 64<<20)
	repeatObject := filepath.Join(dir, "repeat.o")
	if err := repeat.WriteObject(partitionO3QualityBoundaryResult(t, source, leaf), repeatObject, options); err != nil {
		t.Fatalf("write repeated disk-profile O3 object: %v", err)
	}
	if stats := repeat.Stats(); stats.OptimizedModulesReused != 1 || stats.DiskPartitionsReused == 0 || stats.PartitionsEmitted != 0 {
		t.Fatalf("fresh session did not reuse the post-O3 disk cache: %+v", stats)
	}
	t.Logf("fresh-session cache stats: %+v", repeat.Stats())
	if got := incrementalObjectNativeStatus(t, repeatObject); got != 0 {
		t.Fatalf("reused disk-profile executable status=%d, want 0", got)
	}

	editedSource := strings.Replace(source, "return value + 1 + 2", "return value + 2 + 2", 1)
	editedSource = strings.Replace(editedSource, "== 537", "== 538", 1)
	edited := NewIncrementalObjectSession(partitionO3QualityParts, 32<<20)
	edited.minWeight = 0
	edited.UseDiskIncrementalDefaults()
	edited.EnableDiskCache(cache, "o3-quality-boundary", 64<<20)
	editedObject := filepath.Join(dir, "edited.o")
	if err := edited.WriteObject(partitionO3QualityBoundaryResult(t, editedSource, leaf), editedObject, options); err != nil {
		t.Fatalf("write edited disk-profile O3 object: %v", err)
	}
	if stats := edited.Stats(); stats.OptimizedModulesReused != 0 || stats.DiskPartitionsReused == 0 || stats.PartitionsEmitted == 0 {
		t.Fatalf("edited full-module O3 build did not reuse unchanged post-O3 partitions: %+v", stats)
	}
	t.Logf("edited post-O3 cache stats: %+v", edited.Stats())
	if got := incrementalObjectNativeStatus(t, editedObject); got != 0 {
		t.Fatalf("edited disk-profile executable status=%d, want 0", got)
	}
}

func TestDiskFullModuleSharedCodegenPreservesGenericSyntaxAndFacts(t *testing.T) {
	unsetDiskPresetFlags(t)
	t.Setenv("ELISACORE_TEST_NATIVE_WORKERS", "1")
	t.Setenv("ELISACORE_OBJECT_PARTITIONS", "4")
	previousWorkers := partitionWorkersAvailable
	RunPartitionWorkerIfRequested()
	t.Cleanup(func() { partitionWorkersAvailable = previousWorkers })

	result := parseAndAnalyzeBackendTest(t, "disk-o3-generic-publication.elisa", `def identity[T](value: T) -> T:
    return value

def main() -> i32:
    if identity[i64](40) + 2 == 42:
        return 0
    return 1
`)
	identity := result.ActiveFile().Decls[0].(*ast.FuncDecl)
	snapshot, err := ast.NewSyntaxSnapshot(result.ActiveFile(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	beforeSyntax, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	exprTypes := maps.Clone(result.ExprTypes)
	indexBounds := maps.Clone(result.IndexBoundsProven)

	session := NewIncrementalObjectSession(4, 32<<20)
	session.minWeight = 0
	session.UseDiskIncrementalDefaults()
	session.EnableDiskCache(filepath.Join(t.TempDir(), "cache"), "disk-o3-generic-publication", 64<<20)
	object := filepath.Join(t.TempDir(), "generic.o")
	if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}); err != nil {
		t.Fatalf("write generic disk-profile O3 object: %v", err)
	}
	if got := incrementalObjectNativeStatus(t, object); got != 0 {
		t.Fatalf("generic disk-profile executable status=%d, want 0", got)
	}
	if _, err := result.SpecializeFunction(identity, []semantic.Type{result.NamedTypes["i64"]}); err != nil {
		t.Fatalf("generic specialization publication after codegen: %v", err)
	}
	afterSnapshot, err := ast.NewSyntaxSnapshot(result.ActiveFile(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	afterSyntax, err := afterSnapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeSyntax, afterSyntax) {
		t.Fatal("disk-profile shared codegen or later specialization changed canonical syntax")
	}
	if !reflect.DeepEqual(exprTypes, result.ExprTypes) || !reflect.DeepEqual(indexBounds, result.IndexBoundsProven) {
		t.Fatal("disk-profile shared codegen published generator-local expression facts into the semantic result")
	}
}

func TestDiskFullModuleSharedCodegenRestoresResultAfterOptimizationError(t *testing.T) {
	unsetDiskPresetFlags(t)
	result := parseAndAnalyzeBackendTest(t, "disk-o3-error-cleanup.elisa", `def identity[T](value: T) -> T:
    return value

def main() -> i32:
    if identity[i64](40) + 2 == 42:
        return 0
    return 1
`)
	snapshot, err := ast.NewSyntaxSnapshot(result.ActiveFile(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	beforeSyntax, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	exprTypes := maps.Clone(result.ExprTypes)
	indexBounds := maps.Clone(result.IndexBoundsProven)

	session := NewIncrementalObjectSession(4, 32<<20)
	session.minWeight = 0
	session.UseDiskIncrementalDefaults()
	object := filepath.Join(t.TempDir(), "invalid-opt-level.o")
	err = session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel(99)})
	if err == nil {
		t.Fatal("invalid LLVM optimization pipeline unexpectedly succeeded")
	}
	shared := reflect.ValueOf(result).Elem().FieldByName("shared")
	if !shared.IsValid() || shared.Kind() != reflect.Ptr || !shared.IsNil() {
		t.Fatal("failed disk-profile codegen left the shared semantic session active")
	}
	afterSnapshot, err := ast.NewSyntaxSnapshot(result.ActiveFile(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	afterSyntax, err := afterSnapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeSyntax, afterSyntax) {
		t.Fatal("failed disk-profile codegen changed canonical syntax")
	}
	if !reflect.DeepEqual(exprTypes, result.ExprTypes) || !reflect.DeepEqual(indexBounds, result.IndexBoundsProven) {
		t.Fatal("failed disk-profile codegen changed semantic expression facts")
	}
}

func diskFullModuleO3QualityIR(t *testing.T, result *semantic.Result, optLevel OptimizationLevel) string {
	t.Helper()
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	return partitionO3QualityIR(t, result, optLevel, "main", partitionO3QualityParts, func(g *llvmGenerator) {
		g.sharedResult = true
	}, shared)
}

func partitionO3QualityIR(t *testing.T, result *semantic.Result, optLevel OptimizationLevel, caller string, parts int, configure func(*llvmGenerator), shared ...*sharedCodegenLookups) string {
	t.Helper()
	g, err := compileLLVMModuleConfigured(result, optLevel, DefaultPackedLoweringProfile(), "", false, false, configure, shared...)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if configure != nil && g.partBody != nil && !finishPartitionModule(g.module, partitionOwner(caller, parts), parts, "o3-quality-boundary") {
		t.Fatal("partition module was declined")
	}
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if err := g.verify(); err != nil {
		t.Fatalf("generated module failed verification: %v", err)
	}
	if err := g.optimizeModule(optLevel); err != nil {
		t.Fatal(err)
	}
	if err := g.verify(); err != nil {
		t.Fatalf("optimized module failed verification: %v", err)
	}
	return g.printModule()
}

func partitionQualityFunctionBody(ir, name string) (string, bool) {
	lines := strings.Split(ir, "\n")
	for start, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "define ") || !strings.Contains(trimmed, "@"+name+"(") {
			continue
		}
		for end := start + 1; end < len(lines); end++ {
			if strings.TrimSpace(lines[end]) == "}" {
				return strings.Join(lines[start:end+1], "\n"), true
			}
		}
	}
	return "", false
}
