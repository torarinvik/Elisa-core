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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"
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
	hash := uint64(14695981039346656037)
	for i := 0; i < len(name); i++ {
		hash ^= uint64(name[i])
		hash *= 1099511628211
	}
	return int(hash % uint64(parts))
}

func memoizedPartitionBody(part, parts int, previouslyDefined map[string]bool) func(string) bool {
	owners := make(map[string]bool)
	return func(name string) bool {
		if owned, ok := owners[name]; ok {
			return owned
		}
		owned := partitionOwner(name, parts) == part && !previouslyDefined[name]
		owners[name] = owned
		return owned
	}
}

// partitionIRDeclarationWorklists preserves every shared declaration in every
// partition while placing ordinary function bodies only in their owner list.
// Namespace, static-if, and impl wrappers are copied with their children
// partitioned so each generator can keep using its normal declaration logic.
// Functions whose final LLVM symbol needs generator-specific ABI resolution
// remain in every list; the generator's existing owner check stays authoritative
// for those uncommon cases.
func partitionIRDeclarationWorklists(result *semantic.Result, symbols map[ast.Node]*semantic.Symbol, parts int, recorders ...*irInputReadRecorder) [][]ast.Decl {
	if result == nil || result.ActiveFile() == nil || parts < 1 {
		return nil
	}
	var recorder *irInputReadRecorder
	if len(recorders) != 0 {
		recorder = recorders[0]
	}
	view := newIRCodegenReadView(result, recorder)
	worklists := make([][]ast.Decl, parts)
	sameNameExports := make(map[string]struct{}, len(result.ExportedFuncs))
	for _, exported := range result.ExportedFuncs {
		if exported != nil && len(exported.TargetBindings) == 0 && exported.PublicName == exported.TargetName {
			sameNameExports[exported.TargetName] = struct{}{}
		}
	}
	for _, decl := range result.ActiveFile().Decls {
		appendPartitionIRDecl(worklists, decl, result, symbols, sameNameExports, view)
	}
	return worklists
}

func partitionIRFunctionOwner(result *semantic.Result, symbols map[ast.Node]*semantic.Symbol, sameNameExports map[string]struct{}, decl *ast.FuncDecl, parts int, view irCodegenReadView) (int, bool) {
	if result == nil || !view.HasGlobalScope() || decl == nil || parts < 1 {
		return 0, false
	}
	sym := symbols[decl]
	if sym == nil || sym.Name == "" || strings.TrimSpace(sym.LinkName) != "" {
		return 0, false
	}
	if fn, ok := sym.Type.(*semantic.FuncType); ok && fn.IntrinsicName != "" {
		return 0, false
	}
	if _, sameNameExport := sameNameExports[sym.Name]; sameNameExport {
		return 0, false
	}
	resolved, ok := view.LookupGlobalSymbol(sym.Name, "partition-function-owner")
	if !ok || resolved == nil || resolved.Node != decl || strings.TrimSpace(resolved.LinkName) != "" {
		return 0, false
	}
	return partitionOwner(sym.Name, parts), true
}

func partitionIRDeclBranch(decls []ast.Decl, result *semantic.Result, symbols map[ast.Node]*semantic.Symbol, sameNameExports map[string]struct{}, parts int, view irCodegenReadView) [][]ast.Decl {
	branches := make([][]ast.Decl, parts)
	for _, decl := range decls {
		appendPartitionIRDecl(branches, decl, result, symbols, sameNameExports, view)
	}
	return branches
}

func appendPartitionIRDecl(worklists [][]ast.Decl, decl ast.Decl, result *semantic.Result, symbols map[ast.Node]*semantic.Symbol, sameNameExports map[string]struct{}, view irCodegenReadView) {
	parts := len(worklists)
	if parts == 0 || decl == nil {
		return
	}
	appendEverywhere := func(decl ast.Decl) {
		for part := range parts {
			worklists[part] = append(worklists[part], decl)
		}
	}
	switch n := decl.(type) {
	case *ast.FuncDecl:
		if owner, exact := partitionIRFunctionOwner(result, symbols, sameNameExports, n, parts, view); exact {
			worklists[owner] = append(worklists[owner], decl)
			return
		}
		appendEverywhere(decl)
	case *ast.NamespaceDecl:
		children := partitionIRDeclBranch(n.Decls, result, symbols, sameNameExports, parts, view)
		for part := range parts {
			copy := *n
			copy.Decls = children[part]
			worklists[part] = append(worklists[part], &copy)
		}
	case *ast.StaticIfDecl:
		thenDecls := partitionIRDeclBranch(n.Then, result, symbols, sameNameExports, parts, view)
		elifDecls := make([][][]ast.Decl, len(n.Elifs))
		for index, branch := range n.Elifs {
			elifDecls[index] = partitionIRDeclBranch(branch.Body, result, symbols, sameNameExports, parts, view)
		}
		elseDecls := partitionIRDeclBranch(n.Else, result, symbols, sameNameExports, parts, view)
		for part := range parts {
			copy := *n
			copy.Then = thenDecls[part]
			copy.Else = elseDecls[part]
			copy.Elifs = append([]ast.StaticElifDecl(nil), n.Elifs...)
			for index := range copy.Elifs {
				copy.Elifs[index].Body = elifDecls[index][part]
			}
			worklists[part] = append(worklists[part], &copy)
		}
	case *ast.ImplDecl:
		members := make([][]ast.ImplMember, parts)
		for _, member := range n.Members {
			if fn, ok := member.(*ast.FuncDecl); ok {
				if owner, exact := partitionIRFunctionOwner(result, symbols, sameNameExports, fn, parts, view); exact {
					members[owner] = append(members[owner], member)
					continue
				}
			}
			for part := range parts {
				members[part] = append(members[part], member)
			}
		}
		for part := range parts {
			copy := *n
			copy.Members = members[part]
			worklists[part] = append(worklists[part], &copy)
		}
	default:
		appendEverywhere(decl)
	}
}

// finishPartitionModule gives the partition's definitions their final
// linkage. It reports false when a symbol's identity cannot be matched across
// partitions; the caller then uses the module-split path.
func finishPartitionModule(m C.LLVMModuleRef, part, parts int, unit string, localHelpers ...map[string]bool) bool {
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
			// Only helpers explicitly generated in this module are local.
			if len(localHelpers) != 0 && localHelpers[0][llvmValueName(f)] {
				continue
			}
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
	// Partitioned generators import small foreign callees themselves
	// (importPartitionFunction). ELISACORE_PARALLEL_OPT_IMPORT configures only the
	// whole-module split, which remains the fallback when a partition declines.
	if options.OptLevel != OptimizationLevel0 && (!s.diskCodegenFlag("ELISACORE_PARALLEL_IR") ||
		!s.diskCodegenFlag("ELISACORE_PARALLEL_OPT") || !partitionWorkersAvailable) {
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
func (s *IncrementalObjectSession) writePartitionedObject(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions, configKey string, unitID, targetID [32]byte, disposalScope *llvmDisposalScope) error {
	start := time.Now()
	parts := s.partitions
	fastCodegen := options.OptLevel == OptimizationLevel0
	result.PackedLowering = options.PackedProfile.metadata()
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	var sharedReads *irInputReadRecorder
	if s.irInputAudit != nil {
		sharedReads = &irInputReadRecorder{}
	}
	if partitionHelperNamespaceConflicts(result, unitID, sharedReads) {
		return errPartitionDeclined
	}
	shared := newSharedCodegenLookups(result, sharedReads)
	declarations := partitionIRDeclarationWorklists(result, shared.symbolsByNode, parts, sharedReads)
	objects := make([][]byte, parts)
	members := make([][]string, parts)
	definedSpecializations := make([]map[string]bool, parts)
	var specializationQueue *partitionSpecializationQueue
	if options.OptLevel == OptimizationLevel0 {
		specializationQueue = newPartitionSpecializationQueue()
	}
	var auditCollector *partitionIRInputAuditCollector
	if s.irInputAudit != nil {
		auditCollector = newPartitionIRInputAuditCollector(parts)
		defer auditCollector.flush(s.irInputAudit)
	}
	var previousMembers [][]string
	if os.Getenv("ELISACORE_PARTITION_SCHEDULE") != "0" {
		previousMembers = s.disk.loadSchedule(unitID, targetID, parts)
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > parts {
		workers = parts
	}
	order := partitionedIRWorkOrderWithReads(result, parts, sharedReads, previousMembers)
	var declinedPart atomic.Int64
	if err := runPartitionIRWorkers(parts, workers, order, func(part int) error {
		var err error
		var definitions []string
		mode := partitionIRBuildMode{queue: specializationQueue, cachePart: part, disposalScope: disposalScope}
		if auditCollector != nil {
			mode.inputAudit = auditCollector.record
		}
		objects[part], definitions, err = s.buildIRPartitionWithMode(result, options, part, parts, fastCodegen, configKey, unitID, targetID, declarations[part], &members[part], shared, mode)
		if err == nil && len(definitions) != 0 {
			definedSpecializations[part] = make(map[string]bool, len(definitions))
			for _, name := range definitions {
				definedSpecializations[part][name] = true
			}
		}
		if errors.Is(err, errPartitionDeclined) {
			declinedPart.CompareAndSwap(0, int64(part+1))
		}
		return err
	}); err != nil {
		if errors.Is(err, errPartitionDeclined) && os.Getenv("ELISACORE_PARTITION_SCHEDULE") != "0" {
			if len(previousMembers) != parts {
				previousMembers = make([][]string, parts)
			}
			for part, names := range previousMembers {
				filtered := names[:0]
				for _, name := range names {
					if name != partitionScheduleDeclined {
						filtered = append(filtered, name)
					}
				}
				previousMembers[part] = filtered
			}
			if part := int(declinedPart.Load()) - 1; part >= 0 && part < parts {
				previousMembers[part] = append(previousMembers[part], partitionScheduleDeclined)
				s.disk.storeSchedule(unitID, targetID, previousMembers)
			}
		}
		return err
	}
	if specializationQueue != nil {
		supplemental, err := s.buildQueuedPartitionSpecializations(result, options, parts, fastCodegen, configKey, unitID, targetID, shared, specializationQueue, definedSpecializations, members, auditCollector, previousMembers, disposalScope)
		if err != nil {
			return err
		}
		for _, module := range supplemental {
			objects = append(objects, module.object)
		}
	}
	s.disk.storeSchedule(unitID, targetID, members)
	buildDuration := time.Since(start)
	dir, err := os.MkdirTemp(filepath.Dir(outputPath), ".elisa-object-parts-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	files := make([]string, len(objects))
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
