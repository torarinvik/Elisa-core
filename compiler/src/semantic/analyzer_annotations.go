package semantic

import (
	"math/bits"
	"strconv"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func isSupportedExternFunctionAnnotation(name string) bool {
	switch name {
	case "borrows_return", "borrows_return_field", "borrows_return_rebased", "borrows_return_field_rebased":
		return true
	default:
		return false
	}
}

func normalizePackedProfileAnnotationArg(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "canonical", "default", "canon", "variant_sparse", "variant-sparse":
		return "canonical", true
	case "retained_reads", "retained-reads", "retainedreads", "dense_reads", "dense-reads":
		return "retained-reads", true
	case "build_heavy", "build-heavy", "buildheavy", "dense_build_bias", "dense-build-bias", "balanced":
		return "build-heavy", true
	default:
		return "", false
	}
}

func normalizePackedFieldStorageAnnotationArg(value string) (PackedFieldStorageMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inline", "in_place", "in-place", "row":
		return PackedFieldStorageInline, true
	case "side_table", "side-table", "sidetable", "cold":
		return PackedFieldStorageSideTable, true
	default:
		return PackedFieldStorageDefault, false
	}
}

func normalizeInlineAnnotationArg(value string) (FuncInlineMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always", "force", "on":
		return FuncInlineModeAlways, true
	case "never", "off", "no":
		return FuncInlineModeNever, true
	default:
		return FuncInlineModeDefault, false
	}
}

func normalizeStructAlignmentAnnotationArg(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(trimmed, 0, 32)
	if err != nil || parsed == 0 {
		return 0, false
	}
	alignment := int(parsed)
	if bits.OnesCount32(uint32(alignment)) != 1 {
		return 0, false
	}
	return alignment, true
}

func temperatureModeForAnnotationName(name string) (FuncTemperatureMode, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hot":
		return FuncTemperatureModeHot, true
	case "cold":
		return FuncTemperatureModeCold, true
	default:
		return FuncTemperatureModeDefault, false
	}
}

func packedProfileDefaults(profile string) (string, string, bool) {
	switch profile {
	case "canonical":
		return "variant-sparse", "", true
	case "retained-reads":
		return "dense-fixed", "all-words", true
	case "build-heavy":
		return "dense-fixed", "common-only", true
	default:
		return "", "", false
	}
}

func isSupportedEnumAnnotation(name string) bool {
	switch name {
	case "packed_profile":
		return true
	default:
		return false
	}
}

func isSupportedStructAnnotation(name string) bool {
	switch name {
	case "align", "cacheline_aligned":
		return true
	default:
		return false
	}
}

func isSupportedPackedCommonFieldAnnotation(name string) bool {
	switch name {
	case "storage":
		return true
	default:
		return false
	}
}

func (a *Analyzer) analyzePackedCommonFieldAnnotations(enumDecl *ast.EnumDecl, fieldDecl ast.FieldDecl) PackedFieldStorageMode {
	storage := PackedFieldStorageInline
	if enumDecl == nil || len(fieldDecl.Annotations) == 0 {
		return storage
	}
	seen := make(map[string]lexer.Pos, len(fieldDecl.Annotations))
	for _, annotation := range fieldDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on packed enum %q common field %q (first seen at %s:%d:%d)", annotation.Name, enumDecl.Name, fieldDecl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedPackedCommonFieldAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown packed enum common-field annotation @%s on %q.%s", annotation.Name, enumDecl.Name, fieldDecl.Name)
			continue
		}
		switch annotation.Name {
		case "storage":
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@storage on packed enum %q common field %q expects exactly one argument", enumDecl.Name, fieldDecl.Name)
				continue
			}
			normalized, ok := normalizePackedFieldStorageAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@storage on packed enum %q common field %q uses unsupported mode %q (expected inline or side_table)", enumDecl.Name, fieldDecl.Name, annotation.Args[0])
				continue
			}
			storage = normalized
		}
	}
	return storage
}

func (a *Analyzer) analyzeEnumAnnotations(enumDecl *ast.EnumDecl, enumType *EnumType) {
	if enumDecl == nil || enumType == nil || len(enumDecl.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(enumDecl.Annotations))
	profileOverride := ""
	hasProfileOverride := false
	for _, annotation := range enumDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on enum %q (first seen at %s:%d:%d)", annotation.Name, enumDecl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		switch annotation.Name {
		case "packed_abi", "packed_prefix":
			a.errorf(annotation.Position, "@%s on enum %q has been removed; use @packed_profile(canonical|retained_reads|build_heavy) instead", annotation.Name, enumDecl.Name)
			continue
		}
		if !isSupportedEnumAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown enum annotation @%s on %q", annotation.Name, enumDecl.Name)
			continue
		}
		switch annotation.Name {
		case "packed_profile":
			if !enumDecl.Packed {
				a.errorf(annotation.Position, "@packed_profile on enum %q requires a packed enum", enumDecl.Name)
				continue
			}
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@packed_profile on enum %q expects exactly one profile argument", enumDecl.Name)
				continue
			}
			normalized, ok := normalizePackedProfileAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@packed_profile on enum %q uses unsupported profile %q (expected canonical, retained_reads, or build_heavy)", enumDecl.Name, annotation.Args[0])
				continue
			}
			profileOverride = normalized
			hasProfileOverride = true
		}
	}
	if hasProfileOverride {
		enumType.PackedProfile = profileOverride
		enumType.HasPackedProfile = true
		if profileABI, profilePrefix, ok := packedProfileDefaults(profileOverride); ok {
			if profileABI != "" {
				enumType.PackedABIOverride = profileABI
				enumType.HasPackedABIOverride = true
			}
			if profilePrefix != "" {
				enumType.PackedPrefixOverride = profilePrefix
				enumType.HasPackedPrefixOverride = true
			}
		}
	}
}

func (a *Analyzer) analyzeStructAnnotations(structDecl *ast.StructDecl, structType *StructType) {
	if structDecl == nil || structType == nil || len(structDecl.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(structDecl.Annotations))
	alignment := 0
	hasAlignment := false
	alignmentSource := ""
	for _, annotation := range structDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on struct %q (first seen at %s:%d:%d)", annotation.Name, structDecl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedStructAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown struct annotation @%s on %q", annotation.Name, structDecl.Name)
			continue
		}
		switch annotation.Name {
		case "align":
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@align on struct %q expects exactly one integer byte alignment", structDecl.Name)
				continue
			}
			parsed, ok := normalizeStructAlignmentAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@align on struct %q expects a positive power-of-two byte alignment, got %q", structDecl.Name, annotation.Args[0])
				continue
			}
			if hasAlignment && alignment != parsed {
				a.errorf(annotation.Position, "@align on struct %q conflicts with existing @%s request for %d-byte alignment", structDecl.Name, alignmentSource, alignment)
				continue
			}
			alignment = parsed
			hasAlignment = true
			alignmentSource = annotation.Name
		case "cacheline_aligned":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@cacheline_aligned on struct %q does not take arguments", structDecl.Name)
				continue
			}
			const cachelineAlignment = 64
			if hasAlignment && alignment != cachelineAlignment {
				a.errorf(annotation.Position, "@cacheline_aligned on struct %q conflicts with existing @%s request for %d-byte alignment", structDecl.Name, alignmentSource, alignment)
				continue
			}
			alignment = cachelineAlignment
			hasAlignment = true
			alignmentSource = annotation.Name
		}
	}
	if hasAlignment {
		structType.Alignment = alignment
		structType.HasAlignment = true
	}
}

func (a *Analyzer) applyExternFuncAnnotations(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || len(fn.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(fn.Annotations))
	for _, annotation := range fn.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on extern function %q (first seen at %s:%d:%d)", annotation.Name, fn.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedExternFunctionAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown extern function annotation @%s on %q", annotation.Name, fn.Name)
			continue
		}
		switch annotation.Name {
		case "borrows_return":
			a.applyExternBorrowsReturnAnnotation(fn, fnType, annotation)
		case "borrows_return_field":
			a.applyExternBorrowsReturnFieldAnnotation(fn, fnType, annotation)
		case "borrows_return_rebased":
			a.applyExternBorrowsReturnRebasedAnnotation(fn, fnType, annotation)
		case "borrows_return_field_rebased":
			a.applyExternBorrowsReturnFieldRebasedAnnotation(fn, fnType, annotation)
		}
	}
}

func (a *Analyzer) applyExternBorrowsReturnAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@borrows_return on extern function %q expects at least one parameter name", fn.Name)
		return
	}
	var states []regionRefState
	var ownerSummaries []borrowedOwnerRefSummary
	for _, pathText := range annotation.Args {
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, false, "")
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			states = append(states, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			ownerSummaries = append(ownerSummaries, ownerSummary)
		}
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	if len(ownerSummaries) != 0 {
		mergedOwner := cloneBorrowedOwnerRefSummary(ownerSummaries[0])
		for i := 1; i < len(ownerSummaries); i++ {
			if next, ok := mergeBorrowedOwnerRefSummary(mergedOwner, ownerSummaries[i]); ok {
				mergedOwner = next
			}
		}
		fnType.ReturnBorrowedOwnerRefs = mergedOwner
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnFieldAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
		a.errorf(annotation.Position, "@borrows_return_field on extern function %q expects field/path pairs", fn.Name)
		return
	}
	if _, ok := a.resolvedStructFields(fnType.Return); !ok {
		a.errorf(annotation.Position, "@borrows_return_field on extern function %q requires a concrete struct return type, got %s", fn.Name, fnType.Return)
		return
	}
	for i := 0; i < len(annotation.Args); i += 2 {
		returnFieldPath := annotation.Args[i]
		pathText := annotation.Args[i+1]
		returnSteps, ok := a.resolveExternReturnTargetPath(fn, fnType, annotation, returnFieldPath)
		if !ok {
			continue
		}
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, false, returnFieldPath)
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			fnType.ReturnBorrowedOwnerRefs = assignBorrowedOwnerRefSummaryAtPath(fnType.ReturnBorrowedOwnerRefs, returnSteps, ownerSummary)
		}
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	if !hasBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnRebasedAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@borrows_return_rebased on extern function %q expects at least one parameter path", fn.Name)
		return
	}
	var states []regionRefState
	var ownerSummaries []borrowedOwnerRefSummary
	for _, pathText := range annotation.Args {
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, true, "")
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			states = append(states, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			ownerSummaries = append(ownerSummaries, ownerSummary)
		}
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	if len(ownerSummaries) != 0 {
		mergedOwner := cloneBorrowedOwnerRefSummary(ownerSummaries[0])
		for i := 1; i < len(ownerSummaries); i++ {
			if next, ok := mergeBorrowedOwnerRefSummary(mergedOwner, ownerSummaries[i]); ok {
				mergedOwner = next
			}
		}
		fnType.ReturnBorrowedOwnerRefs = mergedOwner
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnFieldRebasedAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
		a.errorf(annotation.Position, "@borrows_return_field_rebased on extern function %q expects field/path pairs", fn.Name)
		return
	}
	if _, ok := a.resolvedStructFields(fnType.Return); !ok {
		a.errorf(annotation.Position, "@borrows_return_field_rebased on extern function %q requires a concrete struct return type, got %s", fn.Name, fnType.Return)
		return
	}
	for i := 0; i < len(annotation.Args); i += 2 {
		returnFieldPath := annotation.Args[i]
		pathText := annotation.Args[i+1]
		returnSteps, ok := a.resolveExternReturnTargetPath(fn, fnType, annotation, returnFieldPath)
		if !ok {
			continue
		}
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, true, returnFieldPath)
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			fnType.ReturnBorrowedOwnerRefs = assignBorrowedOwnerRefSummaryAtPath(fnType.ReturnBorrowedOwnerRefs, returnSteps, ownerSummary)
		}
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	if !hasBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}
