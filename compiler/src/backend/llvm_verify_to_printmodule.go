//go:build cgo

package backend

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/opt/llvm/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/opt/llvm/lib -lLLVM-C -lLLVM
#cgo darwin,amd64 CFLAGS: -I/usr/local/opt/llvm/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/opt/llvm/lib -lLLVM-C -lLLVM
#cgo linux CFLAGS: -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo linux LDFLAGS: -L/usr/lib/llvm-21/lib -L/usr/lib/llvm-20/lib -L/usr/lib/llvm-19/lib -L/usr/lib/llvm-18/lib -L/usr/lib/llvm-17/lib -L/usr/lib/llvm-16/lib -L/usr/lib/llvm-15/lib -lLLVM-C -lLLVM
#include <stdlib.h>
#include <llvm-c/Analysis.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
#include <llvm-c/TargetMachine.h>
*/
import "C"

import (
	"fmt"
	"os"
	"strings"
)

func (g *llvmGenerator) verify() error {
	var message *C.char
	status := C.LLVMVerifyModule(g.module, C.LLVMReturnStatusAction, &message)
	if status == 0 {
		if message != nil {
			C.LLVMDisposeMessage(message)
		}
		return nil
	}
	errText := "LLVM module verification failed"
	if message != nil {
		errText = strings.TrimSpace(C.GoString(message))
		C.LLVMDisposeMessage(message)
	}
	if os.Getenv("ELISACORE_DUMP_MODULE_ON_VERIFY_FAILURE") == "1" {
		fmt.Fprintln(os.Stderr, g.printModule())
	}
	return fmt.Errorf("%s", errText)
}
func (g *llvmGenerator) printModule() string {
	text := C.LLVMPrintModuleToString(g.module)
	defer C.LLVMDisposeMessage(text)
	return C.GoString(text)
}
