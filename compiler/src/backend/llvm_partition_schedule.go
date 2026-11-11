//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

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
	if g == nil || g.result == nil || !newIRCodegenReadView(g.result, nil).HasGlobalScope() || len(g.result.EASMModules) != 0 {
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
	if os.Getenv("ELISACORE_PARTITION_DECLINE_DEBUG") == "1" && result != nil {
		fmt.Fprintf(os.Stderr, "[partition-decline] namespace check: globalScope=%v easmModules=%d\n", newIRCodegenReadView(result, nil).HasGlobalScope(), len(result.EASMModules))
	}
	if result == nil || !newIRCodegenReadView(result, nil).HasGlobalScope() || len(result.EASMModules) != 0 {
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
