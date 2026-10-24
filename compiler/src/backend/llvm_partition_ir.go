//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Analysis.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include "parallel_emit.h"

extern void elisacorePruneObjectDeclarations(LLVMModuleRef m);
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// Partitioned IR generation for incremental object builds.
//
// Instead of generating one module and splitting it, each partition gets its
// own generator, LLVM context and module, and the generators run concurrently
// over one shared analysis. A partition emits the bodies of the top-level
// external functions whose symbol name hashes to it; it declares everything
// else, and emits on demand whatever its own bodies need (specializations,
// helpers). Symbols are named by mangling, never by creation order, so every
// partition agrees on them. A definition another partition also emits is
// made weak_odr, so the linker keeps one copy:
//   - an external function body is strong only in its owner partition;
//   - an external global is strong only in partition 0.
// Every external definition comes from the same generator code over the same
// analysis, so duplicate copies are identical.

var partitionIRNanos, partitionDefs, partitionOwned, partitionGenNanos, partitionBitcodeNanos, partitionNativeNanos, partitionNativeCount atomic.Int64

func llvmValueName(v C.LLVMValueRef) string {
	var n C.size_t
	p := C.LLVMGetValueName2(v, &n)
	return C.GoStringN(p, C.int(n))
}

func partitionOwner(name string, parts int) int {
	h := fnv.New64a()
	h.Write([]byte(name))
	return int(h.Sum64() % uint64(parts))
}

// finishPartitionModule gives the partition's definitions their final
// linkage. It reports false when a symbol's identity cannot be matched across
// partitions; the caller then uses the module-split path.
func finishPartitionModule(m C.LLVMModuleRef, part, parts int, unit string) bool {
	for f := C.LLVMGetFirstFunction(m); f != nil; f = C.LLVMGetNextFunction(f) {
		if C.LLVMIsDeclaration(f) != 0 {
			continue
		}
		switch C.LLVMGetLinkage(f) {
		case C.LLVMExternalLinkage:
			if partitionOwner(llvmValueName(f), parts) != part {
				C.LLVMSetLinkage(f, C.LLVMWeakODRLinkage)
			}
		case C.LLVMPrivateLinkage, C.LLVMInternalLinkage:
			if !shareLocalDefinition(f, unit) {
				return false
			}
		}
	}
	for g := C.LLVMGetFirstGlobal(m); g != nil; g = C.LLVMGetNextGlobal(g) {
		if C.LLVMIsDeclaration(g) != 0 {
			continue
		}
		switch C.LLVMGetLinkage(g) {
		case C.LLVMExternalLinkage:
			if part != 0 {
				C.LLVMSetLinkage(g, C.LLVMWeakODRLinkage)
			}
		case C.LLVMPrivateLinkage, C.LLVMInternalLinkage:
			if C.LLVMGetUnnamedAddress(g) == C.LLVMGlobalUnnamedAddr {
				continue // address-insignificant: a private copy per partition is equivalent
			}
			if !shareLocalDefinition(g, unit) {
				return false
			}
		}
	}
	// The cache key is the module's bitcode, so it must not depend on what
	// other partitions do: drop declarations this partition does not use and
	// name its private constants by their order here, not by the generator's
	// program-wide counter.
	C.elisacorePruneObjectDeclarations(m)
	ordinal := 0
	for g := C.LLVMGetFirstGlobal(m); g != nil; g = C.LLVMGetNextGlobal(g) {
		if C.LLVMGetLinkage(g) == C.LLVMPrivateLinkage && C.LLVMGetUnnamedAddress(g) == C.LLVMGlobalUnnamedAddr {
			name := fmt.Sprintf("elisa.p.%d", ordinal)
			cname := C.CString(name)
			C.LLVMSetValueName2(g, cname, C.size_t(len(name)))
			C.free(unsafe.Pointer(cname))
			ordinal++
		}
	}
	return true
}

// shareLocalDefinition turns a module-local definition every partition may
// emit into one symbol the linker merges, so its address is the same from
// every partition. Its name must come from the source, not from LLVM's
// collision suffixes, or two partitions could disagree on it.
func shareLocalDefinition(v C.LLVMValueRef, unit string) bool {
	name := llvmValueName(v)
	if name == "" || numericNameSuffix.MatchString(name) {
		return false
	}
	shared := C.CString("elisa.unit." + unit + "." + name)
	C.LLVMSetValueName2(v, shared, C.size_t(len("elisa.unit."+unit+"."+name)))
	C.free(unsafe.Pointer(shared))
	C.LLVMSetLinkage(v, C.LLVMWeakODRLinkage)
	C.LLVMSetVisibility(v, C.LLVMHiddenVisibility)
	return true
}

var numericNameSuffix = regexp.MustCompile(`\.[0-9]+$`)

func partitionedIREnabled() bool { return os.Getenv("ELISACORE_PARALLEL_IR") != "0" }

// Optimized partition generation is an explicit experiment alongside the
// existing pre-optimization partition mode. It has the same cross-partition
// inlining limitation and does not replace the default whole-module O3 path.
// Programs must be large enough to repay a generator per partition.
func (s *IncrementalObjectSession) partitionedIRApplies(result *semantic.Result, options LLVMObjectEmitOptions) bool {
	if !partitionedIREnabled() {
		return false
	}
	if options.OptLevel != OptimizationLevel0 && (os.Getenv("ELISACORE_PARALLEL_IR") != "1" ||
		!parallelOptEnabled() || parallelOptImportWeight() != 0 || !partitionWorkersAvailable) {
		return false
	}
	if s.minWeight == 0 {
		return true
	}
	functions := 0
	for _, decl := range result.ActiveFile().Decls {
		if _, ok := decl.(*ast.FuncDecl); ok {
			functions++
		}
	}
	return functions >= 64
}

// writePartitionedObject is WriteObject with partitioned IR generation. The
// caller has checked that the target is native and the output is a real file.
func (s *IncrementalObjectSession) writePartitionedObject(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions, configKey string, unitID, targetID [32]byte) error {
	start := time.Now()
	parts := s.partitions
	fastCodegen := options.OptLevel == OptimizationLevel0
	result.PackedLowering = options.PackedProfile.metadata()
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	objects := make([][]byte, parts)
	members := make([][]string, parts)
	var previousMembers [][]string
	if os.Getenv("ELISACORE_PARTITION_SCHEDULE") != "0" {
		previousMembers = s.disk.loadSchedule(unitID, targetID, parts)
	}
	errs := make([]error, parts)
	workers := runtime.GOMAXPROCS(0)
	if workers > parts {
		workers = parts
	}
	next := make(chan int, parts)
	for _, part := range partitionedIRWorkOrder(result, parts, previousMembers) {
		next <- part
	}
	close(next)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for part := range next {
				objects[part], errs[part] = s.buildIRPartition(result, options, part, parts, fastCodegen, configKey, unitID, targetID, &members[part], shared)
			}
		}()
	}
	wg.Wait()
	for part := range errs {
		if errs[part] != nil {
			return errs[part]
		}
	}
	s.disk.storeSchedule(unitID, targetID, members)
	buildDuration := time.Since(start)
	dir, err := os.MkdirTemp(filepath.Dir(outputPath), ".elisa-object-parts-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	files := make([]string, parts)
	for part := range objects {
		files[part] = filepath.Join(dir, fmt.Sprintf("part%d.o", part))
		if err := os.WriteFile(files[part], objects[part], 0600); err != nil {
			return err
		}
	}
	mergeStart := time.Now()
	if err := s.mergeObjects(dir, files, outputPath); err != nil {
		return err
	}
	if os.Getenv("ELISACORE_BUILD_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "[build-timing] partitioned object: ir+partitions %v, merge %v; ir cpu %v (generate %v), bitcode %v, native %v x%d, defs %d owned %d\n", buildDuration.Round(time.Millisecond), time.Since(mergeStart).Round(time.Millisecond), time.Duration(partitionIRNanos.Swap(0)).Round(time.Millisecond), time.Duration(partitionGenNanos.Swap(0)).Round(time.Millisecond), time.Duration(partitionBitcodeNanos.Swap(0)).Round(time.Millisecond), time.Duration(partitionNativeNanos.Swap(0)).Round(time.Millisecond), partitionNativeCount.Swap(0), partitionDefs.Swap(0), partitionOwned.Swap(0))
	}
	return nil
}

// buildIRPartition generates one partition's module, looks its exact bitcode
// up in the session and compiles it on a miss.
var errPartitionDeclined = errors.New("partitioned IR declined")

func (s *IncrementalObjectSession) buildIRPartition(result *semantic.Result, options LLVMObjectEmitOptions, part, parts int, fastCodegen bool, configKey string, unitID, targetID [32]byte, members *[]string, shared *sharedCodegenLookups) ([]byte, error) {
	unit := hex.EncodeToString(unitID[:8])
	irStart := time.Now()
	g, err := compileLLVMModuleConfigured(result, options.OptLevel, options.PackedProfile, options.TargetTriple, options.DebugInfo, options.Trace, func(g *llvmGenerator) {
		g.partBody = func(name string) bool { return partitionOwner(name, parts) == part }
		g.partitionGlobalDeclarationsOnly = part != 0
		g.sharedResult = true
		// LLVM retains GlobalValue names while discarding non-semantic local SSA
		// names. Set this before lowering to avoid creating source-offset names.
		if !options.DebugInfo {
			C.LLVMContextSetDiscardValueNames(g.context, 1)
		}
	}, shared)
	partitionGenNanos.Add(int64(time.Since(irStart)))
	if err != nil {
		return nil, fmt.Errorf("partition %d: %w", part, err)
	}
	defer g.disposeInBackground()
	if !finishPartitionModule(g.module, part, parts, unit) {
		return nil, errPartitionDeclined
	}
	if err := g.verify(); err != nil {
		return nil, fmt.Errorf("partition %d: %w", part, err)
	}
	if err := g.ensureTargetMachine(); err != nil {
		return nil, err
	}
	irDuration := time.Since(irStart)
	partitionIRNanos.Add(int64(irDuration))
	var bodies, defs int64
	for f := C.LLVMGetFirstFunction(g.module); f != nil; f = C.LLVMGetNextFunction(f) {
		if C.LLVMIsDeclaration(f) == 0 {
			defs++
			if s.disk != nil {
				*members = append(*members, partitionScheduleStem(llvmValueName(f)))
			}
		}
		if C.LLVMIsDeclaration(f) == 0 && C.LLVMGetLinkage(f) != C.LLVMWeakODRLinkage {
			bodies++
		}
	}
	partitionDefs.Add(defs)
	partitionOwned.Add(bodies)
	bitcodeStart := time.Now()
	bitcode := C.LLVMWriteBitcodeToMemoryBuffer(g.module)
	inputData := C.LLVMGetBufferStart(bitcode)
	inputSize := int(C.LLVMGetBufferSize(bitcode))
	inputBytes := unsafe.Slice((*byte)(unsafe.Pointer(inputData)), inputSize)
	var object []byte
	retainInput := inputSize <= s.maxBytes-len(configKey)
	if retainInput {
		object = s.lookupObjectBytes(inputBytes, configKey)
	}
	var retainedInput string
	if object == nil && retainInput {
		retainedInput = string(inputBytes)
	}
	defer C.LLVMDisposeMemoryBuffer(bitcode)
	bitcodeDuration := time.Since(bitcodeStart)
	partitionBitcodeNanos.Add(int64(bitcodeDuration))
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.IRTime += irDuration
		stats.BitcodeTime += bitcodeDuration
	})
	if object != nil {
		return object, nil
	}
	triple := C.LLVMGetTargetMachineTriple(g.targetMachine)
	cpu := C.LLVMGetTargetMachineCPU(g.targetMachine)
	features := C.LLVMGetTargetMachineFeatureString(g.targetMachine)
	defer C.LLVMDisposeMessage(triple)
	defer C.LLVMDisposeMessage(cpu)
	defer C.LLVMDisposeMessage(features)
	nativeStart := time.Now()
	if options.OptLevel != OptimizationLevel0 {
		var err error
		var fragment *nativeFragmentContext
		if nativeFragmentsEnabled() && s.disk != nil {
			fragment = &nativeFragmentContext{request: nativeFragmentRequest{
				Root:      filepath.Join(s.disk.root, "native-fragments-v1"),
				Namespace: fmt.Sprintf("%x.ir.%d", unitID, part), Target: configKey,
				Merger: s.objectMergerCommand(), MaxBytes: s.disk.maxBytes,
			}, record: func(fragment nativeFragmentStats) {
				s.recordStats(func(stats *IncrementalObjectStats) {
					stats.NativeFragmentsEmitted += fragment.Emitted
					stats.NativeFragmentsReused += fragment.Reused
					if fragment.Declined {
						stats.NativeFragmentFallbacks++
					}
				})
			}}
		}
		object, err = emitPreoptimizedPartition(inputBytes, optimizationPipeline(options.OptLevel), triple, cpu, features, fragment)
		if err != nil {
			return nil, fmt.Errorf("partition %d: %w", part, err)
		}
	} else {
		var buffer C.LLVMMemoryBufferRef
		var message *C.char
		if C.elisacoreEmitPartitionObject(g.module, triple, cpu, features, cBool(fastCodegen), &buffer, &message) == 0 {
			text := "native emission failed"
			if message != nil {
				text = C.GoString(message)
				C.free(unsafe.Pointer(message))
			}
			return nil, fmt.Errorf("partition %d: %s", part, text)
		}
		object = C.GoBytes(unsafe.Pointer(C.LLVMGetBufferStart(buffer)), C.int(C.LLVMGetBufferSize(buffer)))
		C.LLVMDisposeMemoryBuffer(buffer)
	}

	nativeDuration := time.Since(nativeStart)
	if os.Getenv("ELISACORE_BUILD_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "[build-timing] partition %d: native-start %s, native %v, input %d bytes\n", part, nativeStart.Format("15:04:05.000"), nativeDuration.Round(time.Millisecond), inputSize)
	}
	partitionNativeNanos.Add(int64(nativeDuration))
	partitionNativeCount.Add(1)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.NativeTime += nativeDuration
		stats.PartitionsEmitted++
	})
	if retainInput {
		s.retainObjectForSlot(retainedInput, configKey, object, incrementalObjectSlot(unitID, targetID, part))
	}
	return object, nil
}

// Code generation records types for the expressions it synthesizes. A
// generator that shares its Result with others keeps those records in its own
// overlay, which its reads consult first; the shared maps stay read-only.
// ELISACORE_IR_OVERLAY=1 uses the overlay on the serial path too, so serial
// output can be compared with and without it.

var forceIROverlay = os.Getenv("ELISACORE_IR_OVERLAY") == "1"

func (g *llvmGenerator) usesOverlay() bool { return g.sharedResult || forceIROverlay }

func (g *llvmGenerator) resultExprTypeOK(expr ast.Expr) (semantic.Type, bool) {
	if g.exprOverlay != nil {
		if t, ok := g.exprOverlay[expr]; ok {
			return t, t != nil
		}
	}
	t, ok := g.result.ExprTypes[expr]
	return t, ok
}

func (g *llvmGenerator) resultExprType(expr ast.Expr) semantic.Type {
	t, _ := g.resultExprTypeOK(expr)
	return t
}

func (g *llvmGenerator) setResultExprType(expr ast.Expr, t semantic.Type) {
	if !g.usesOverlay() {
		g.result.ExprTypes[expr] = t
		return
	}
	if g.exprOverlay == nil {
		g.exprOverlay = map[ast.Expr]semantic.Type{}
	}
	g.exprOverlay[expr] = t
}

// deleteResultExprType removes a record; in the overlay a nil entry hides the
// shared one, as a delete would.
func (g *llvmGenerator) deleteResultExprType(expr ast.Expr) {
	if !g.usesOverlay() {
		delete(g.result.ExprTypes, expr)
		return
	}
	if g.exprOverlay == nil {
		g.exprOverlay = map[ast.Expr]semantic.Type{}
	}
	g.exprOverlay[expr] = nil
}

func (g *llvmGenerator) resultIndexBoundsProven(expr *ast.IndexExpr) bool {
	if g.boundsOverlay[expr] {
		return true
	}
	return g.result.IndexBoundsProven[expr]
}

func (g *llvmGenerator) setResultIndexBoundsProven(expr *ast.IndexExpr, proven bool) {
	if !g.usesOverlay() {
		g.result.IndexBoundsProven[expr] = proven
		return
	}
	if g.boundsOverlay == nil {
		g.boundsOverlay = map[*ast.IndexExpr]bool{}
	}
	g.boundsOverlay[expr] = proven
}

// Start likely misses early, so their native optimization overlaps generation
// of cache-hit units. Hints only affect scheduling; all units still generate
// and validate their full current bitcode before any cache lookup.
func partitionedIRWorkOrder(result *semantic.Result, parts int, previous ...[][]string) []int {
	preferred := make([]bool, parts)
	for _, name := range result.CodegenFunctionPriorities() {
		if sym, ok := result.GlobalScope.Lookup(name); ok && sym != nil && sym.LinkName != "" {
			name = sym.LinkName
		}
		preferred[partitionOwner(name, parts)] = true
		if len(previous) != 0 && len(previous[0]) == parts {
			genericStem := sanitizeIdentifier(name)
			if len(genericStem) > 48 {
				genericStem = genericStem[:48]
			}
			for part, names := range previous[0] {
				for _, member := range names {
					if member == name || member == genericStem {
						preferred[part] = true
						break
					}
				}
			}
		}
	}
	order := make([]int, 0, parts)
	for part, want := range preferred {
		if want {
			order = append(order, part)
		}
	}
	for part, want := range preferred {
		if !want {
			order = append(order, part)
		}
	}
	return order
}
