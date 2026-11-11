//go:build cgo && !elisac_full_debug

package storagepasses

// Match the default debug level of the other native LLVM passes.
// The elisac_full_debug tag keeps the full debug information from cgo.

/*
#cgo CXXFLAGS: -gline-tables-only
*/
import "C"
