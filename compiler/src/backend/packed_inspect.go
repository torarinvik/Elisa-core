//go:build cgo

package backend

import (
	"fmt"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func DescribePackedLowering(result *semantic.Result, profile PackedLoweringProfile) (string, error) {
	g, err := newLLVMGenerator(result)
	if err != nil {
		return "", err
	}
	defer g.dispose()
	g.packedProfile = profile
	g.packedEnumABI = profile.packedModeForStore(nil)
	if g.result != nil {
		g.result.PackedLowering = profile.metadata()
	}

	var builder strings.Builder
	meta := profile.metadata()
	builder.WriteString("packed lowering\n")
	builder.WriteString(fmt.Sprintf("  contract: %s\n", meta.Contract))
	builder.WriteString(fmt.Sprintf("  canonical: %s\n", meta.CanonicalPackedLowering))
	builder.WriteString(fmt.Sprintf("  readonly publication gate: %s\n", meta.PublicationReadonlyGateStoreState))

	packedEnums := make([]*semantic.EnumType, 0)
	seen := map[string]bool{}
	if result != nil && result.File != nil {
		for _, decl := range result.File.Decls {
			enumDecl, ok := decl.(*ast.EnumDecl)
			if !ok {
				continue
			}
			named, ok := result.NamedTypes[enumDecl.Name]
			if !ok {
				continue
			}
			enumType, ok := named.(*semantic.EnumType)
			if !ok || enumType == nil || !enumType.Packed || seen[enumType.Name] {
				continue
			}
			seen[enumType.Name] = true
			packedEnums = append(packedEnums, enumType)
		}
	}
	if len(packedEnums) == 0 {
		builder.WriteString("  enums: none\n")
		return builder.String(), nil
	}

	for _, enumType := range packedEnums {
		rowType, err := g.ensurePackedEnumStorageType(enumType)
		if err != nil {
			return "", err
		}
		rowBytes, err := g.abiSizeOfLLVMType(rowType)
		if err != nil {
			return "", err
		}
		prefixWords, err := g.packedEnumCommonPrefixWordCount(enumType)
		if err != nil {
			return "", err
		}
		sideWords, err := g.packedEnumCommonSideTableWordCount(enumType)
		if err != nil {
			return "", err
		}
		variantNames := make([]string, 0, len(enumType.Variants))
		for _, variant := range enumType.Variants {
			if variant != nil {
				variantNames = append(variantNames, variant.Name)
			}
		}
		sort.Strings(variantNames)

		builder.WriteString(fmt.Sprintf("\n%s\n", enumType.Name))
		builder.WriteString(fmt.Sprintf("  effective abi: %s\n", packedModeName(g.packedModeForEnum(enumType))))
		if enumType.HasPackedProfile {
			builder.WriteString(fmt.Sprintf("  profile: %s\n", enumType.PackedProfile))
		}
		if enumType.HasPackedABIOverride {
			builder.WriteString(fmt.Sprintf("  declared abi override: %s\n", enumType.PackedABIOverride))
		}
		if enumType.HasPackedPrefixOverride {
			builder.WriteString(fmt.Sprintf("  declared prefix override: %s\n", enumType.PackedPrefixOverride))
		}
		builder.WriteString(fmt.Sprintf("  row bytes: %d\n", rowBytes))
		builder.WriteString(fmt.Sprintf("  common prefix words: %d\n", prefixWords))
		builder.WriteString(fmt.Sprintf("  side-table common words: %d\n", sideWords))
		builder.WriteString(fmt.Sprintf("  variants: %s\n", strings.Join(variantNames, ", ")))
		if enumType.Decl == nil || len(enumType.Decl.Common) == 0 {
			builder.WriteString("  common fields: none\n")
			continue
		}
		builder.WriteString("  common fields:\n")
		for _, fieldDecl := range enumType.Decl.Common {
			layout, err := g.packedEnumCommonFieldLayout(enumType, fieldDecl.Name)
			if err != nil {
				return "", err
			}
			if layout.StoredInline {
				builder.WriteString(fmt.Sprintf("    - %s: %s inline row_field=%d\n", fieldDecl.Name, layout.Field.Type.String(), layout.RowFieldIndex))
				continue
			}
			builder.WriteString(fmt.Sprintf("    - %s: %s side_table word_offset=%d words=%d\n", fieldDecl.Name, layout.Field.Type.String(), layout.SideWordOffset, layout.WordCount))
		}
	}
	return builder.String(), nil
}
