//go:build cgo

package backend

// #include <stdlib.h>
import "C"

import "unsafe"

// These bindings preserve the existing allocation and release sites; they only
// keep Cgo out of files that manage names and constant-evaluation strings.
func llvmCString(text string) *llvmIRChar  { return C.CString(text) }
func llvmGoString(text *llvmIRChar) string { return C.GoString(text) }
func llvmFree(pointer unsafe.Pointer)      { C.free(pointer) }
