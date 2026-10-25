package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A plain source coordinate transition pairs exact syntax field paths. It never
// estimates byte offsets from line deltas or treats a nearby span as a producer.
// The plan is invocation-local; no live syntax or semantic value changes until
// the surrounding plain-default transaction has activated.
type plainCoordinateWrite struct {
	field    reflect.Value
	position lexer.Pos
}
type plainCoordinateSignature struct {
	typ       *FuncType
	signature *SpecSignature
}
type plainCoordinatePlan struct {
	syntax     []plainCoordinateWrite
	signatures []plainCoordinateSignature
	analyses   []*FunctionAnalysis
}

func (r *RetainedAnalysis) planPlainSourceCoordinates(pairs map[*ast.FuncDecl]*ast.FuncDecl, bodyChanged map[*ast.FuncDecl]bool) (*plainCoordinatePlan, error) {
	plan := &plainCoordinatePlan{}
	for live, fresh := range pairs {
		if retainedHeader(live) != retainedHeader(fresh) {
			continue
		} // replacement has fresh syntax/types
		if err := plainDefaultProductShape(r.a, live); err != nil {
			return nil, err
		}
		if err := plainDefaultFunctionSyntaxShape(r.a, fresh); err != nil {
			return nil, err
		}
		var pair func(reflect.Value, reflect.Value, string) error
		pair = func(old, next reflect.Value, path string) error {
			if old.Type() != next.Type() {
				return fmt.Errorf("coordinate syntax type differs at %s", path)
			}
			if old.Type() == reflect.TypeFor[lexer.Pos]() {
				if !old.CanSet() {
					return fmt.Errorf("coordinate syntax is not writable at %s", path)
				}
				plan.syntax = append(plan.syntax, plainCoordinateWrite{old, next.Interface().(lexer.Pos)})
				return nil
			}
			switch old.Kind() {
			case reflect.Interface, reflect.Pointer:
				if old.IsNil() != next.IsNil() {
					return fmt.Errorf("coordinate syntax presence differs at %s", path)
				}
				if !old.IsNil() {
					return pair(old.Elem(), next.Elem(), path+".*")
				}
			case reflect.Struct:
				for i := 0; i < old.NumField(); i++ {
					field := old.Type().Field(i).Name
					if old.Type() == reflect.TypeFor[ast.FuncDecl]() && field == "Body" && bodyChanged[live] {
						continue
					}
					if err := pair(old.Field(i), next.Field(i), path+"."+field); err != nil {
						return err
					}
				}
			case reflect.Slice, reflect.Array:
				if old.Len() != next.Len() {
					return fmt.Errorf("coordinate syntax length differs at %s", path)
				}
				for i := 0; i < old.Len(); i++ {
					if err := pair(old.Index(i), next.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
						return err
					}
				}
			case reflect.Map:
				if old.Len() != 0 || next.Len() != 0 {
					return fmt.Errorf("coordinate syntax map unsupported at %s", path)
				}
			default:
				if !reflect.DeepEqual(old.Interface(), next.Interface()) {
					return fmt.Errorf("coordinate syntax value differs at %s", path)
				}
			}
			return nil
		}
		if err := pair(reflect.ValueOf(live), reflect.ValueOf(fresh), live.Name); err != nil {
			return nil, err
		}
		symbol := r.a.funcDeclSymbols[live]
		if symbol == nil {
			return nil, fmt.Errorf("coordinate owner has no callable symbol")
		}
		typ, ok := symbol.Type.(*FuncType)
		if !ok || typ.SpecSignature == nil {
			return nil, fmt.Errorf("coordinate owner has no verification signature")
		}
		before := typ.SpecSignature
		after := cloneSpecSignature(before)
		if len(before.Params) != len(live.Params) {
			return nil, fmt.Errorf("coordinate binder count differs")
		}
		for i, param := range live.Params {
			if before.Params[i].SourcePos != param.Position || before.Params[i].SourceType != param.Type {
				return nil, fmt.Errorf("coordinate parameter producer differs")
			}
			after.Params[i].SourcePos = fresh.Params[i].Position
		}
		oldPosition, newPosition := lexer.Pos{}, lexer.Pos{}
		if len(live.Params) > 0 {
			oldPosition, newPosition = live.Params[0].Position, fresh.Params[0].Position
		} else if live.ReturnType != nil {
			oldPosition, newPosition = live.ReturnType.Pos(), fresh.ReturnType.Pos()
		}
		if before.Position != oldPosition {
			return nil, fmt.Errorf("coordinate signature producer differs")
		}
		after.Position = newPosition
		if live.ReturnType != nil {
			if before.Result == nil || before.Result.SourcePos != live.ReturnType.Pos() || before.Result.SourceType != live.ReturnType {
				return nil, fmt.Errorf("coordinate result producer differs")
			}
			after.Result.SourcePos = fresh.ReturnType.Pos()
		} else if before.Result != nil {
			return nil, fmt.Errorf("coordinate result has no producer")
		}
		plan.signatures = append(plan.signatures, plainCoordinateSignature{typ, after})
		if !bodyChanged[live] {
			if err := plan.planFunctionAnalysis(r.a.functionAnalyses[live], live, fresh); err != nil {
				return nil, err
			}
		}
	}
	return plan, nil
}

func (plan *plainCoordinatePlan) apply() {
	if plan == nil {
		return
	}
	for _, write := range plan.syntax {
		write.field.Set(reflect.ValueOf(write.position))
	}
	for _, write := range plan.signatures {
		write.typ.SpecSignature = write.signature
	}
	for _, analysis := range plan.analyses {
		analysis.FactExitSummary = buildFunctionFactExitSummary(analysis.FactTransforms)
	}
}

// The closed body has exactly one scalar return, so its CFG producers can be
// reconstructed without semantic analysis or proof queries. Validate complete
// instructions/transforms before retaining their field addresses for mutation.
func (plan *plainCoordinatePlan) planFunctionAnalysis(analysis *FunctionAnalysis, live, fresh *ast.FuncDecl) error {
	if analysis == nil || analysis.CFG == nil {
		return fmt.Errorf("coordinate owner has no CFG")
	}
	oldCFG, newCFG := ConstructCFG(live), ConstructCFG(fresh)
	populateBasicFlowInstrs(oldCFG)
	populateBasicFlowInstrs(newCFG)
	cfg := analysis.CFG
	if cfg.Entry != oldCFG.Entry || !reflect.DeepEqual(cfg.ExitBlocks, oldCFG.ExitBlocks) || !reflect.DeepEqual(cfg.ParamLocations, oldCFG.ParamLocations) || len(cfg.Blocks) != len(oldCFG.Blocks) {
		return fmt.Errorf("coordinate CFG shape differs from source producer")
	}
	for i := range cfg.Blocks {
		block, old, freshBlock := &cfg.Blocks[i], &oldCFG.Blocks[i], &newCFG.Blocks[i]
		if block.ID != old.ID || !reflect.DeepEqual(block.Nodes, old.Nodes) || !reflect.DeepEqual(block.Edges, old.Edges) || !reflect.DeepEqual(block.Instrs, old.Instrs) || block.Terminator != old.Terminator {
			return fmt.Errorf("coordinate CFG block differs from source producer")
		}
		plan.syntax = append(plan.syntax, plainCoordinateWrite{reflect.ValueOf(&block.Terminator.Position).Elem(), freshBlock.Terminator.Position})
		for j := range block.Instrs {
			instr := block.Instrs[j]
			if instr.Kind != FlowInstrReturn {
				return fmt.Errorf("coordinate CFG instruction lacks closed return producer")
			}
			plan.syntax = append(plan.syntax, plainCoordinateWrite{reflect.ValueOf(&block.Instrs[j].Position).Elem(), freshBlock.Instrs[j].Position})
		}
	}
	// Reproduce the complete descriptive producer sequences, including truly
	// producerless alias descriptions. No zero position is an exemption: a return
	// with its SourcePos cleared, an omitted occurrence, or an extra duplicate
	// must differ from these exact producer outputs.
	oldTransforms := populateCFGBlockFactTransforms(oldCFG)
	newTransforms := populateCFGBlockFactTransforms(newCFG)
	aliases := computeGraphPartitionsFromCFG(oldCFG).AliasSets()
	if !reflect.DeepEqual(aliases, analysis.AliasSets) {
		return fmt.Errorf("coordinate alias producers differ")
	}
	oldAll := dedupeAndSortFactTransforms(append(oldTransforms, factTransformsFromAliasSets(aliases)...))
	newAll := dedupeAndSortFactTransforms(append(newTransforms, factTransformsFromAliasSets(aliases)...))
	transforms := func(values, before, after []FactTransform) error {
		if !reflect.DeepEqual(values, before) || len(values) != len(after) {
			return fmt.Errorf("coordinate complete fact producer sequence differs")
		}
		for i := range values {
			plan.syntax = append(plan.syntax, plainCoordinateWrite{reflect.ValueOf(&values[i].SourcePos).Elem(), after[i].SourcePos})
		}
		return nil
	}
	if err := transforms(analysis.FactTransforms, oldAll, newAll); err != nil {
		return err
	}
	oldBlocks, newBlocks := collectCFGBlockFactTransforms(oldCFG), collectCFGBlockFactTransforms(newCFG)
	if len(analysis.BlockFactTransforms) != len(oldBlocks) {
		return fmt.Errorf("coordinate fact block producer count differs")
	}
	for i := range analysis.BlockFactTransforms {
		if analysis.BlockFactTransforms[i].BlockID != oldBlocks[i].BlockID {
			return fmt.Errorf("coordinate fact block identity differs")
		}
		if err := transforms(analysis.BlockFactTransforms[i].Transforms, oldBlocks[i].Transforms, newBlocks[i].Transforms); err != nil {
			return err
		}
	}
	for i := range cfg.Blocks {
		if err := transforms(cfg.Blocks[i].FactTransforms, oldCFG.Blocks[i].FactTransforms, newCFG.Blocks[i].FactTransforms); err != nil {
			return err
		}
	}
	plan.analyses = append(plan.analyses, analysis)
	return nil
}
