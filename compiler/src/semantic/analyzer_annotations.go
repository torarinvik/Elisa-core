package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"math/bits"
	"strconv"
	"strings"
)

// removedAnnotationMigration reports the replacement guidance for an attribute that
// has been removed as redundant with a more general form. The returned string is a
// migration hint ("use @X"); ok is false for still-supported names. Shared across the
// function / extern-function / struct validation paths so every use site gets the same
// actionable message instead of a bare "unknown annotation".
func removedAnnotationMigration(name string) (string, bool) {
	switch name {
	case "ignore":
		return "use @skip", true
	case "c_abi":
		return "use @callconv with the convention name, e.g. @callconv(c)", true
	case "stdcall":
		return "use @callconv(stdcall)", true
	case "cacheline_aligned":
		return "use @align(64)", true
	case "c_bind_prefix":
		return "use @c_bind with a trailing `prefix` flag, e.g. @c_bind(header, name, prefix)", true
	case "borrows_return_field":
		return "use @borrows_return with a leading `field` flag, e.g. @borrows_return(field, return_path, param_path, …)", true
	case "borrows_return_rebased":
		return "use @borrows_return with a leading `rebased` flag, e.g. @borrows_return(rebased, param_path, …)", true
	case "borrows_return_field_rebased":
		return "use @borrows_return with leading `field, rebased` flags, e.g. @borrows_return(field, rebased, return_path, param_path, …)", true
	default:
		return "", false
	}
}

func isSupportedExternFunctionAnnotation(name string) bool {
	switch name {
	case "borrows_return", "link_name", "intrinsic", "wasm_import_module", "callconv", "internal", "blocking", "nonblocking", "segment_transition", "reentrant_safe", "deprecated", "trusted", "bounds":
		return true
	default:
		return false
	}
}

func isSupportedExternVarAnnotation(name string) bool {
	switch name {
	case "link_name":
		return true
	default:
		return false
	}
}

func isSupportedExternTypeAnnotation(name string) bool {
	switch name {
	case "c_opaque":
		return true
	default:
		return false
	}
}

func (a *Analyzer) markRawExternFuncType(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if a == nil || fn == nil || fnType == nil || !a.enforceUnsafePermissions {
		return
	}
	// Elisa builtin intrinsics are compiler-synthesized pure arithmetic (no foreign code,
	// no memory access), so calling one is not a raw extern call.
	if isElisaBuiltinIntrinsicExtern(fn) {
		return
	}
	fnType.PermissionRefs = mergePermissionRefs(fnType.PermissionRefs, unsafeRawExternRefs(fn.Pos()))
	fnType.Permissions = permissionFamiliesFromRefs(fnType.PermissionRefs)
}

func isElisaBuiltinIntrinsicExtern(fn *ast.ExternFuncDecl) bool {
	for _, annotation := range fn.Annotations {
		if annotation.Name == "intrinsic" && len(annotation.Args) == 1 && IsElisaBuiltinIntrinsic(annotation.Args[0]) {
			return true
		}
	}
	return false
}

func externLinkNameFromAnnotations(annotations []ast.Annotation) (string, bool) {
	for _, annotation := range annotations {
		if (annotation.Name != "link_name" && annotation.Name != "intrinsic") || len(annotation.Args) != 1 {
			continue
		}
		linkName := strings.TrimSpace(annotation.Args[0])
		if linkName == "" {
			continue
		}
		return linkName, true
	}
	return "", false
}

// functionLinkNameFromAnnotations returns the native symbol spelling for an
// Elisa-defined function. Unlike externLinkNameFromAnnotations, this only
// accepts @link_name: @intrinsic is meaningful for native declarations, not
// for a function body that Elisa emits itself.
func functionLinkNameFromAnnotations(annotations []ast.Annotation) (string, bool) {
	for _, annotation := range annotations {
		if annotation.Name != "link_name" || len(annotation.Args) != 1 {
			continue
		}
		linkName := strings.TrimSpace(annotation.Args[0])
		if linkName == "" {
			continue
		}
		return linkName, true
	}
	return "", false
}

func normalizeExternCallConvAnnotationArg(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "c", "cdecl", "default":
		return "c", true
	case "fast", "fastcall":
		return "fast", true
	case "cold", "coldcc":
		return "cold", true
	case "stdcall", "x86_stdcall", "x86-stdcall":
		return "stdcall", true
	case "winapi", "windows":
		return "winapi", true
	default:
		return "", false
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
	case "align", "fixed_layout", "c_bind", "abi_layout", "intrusive", "append_only":
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
	// Unannotated commons stay Default: placement is an ABI-mode decision (cold side table when the
	// handle can index a parallel array, inline otherwise) and the mode is only known in the backend.
	storage := PackedFieldStorageDefault
	if enumDecl == nil || len(fieldDecl.Annotations) == 0 {
		return storage
	}
	seen := make(map[string]lexer.Pos, len(fieldDecl.Annotations))
	for _, annotation := range fieldDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on packed enum %q common field %q (first seen at line %d:%d)", annotation.Name, enumDecl.Name, fieldDecl.Name, prev.Line, prev.Col)
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
			a.errorf(annotation.Position, "duplicate @%s annotation on enum %q (first seen at line %d:%d)", annotation.Name, enumDecl.Name, prev.Line, prev.Col)
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
			if !enumType.SourcePacked {
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
		if hint, removed := removedAnnotationMigration(annotation.Name); removed {
			a.errorf(annotation.Position, "@%s has been removed; %s", annotation.Name, hint)
			continue
		}
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
		case "fixed_layout":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@fixed_layout on struct %q does not take arguments", structDecl.Name)
				continue
			}
		case "abi_layout":
			a.validateStructAbiLayoutAnnotation(structDecl, annotation)
		case "append_only":
			// Shape and surface rules live in analyzer_append_only_store.go.
			structType.AppendOnly = true
			// Two copies of a store header append into the same spare capacity and
			// overwrite each other's viewed bytes, so a store, and every aggregate that
			// holds one, is affine: it moves, it never copies.
			structType.Affine = true
			structType.Droppable = true
		case "c_bind":
			// @c_bind(header, name) binds the struct to an exact C type; an optional
			// trailing `prefix` flag (@c_bind(header, name, prefix)) marks `name` as a
			// symbol prefix instead — this replaced the removed @c_bind_prefix variant.
			if len(annotation.Args) != 2 && len(annotation.Args) != 3 {
				a.errorf(annotation.Position, "@c_bind on struct %q expects header and C type name, with an optional trailing `prefix` flag", structDecl.Name)
				continue
			}
			prefixMode := false
			if len(annotation.Args) == 3 {
				if strings.ToLower(strings.Trim(strings.TrimSpace(annotation.Args[2]), "\"")) != "prefix" {
					a.errorf(annotation.Position, "@c_bind on struct %q accepts only `prefix` as a third argument, got %q", structDecl.Name, annotation.Args[2])
					continue
				}
				prefixMode = true
			}
			header := strings.Trim(strings.TrimSpace(annotation.Args[0]), "\"")
			cName := strings.Trim(strings.TrimSpace(annotation.Args[1]), "\"")
			if header == "" || cName == "" {
				a.errorf(annotation.Position, "@c_bind on struct %q requires non-empty header and C type name arguments", structDecl.Name)
				continue
			}
			if !structDecl.ReprC {
				a.errorf(annotation.Position, "@%s on struct %q requires `layout(c)` so Elisa and C agree on ABI layout rules", annotation.Name, structDecl.Name)
				continue
			}
			if len(structDecl.TypeParams) != 0 || len(structDecl.GenericParams) != 0 {
				a.errorf(annotation.Position, "@%s on struct %q does not support generic structs", annotation.Name, structDecl.Name)
				continue
			}
			structType.CBindHeader = header
			structType.CBindName = cName
			structType.CBindPrefix = prefixMode
		}
	}
	if hasAlignment {
		structType.Alignment = alignment
		structType.HasAlignment = true
	}
}

func (a *Analyzer) validateStructAbiLayoutAnnotation(structDecl *ast.StructDecl, annotation ast.Annotation) {
	if !structDecl.ReprC && !structDeclHasAnnotation(structDecl, "fixed_layout") {
		a.errorf(annotation.Position, "@abi_layout on struct %q requires `layout(c)` or @fixed_layout so the asserted offsets are intentional ABI surface", structDecl.Name)
		return
	}
	if len(structDecl.TypeParams) != 0 || len(structDecl.GenericParams) != 0 {
		a.errorf(annotation.Position, "@abi_layout on struct %q does not support generic structs", structDecl.Name)
		return
	}
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@abi_layout on struct %q expects layout assertions such as @abi_layout(size, 16, align, 8, field, payload, 8)", structDecl.Name)
		return
	}
	for i := 0; i < len(annotation.Args); {
		key := strings.TrimSpace(annotation.Args[i])
		switch key {
		case "size", "align":
			if i+1 >= len(annotation.Args) {
				a.errorf(annotation.Position, "@abi_layout on struct %q expects an integer byte value after %q", structDecl.Name, key)
				return
			}
			if _, ok := parseAbiLayoutUint(annotation.Args[i+1]); !ok {
				a.errorf(annotation.Position, "@abi_layout on struct %q expects a non-negative integer byte value after %q, got %q", structDecl.Name, key, annotation.Args[i+1])
				return
			}
			i += 2
		case "field":
			if i+2 >= len(annotation.Args) {
				a.errorf(annotation.Position, "@abi_layout on struct %q expects field, name, offset triples", structDecl.Name)
				return
			}
			fieldName := strings.TrimSpace(annotation.Args[i+1])
			if fieldName == "" || !structDeclHasField(structDecl, fieldName) {
				a.errorf(annotation.Position, "@abi_layout on struct %q references unknown field %q", structDecl.Name, fieldName)
				return
			}
			if _, ok := parseAbiLayoutUint(annotation.Args[i+2]); !ok {
				a.errorf(annotation.Position, "@abi_layout on struct %q expects a non-negative integer offset for field %q, got %q", structDecl.Name, fieldName, annotation.Args[i+2])
				return
			}
			i += 3
		default:
			a.errorf(annotation.Position, "@abi_layout on struct %q uses unknown assertion key %q (expected size, align, or field)", structDecl.Name, key)
			return
		}
	}
}

func parseAbiLayoutUint(text string) (uint64, bool) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(text), 0, 64)
	return parsed, err == nil
}

func structDeclHasField(structDecl *ast.StructDecl, fieldName string) bool {
	if structDecl == nil {
		return false
	}
	for _, field := range structDecl.Fields {
		if field.Name == fieldName {
			return true
		}
	}
	return false
}

func structDeclHasAnnotation(structDecl *ast.StructDecl, annotationName string) bool {
	if structDecl == nil {
		return false
	}
	for _, annotation := range structDecl.Annotations {
		if annotation.Name == annotationName {
			return true
		}
	}
	return false
}
