package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strconv"
)

func (a *Analyzer) analyzeMarkStmt(stmt *ast.MarkStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markType, ok := a.namedTypes["ArenaMark"]
	if !ok {
		a.errorf(stmt.Pos(), "missing builtin ArenaMark type for region checkpoints")
		markType = invalidType
	}
	markSym := &Symbol{Name: stmt.Name, Kind: SymbolRegionMark, Type: markType, Node: stmt, Mutable: false}
	a.defineLocal(markSym, stmt.Pos())
	state.Generation++
	a.setCurrentRegionState(regionSym, state)
	if a.currentRegionMarks == nil {
		a.currentRegionMarks = map[*Symbol]regionMarkState{}
	}
	a.currentRegionMarks[markSym] = regionMarkState{Region: regionSym, Generation: state.Generation, Valid: true}
}

func (a *Analyzer) restoreFromRegionCheckpoint(pos lexer.Pos, regionSym *Symbol, markSym *Symbol, markState regionMarkState) {
	state, ok := a.currentRegions[regionSym]
	if !ok {
		return
	}
	if !markState.Valid {
		a.errorf(pos, "checkpoint %q is invalid after %s", markSym.Name, markState.InvalidatedBy)
		return
	}
	before := state.Generation
	reason := fmt.Sprintf("restore of region %q from checkpoint %q", regionSym.Name, markSym.Name)
	a.invalidateRegionRefs(regionSym, func(dep regionDependencyState) bool {
		return dep.Generation >= markState.Generation
	}, reason)
	a.invalidateRegionMarks(regionSym, func(other regionMarkState) bool {
		return other.Generation > markState.Generation
	}, reason)
	state.Generation = markState.Generation
	a.setCurrentRegionState(regionSym, state)
	a.recordRegionInvalidateTransform(pos, regionSym.Name, markSym.Name, "restore region checkpoint", before, markState.Generation)
	if saved, ok := a.currentRegionMarks[markSym]; ok {
		saved.Valid = true
		saved.InvalidatedBy = ""
		a.currentRegionMarks[markSym] = saved
	}
}

func (a *Analyzer) analyzeCheckpointStmt(stmt *ast.CheckpointStmt) {
	if stmt == nil {
		return
	}
	scope := a.currentScope
	if len(stmt.Body) != 0 {
		scope = NewScope(a.currentScope)
	}
	binding, ok := a.bindCheckpointTarget(stmt.Pos(), stmt.Name, stmt.Target, scope, stmt)
	if !ok {
		if len(stmt.Body) != 0 {
			a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
		}
		return
	}
	if len(stmt.Body) != 0 {
		a.analyzeBlockWithAffineClone(stmt.Body, scope)
		if binding.regionSym != nil {
			a.restoreFromRegionCheckpoint(stmt.Pos(), binding.regionSym, binding.markSym, binding.regionMarkState)
		}
	}
}

type checkpointBindingHandle struct {
	markSym         *Symbol
	regionSym       *Symbol
	regionMarkState regionMarkState
}

func (a *Analyzer) bindCheckpointTarget(pos lexer.Pos, name string, target ast.Expr, scope *Scope, node ast.Node) (checkpointBindingHandle, bool) {
	if target == nil {
		return checkpointBindingHandle{}, false
	}
	targetType := a.analyzeExpr(target)
	if ident, ok := target.(*ast.Ident); ok {
		if regionSym, state := a.lookupRegionState(ident.Name); regionSym != nil {
			if state.Destroyed {
				a.errorf(pos, "region %q has already been destroyed", ident.Name)
				return checkpointBindingHandle{}, false
			}
			markType, ok := a.namedTypes["ArenaMark"]
			if !ok {
				a.errorf(pos, "missing builtin ArenaMark type for region checkpoints")
				markType = invalidType
			}
			markSym := &Symbol{Name: name, Kind: SymbolRegionMark, Type: markType, Node: node, Mutable: false}
			if scope != nil {
				a.defineLocalInScope(scope, markSym, pos)
			}
			state.Generation++
			a.setCurrentRegionState(regionSym, state)
			if a.currentRegionMarks == nil {
				a.currentRegionMarks = map[*Symbol]regionMarkState{}
			}
			markState := regionMarkState{Region: regionSym, Generation: state.Generation, Valid: true}
			a.currentRegionMarks[markSym] = markState
			return checkpointBindingHandle{markSym: markSym, regionSym: regionSym, regionMarkState: markState}, true
		}
	}
	if _, ok := checkpointDArraySourceType(targetType); !ok {
		a.errorf(target.Pos(), "checkpoint requires a region or mutable darray value, got %s", targetType)
		return checkpointBindingHandle{}, false
	}
	markSym := &Symbol{Name: name, Kind: SymbolCheckpoint, Type: a.namedTypes["usize"], Node: node, Mutable: false}
	if scope != nil {
		a.defineLocalInScope(scope, markSym, pos)
	}
	if a.currentCheckpoints == nil {
		a.currentCheckpoints = map[*Symbol]checkpointState{}
	}
	a.currentCheckpoints[markSym] = checkpointState{Kind: checkpointKindDArray, Target: target, TargetType: targetType, Valid: true}
	return checkpointBindingHandle{markSym: markSym}, true
}

func anonymousCheckpointName(pos lexer.Pos, index int) string {
	return fmt.Sprintf("__anon_checkpoint_%d_%d_%d", pos.Line, pos.Col, index)
}

func (a *Analyzer) analyzeGroupedCheckpointStmt(stmt *ast.GroupedCheckpointStmt) {
	if stmt == nil {
		return
	}
	regionRestores := make([]checkpointBindingHandle, 0, len(stmt.Targets))
	for i, target := range stmt.Targets {
		binding, ok := a.bindCheckpointTarget(stmt.Pos(), anonymousCheckpointName(stmt.Pos(), i), target, nil, stmt)
		if !ok {
			continue
		}
		if binding.regionSym != nil {
			regionRestores = append(regionRestores, binding)
		}
	}
	a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
	for i := len(regionRestores) - 1; i >= 0; i-- {
		binding := regionRestores[i]
		a.restoreFromRegionCheckpoint(stmt.Pos(), binding.regionSym, binding.markSym, binding.regionMarkState)
	}
}

func (a *Analyzer) analyzeRestoreStmt(stmt *ast.RestoreStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markSym, markState := a.lookupRegionMark(stmt.MarkName)
	if markSym == nil {
		a.errorf(stmt.Pos(), "undefined checkpoint %q", stmt.MarkName)
		return
	}
	if markState.Region != regionSym {
		a.errorf(stmt.Pos(), "checkpoint %q belongs to region %q, not %q", stmt.MarkName, markState.Region.Name, stmt.RegionName)
		return
	}
	if !markState.Valid {
		a.errorf(stmt.Pos(), "checkpoint %q is invalid after %s", stmt.MarkName, markState.InvalidatedBy)
		return
	}
	a.restoreFromRegionCheckpoint(stmt.Pos(), regionSym, markSym, markState)
}

func (a *Analyzer) analyzeRestoreCheckpointStmt(stmt *ast.RestoreCheckpointStmt) {
	if stmt == nil {
		return
	}
	if markSym, markState := a.lookupRegionMark(stmt.Name); markSym != nil {
		a.restoreFromRegionCheckpoint(stmt.Pos(), markState.Region, markSym, markState)
		return
	}
	markSym, state := a.lookupCheckpoint(stmt.Name)
	if markSym == nil {
		a.errorf(stmt.Pos(), "undefined checkpoint %q", stmt.Name)
		return
	}
	if !state.Valid {
		a.errorf(stmt.Pos(), "checkpoint %q is invalid after %s", stmt.Name, state.InvalidatedBy)
	}
}

func (a *Analyzer) analyzeResetStmt(stmt *ast.ResetStmt) {
	regionSym, state := a.lookupRegionState(stmt.Name)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.Name)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.Name)
		return
	}
	reason := fmt.Sprintf("reset of region %q", stmt.Name)
	before := state.Generation
	a.invalidateRegionRefs(regionSym, func(regionDependencyState) bool { return true }, reason)
	a.invalidateRegionMarks(regionSym, func(regionMarkState) bool { return true }, reason)
	state.Generation = 0
	a.setCurrentRegionState(regionSym, state)
	a.recordRegionInvalidateTransform(stmt.Pos(), stmt.Name, "", "reset region", before, 0)
}

func (a *Analyzer) recordRegionInvalidateTransform(pos lexer.Pos, regionName string, checkpointName string, operation string, before int, after int) {
	if a == nil || regionName == "" {
		return
	}
	details := []FactTransformDetail{{Name: "operation", Value: operation}}
	if checkpointName != "" {
		details = append(details, FactTransformDetail{Name: "checkpoint", Value: checkpointName})
	}
	details = append(details, FactTransformDetail{Name: "generation_before", Value: strconv.Itoa(before)})
	afterText := strconv.Itoa(after)
	if after < 0 {
		afterText = "destroyed"
	}
	details = append(details, FactTransformDetail{Name: "generation_after", Value: afterText})
	a.currentRegionFactTransforms = append(a.currentRegionFactTransforms, FactTransform{
		Kind:       FactTransformInvalidate,
		Classes:    []FactClass{FactRegionDeps},
		Target:     regionName,
		Source:     checkpointName,
		SourcePos:  pos,
		SourceKind: FactSourceRegion,
		Details:    details,
		Reason:     operation,
	})
}
