package semantic

import (
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) currentConservativeCallWideningTransforms() []FactTransform {
	if a == nil || len(a.currentConservativeCallWidenings) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0)
	for root, widenings := range a.currentConservativeCallWidenings {
		if root == nil {
			continue
		}
		for _, widening := range widenings {
			reason := widening.Reason
			if reason == "" {
				reason = "ref call without matching ensures"
			}
			transforms = append(transforms, FactTransform{
				Kind:       FactTransformWiden,
				Classes:    []FactClass{FactTypestate},
				Target:     namedStateTargetDisplayName(root, widening.Path),
				Source:     widening.Source,
				SourcePos:  widening.SourcePos,
				SourceKind: FactSourceCallWiden,
				Details:    conservativeCallWideningDetails(widening),
				Reason:     reason,
			})
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func conservativeCallWideningDetails(widening conservativeCallWidening) []FactTransformDetail {
	details := make([]FactTransformDetail, 0, 2)
	if widening.Before != "" {
		details = append(details, FactTransformDetail{Name: "before", Value: widening.Before})
	}
	if widening.After != "" {
		details = append(details, FactTransformDetail{Name: "after", Value: widening.After})
	}
	return details
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

func populateCFGBlockFactTransforms(cfg *CFG) []FactTransform {
	if cfg == nil {
		return nil
	}
	all := make([]FactTransform, 0)
	for i := range cfg.Blocks {
		transforms := factTransformsFromCFGBlock(cfg.Blocks[i])
		cfg.Blocks[i].FactTransforms = transforms
		all = append(all, transforms...)
	}
	return dedupeAndSortFactTransforms(all)
}

func factTransformsFromCFGBlock(block CFGBlock) []FactTransform {
	transforms := make([]FactTransform, 0)
	for _, edge := range block.Edges {
		transforms = append(transforms, factTransformsFromRefinementFacts(edge.Guard)...)
	}
	for _, instr := range block.Instrs {
		if transform, ok := factTransformFromFlowInstr(instr); ok {
			transforms = append(transforms, transform)
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
			if transform, ok := factTransformFromFlowInstr(instr); ok {
				transforms = append(transforms, transform)
			}
		}
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformFromFlowInstr(instr FlowInstr) (FactTransform, bool) {
	switch instr.Kind {
	case FlowInstrAlias:
		if instr.Location == "" || instr.Source == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformRefine, Classes: []FactClass{FactAliasClass}, Target: instr.Location, Source: instr.Source, SourcePos: instr.Position, SourceKind: FactSourceFlowInstr, Details: []FactTransformDetail{{Name: "alias_member", Value: instr.Source}}, Reason: flowInstrFactReason(instr, "alias fact")}, true
	case FlowInstrInvalidate:
		if instr.Location == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformInvalidate, Classes: []FactClass{FactRegionDeps}, Target: instr.Location, Source: flowInstrFactSource(instr, "control-flow instruction"), SourcePos: instr.Position, SourceKind: FactSourceRegion, Details: flowInstrFactDetails(instr), Reason: flowInstrFactReason(instr, "invalidate region dependencies")}, true
	case FlowInstrProduce:
		if instr.Location == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformProduce, Classes: flowInstrProduceClasses(instr), Target: instr.Location, Source: flowInstrFactSource(instr, "control-flow instruction"), SourcePos: instr.Position, SourceKind: flowInstrProduceSourceKind(instr), Details: flowInstrProduceDetails(instr), Reason: flowInstrFactReason(instr, "produce value")}, true
	case FlowInstrRebase:
		if instr.Location == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformRebase, Classes: []FactClass{FactStoreDeps}, Target: instr.Location, Source: flowInstrFactSource(instr, "control-flow instruction"), SourcePos: instr.Position, SourceKind: FactSourceStore, Details: flowInstrStoreDetails(instr), Reason: flowInstrFactReason(instr, "rebase provenance")}, true
	case FlowInstrConsume:
		if instr.Location == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformConsume, Classes: []FactClass{FactUsage}, Target: instr.Location, Source: flowInstrFactSource(instr, "control-flow instruction"), SourcePos: instr.Position, SourceKind: FactSourceFlowInstr, Reason: flowInstrFactReason(instr, "consume value")}, true
	case FlowInstrMutate:
		if instr.Location == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformRecompute, Classes: flowInstrMutationClasses(instr), Target: instr.Location, Source: flowInstrFactSource(instr, "control-flow instruction"), SourcePos: instr.Position, SourceKind: FactSourceFlowInstr, Details: flowInstrMutationDetails(instr), Reason: flowInstrFactReason(instr, "mutation recomputes derived facts")}, true
	case FlowInstrErrorExit:
		if instr.Location == "" {
			return FactTransform{}, false
		}
		return FactTransform{Kind: FactTransformProduce, Classes: []FactClass{FactErrorPath}, Target: instr.Location, Source: flowInstrFactSource(instr, "control-flow instruction"), SourcePos: instr.Position, SourceKind: FactSourceErrorPath, Reason: flowInstrFactReason(instr, "error path")}, true
	case FlowInstrReturn:
		return FactTransform{Kind: FactTransformProduce, Classes: []FactClass{FactRepresentation}, Target: "<return>", Source: "return statement", SourcePos: instr.Position, SourceKind: FactSourceReturn, Reason: flowInstrFactReason(instr, "return exit")}, true
	default:
		return FactTransform{}, false
	}
}

func flowInstrMutationClasses(instr FlowInstr) []FactClass {
	classes := []FactClass{FactTypestate, FactShape, FactOptimization}
	if strings.Contains(instr.Note, "as-ref") || strings.Contains(instr.Note, "store") {
		classes = append(classes, FactStoreDeps)
	}
	return classes
}

func flowInstrMutationDetails(instr FlowInstr) []FactTransformDetail {
	if instr.Note == "" {
		return nil
	}
	return []FactTransformDetail{{Name: "mutation", Value: instr.Note}}
}

func flowInstrProduceClasses(instr FlowInstr) []FactClass {
	classes := []FactClass{FactRepresentation, FactStorage}
	if strings.Contains(instr.Note, "freeze") {
		classes = append(classes, FactStoreDeps)
	}
	return classes
}

func flowInstrProduceDetails(instr FlowInstr) []FactTransformDetail {
	details := make([]FactTransformDetail, 0, 2)
	if strings.Contains(instr.Note, "freeze") {
		details = append(details, FactTransformDetail{Name: "operation", Value: "freeze"})
		if instr.Source != "" {
			details = append(details, FactTransformDetail{Name: "store_deps", Value: instr.Source})
		}
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

func flowInstrStoreDetails(instr FlowInstr) []FactTransformDetail {
	if !strings.Contains(instr.Note, "freeze") {
		return nil
	}
	return []FactTransformDetail{{Name: "operation", Value: "freeze"}, {Name: "before", Value: instr.Location}, {Name: "after", Value: "frozen/public store"}}
}

func flowInstrProduceSourceKind(instr FlowInstr) FactTransformSourceKind {
	if strings.Contains(instr.Note, "freeze") || strings.Contains(instr.Source, ".Store") {
		return FactSourceStore
	}
	return FactSourceFlowInstr
}

func flowInstrFactDetails(instr FlowInstr) []FactTransformDetail {
	if instr.Kind != FlowInstrInvalidate {
		return nil
	}
	details := []FactTransformDetail{}
	if instr.Note != "" {
		details = append(details, FactTransformDetail{Name: "operation", Value: instr.Note})
	}
	if instr.Source != "" {
		details = append(details, FactTransformDetail{Name: "checkpoint", Value: instr.Source})
	}
	return details
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
		// An IMPLICIT preserve (strict protocol balance) is synthesized by the analyzer, not written by
		// the user; it is an internal verification obligation, not a declared signature effect, so it is
		// not surfaced as a fact transform (which mirror the source-level contract).
		if poststate.Implicit {
			continue
		}
		target := functionPoststateTargetName(fnType, poststate)
		if target == "" {
			continue
		}
		transform := FactTransform{
			Kind:       FactTransformEnsure,
			Target:     target,
			Source:     "ensures " + funcPoststateConditionLabel(poststate.Condition),
			SourceKind: FactSourceSignature,
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
			Kind:       FactTransformRequire,
			Classes:    []FactClass{FactEffects},
			Target:     target,
			Source:     "function signature",
			SourceKind: FactSourcePermission,
			Reason:     "requires effect authority",
		})
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromGenericInterfaceBounds(fn *ast.FuncDecl) []FactTransform {
	if fn == nil || len(fn.GenericParams) == 0 {
		return nil
	}
	transforms := make([]FactTransform, 0, len(fn.GenericParams))
	for _, param := range fn.GenericParams {
		if param.Kind != ast.GenericParamType || param.InterfaceBound == "" || param.Name == "" {
			continue
		}
		transforms = append(transforms, FactTransform{
			Kind:       FactTransformRequire,
			Classes:    []FactClass{FactInterface},
			Target:     param.Name + ":" + param.InterfaceBound,
			Source:     "generic parameter",
			SourcePos:  param.Position,
			SourceKind: FactSourceSignature,
			Reason:     "required interface fact",
		})
	}
	return dedupeAndSortFactTransforms(transforms)
}

func factTransformsFromRefinementFacts(facts RefinementFacts) []FactTransform {
	return factTransformsFromGuardFactSet(facts)
}

func factTransformsFromGuardFactSet(guards GuardFactSet) []FactTransform {
	transforms := make([]FactTransform, 0)
	for _, target := range sortedBoolFactKeys(guards.NonNull) {
		transforms = append(transforms, FactTransform{
			Kind:       FactTransformRefine,
			Classes:    []FactClass{FactRefState},
			Target:     target,
			Source:     "control-flow guard",
			SourceKind: FactSourceGuard,
			Reason:     "guard proves non-null",
		})
	}
	for _, target := range sortedBoolFactKeys(guards.Null) {
		transforms = append(transforms, FactTransform{
			Kind:       FactTransformRefine,
			Classes:    []FactClass{FactRefState},
			Target:     target,
			Source:     "control-flow guard",
			SourceKind: FactSourceGuard,
			Reason:     "guard proves null",
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
			Kind:       FactTransformRefine,
			Classes:    []FactClass{FactTypestate},
			Target:     target,
			Source:     "control-flow guard",
			SourceKind: FactSourceGuard,
			Reason:     "guard proves variant " + guard.EnumName + "." + guard.VariantName,
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
				Kind:       FactTransformRefine,
				Classes:    []FactClass{FactOptimization},
				Target:     left,
				Source:     "control-flow guard",
				SourceKind: FactSourceGuard,
				Reason:     "guard proves <= " + right,
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
		factTransformPosKey(transform.SourcePos),
		transform.SourceKind.String(),
		factTransformDetailsKey(transform.Details),
		transform.Reason,
	}, "\x00")
}

func factTransformPosKey(pos lexer.Pos) string {
	if pos.IsZero() {
		return ""
	}
	parts := []string{
		pos.File,
		strconv.Itoa(pos.Line),
		strconv.Itoa(pos.Col),
		strconv.Itoa(pos.Offset),
		strconv.Itoa(pos.EndLine),
		strconv.Itoa(pos.EndCol),
		strconv.Itoa(pos.EndOffset),
	}
	return strings.Join(parts, ":")
}

func factTransformDetailsKey(details []FactTransformDetail) string {
	if len(details) == 0 {
		return ""
	}
	parts := make([]string, 0, len(details))
	for _, detail := range details {
		parts = append(parts, detail.Name+"="+detail.Value)
	}
	return strings.Join(parts, ",")
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
