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
	// docs/77: the lowering mode is a ROOT-level fact — a sealed hierarchy shares ONE store, so
	// every sub-category must resolve the same mode as the root that shaped it (a child carries no
	// layout suffix of its own; consulting the child's fields here would split the hierarchy
	// between AoS constructors and a columnar root store).
	root := enumType.Root()
	if root.HasPackedABIOverride {
		if abi, err := ParsePackedEnumABI(root.PackedABIOverride); err == nil {
			if mode, err := abi.mode(); err == nil {
				return mode
			}
		}
	}
	// docs/76: a recursive plain enum defaults to AoS (one contiguous {tag, common, payload} record
	// per node, dense index handle) — the speed default — unless the author opted into the columnar
	// layout with `enum X layout soa`. Verified at scale (binary-trees N=18: 0.38s/22MB, ~5.5x faster
	// and 4x lighter than SoA, beating the struct form and hand-written C++/Rust arenas).
	if root.RecursivePlain && !(root.LayoutSet && root.Layout == ast.StructLayoutSOA) {
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
