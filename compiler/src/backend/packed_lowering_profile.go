package backend

import "llcontext/src/semantic"

type PackedLoweringContract string

const (
	PackedLoweringContractCanonicalCompilerGraph PackedLoweringContract = "canonical-compiler-graph"
	PackedLoweringContractLegacyOverride         PackedLoweringContract = "legacy-override"
)

type PackedLoweringProfile struct {
	contract          PackedLoweringContract
	legacyOverride    PackedEnumABI
	hasLegacyOverride bool
	legacyMode        packedEnumABIMode
}

func DefaultPackedLoweringProfile() PackedLoweringProfile {
	return PackedLoweringProfile{contract: PackedLoweringContractCanonicalCompilerGraph}
}

func LegacyPackedLoweringProfile(abi PackedEnumABI) (PackedLoweringProfile, error) {
	normalized, err := ParsePackedEnumABI(string(abi))
	if err != nil {
		return PackedLoweringProfile{}, err
	}
	mode, err := normalized.mode()
	if err != nil {
		return PackedLoweringProfile{}, err
	}
	return PackedLoweringProfile{
		contract:          PackedLoweringContractLegacyOverride,
		legacyOverride:    normalized,
		hasLegacyOverride: true,
		legacyMode:        mode,
	}, nil
}

func mustLegacyPackedLoweringProfile(abi PackedEnumABI) PackedLoweringProfile {
	profile, err := LegacyPackedLoweringProfile(abi)
	if err != nil {
		panic(err)
	}
	return profile
}

func (p PackedLoweringProfile) Contract() PackedLoweringContract {
	if p.contract == "" {
		return PackedLoweringContractCanonicalCompilerGraph
	}
	return p.contract
}

func (p PackedLoweringProfile) HasLegacyOverride() bool {
	return p.hasLegacyOverride
}

func (p PackedLoweringProfile) LegacyOverride() (PackedEnumABI, bool) {
	if !p.hasLegacyOverride {
		return "", false
	}
	return p.legacyOverride, true
}

func (p PackedLoweringProfile) canonicalPackedMode() packedEnumABIMode {
	return packedEnumABIIndexSOA
}

func (p PackedLoweringProfile) packedModeForPackedEnum(enumType *semantic.EnumType) packedEnumABIMode {
	if enumType == nil || !enumType.Packed {
		return packedEnumABIRowHandle
	}
	if p.hasLegacyOverride {
		return p.legacyMode
	}
	return p.canonicalPackedMode()
}

func (p PackedLoweringProfile) packedModeForStore(storeType *semantic.PackedEnumStoreType) packedEnumABIMode {
	if storeType != nil && storeType.Enum != nil {
		return p.packedModeForPackedEnum(storeType.Enum)
	}
	if p.hasLegacyOverride {
		return p.legacyMode
	}
	return p.canonicalPackedMode()
}

func (p PackedLoweringProfile) metadata() semantic.PackedLoweringMetadata {
	legacyOverride := ""
	if p.hasLegacyOverride {
		legacyOverride = string(p.legacyOverride)
	}
	return semantic.PackedLoweringMetadata{
		Contract:                          string(p.Contract()),
		CanonicalPackedLowering:           string(PackedEnumABIIndexSOA),
		LegacyOverride:                    legacyOverride,
		UsesLegacyOverride:                p.hasLegacyOverride,
		OnePackedEnumOneHandleInvariant:   true,
		PublicationReadonlyGateStoreState: "Frozen",
	}
}
