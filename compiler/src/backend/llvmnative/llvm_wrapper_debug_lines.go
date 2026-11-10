//go:build cgo && !elisac_full_debug

package llvmnative

// The LLVM C++ wrapper unity file is built with line tables only by default. This keeps
// file:line in backtraces and skips variable debug info, which measured ~0.9 s of the cold
// Stage0 build. cgo's default CXXFLAGS already carry -g, and the last debug option wins,
// so this directive overrides it. Build with `-tags elisac_full_debug` (Makefile:
// ELISAC_FULL_DEBUG=true) to keep the full -g from cgo's defaults.

/*
#cgo CXXFLAGS: -gline-tables-only
*/
import "C"
