package backend

import "elisacore/src/semantic"

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
