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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
	if options.OptLevel != OptimizationLevel0 && (!s.diskCodegenFlag("ELISACORE_PARALLEL_IR") ||
		!s.diskCodegenFlag("ELISACORE_PARALLEL_OPT") || parallelOptImportWeight() != 0 || !partitionWorkersAvailable) {
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

type partitionIRSupplementalObject struct {
	owner       int
	object      []byte
	definitions []string
}

// buildQueuedPartitionSpecializations drains generic bodies discovered during
// ordinary partition lowering. Each supplemental module emits only bodies
// owned by one partition; requests discovered while lowering those bodies are
// picked up in the next round until the shared queue reaches a fixed point.
func (s *IncrementalObjectSession) buildQueuedPartitionSpecializations(result *semantic.Result, options LLVMObjectEmitOptions, parts int, fastCodegen bool, configKey string, unitID, targetID [32]byte, shared *sharedCodegenLookups, queue *partitionSpecializationQueue, definedByPart []map[string]bool, members [][]string, audit *partitionIRInputAuditCollector, previousMembers [][]string, disposalScope *llvmDisposalScope) ([]partitionIRSupplementalObject, error) {
	if queue == nil {
		return nil, nil
	}
	if result == nil || parts < 1 || len(definedByPart) != parts || len(members) != parts {
		return nil, errPartitionDeclined
	}
	var supplemental []partitionIRSupplementalObject
	for round := 0; ; round++ {
		pending := missingPartitionSpecializations(queue.snapshot(), definedByPart)
		if len(pending) == 0 {
			return supplemental, nil
		}
		byOwner := make([][]partitionSpecializationRequest, parts)
		for _, request := range pending {
			if request.ownerPart < 0 || request.ownerPart >= parts {
				return nil, errPartitionDeclined
			}
			byOwner[request.ownerPart] = append(byOwner[request.ownerPart], request)
		}
		ownerOrder := make([]int, 0, len(pending))
		for owner, requests := range byOwner {
			if len(requests) != 0 {
				ownerOrder = append(ownerOrder, owner)
			}
		}
		roundModules := make([]partitionIRSupplementalObject, parts)
		roundPresent := make([]bool, parts)
		var declinedOwner atomic.Int64
		declinedOwner.Store(-1)
		workers := runtime.GOMAXPROCS(0)
		if workers > len(ownerOrder) {
			workers = len(ownerOrder)
		}
		err := runPartitionIRWorkers(parts, workers, ownerOrder, func(owner int) error {
			requests := byOwner[owner]
			mode := partitionIRBuildMode{
				queue:              queue,
				requests:           requests,
				previouslyDefined:  definedByPart[owner],
				specializationOnly: true,
				cachePart:          parts + owner,
				supplementRound:    round,
				disposalScope:      disposalScope,
			}
			if audit != nil {
				mode.inputAudit = audit.record
			}
			object, definitions, err := s.buildIRPartitionWithMode(result, options, owner, parts, fastCodegen, configKey, unitID, targetID, nil, &members[owner], shared, mode)
			if err != nil {
				if errors.Is(err, errPartitionDeclined) {
					declinedOwner.CompareAndSwap(-1, int64(owner))
				}
				return err
			}
			roundModules[owner] = partitionIRSupplementalObject{owner: owner, object: object, definitions: definitions}
			roundPresent[owner] = true
			return nil
		})
		if err != nil {
			if errors.Is(err, errPartitionDeclined) {
				if owner := int(declinedOwner.Load()); owner >= 0 {
					s.recordDeclinedPartitionSchedule(unitID, targetID, parts, previousMembers, owner)
				}
			}
			return nil, err
		}
		for owner, module := range roundModules {
			if !roundPresent[owner] {
				continue
			}
			requests := byOwner[owner]
			defined := make(map[string]bool, len(module.definitions))
			for _, name := range module.definitions {
				defined[name] = true
			}
			for _, request := range requests {
				if !defined[request.name] {
					s.recordDeclinedPartitionSchedule(unitID, targetID, parts, previousMembers, owner)
					return nil, errPartitionDeclined
				}
			}
			if definedByPart[owner] == nil {
				definedByPart[owner] = make(map[string]bool)
			}
			for _, name := range module.definitions {
				definedByPart[owner][name] = true
			}
			supplemental = append(supplemental, module)
		}
	}
}

func (s *IncrementalObjectSession) recordDeclinedPartitionSchedule(unitID, targetID [32]byte, parts int, previous [][]string, owner int) {
	if s == nil || s.disk == nil || os.Getenv("ELISACORE_PARTITION_SCHEDULE") == "0" || owner < 0 || owner >= parts {
		return
	}
	if len(previous) != parts {
		previous = make([][]string, parts)
	} else {
		previous = append([][]string(nil), previous...)
	}
	for part, names := range previous {
		filtered := names[:0]
		for _, name := range names {
			if name != partitionScheduleDeclined {
				filtered = append(filtered, name)
			}
		}
		previous[part] = filtered
	}
	previous[owner] = append(previous[owner], partitionScheduleDeclined)
	s.disk.storeSchedule(unitID, targetID, previous)
}

// partitionNativeNameReserved is used only by the opt-in submit experiment.
// Every source/link/export spelling may be deliberately chosen to collide.
func (g *llvmGenerator) partitionNativeNameReserved(name string) bool {
	if g == nil || g.result == nil || g.result.GlobalScope == nil || len(g.result.EASMModules) != 0 {
		return true
	}
	for source, symbol := range g.codegenReadView().GlobalSymbolsMap("partition-synthetic-specialization-reservations") {
		if source == name || symbol != nil && (symbol.Name == name || symbol.LinkName == name) {
			return true
		}
	}
	for _, export := range g.result.ExportedFuncs {
		if export != nil && (export.PublicName == name || export.LinkName == name) {
			return true
		}
	}
	for _, export := range g.result.ExportedGlobals {
		if export != nil && export.PublicName == name {
			return true
		}
	}
	return false
}

// partitionHelperNamespaceConflicts rejects caller-controlled native symbols
// in the generated helper namespace. EASM can supply arbitrary native symbols
// that are not represented in GlobalScope, so it must use whole-module IR.
func partitionHelperNamespaceConflicts(result *semantic.Result, unitID [32]byte, reads *irInputReadRecorder) bool {
	if result == nil || result.GlobalScope == nil || len(result.EASMModules) != 0 {
		return true
	}
	prefix := fmt.Sprintf("elisa.partition.%x.", unitID)
	for name, symbol := range newIRCodegenReadView(result, reads).GlobalSymbolsMap("partition-helper-symbols") {
		if strings.HasPrefix(name, prefix) {
			return true
		}
		if symbol != nil && (strings.HasPrefix(symbol.Name, prefix) || strings.HasPrefix(symbol.LinkName, prefix)) {
			return true
		}
	}
	for _, export := range result.ExportedFuncs {
		if export != nil && (strings.HasPrefix(export.PublicName, prefix) || strings.HasPrefix(export.LinkName, prefix)) {
			return true
		}
	}
	for _, export := range result.ExportedGlobals {
		if export != nil && strings.HasPrefix(export.PublicName, prefix) {
			return true
		}
	}
	return false
}

// runPartitionIRWorkers stops scheduling new partitions after a decline, but
// joins every started worker before the shared semantic view can be released.
func runPartitionIRWorkers(parts, workers int, order []int, build func(int) error) error {
	errs := make([]error, parts)
	next := make(chan int, len(order))
	for _, part := range order {
		next <- part
	}
	close(next)
	var wg sync.WaitGroup
	var declined atomic.Bool
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for part := range next {
				if declined.Load() {
					continue
				}
				errs[part] = build(part)
				if errors.Is(errs[part], errPartitionDeclined) {
					declined.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, errPartitionDeclined) {
			return err
		}
	}
	if declined.Load() {
		return errPartitionDeclined
	}
	return nil
}

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
		g.partitionImportDefaults = s.diskDefaultsActive()
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
			return nil, nil, fmt.Errorf("partition %d: %w", part, err)
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
		s.retainObjectForSlot(retainedInput, configKey, object, incrementalObjectSlot(unitID, targetID, mode.cachePart))
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
	return g.semanticReadView().ValueIndexBoundsProven(expr)
}

func (g *llvmGenerator) setResultIndexBoundsProven(expr *ast.IndexExpr, proven bool) {
	// Synthesized codegen proofs have generator-local ownership, not a semantic
	// function owner. Never publish them into a retained Result: a later update
	// cannot revoke a detached synthesized proof by semantic declaration identity.
	if g.boundsOverlay == nil {
		g.boundsOverlay = map[*ast.IndexExpr]bool{}
	}
	g.boundsOverlay[expr] = proven
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
	for part, want := range preferred {
		if !want && !declined[part] {
			order = append(order, part)
		}
	}
	return order
}
