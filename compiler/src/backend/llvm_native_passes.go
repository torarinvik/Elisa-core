//go:build cgo

package backend

// Link the pass groups at their common caller. A dependency between native
// packages would make Go rebuild the caller's C++ translation unit after an
// edit to the callee, even when the native interface has stayed the same.
import (
	_ "elisacore/src/backend/llvmnative/digitcounts"
	_ "elisacore/src/backend/llvmnative/divisionloops"
	_ "elisacore/src/backend/llvmnative/looptransforms"
	_ "elisacore/src/backend/llvmnative/storagepasses"
)
