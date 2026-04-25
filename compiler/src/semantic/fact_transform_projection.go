package semantic

import (
	"sort"
	"strings"

	"llcontext/src/ast"
)

func (a *Analyzer) currentConservativeCallWideningTransforms() []FactTransform {
	if a == nil || len(a.currentConservativeCallWidenings) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for root, paths := range a.currentConservativeCallWidenings {
		if root == nil {
			continue
		}
		for _, path := range paths {
			transforms = append(transforms, FactTransform{
				Kind:    FactTransformWiden,
				Classes: []FactClass{FactTypestate},
				Target:  namedStateTargetDisplayName(root, path),
				Reason:  "ref call without matching ensures",
			})
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromCFGGuards(cfg *CFG) []FactTransform {
	if cfg == nil {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for _, block := range cfg.Blocks {
		for _, edge := range block.Edges {
			transforms = append(transforms, factTransformsFromGuardFactSet(edge.Guard)...)
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromCFGFlowInstrs(cfg *CFG) []FactTransform {
	if cfg == nil {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for _, block := range cfg.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Kind {
			case FlowInstrAlias:
				if instr.Location == "" || instr.Source == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformRefine,
					Classes: []FactClass{FactAliasClass},
					Target:  instr.Location,
					Source:  instr.Source,
					Reason:  flowInstrFactReason(instr, "alias fact"),
				})
			case FlowInstrInvalidate:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformInvalidate,
					Classes: []FactClass{FactRegionDeps},
					Target:  instr.Location,
					Source:  flowInstrFactSource(instr, "control-flow instruction"),
					Reason:  flowInstrFactReason(instr, "invalidate region dependencies"),
				})
			case FlowInstrProduce:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformProduce,
					Classes: []FactClass{FactRepresentation, FactStorage},
					Target:  instr.Location,
					Source:  flowInstrFactSource(instr, "control-flow instruction"),
					Reason:  flowInstrFactReason(instr, "produce value"),
				})
			case FlowInstrRebase:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformRebase,
					Classes: []FactClass{FactStoreDeps},
					Target:  instr.Location,
					Source:  flowInstrFactSource(instr, "control-flow instruction"),
					Reason:  flowInstrFactReason(instr, "rebase provenance"),
				})
			case FlowInstrConsume:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformConsume,
					Classes: []FactClass{FactUsage},
					Target:  instr.Location,
					Source:  "control-flow instruction",
					Reason:  flowInstrFactReason(instr, "consume value"),
				})
			case FlowInstrMutate:
				if instr.Location == "" {
					continue
				}
				transforms = append(transforms, FactTransform{
					Kind:    FactTransformRecompute,
					Classes: []FactClass{FactTypestate},
					Target:  instr.Location,
					Source:  "control-flow instruction",
					Reason:  flowInstrFactReason(instr, "mutation recomputes derived facts"),
				})
			}
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func flowInstrFactSource(instr FlowInstr, fallback string) string {
	if instr.Source != "" {
		return instr.Source
	}
	return fallback
}

func flowInstrFactReason(instr FlowInstr, fallback string) string {
	if instr.Note != "" {
		return instr.Note
	}
	return fallback
}

func factTransformsFromPoststates(fnType *FuncType) []FactTransform {
	if fnType == nil || len(fnType.Poststates) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0, len(fnType.Poststates))
	for _, poststate := range fnType.Poststates {
		target := functionPoststateTargetName(fnType, poststate)
		if target == "" {
			continue
		}
		transform := FactTransform{
			Kind:   FactTransformEnsure,
			Target: target,
			Source: "ensures " + funcPoststateConditionLabel(poststate.Condition),
		}
		switch poststate.Kind {
		case FuncPoststateKindNamedState:
			transform.Classes = []FactClass{FactTypestate}
			transform.Reason = "ensures typestate " + strings.Join(poststate.StateCases, "|")
		case FuncPoststateKindRefState:
			transform.Classes = []FactClass{FactRefState}
			transform.Reason = "ensures refstate " + ast.RefStateMarker(ast.RefState(poststate.RefState))
		case FuncPoststateKindPreserve:
			transform.Classes = []FactClass{FactTypestate, FactRefState}
			transform.Reason = "ensures preserve"
		default:
			continue
		}
		transforms = append(transforms, transform)
	}
	return dedupeAndSortFactTransforms(transforms)
}

func functionPoststateTargetName(fnType *FuncType, poststate FuncPoststate) string {
	base := functionParamName(fnType, poststate.ParamIndex)
	if base == "" {
		return ""
	}
	return base + borrowAnnotationPathSuffix(poststate.Path)
}

func factTransformsFromPermissions(fnType *FuncType) []FactTransform {
	if fnType == nil {
		return nil
	}
	refs := functionPermissionRefs(fnType)
	if len(refs) == 0 {
		return nil
	}
	refs = canonicalizePermissionRefs(refs)
	transforms := make([]FactTransform, 0, len(refs))
	for _, ref := range refs {
		target := PermissionRefString(ref)
		if target == "" {
			continue
		}
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRequire,
			Classes: []FactClass{FactEffects},
			Target:  target,
			Source:  "function signature",
			Reason:  "requires effect authority",
		})
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromGuardFactSet(guards GuardFactSet) []FactTransform {
	transforms := make([]FactTransform, 0)
	for _, target := range sortedBoolFactKeys(guards.NonNull) {
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRefine,
			Classes: []FactClass{FactRefState},
			Target:  target,
			Source:  "control-flow guard",
			Reason:  "guard proves non-null",
		})
	}
	for _, target := range sortedBoolFactKeys(guards.Null) {
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRefine,
			Classes: []FactClass{FactRefState},
			Target:  target,
			Source:  "control-flow guard",
			Reason:  "guard proves null",
		})
	}
	variantTargets := make([]string, 0, len(guards.PackedVariants))
	for target := range guards.PackedVariants {
		variantTargets = append(variantTargets, target)
	}
	sort.Strings(variantTargets)
	for _, target := range variantTargets {
		guard := guards.PackedVariants[target]
		if guard.EnumName == "" || guard.VariantName == "" {
			continue
		}
		transforms = append(transforms, FactTransform{
			Kind:    FactTransformRefine,
			Classes: []FactClass{FactTypestate},
			Target:  target,
			Source:  "control-flow guard",
			Reason:  "guard proves variant " + guard.EnumName + "." + guard.VariantName,
		})
	}
	lefts := make([]string, 0, len(guards.Leq))
	for left := range guards.Leq {
		lefts = append(lefts, left)
	}
	sort.Strings(lefts)
	for _, left := range lefts {
		rights := sortedBoolFactKeys(guards.Leq[left])
		for _, right := range rights {
			transforms = append(transforms, FactTransform{
				Kind:    FactTransformRefine,
				Classes: []FactClass{FactOptimization},
				Target:  left,
				Source:  "control-flow guard",
				Reason:  "guard proves <= " + right,
			})
		}
	}
	return transforms
}

func sortedBoolFactKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key != "" && value {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func dedupeAndSortFactTransforms(transforms []FactTransform) []FactTransform {
	if len(transforms) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]FactTransform, 0, len(transforms))
	for _, transform := range transforms {
		if transform.Kind == "" {
			continue
		}
		key := factTransformDedupeKey(transform)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, transform)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		if factClassListKey(out[i].Classes) != factClassListKey(out[j].Classes) {
			return factClassListKey(out[i].Classes) < factClassListKey(out[j].Classes)
		}
		if out[i].Reason != out[j].Reason {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func factTransformDedupeKey(transform FactTransform) string {
	return strings.Join([]string{
		transform.Kind.String(),
		transform.Target,
		factClassListKey(transform.Classes),
		transform.Source,
		transform.Reason,
	}, "\x00")
}

func factClassListKey(classes []FactClass) string {
	if len(classes) == 0 {
		return ""
	}
	values := make([]string, 0, len(classes))
	for _, class := range classes {
		values = append(values, class.String())
	}
	return strings.Join(values, ",")
}
