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
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unsafe"
)

// buildIRPartition generates one partition's module, looks its exact bitcode
// up in the session and compiles it on a miss.
var errPartitionDeclined = errors.New("partitioned IR declined")

func (s *IncrementalObjectSession) buildIRPartition(result *semantic.Result, options LLVMObjectEmitOptions, part, parts int, fastCodegen bool, configKey string, unitID, targetID [32]byte, declarations []ast.Decl, members *[]string, shared *sharedCodegenLookups) ([]byte, error) {
	mode := partitionIRBuildMode{cachePart: part, inputAudit: s.irInputAudit}
	object, _, err := s.buildIRPartitionWithMode(result, options, part, parts, fastCodegen, configKey, unitID, targetID, declarations, members, shared, mode)
	return object, err
}

func (s *IncrementalObjectSession) buildIRPartitionWithMode(result *semantic.Result, options LLVMObjectEmitOptions, part, parts int, fastCodegen bool, configKey string, unitID, targetID [32]byte, declarations []ast.Decl, members *[]string, shared *sharedCodegenLookups, mode partitionIRBuildMode) ([]byte, []string, error) {
	unit := hex.EncodeToString(unitID[:])
	irStart := time.Now()
	var inputReads *irInputReadRecorder
	if s.irInputAudit != nil {
		inputReads = &irInputReadRecorder{}
		inputReads.codegenObserver = s.irCodegenObserver
		inputReads.shared = shared.inputReads
		defer func() {
			if mode.inputAudit != nil {
				mode.inputAudit(part, inputReads.snapshot())
			}
		}()
	}
	g, err := compileLLVMModuleConfigured(result, options.OptLevel, options.PackedProfile, options.TargetTriple, options.DebugInfo, options.Trace, func(g *llvmGenerator) {
		g.inputReads = inputReads
		g.deferPartitionCacheVerification = !mode.specializationOnly
		g.verifyObserver = mode.verifyObserver
		if mode.specializationOnly {
			g.partSynthNS = fmt.Sprintf("elisa.partition.%x.%d.specialization.%d.", unitID, part, mode.supplementRound)
		} else {
			g.partSynthNS = fmt.Sprintf("elisa.partition.%x.%d.", unitID, part)
		}
		g.partBody = memoizedPartitionBody(part, parts, mode.previouslyDefined)
		g.partitionGlobalDeclarationsOnly = mode.specializationOnly || part != 0
		g.partitionSpecializationQueue = mode.queue
		g.partitionSpecializationParts = parts
		g.partitionSpecializationOnly = mode.specializationOnly
		g.sharedResult = true
		g.partitionModuleDecls = declarations
		g.hasPartitionModuleDecls = true
		// Imports are the cross-partition inlining path. They default on whenever a partition
		// is lowered, independent of the disk preset, because explicit PARALLEL flags disable
		// that preset. ELISACORE_IR_IMPORTS still overrides, and the exact partition bitcode,
		// which contains every imported body, remains the cache authority.
		g.partitionImportDefaults = true
		// LLVM retains GlobalValue names while discarding non-semantic local SSA
		// names. Set this before lowering to avoid creating source-offset names.
		if !options.DebugInfo {
			C.LLVMContextSetDiscardValueNames(g.context, 1)
		}
	}, shared)
	partitionGenNanos.Add(int64(time.Since(irStart)))
	if err != nil {
		return nil, nil, fmt.Errorf("partition %d: %w", part, err)
	}
	defer g.disposeInBackground(mode.disposalScope)
	// These workers synthesize types that can flow into generic symbol names.
	// Until their cross-module identity is qualified, use whole-module lowering.
	if g.partUnsafeHelpers {
		if os.Getenv("ELISACORE_PARTITION_DECLINE_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "[partition-decline] part %d: partUnsafeHelpers\n", part)
		}
		return nil, nil, errPartitionDeclined
	}
	if mode.specializationOnly {
		for _, request := range mode.requests {
			if request.ownerPart != part {
				return nil, nil, errPartitionDeclined
			}
			switch request.kind {
			case partitionSpecializationFunction:
				if _, _, err := g.ensureSpecializedFunction(request.decl, request.base, request.bindings); err != nil {
					return nil, nil, err
				}
			case partitionSpecializationImplMethod:
				if _, _, _, err := g.ensureSpecializedImplMethod(request.decl, request.base, request.implTypeParams, request.impl, request.implMethod, request.bindings); err != nil {
					return nil, nil, err
				}
			default:
				return nil, nil, errPartitionDeclined
			}
		}
		if g.di != nil {
			g.di.finalize()
		}
		g.alignPackedRecordAccesses()
		if err := g.verify(); err != nil {
			return nil, nil, fmt.Errorf("partition %d specialization module: %w", part, err)
		}
	}
	if !finishPartitionModule(g.module, part, parts, unit, g.partLocalHelpers) {
		if os.Getenv("ELISACORE_PARTITION_DECLINE_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "[partition-decline] part %d: finishPartitionModule\n", part)
		}
		// Preserve the pre-cache verifier for a declined partition. Ordinary
		// partition modules defer only when they may reach the exact cache lookup.
		if g.deferPartitionCacheVerification {
			if err := g.verify(); err != nil {
				return nil, nil, fmt.Errorf("partition %d: %w", part, err)
			}
		}
		return nil, nil, errPartitionDeclined
	}
	if err := g.ensureTargetMachine(); err != nil {
		return nil, nil, err
	}
	if inputReads != nil {
		inputReads.mu.Lock()
		inputReads.lowered = true
		inputReads.mu.Unlock()
	}
	irDuration := time.Since(irStart)
	partitionIRNanos.Add(int64(irDuration))
	var bodies, defs int64
	var specializationDefinitions []string
	for f := C.LLVMGetFirstFunction(g.module); f != nil; f = C.LLVMGetNextFunction(f) {
		isDeclaration := C.LLVMIsDeclaration(f) != 0
		if !isDeclaration {
			defs++
			name := llvmValueName(f)
			if g.partSpecializationDefinitions[name] {
				specializationDefinitions = append(specializationDefinitions, name)
			}
			if s.disk != nil {
				*members = append(*members, partitionScheduleStem(name))
			}
		}
		if !isDeclaration && C.LLVMGetLinkage(f) != C.LLVMWeakODRLinkage {
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
	var objectKey [32]byte
	retainInput := inputSize <= s.maxBytes-len(configKey)
	if retainInput {
		objectKey = incrementalObjectKeyBytes(inputBytes, configKey)
		object = s.lookupObjectBytesWithKey(inputBytes, configKey, objectKey)
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
		// A hit is keyed by these exact bitcode bytes (which name the LLVM producer), and
		// only modules that passed verification are ever cached, so this module verified.
		return object, specializationDefinitions, nil
	}
	verifyStart := time.Now()
	if err := g.verify(); err != nil {
		return nil, nil, fmt.Errorf("partition %d: %w", part, err)
	}
	s.recordStats(func(stats *IncrementalObjectStats) { stats.IRTime += time.Since(verifyStart) })
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
		if s.diskCodegenFlag("ELISACORE_NATIVE_FRAGMENTS") && s.disk != nil {
			fragment = &nativeFragmentContext{request: nativeFragmentRequest{
				CodegenMode: machineCodegenMode(options.OptLevel),
				Root:        filepath.Join(s.disk.root, "native-fragments-v1"),
				Namespace:   fmt.Sprintf("%x.ir.%d", unitID, part), Target: configKey,
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
			return nil, nil, fmt.Errorf("partition %d: %w", part, err)
		}
	} else {
		var buffer C.LLVMMemoryBufferRef
		var message *C.char
		if C.elisacoreEmitPartitionObject(g.module, triple, cpu, features, C.int(machineCodegenMode(options.OptLevel)), &buffer, &message) == 0 {
			text := "native emission failed"
			if message != nil {
				text = C.GoString(message)
				C.free(unsafe.Pointer(message))
			}
			return nil, nil, fmt.Errorf("partition %d: %s", part, text)
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
		s.retainObjectForSlotWithKey(retainedInput, configKey, object, incrementalObjectSlot(unitID, targetID, mode.cachePart), objectKey)
	}
	return object, specializationDefinitions, nil
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
			if g.inputReads != nil {
				g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: g.result, Table: "ExprTypes", Operation: "overlay-lookup", Key: expr, Value: t, Present: t != nil})
			}
			return t, t != nil
		}
	}
	t, ok := g.semanticReadView().LookupExprTypes(expr)
	return t, ok
}

func (g *llvmGenerator) resultExprType(expr ast.Expr) semantic.Type {
	t, _ := g.resultExprTypeOK(expr)
	return t
}

func (g *llvmGenerator) setResultExprType(expr ast.Expr, t semantic.Type) {
	overlay := g.usesOverlay()
	if g.inputReads != nil {
		operation := "write"
		if overlay {
			operation = "overlay-write"
		}
		g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: g.result, Table: "ExprTypes", Operation: operation, Key: expr, Value: t, Present: true})
	}
	if !overlay {
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
	overlay := g.usesOverlay()
	if g.inputReads != nil {
		operation := "delete"
		if overlay {
			operation = "overlay-delete"
		}
		g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: g.result, Table: "ExprTypes", Operation: operation, Key: expr})
	}
	if !overlay {
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
	return g.semanticReadView().ValueIndexBoundsProven(expr)
}

func (g *llvmGenerator) setResultIndexBoundsProven(expr *ast.IndexExpr, proven bool) {
	// Synthesized codegen proofs have generator-local ownership, not a semantic
	// function owner. Never publish them into a retained Result: a later update
	// cannot revoke a detached synthesized proof by semantic declaration identity.
	if g.boundsOverlay == nil {
		g.boundsOverlay = map[*ast.IndexExpr]bool{}
	}
	previous, present := g.boundsOverlay[expr]
	g.boundsOverlay[expr] = proven
	// A replacement can change proof authority without changing map size.
	// Invalidate derived span indexes so rebuilt nodes cannot retain a proof
	// that the generator has subsequently removed.
	if (!present || previous != proven) && g.provenIndexSpansResult == g.result {
		g.provenIndexSpans = nil
	}
}

// Start likely misses early, so their native optimization overlaps generation
// of cache-hit units. Hints only affect scheduling; all units still generate
// and validate their full current bitcode before any cache lookup.
func partitionedIRWorkOrder(result *semantic.Result, parts int, previous ...[][]string) []int {
	return partitionedIRWorkOrderWithReads(result, parts, nil, previous...)
}

func partitionedIRWorkOrderWithReads(result *semantic.Result, parts int, recorder *irInputReadRecorder, previous ...[][]string) []int {
	preferred := make([]bool, parts)
	view := newIRCodegenReadView(result, recorder)
	for _, name := range result.CodegenFunctionPriorities() {
		if sym, ok := view.LookupGlobalSymbol(name, "partition-priority-symbol"); ok && sym != nil && sym.LinkName != "" {
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
	declined := make([]bool, parts)
	if len(previous) != 0 && len(previous[0]) == parts {
		for part, names := range previous[0] {
			for _, name := range names {
				if name == partitionScheduleDeclined {
					declined[part] = true
					break
				}
			}
		}
	}
	order := make([]int, 0, parts)
	for part, want := range declined {
		if want {
			order = append(order, part)
		}
	}
	for part, want := range preferred {
		if want && !declined[part] {
			order = append(order, part)
		}
	}
	rest := make([]int, 0, parts)
	for part, want := range preferred {
		if !want && !declined[part] {
			rest = append(rest, part)
		}
	}
	// Largest first, by the previous build's definitions per partition, so no big
	// partition starts last and stretches the wall time. Order never affects output.
	if len(previous) != 0 && len(previous[0]) == parts {
		sort.SliceStable(rest, func(i, j int) bool { return len(previous[0][rest[i]]) > len(previous[0][rest[j]]) })
	}
	return append(order, rest...)
}
