package semantic

import "elisacore/src/astscan"

func walkPruned(root any, visit func(any) bool) {
	astscan.WalkPruned(root, visit)
}
