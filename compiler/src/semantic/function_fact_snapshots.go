package semantic

import (
	"llcontext/src/ast"
	"strings"
)

func (a *Analyzer) constructCFG(fn *ast.FuncDecl) *CFG {
	if a == nil {
		return ConstructCFG(fn)
	}
	return constructCFG(fn, a.guardFactsForConditionWithMetadata)
}

func (a *Analyzer) finalizeFunctionAnalysis(fn *ast.FuncDecl, fnType *FuncType) {
	if a == nil || fn == nil || fnType == nil {
		return
	}
	if !fnType.SinkParamsKnown {
		a.inferFuncSinkParams(fn, fnType)
	}
	cfg := a.constructCFG(fn)
	configureCFGParamLocations(cfg, fnType)
	populateBasicFlowInstrs(cfg)
	a.addImplicitSinkFlowInstrs(cfg)
	partitions := computeGraphPartitionsFromCFG(cfg)
	cleanupPlan := SynthesizeParamCleanupPlan(fn, fnType)
	returnIsolation := summarizeReturnIsolation(fn, fnType, partitions)
	fnType.ReturnIsolation = returnIsolation
	fnType.ReturnIsolationKnown = true
	cfgTransforms := populateCFGBlockFactTransforms(cfg)
	factTransforms := a.currentConservativeCallWideningTransforms()
	factTransforms = append(factTransforms, cfgTransforms...)
	factTransforms = append(factTransforms, a.currentRegionFactTransforms...)
	factTransforms = append(factTransforms, factTransformsFromPoststates(fnType)...)
	factTransforms = append(factTransforms, factTransformsFromPermissions(fnType)...)
	factTransforms = append(factTransforms, factTransformsFromGenericInterfaceBounds(fn)...)
	aliasSets := partitions.AliasSets()
	factTransforms = append(factTransforms, factTransformsFromAliasSets(aliasSets)...)
	factTransforms = dedupeAndSortFactTransforms(factTransforms)
	blockFactTransforms := collectCFGBlockFactTransforms(cfg)
	factSnapshot := buildFunctionFactSnapshot(fnType, cfg, factTransforms, aliasSets)
	factExitSummary := buildFunctionFactExitSummary(factTransforms)
	effectSummary := buildFunctionFactEffectSummary(factTransforms)
	analysis := &FunctionAnalysis{
		CFG:                 cfg,
		Partitions:          partitions,
		CleanupPlan:         cleanupPlan,
		SinkParams:          append([]bool(nil), fnType.SinkParams...),
		ReturnIsolation:     returnIsolation,
		FactTransforms:      factTransforms,
		BlockFactTransforms: blockFactTransforms,
		FactSnapshot:        factSnapshot,
		FactExitSummary:     factExitSummary,
		AliasSets:           aliasSets,
		EffectSummary:       effectSummary,
	}
	a.functionAnalyses[fn] = analysis
}

func collectCFGBlockFactTransforms(cfg *CFG) []FactBlockTransforms {
	if cfg == nil {
		return nil
	}
	out := make([]FactBlockTransforms, 0, len(cfg.Blocks))
	for _, block := range cfg.Blocks {
		if len(block.FactTransforms) == 0 {
			continue
		}
		out = append(out, FactBlockTransforms{BlockID: block.ID, Transforms: append([]FactTransform(nil), block.FactTransforms...)})
	}
	return out
}

func buildFunctionFactSnapshot(fnType *FuncType, cfg *CFG, transforms []FactTransform, aliasSets []FactAliasSet) FactSnapshot {
	snapshot := FactSnapshot{}
	if cfg != nil {
		snapshot.Parameters = append(snapshot.Parameters, cfg.ParamLocations...)
	}
	if fnType != nil && fnType.Return != nil && !isVoidType(fnType.Return) {
		snapshot.Returns = append(snapshot.Returns, fnType.Return.String())
		snapshot.StoreDeps = append(snapshot.StoreDeps, storeDependencyFactLabelsFromRegionRefState(fnType.ReturnProvenance)...)
	}
	for _, transform := range transforms {
		if path := factPathFromTarget(transform.Target); path.Target != "" {
			snapshot.PathFacts = append(snapshot.PathFacts, path)
		}
		switch transform.Kind {
		case FactTransformRequire:
			if hasFactClass(transform.Classes, FactEffects) {
				snapshot.RequiredEffects = append(snapshot.RequiredEffects, transform.Target)
			}
			if hasFactClass(transform.Classes, FactInterface) {
				snapshot.RequiredInterfaces = append(snapshot.RequiredInterfaces, transform.Target)
			}
		case FactTransformEnsure:
			snapshot.Ensured = append(snapshot.Ensured, transform.Target)
		case FactTransformRefine:
			snapshot.Refined = append(snapshot.Refined, transform.Target)
		case FactTransformWiden:
			snapshot.Widened = append(snapshot.Widened, transform.Target)
		case FactTransformConsume:
			snapshot.Consumed = append(snapshot.Consumed, transform.Target)
		case FactTransformProduce:
			if hasFactClass(transform.Classes, FactErrorPath) {
				snapshot.ErrorExits = append(snapshot.ErrorExits, transform.Reason)
			} else if transform.Target == "<return>" {
				snapshot.Returns = append(snapshot.Returns, transform.Source)
				snapshot.PathFacts = append(snapshot.PathFacts, NewReturnFactPath())
			} else {
				snapshot.Produced = append(snapshot.Produced, transform.Target)
				if hasFactClass(transform.Classes, FactStoreDeps) {
					if deps := factTransformDetailValue(transform.Details, "store_deps"); deps != "" {
						snapshot.HandleStoreDeps = append(snapshot.HandleStoreDeps, transform.Target+"<-"+deps)
					}
				}
			}
		case FactTransformInvalidate:
			snapshot.InvalidatedRegions = append(snapshot.InvalidatedRegions, transform.Target)
			if hasFactClass(transform.Classes, FactRegionDeps) {
				if key := regionDependencyKey(transform.Target, factTransformDetailValue(transform.Details, "generation_before"), factTransformDetailValue(transform.Details, "generation_after")); key != "" {
					snapshot.RegionDeps = append(snapshot.RegionDeps, key)
				}
			}
		case FactTransformRebase:
			snapshot.RebasedStores = append(snapshot.RebasedStores, transform.Target)
		}
	}
	for _, set := range aliasSets {
		if set.ID != "" {
			snapshot.AliasClasses = append(snapshot.AliasClasses, set.ID)
		}
	}
	snapshot.Parameters = canonicalStringList(snapshot.Parameters)
	snapshot.Returns = canonicalStringList(snapshot.Returns)
	snapshot.Consumed = canonicalStringList(snapshot.Consumed)
	snapshot.Produced = canonicalStringList(snapshot.Produced)
	snapshot.InvalidatedRegions = canonicalStringList(snapshot.InvalidatedRegions)
	snapshot.RebasedStores = canonicalStringList(snapshot.RebasedStores)
	snapshot.RequiredEffects = canonicalStringList(snapshot.RequiredEffects)
	snapshot.RequiredInterfaces = canonicalStringList(snapshot.RequiredInterfaces)
	snapshot.Ensured = canonicalStringList(snapshot.Ensured)
	snapshot.Refined = canonicalStringList(snapshot.Refined)
	snapshot.Widened = canonicalStringList(snapshot.Widened)
	snapshot.ErrorExits = canonicalStringList(snapshot.ErrorExits)
	snapshot.StoreDeps = canonicalStringList(snapshot.StoreDeps)
	snapshot.PathFacts = canonicalFactPaths(snapshot.PathFacts)
	snapshot.AliasClasses = canonicalStringList(snapshot.AliasClasses)
	snapshot.HandleStoreDeps = canonicalStringList(snapshot.HandleStoreDeps)
	snapshot.RegionDeps = canonicalStringList(snapshot.RegionDeps)
	return snapshot
}

func buildFunctionFactEffectSummary(transforms []FactTransform) FactEffectSummary {
	summary := FactEffectSummary{}
	for _, transform := range transforms {
		if hasFactClass(transform.Classes, FactEffects) {
			switch transform.Kind {
			case FactTransformRequire:
				summary.Required = append(summary.Required, transform.Target)
			case FactTransformProduce, FactTransformEnsure:
				summary.Provided = append(summary.Provided, transform.Target)
			}
		}
	}
	summary.Required = canonicalStringList(summary.Required)
	summary.Provided = canonicalStringList(summary.Provided)
	return summary
}

func buildFunctionFactExitSummary(transforms []FactTransform) FactExitSummary {
	summary := FactExitSummary{}
	for _, transform := range transforms {
		text := FormatFactTransform(transform)
		if text == "" {
			continue
		}
		if hasFactClass(transform.Classes, FactErrorPath) {
			summary.Error = append(summary.Error, text)
			continue
		}
		if transform.Kind == FactTransformEnsure || (transform.Kind == FactTransformProduce && transform.Target == "<return>") {
			summary.Normal = append(summary.Normal, text)
		}
	}
	summary.Normal = canonicalStringList(summary.Normal)
	summary.Error = canonicalStringList(summary.Error)
	return summary
}

func factPathFromTarget(target string) FactPath {
	return NewFactPath(target)
}

func factTransformsFromAliasSets(sets []FactAliasSet) []FactTransform {
	if len(sets) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0, len(sets)*2)
	for _, set := range sets {
		members := canonicalStringList(set.Members)
		if set.ID == "" || len(members) == 0 {
			continue
		}
		affected := canonicalStringList(set.AffectedPaths)
		if len(affected) == 0 {
			affected = members
		}
		details := []FactTransformDetail{{Name: "members", Value: strings.Join(members, "|")}, {Name: "affected_paths", Value: strings.Join(affected, "|")}}
		transforms = append(transforms, FactTransform{Kind: FactTransformRefine, Classes: []FactClass{FactAliasClass}, Target: set.ID, Source: strings.Join(members, ","), SourceKind: FactSourceFlowInstr, Details: details, Reason: "alias equivalence set"})
		if set.Mutated {
			transforms = append(transforms, FactTransform{Kind: FactTransformRecompute, Classes: []FactClass{FactAliasClass}, Target: set.ID, Source: strings.Join(members, ","), SourceKind: FactSourceFlowInstr, Details: details, Reason: "mutation recomputes facts for alias class"})
			for _, target := range affected {
				transforms = append(transforms, FactTransform{Kind: FactTransformRecompute, Classes: []FactClass{FactTypestate, FactShape, FactOptimization, FactStoreDeps}, Target: target, Source: set.ID, SourceKind: FactSourceFlowInstr, Details: []FactTransformDetail{{Name: "alias_class", Value: set.ID}}, Reason: "alias mutation recomputes dependent path facts"})
			}
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func hasFactClass(classes []FactClass, target FactClass) bool {
	for _, class := range classes {
		if class == target {
			return true
		}
	}
	return false
}

func storeDependencyFactLabelsFromRegionRefState(state regionRefState) []string {
	out := make([]string, 0)
	storeDependencyFactLabelsFromRegionRefStateSeen(state, map[uintptr]bool{}, &out)
	return canonicalStringList(out)
}

func storeDependencyFactLabelsFromRegionRefStateSeen(state regionRefState, seen map[uintptr]bool, out *[]string) {
	for _, dep := range state.StoreDeps {
		if dep.Type != nil {
			*out = append(*out, dep.Type.String())
		}
	}
	fieldsID := regionRefFieldsIdentity(state.Fields)
	if fieldsID != 0 {
		if seen[fieldsID] {
			return
		}
		seen[fieldsID] = true
		defer delete(seen, fieldsID)
	}
	for _, field := range state.Fields {
		storeDependencyFactLabelsFromRegionRefStateSeen(field, seen, out)
	}
}

func configureCFGParamLocations(cfg *CFG, fnType *FuncType) {
	if cfg == nil || fnType == nil {
		return
	}
	cfg.ParamLocations = cfg.ParamLocations[:0]
	for i := range fnType.Params {
		name := functionParamName(fnType, i)
		if name == "" {
			name = "<param>"
		}
		cfg.ParamLocations = append(cfg.ParamLocations, name)
	}
}
