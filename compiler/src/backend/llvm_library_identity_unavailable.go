//go:build !cgo || (!darwin && !linux)

package backend

import "fmt"

// Native cache reuse is disabled where the loaded LLVM image cannot be pinned.
func LLVMLibraryIdentity() (string, error) {
	return "", fmt.Errorf("loaded LLVM image identity is unavailable on this platform")
}

func LLVMLibraryCacheDependency() (string, string, error) {
	return "", "", fmt.Errorf("loaded LLVM image identity is unavailable on this platform")
}
