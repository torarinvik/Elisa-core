package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type PackedLoweringContract string

const (
	PackedLoweringContractCanonicalCompilerGraph PackedLoweringContract = "canonical-compiler-graph"
)

type PackedLoweringProfile struct {
	explicitABI     PackedEnumABI
	explicitMode    packedEnumABIMode
	hasExplicitMode bool
}

func DefaultPackedLoweringProfile() PackedLoweringProfile {
	return PackedLoweringProfile{}
}

func ExplicitPackedLoweringProfile(abi PackedEnumABI) (PackedLoweringProfile, error) {
	normalized, err := ParsePackedEnumABI(string(abi))
	if err != nil {
		return PackedLoweringProfile{}, err
	}
	mode, err := normalized.mode()
	if err != nil {
		return PackedLoweringProfile{}, err
	}
	return PackedLoweringProfile{
		explicitABI:     normalized,
		explicitMode:    mode,
		hasExplicitMode: true,
	}, nil
}

func mustExplicitPackedLoweringProfile(abi PackedEnumABI) PackedLoweringProfile {
	profile, err := ExplicitPackedLoweringProfile(abi)
	if err != nil {
		panic(err)
	}
	return profile
}

func (p PackedLoweringProfile) Contract() PackedLoweringContract {
	return PackedLoweringContractCanonicalCompilerGraph
}

func (p PackedLoweringProfile) SelectionKey() string {
	if !p.hasExplicitMode {
		return "canonical"
	}
	return "explicit:" + string(p.explicitABI)
}

func (p PackedLoweringProfile) canonicalPackedMode() packedEnumABIMode {
	return packedEnumABIVariantSparse
}

func (p PackedLoweringProfile) packedModeForPackedEnum(enumType *semantic.EnumType) packedEnumABIMode {
	if enumType == nil || !enumType.Packed {
		return packedEnumABIVariantSparse
	}
	if p.hasExplicitMode {
		return p.explicitMode
	}
	if enumType.HasPackedABIOverride {
		if abi, err := ParsePackedEnumABI(enumType.PackedABIOverride); err == nil {
			if mode, err := abi.mode(); err == nil {
				return mode
			}
		}
	}
	// docs/76: a recursive plain enum will default to AoS (one record per node) — unless the author
	// opted into the columnar layout with `enum X layout soa`. NOT YET ENABLED: the AoS runtime store
	// has a growth/scale bug (correct for small trees within one chunk, wrong/segfault as the store
	// grows). Until that's fixed, recursive plain enums stay on the correct SoA path. Flip the guard
	// below (currently `false &&`) once the AoS store is verified at scale.
	if false && enumType.RecursivePlain && !(enumType.LayoutSet && enumType.Layout == ast.StructLayoutSOA) {
		return packedEnumABIAoS
	}
	return p.canonicalPackedMode()
}

func (p PackedLoweringProfile) packedModeForStore(storeType *semantic.PackedEnumStoreType) packedEnumABIMode {
	if storeType != nil && storeType.Enum != nil {
		return p.packedModeForPackedEnum(storeType.Enum)
	}
	if p.hasExplicitMode {
		return p.explicitMode
	}
	return p.canonicalPackedMode()
}

func (p PackedLoweringProfile) metadata() semantic.PackedLoweringMetadata {
	return semantic.PackedLoweringMetadata{
		Contract:                          string(p.Contract()),
		CanonicalPackedLowering:           string(PackedEnumABIVariantSparse),
		OnePackedEnumOneHandleInvariant:   true,
		PublicationReadonlyGateStoreState: "Frozen",
	}
}
