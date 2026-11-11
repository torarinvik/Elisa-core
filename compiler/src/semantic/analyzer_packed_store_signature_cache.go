package semantic

import (
	"maps"
	"slices"
)

type packedStoreSignatureCache struct {
	short map[[2]Type]map[string]*EnumType
	long  map[[4]Type]map[string]*EnumType
}

// Cache complete positive signatures while declaration shapes are fixed. A
// type walk shares depth and first-witness state across roots, so only reuse
// an exact ordered sequence. Keep larger signatures on the ordinary path.
// The caller supplies an empty output map.
func collectPackedStoreSignature(ft *FuncType, out map[string]*EnumType, fieldRoots map[*StructType][]Type, emptyRoots map[Type]bool, signatures *packedStoreSignatureCache) {
	var key [4]Type
	count := 0
	explicit := min(funcTypeExplicitParamCount(ft), len(ft.Params))
	for i := 0; i <= explicit; i++ {
		t := ft.Return
		if i < explicit {
			t = ft.Params[i]
		}
		root := packedStoreSignatureRoot(t)
		// Each root is visited at depth zero. Revisiting the same type
		// cannot expose a shallower path or change the first root witness.
		if root == nil || emptyRoots[root] || slices.Contains(key[:count], root) {
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
	var cached map[string]*EnumType
	if count <= 2 {
		cached = signatures.short[[2]Type(key[:2])]
	} else {
		cached = signatures.long[key]
	}
	if cached != nil {
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
	if len(out) == 0 {
		return
	}
	if count <= 2 {
		if signatures.short == nil {
			signatures.short = make(map[[2]Type]map[string]*EnumType)
		}
		signatures.short[[2]Type(key[:2])] = maps.Clone(out)
	} else {
		if signatures.long == nil {
			signatures.long = make(map[[4]Type]map[string]*EnumType)
		}
		signatures.long[key] = maps.Clone(out)
	}
}
