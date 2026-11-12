package astscan

import "elisacore/src/astprunedscan"

func WalkPruned(root any, visit func(any) bool) { astprunedscan.WalkPruned(root, visit) }
