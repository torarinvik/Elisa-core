//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
#include <llvm-c/TargetMachine.h>

#if defined(__unix__) || defined(__APPLE__)
#include <pthread.h>

enum {
	elisaPthreadMutexSize = sizeof(pthread_mutex_t),
	elisaPthreadMutexAlign = _Alignof(pthread_mutex_t),
	elisaPthreadCondSize = sizeof(pthread_cond_t),
	elisaPthreadCondAlign = _Alignof(pthread_cond_t),
	elisaPthreadOnceSize = sizeof(pthread_once_t),
	elisaPthreadOnceAlign = _Alignof(pthread_once_t),
};
#else
enum {
	elisaPthreadMutexSize = 0, elisaPthreadMutexAlign = 0,
	elisaPthreadCondSize = 0, elisaPthreadCondAlign = 0,
	elisaPthreadOnceSize = 0, elisaPthreadOnceAlign = 0,
};
#endif
*/
import "C"

import (
	"fmt"
	"strings"

	"elisacore/src/semantic"
)

func pthreadOpaqueLayout(t *semantic.OpaqueType) (size, alignment int) {
	if t.CHeader != "pthread.h" {
		return 0, 0
	}
	switch t.CType {
	case "pthread_mutex_t":
		return C.elisaPthreadMutexSize, C.elisaPthreadMutexAlign
	case "pthread_cond_t":
		return C.elisaPthreadCondSize, C.elisaPthreadCondAlign
	case "pthread_once_t":
		return C.elisaPthreadOnceSize, C.elisaPthreadOnceAlign
	default:
		return 0, 0
	}
}

func (g *llvmGenerator) lowerOpaqueType(t *semantic.OpaqueType) (C.LLVMTypeRef, error) {
	size, alignment := pthreadOpaqueLayout(t)
	if size == 0 {
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	}
	if requested := strings.TrimSpace(g.requestedTargetTriple); requested != "" {
		host := C.LLVMGetDefaultTargetTriple()
		native := C.GoString(host) == requested
		C.LLVMDisposeMessage(host)
		if !native {
			return nil, fmt.Errorf("inline %s requires the host pthread layout", t.CType)
		}
	}
	if err := g.ensureTargetMachine(); err != nil {
		return nil, err
	}
	// The word array preserves the C alignment when embedded in another type.
	word := C.LLVMIntTypeInContext(g.context, C.uint(8*alignment))
	storage := C.LLVMArrayType2(word, C.uint64_t(size/alignment))
	if int(C.LLVMABISizeOfType(g.targetData, storage)) != size ||
		int(C.LLVMABIAlignmentOfType(g.targetData, storage)) != alignment {
		return nil, fmt.Errorf("cannot represent inline %s with its host size and alignment", t.CType)
	}
	return storage, nil
}
