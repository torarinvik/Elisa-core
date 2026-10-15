//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// programRegionArena returns the arena of a program-lived region (docs/128 section 5): one
// zero-initialized Arena global per region name. A zero Arena is a valid lazy arena (the
// first allocation creates its first block), it is never reset or freed, and the OS
// releases it at exit. linkonce merges the definitions emitted by separately compiled
// objects into one arena.
func (s *functionState) programRegionArena(name string) (valueBinding, bool, error) {
	if s == nil || s.g == nil || !s.g.result.IsProgramRegion(name) {
		return valueBinding{}, false, nil
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	if arenaType == nil {
		return valueBinding{}, false, fmt.Errorf("missing builtin Arena type for program-lived region %s", name)
	}
	key := "__elisa_program_region." + name
	if value, ok := s.g.globals[key]; ok {
		return valueBinding{ptr: value, typ: arenaType}, true, nil
	}
	llvmType, err := s.g.lowerType(arenaType)
	if err != nil {
		return valueBinding{}, false, err
	}
	nameC := C.CString(key)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddGlobal(s.g.module, llvmType, nameC)
	C.LLVMSetInitializer(value, C.LLVMConstNull(llvmType))
	C.LLVMSetLinkage(value, C.LLVMLinkOnceAnyLinkage)
	s.g.applyTypeAlignment(value, arenaType)
	s.g.globals[key] = value
	return valueBinding{ptr: value, typ: arenaType}, true, nil
}
