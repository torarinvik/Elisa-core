//go:build cgo && linux

package backend

/*
#cgo LDFLAGS: -ldl
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdint.h>
#include <llvm-c/Core.h>
static uintptr_t elisa_llvm_address(void) { return (uintptr_t)&LLVMContextCreate; }
static const char *elisa_llvm_image_path(void) {
    Dl_info info;
    return dladdr((const void *)&LLVMContextCreate, &info) && info.dli_fname ? info.dli_fname : NULL;
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func llvmLoadedImage() (string, uint64, uint64, error) {
	name := C.elisa_llvm_image_path()
	if name == nil {
		return "", 0, 0, fmt.Errorf("cannot locate loaded LLVM image")
	}
	address := uint64(C.elisa_llvm_address())
	maps, err := os.Open("/proc/self/maps")
	if err != nil {
		return "", 0, 0, err
	}
	defer maps.Close()
	scan := bufio.NewScanner(maps)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) < 5 {
			continue
		}
		bounds := strings.Split(fields[0], "-")
		if len(bounds) != 2 {
			continue
		}
		start, e1 := strconv.ParseUint(bounds[0], 16, 64)
		end, e2 := strconv.ParseUint(bounds[1], 16, 64)
		if e1 != nil || e2 != nil || address < start || address >= end {
			continue
		}
		device := strings.Split(fields[3], ":")
		if len(device) != 2 {
			break
		}
		major, e1 := strconv.ParseUint(device[0], 16, 32)
		minor, e2 := strconv.ParseUint(device[1], 16, 32)
		ino, e3 := strconv.ParseUint(fields[4], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || ino == 0 {
			break
		}
		// Linux's dev_t packs the major and minor values in non-contiguous bits.
		dev := ((major & 0xfff) << 8) | ((major & 0xfffff000) << 32) | (minor & 0xff) | ((minor & 0xffffff00) << 12)
		return C.GoString(name), dev, ino, nil
	}
	if err := scan.Err(); err != nil {
		return "", 0, 0, err
	}
	return "", 0, 0, fmt.Errorf("cannot identify loaded LLVM image mapping")
}
func llvmFileCTime(s *syscall.Stat_t) (int64, int64) { return s.Ctim.Sec, s.Ctim.Nsec }
