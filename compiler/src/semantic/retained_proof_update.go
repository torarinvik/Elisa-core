package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

// prepareRetainedProofUpdate checks report dependencies and stages coordinate relocation.
func (r *RetainedAnalysis) prepareRetainedProofUpdate(file *ast.File, newDecls []ast.Decl, paired retainedDeclarationPairing, comparison *retainedComparison, liveFns map[fnKey][]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl) (bool, func(*Analyzer) string, error) {
	added, removed := comparison.added, comparison.removed
	addedConsts, removedConsts := paired.addedConsts, paired.removedConsts
	addedStructs, removedStructs := paired.addedStructs, paired.removedStructs
	usingsAdded, usingsRemoved := paired.usingsAdded, paired.usingsRemoved
	splices, shadowEdits := comparison.splices, comparison.shadowEdits
	pairedNew, pairedOld := comparison.pairedNew, comparison.pairedOld
	structPairs, constPairs, constEnumPairs := comparison.structPairs, comparison.constPairs, comparison.constEnumPairs
	enumPairs, hierarchyPairs := comparison.enumPairs, comparison.hierarchyPairs
	marks, newTexts := comparison.marks, comparison.newTexts
	reportDeclarationImport := false
	var relocateProofReport func(*Analyzer) string
	if r.a != nil {
		changedOwners := make([]*ast.FuncDecl, 0, len(splices))
		for _, edit := range splices {
			changedOwners = append(changedOwners, edit.live)
		}
		declarationChange := len(added)+len(removed)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+usingsAdded+usingsRemoved != 0
		onlyFunctionRemovals := len(removed) != 0 && len(added)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+usingsAdded+usingsRemoved == 0
		removedOwners := make([]*ast.FuncDecl, 0, len(removed))
		if onlyFunctionRemovals {
			for _, old := range removed {
				candidates := liveFns[fnKey{retainedLineKey(old.Pos()), old.Name}]
				if len(candidates) != 1 {
					return false, nil, fmt.Errorf("proof report removed owner cannot be paired")
				}
				removedOwners = append(removedOwners, candidates[0])
			}
			declarationChange = false
		}
		proofBodies, why := r.proofReportBodyReanalysisClosure(changedOwners, removedOwners)
		if why != "" {
			return false, nil, fmt.Errorf("%s", why)
		}
		if len(proofBodies) != 0 {
			// Traverse independently parsed declarations in source order, using
			// ONLY the already-established exact live/fresh pairing. No source
			// position, function spelling or fact-table key selects an owner.
			liveOf := map[*ast.FuncDecl]*ast.FuncDecl{}
			for live, fresh := range pairedNew {
				liveOf[fresh] = live
			}
			for _, decl := range newDecls {
				fresh, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				live := liveOf[fresh]
				if !proofBodies[live] {
					continue
				}
				old := pairedOld[live]
				if old == nil {
					return false, nil, fmt.Errorf("proof report dependent body has no exact baseline parse")
				}
				if _, known := scoped[live]; !known {
					return false, nil, fmt.Errorf("proof report dependent body is not independently analyzable")
				}
				splices = append(splices, retainedSplice{live, fresh})
				shadowEdits = append(shadowEdits, [3]*ast.FuncDecl{live, old, fresh})
				newTexts[fresh] = retainedText(fresh)
				changedOwners = append(changedOwners, live)
				delete(proofBodies, live)
			}
			if len(proofBodies) != 0 {
				return false, nil, fmt.Errorf("proof report dependent body has no exact fresh source counterpart")
			}
		}
		reportDeclarationImport = declarationChange
		reportLocationsChanged := len(marks) != 0
		for live, fresh := range pairedNew {
			if fresh.Pos().Offset != live.Pos().Offset {
				reportLocationsChanged = true
			}
		}
		if reportLocationsChanged && len(r.a.proofReport) != 0 && r.DeferProofReportCoordinates {
			r.proofCoordinatesDeferred = true
		} else if reportLocationsChanged && len(r.a.proofReport) != 0 {
			var why string
			relocateProofReport, why = r.prepareProofReportCoordinates(file, marks)
			if why != "" {
				return false, nil, fmt.Errorf("proof report source relocation: %s", why)
			}
		}
		// A complete coordinate adapter replaces the location-only admission
		// refusal. Dependency and source-journal admission remain authoritative.
		if why := r.admitProofReportUpdate(changedOwners, removedOwners, false, declarationChange); why != "" {
			return false, nil, fmt.Errorf("%s", why)
		}
	}
	comparison.splices, comparison.shadowEdits = splices, shadowEdits
	return reportDeclarationImport, relocateProofReport, nil
}
