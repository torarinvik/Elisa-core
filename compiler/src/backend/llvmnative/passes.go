//go:build cgo

// Package llvmnative owns the LLVM C++ passes independently of the Go emitters.
// Backend-only edits can reuse this package's compiled native translation unit.
package llvmnative

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/opt/llvm/include
#cgo darwin,arm64 CXXFLAGS: -std=c++17 -I/opt/homebrew/opt/llvm/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/opt/llvm/lib -lLLVM
#cgo darwin,amd64 CFLAGS: -I/usr/local/opt/llvm/include
#cgo darwin,amd64 CXXFLAGS: -std=c++17 -I/usr/local/opt/llvm/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/opt/llvm/lib -lLLVM
#cgo linux CFLAGS: -I/usr/lib/llvm-23/include -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo linux CXXFLAGS: -std=c++17 -I/usr/lib/llvm-23/include -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo linux LDFLAGS: -L/usr/lib/llvm-23/lib -Wl,-rpath,/usr/lib/llvm-23/lib -L/usr/lib/llvm-21/lib -L/usr/lib/llvm-20/lib -L/usr/lib/llvm-19/lib -L/usr/lib/llvm-18/lib -L/usr/lib/llvm-17/lib -L/usr/lib/llvm-16/lib -L/usr/lib/llvm-15/lib -lLLVM
#cgo windows CFLAGS: -IC:/msys64/mingw64/include
#cgo windows CXXFLAGS: -std=c++17 -IC:/msys64/mingw64/include
#cgo windows LDFLAGS: -LC:/msys64/mingw64/lib -lLLVM-22
#include <llvm-c/Core.h>
unsigned elisacoreBiasValidationReturns(LLVMModuleRef);
*/
import "C"

import "unsafe"

// BiasValidationReturns adjusts branches in validation routines. Calling a native
// pass through this package also links the shared translation unit's entry points
// used by the remaining backend bindings.
func BiasValidationReturns(module unsafe.Pointer) int {
	return int(C.elisacoreBiasValidationReturns(C.LLVMModuleRef(module)))
}
