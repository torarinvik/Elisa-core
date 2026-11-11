package semantic

import "maps"

// Cache complete positive signatures while declaration shapes are fixed. A
// type walk shares depth and first-witness state across roots, so only reuse
// an exact ordered sequence. Keep larger signatures on the ordinary path.
// The caller supplies an empty output map.
func collectPackedStoreSignature(ft *FuncType, out map[string]*EnumType, fieldRoots map[*StructType][]Type, emptyRoots map[Type]bool, signatures map[[2]Type]map[string]*EnumType) {
	var key [2]Type
	count := 0
	explicit := min(funcTypeExplicitParamCount(ft), len(ft.Params))
	for i := 0; i <= explicit; i++ {
		t := ft.Return
		if i < explicit {
			t = ft.Params[i]
		}
		root := packedStoreSignatureRoot(t)
		if root == nil || emptyRoots[root] {
			continue
		}
		if count == len(key) {
			// Continue in signature order without rescanning the prefix.
			walk := packedTypeWalk{fieldRoots: fieldRoots}
			for _, prior := range key {
				walk.collectSignatureRoot(prior, out, emptyRoots)
			}
			walk.collectSignatureRoot(root, out, emptyRoots)
			for j := i + 1; j < explicit; j++ {
				walk.collectSignatureRoot(ft.Params[j], out, emptyRoots)
			}
			if i < explicit {
				walk.collectSignatureRoot(ft.Return, out, emptyRoots)
			}
			releasePackedTypeSeen(walk.seen)
			return
		}
		key[count] = root
		count++
	}
	if count == 0 {
		return
	}
	if cached := signatures[key]; cached != nil {
		maps.Copy(out, cached)
		return
	}
	walk := packedTypeWalk{fieldRoots: fieldRoots}
	for _, root := range key[:count] {
		walk.collectSignatureRoot(root, out, emptyRoots)
	}
	if walk.seen != nil {
		releasePackedTypeSeen(walk.seen)
	}
	if len(out) != 0 {
		signatures[key] = maps.Clone(out)
	}
}
