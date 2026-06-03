package semantic

// Region backing strategies (docs/68 §3). A region's backing governs how it
// grows, whether its interior pointers are stable, and whether it can be spliced
// into another region by `adopt`. The surface keeps the selector in
// RegionStmt.Allocator (`region NAME(cap) using <strategy>`); an empty selector
// and the legacy "malloc" override both denote chained-block backing.

func validRegionBacking(allocator string) bool {
	switch allocator {
	case "", "fixed", "chained", "reserve_commit", "scratch", "malloc":
		return true
	default:
		return false
	}
}

// normalizeBacking maps a backing selector to its canonical family name. Idempotent.
func normalizeBacking(allocator string) string {
	switch allocator {
	case "", "chained", "malloc":
		return "chained"
	default:
		return allocator
	}
}

// backingFamiliesCompatible reports whether a child region can be spliced into a
// parent by `adopt` (docs/67 §3.5). For now adoption requires the same normalized
// family (e.g. chained↔chained, reserve_commit↔reserve_commit).
func backingFamiliesCompatible(child, parent string) bool {
	return normalizeBacking(child) == normalizeBacking(parent)
}
