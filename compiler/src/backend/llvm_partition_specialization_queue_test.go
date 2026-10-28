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

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func partitionQueueGenericBaseForOwner(t testing.TB, prefix string, parts, owner int) string {
	t.Helper()
	i64 := &semantic.BuiltinType{Name: "i64"}
	return partitionQueueGenericBaseForTypeOwner(t, prefix, parts, owner, i64)
}

func partitionQueueGenericBaseForTypeOwner(t testing.TB, prefix string, parts, owner int, typ semantic.Type) string {
	t.Helper()
	for index := 0; index < 10000; index++ {
		base := fmt.Sprintf("%s_%d", prefix, index)
		symbol, err := (&llvmGenerator{}).mangleGenericType(base, []semantic.Type{typ})
		if err != nil {
			t.Fatal(err)
		}
		if partitionOwner(symbol, parts) == owner {
			return base
		}
	}
	t.Fatalf("could not find a generic name owned by partition %d", owner)
	return ""
}

func TestPartitionedO0NestedPackedAggregateSpecializationLinksAndRuns(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	const parts = 4
	i64 := &semantic.BuiltinType{Name: "i64"}
	record := &semantic.GenericInstanceType{
		Name: "Record",
		Base: &semantic.StructType{Name: "Record", TypeParams: []string{"T"}},
		Args: []semantic.Type{i64},
	}
	inner := partitionQueueGenericBaseForTypeOwner(t, "packed_queue_inner", parts, 1, record)
	outer := partitionQueueGenericBaseForTypeOwner(t, "packed_queue_outer", parts, 0, record)
	innerSymbol, err := (&llvmGenerator{}).mangleGenericType(inner, []semantic.Type{record})
	if err != nil {
		t.Fatal(err)
	}
	outerSymbol, err := (&llvmGenerator{}).mangleGenericType(outer, []semantic.Type{record})
	if err != nil {
		t.Fatal(err)
	}
	relay := partitionQueueOrdinaryNameForOwner(t, "packed_queue_relay", parts, 2)
	if partitionOwner(outerSymbol, parts) == partitionOwner(relay, parts) {
		t.Fatal("fixture requires the generic owner to have no direct caller")
	}
	source := fmt.Sprintf(`struct Record[T] layout(packed):
    tag: u8
    payload: mutable T

def %s[T](value: T) -> T:
    return value

def %s[T](value: T) -> T:
    can Global.Read:
        return %s[T](value)

def %s(value: Record[i64]) -> Record[i64]:
    can Global.Read:
        return %s[Record[i64]](value)

def main() -> i64:
    can Global.Read:
        original: Record[i64] = Record[i64]{tag: 7, payload: 41}
        copied: Record[i64] = %s(original)
        if copied.tag != 7:
            return 1
        if copied.payload != 41:
            return 2
        return 0
`, inner, outer, inner, relay, outer, relay)
	result := parseAndAnalyzeBackendTest(t, "partition_packed_aggregate_specialization.elisa", source)
	cacheRoot := t.TempDir()
	session := NewIncrementalObjectSession(parts, 64<<20)
	session.minWeight = 0
	session.EnableDiskCache(cacheRoot, "partition-packed-specialization", 64<<20)
	object := filepath.Join(t.TempDir(), "packed-specialization.o")
	if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatalf("partitioned object build: %v", err)
	}
	if stats := session.Stats(); stats.SerialFallbacks != 0 {
		t.Fatalf("packed aggregate fixture did not use partitioned IR: %+v", stats)
	}
	if got := partitionQueueNativeStatus(t, object); got != 0 {
		t.Fatalf("linked packed aggregate specialization exited with %d", got)
	}
	replaySession := NewIncrementalObjectSession(parts, 64<<20)
	replaySession.minWeight = 0
	replaySession.EnableDiskCache(cacheRoot, "partition-packed-specialization", 64<<20)
	replayed := filepath.Join(t.TempDir(), "packed-specialization-replayed.o")
	if err := replaySession.WriteObject(result, replayed, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatalf("replay partitioned object: %v", err)
	}
	if reused := replaySession.Stats().DiskPartitionsReused; reused < parts+1 {
		t.Fatalf("repeat build reused %d disk partitions, want all %d declarations plus a supplemental specialization owner", reused, parts)
	}
	if got := partitionQueueNativeStatus(t, replayed); got != 0 {
		t.Fatalf("replayed packed aggregate specialization exited with %d", got)
	}
	if innerSymbol == outerSymbol {
		t.Fatal("nested generic specializations unexpectedly share one symbol")
	}
}

func TestPartitionedO0LaterOwnerModuleReferencesEarlierSpecialization(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	const parts = 4
	leaf := partitionQueueGenericBaseForOwner(t, "partition_queue_prior_leaf", parts, 1)
	later := partitionQueueGenericBaseForOwner(t, "partition_queue_later_body", parts, 1)
	root := partitionQueueGenericBaseForOwner(t, "partition_queue_root_body", parts, 0)
	source := fmt.Sprintf("def %s[T](value: T) -> T:\n    return value\n\ndef %s[T](value: T) -> T:\n    return %s[T](value)\n\ndef %s[T](value: T) -> T:\n    return %s[T](value)\n\ndef main() -> i64:\n    return %s[i64](1) + %s[i64](2) - 3\n", leaf, later, leaf, root, later, root, leaf)
	result := parseAndAnalyzeBackendTest(t, "partition_prior_owner_specialization.elisa", source)
	session := NewIncrementalObjectSession(parts, 64<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "partition-prior-owner-specialization", 64<<20)
	object := filepath.Join(t.TempDir(), "prior-owner-specialization.o")
	if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatalf("partitioned object build: %v", err)
	}
	if stats := session.Stats(); stats.SerialFallbacks != 0 {
		t.Fatalf("prior-owner fixture did not use partitioned IR: %+v", stats)
	}
	if got := partitionQueueNativeStatus(t, object); got != 0 {
		t.Fatalf("linked prior-owner specialization exited with %d", got)
	}
}

func partitionQueueNativeStatus(t *testing.T, object string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal("native specialization qualification requires clang:", err)
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "arena_free.c")
	if err := os.WriteFile(shim, []byte("void arena_free(void *owner) {(void)owner;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "specialization.exe")
	if output, err := exec.CommandContext(ctx, clang, object, shim, "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("native specialization link: %v\n%s", err, output)
	}
	err = exec.CommandContext(ctx, executable).Run()
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	t.Fatal(err)
	return -1
}

func partitionQueueOrdinaryNameForOwner(t testing.TB, prefix string, parts, owner int) string {
	t.Helper()
	for index := 0; index < 10000; index++ {
		name := fmt.Sprintf("%s_%d", prefix, index)
		if partitionOwner(name, parts) == owner {
			return name
		}
	}
	t.Fatalf("could not find a function name owned by partition %d", owner)
	return ""
}

func TestPartitionedO0SpecializationQueueDrainsNestedAndRecursiveBodies(t *testing.T) {
	const parts = 4
	inner := partitionQueueGenericBaseForOwner(t, "partition_queue_inner", parts, 1)
	outer := partitionQueueGenericBaseForOwner(t, "partition_queue_outer", parts, 0)
	recursive := partitionQueueGenericBaseForOwner(t, "partition_queue_recursive", parts, 2)
	caller := partitionQueueOrdinaryNameForOwner(t, "partition_queue_caller", parts, 3)
	unrelated := partitionQueueOrdinaryNameForOwner(t, "partition_queue_unrelated", parts, 0)
	innerSymbol, err := (&llvmGenerator{}).mangleGenericType(inner, []semantic.Type{&semantic.BuiltinType{Name: "i64"}})
	if err != nil {
		t.Fatal(err)
	}
	outerSymbol, err := (&llvmGenerator{}).mangleGenericType(outer, []semantic.Type{&semantic.BuiltinType{Name: "i64"}})
	if err != nil {
		t.Fatal(err)
	}
	recursiveSymbol, err := (&llvmGenerator{}).mangleGenericType(recursive, []semantic.Type{&semantic.BuiltinType{Name: "i64"}})
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`global mutable partition_queue_counter: i64 = 1

def %s[T](value: T) -> T:
    can Global.Read:
        if partition_queue_counter < 0:
            return value
    return value

def %s[T](value: T) -> T:
    can Global.Read:
        return %s[T](value)

def %s[T](value: T, count: i64) -> T:
    if count == 0:
        return value
    return %s[T](value, count - 1)

def %s() -> i64:
    can Global.Read:
        return %s[i64](%s[i64](17, 1))

def %s() -> i64:
    return 99
`, inner, outer, inner, recursive, recursive, caller, outer, recursive, unrelated)
	result := parseAndAnalyzeBackendTest(t, "partition_specialization_queue.elisa", source)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	declarations := partitionIRDeclarationWorklists(result, shared.symbolsByNode, parts)
	queue := newPartitionSpecializationQueue()
	session := NewIncrementalObjectSession(parts, 64<<20)
	var unitID, targetID [32]byte
	definedByPart := make([]map[string]bool, parts)
	members := make([][]string, parts)
	for part := 0; part < parts; part++ {
		_, definitions, err := session.buildIRPartitionWithMode(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}, part, parts, true, "queue-test", unitID, targetID, declarations[part], &members[part], shared, partitionIRBuildMode{queue: queue, cachePart: part})
		if err != nil {
			t.Fatalf("lower ordinary partition %d: %v", part, err)
		}
		definedByPart[part] = make(map[string]bool)
		for _, name := range definitions {
			if name == innerSymbol || name == outerSymbol || name == recursiveSymbol {
				t.Fatalf("ordinary partition %d unexpectedly defined deferred specialization %q", part, name)
			}
			definedByPart[part][name] = true
		}
	}
	for _, name := range []string{outerSymbol, recursiveSymbol} {
		if !queue.hasName(name) {
			t.Fatalf("ordinary caller did not queue specialization %q", name)
		}
	}
	if queue.hasName(innerSymbol) {
		t.Fatal("nested specialization was queued before lowering its outer body")
	}

	supplemental, err := session.buildQueuedPartitionSpecializations(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}, parts, true, "queue-test", unitID, targetID, shared, queue, definedByPart, members, nil, nil)
	if err != nil {
		t.Fatalf("drain specialization queue: %v", err)
	}
	if len(supplemental) != 3 {
		t.Fatalf("got %d supplemental modules, want owner modules for outer, recursive, and nested inner bodies", len(supplemental))
	}
	counts := map[string]int{}
	owners := map[string]int{}
	for _, module := range supplemental {
		for _, name := range module.definitions {
			if name == unrelated {
				t.Fatalf("supplemental module for owner %d emitted unrelated body %q", module.owner, name)
			}
			if name == innerSymbol || name == outerSymbol || name == recursiveSymbol {
				counts[name]++
				owners[name] = module.owner
			}
		}
	}
	for name, owner := range map[string]int{innerSymbol: 1, outerSymbol: 0, recursiveSymbol: 2} {
		if counts[name] != 1 {
			t.Errorf("specialization %q was defined %d times, want exactly once", name, counts[name])
		}
		if owners[name] != owner {
			t.Errorf("specialization %q was defined in partition %d, want owner %d", name, owners[name], owner)
		}
	}
}

func TestPartitionSpecializationQueueDeclinesIdentityConflicts(t *testing.T) {
	i64 := &semantic.BuiltinType{Name: "i64"}
	heap := &semantic.RegionParamType{Name: "heap"}
	stack := &semantic.RegionParamType{Name: "stack"}
	base := &semantic.FuncType{
		Name:         "queue_identity",
		TypeParams:   []string{"T"},
		RegionParams: []string{"r"},
		Params:       []semantic.Type{&semantic.RefType{Elem: &semantic.TypeParamType{Name: "T"}, Region: "r"}},
		Return:       &semantic.RefType{Elem: &semantic.TypeParamType{Name: "T"}, Region: "r"},
	}
	decl := &ast.FuncDecl{Name: "queue_identity"}
	result := &semantic.Result{}
	queue := newPartitionSpecializationQueue()
	name := "same-global-specialization-symbol"
	heapType := &semantic.FuncType{
		Name:   "queue_identity",
		Params: []semantic.Type{&semantic.RefType{Elem: i64, Region: "heap"}},
		Return: &semantic.RefType{Elem: i64, Region: "heap"},
	}
	if err := queue.addFunction(result, name, decl, base, heapType, map[string]semantic.Type{"T": i64, "r": heap}, 4); err != nil {
		t.Fatal("first specialization request declined:", err)
	}
	wrongSignature := &semantic.FuncType{
		Name:   "queue_identity",
		Params: []semantic.Type{&semantic.RefType{Elem: i64, Region: "heap"}},
		Return: i64,
	}
	if err := queue.addFunction(result, name, decl, base, wrongSignature, map[string]semantic.Type{"T": i64, "r": heap}, 4); err != errPartitionDeclined {
		t.Fatalf("same symbol with a different specialized signature returned %v, want errPartitionDeclined", err)
	}
	stackType := &semantic.FuncType{
		Name:   "queue_identity",
		Params: []semantic.Type{&semantic.RefType{Elem: i64, Region: "stack"}},
		Return: &semantic.RefType{Elem: i64, Region: "stack"},
	}
	if err := queue.addFunction(result, name, decl, base, stackType, map[string]semantic.Type{"r": stack, "T": i64}, 4); err != errPartitionDeclined {
		t.Fatalf("same symbol with different effective bindings returned %v, want errPartitionDeclined", err)
	}
	if err := queue.addFunction(result, name, &ast.FuncDecl{Name: "queue_identity"}, base, heapType, map[string]semantic.Type{"T": i64, "r": heap}, 4); err != errPartitionDeclined {
		t.Fatalf("same symbol with different source body returned %v, want errPartitionDeclined", err)
	}

	implDecl := &ast.ImplDecl{GenericParams: []ast.GenericParam{{Name: "A"}, {Name: "B"}}}
	impl := &semantic.StaticImpl{
		InterfaceName: "Protocol",
		Receiver:      &semantic.StructType{Name: "QueueReceiver"},
		Decl:          implDecl,
		TypeParams:    []string{"A", "B"},
	}
	methodDecl := &ast.FuncDecl{Name: "read"}
	methodBase := &semantic.FuncType{Name: "read", Params: []semantic.Type{&semantic.TypeParamType{Name: "A"}}, Return: &semantic.TypeParamType{Name: "A"}}
	methodType := &semantic.FuncType{Name: "read", Params: []semantic.Type{i64}, Return: i64}
	implQueue := newPartitionSpecializationQueue()
	if err := implQueue.addImplMethod(result, "impl-method-symbol", methodDecl, methodBase, methodType, []string{"A", "B"}, impl, "read", map[string]semantic.Type{"A": i64, "B": i64}, 4); err != nil {
		t.Fatal("ordered impl specialization request declined:", err)
	}
	if err := implQueue.addImplMethod(result, "impl-method-symbol", methodDecl, methodBase, methodType, []string{"B", "A"}, impl, "read", map[string]semantic.Type{"A": i64, "B": i64}, 4); err != errPartitionDeclined {
		t.Fatalf("impl request with reordered source type parameters returned %v, want errPartitionDeclined", err)
	}
}

func TestPartitionSyntheticStableSpecializationQueuesToOwner(t *testing.T) {
	const parts = 2
	base := partitionQueueGenericBaseForOwner(t, "partition_synthetic_generic", parts, 0)
	caller := partitionQueueOrdinaryNameForOwner(t, "partition_synthetic_caller", parts, 1)
	symbol, err := (&llvmGenerator{}).mangleGenericType(base, []semantic.Type{&semantic.BuiltinType{Name: "i64"}})
	if err != nil {
		t.Fatal(err)
	}
	result := parseAndAnalyzeBackendTest(t, "partition_synthetic_specialization.elisa", fmt.Sprintf(`def %s[T](value: T) -> T:
    return value

def %s() -> i64:
    return %s[i64](23)
`, base, caller, base))
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	worklists := partitionIRDeclarationWorklists(result, shared.symbolsByNode, parts)
	queue := newPartitionSpecializationQueue()
	part := 1
	g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
		g.partBody = func(name string) bool { return partitionOwner(name, parts) == part }
		g.partitionGlobalDeclarationsOnly = part != 0
		g.partitionSpecializationQueue = queue
		g.partitionSpecializationParts = parts
		g.partSyntheticDepth = 1
		g.sharedResult = true
		g.partitionModuleDecls = worklists[part]
		g.hasPartitionModuleDecls = true
	}, shared)
	if err != nil {
		t.Fatal("compile synthetic partition context:", err)
	}
	defer g.dispose()
	if !queue.hasName(symbol) {
		t.Fatalf("stable synthetic-context specialization %q did not enter the global owner queue", symbol)
	}
	definitionFound := false
	for _, line := range strings.Split(g.printModule(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "define ") && strings.Contains(line, symbol) {
			definitionFound = true
			break
		}
	}
	if definitionFound {
		t.Fatalf("non-owner synthetic-context partition defined specialization %q", symbol)
	}
}
