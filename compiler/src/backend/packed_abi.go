package backend

import (
	"fmt"
	"strings"
)

type packedEnumABIMode int

const (
	packedEnumABIIndexSOA packedEnumABIMode = iota
	packedEnumABIVariantSparse
	// packedEnumABIAoS (docs/76 Slice 2): Array-of-Structs — one contiguous {tag, common, payload}
	// record per node in a growable region-backed array, addressed by a dense index handle. No
	// columns; one record write per node. The default for a recursive plain enum.
	packedEnumABIAoS
)

type PackedEnumABI string

const (
	PackedEnumABIDenseFixed    PackedEnumABI = "dense-fixed"
	PackedEnumABIIndexSOA      PackedEnumABI = "index-soa"
	PackedEnumABIVariantSparse PackedEnumABI = "variant-sparse"
)

func ParsePackedEnumABI(value string) (PackedEnumABI, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(PackedEnumABIDenseFixed), "dense_fixed", "densefixed", "fixed_dense", "fixed-dense":
		return PackedEnumABIDenseFixed, nil
	case string(PackedEnumABIIndexSOA), "index", "soa", "indexsoa", "index_soa":
		return PackedEnumABIIndexSOA, nil
	case string(PackedEnumABIVariantSparse), "variant", "variantsparse", "variant_sparse", "sparse":
		return PackedEnumABIVariantSparse, nil
	default:
		return "", fmt.Errorf("unsupported packed enum ABI %q (expected %q, %q, or %q)", value, PackedEnumABIDenseFixed, PackedEnumABIIndexSOA, PackedEnumABIVariantSparse)
	}
}
func (abi PackedEnumABI) mode() (packedEnumABIMode, error) {
	normalized, err := ParsePackedEnumABI(string(abi))
	if err != nil {
		return packedEnumABIIndexSOA, err
	}
	switch normalized {
	case PackedEnumABIDenseFixed:
		return packedEnumABIIndexSOA, nil
	case PackedEnumABIIndexSOA:
		return packedEnumABIIndexSOA, nil
	case PackedEnumABIVariantSparse:
		return packedEnumABIVariantSparse, nil
	default:
		return packedEnumABIIndexSOA, fmt.Errorf("unsupported packed enum ABI %q", abi)
	}
}
func packedModeUsesDenseIndexHandle(mode packedEnumABIMode) bool {
	return mode == packedEnumABIIndexSOA || mode == packedEnumABIVariantSparse || mode == packedEnumABIAoS
}
func packedModeIsAoS(mode packedEnumABIMode) bool {
	return mode == packedEnumABIAoS
}
func packedModeUsesDirectWordReads(mode packedEnumABIMode) bool {
	// AoS reads fields from the per-node record (base+index*stride+offset), not from store columns,
	// so it is NOT a column-word-read mode — it has its own read path.
	return mode == packedEnumABIIndexSOA || mode == packedEnumABIVariantSparse
}
func packedModeName(mode packedEnumABIMode) string {
	switch mode {
	case packedEnumABIIndexSOA:
		return string(PackedEnumABIIndexSOA)
	case packedEnumABIVariantSparse:
		return string(PackedEnumABIVariantSparse)
	case packedEnumABIAoS:
		return "aos"
	default:
		return fmt.Sprintf("mode-%d", mode)
	}
}
