//go:build cgo

package llvmnative

import "embed"

// Go tracks the unity file but does not follow its C++ includes. Make those
// inputs part of the native package's build key and the Stage0 receipt.
// The variable is never read, so the linker drops it from the executable.
//
//go:embed llvm_wrappers/*.cpp llvm_wrappers/*.h
var llvmWrapperInputs embed.FS
