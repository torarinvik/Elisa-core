//go:build cgo && linux

package backend

/*
#cgo LDFLAGS: -ldl
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <link.h>
#include <llvm-c/Core.h>
static uintptr_t elisa_llvm_address(void) { return (uintptr_t)&LLVMContextCreate; }
static const char *elisa_llvm_image_path(void) {
    Dl_info info;
    return dladdr((const void *)&LLVMContextCreate, &info) && info.dli_fname ? info.dli_fname : NULL;
}
// RTLD_NOLOAD must prove this exact opened image was already loaded. Also
// compare the owner base: dlsym alone could resolve LLVM through a dependency.
static int elisa_llvm_loaded_alias(const char *alias) {
    Dl_info info;
    if (!dladdr((const void *)&LLVMContextCreate, &info)) return 0;
    void *handle = dlopen(alias, RTLD_NOW | RTLD_NOLOAD);
    if (!handle) return 0;
    struct link_map *map = NULL;
    int same = dlinfo(handle, RTLD_DI_LINKMAP, &map) == 0 && map &&
        (void *)(uintptr_t)map->l_addr == info.dli_fbase &&
        dlsym(handle, "LLVMContextCreate") == (void *)&LLVMContextCreate;
    dlclose(handle);
    return same;
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
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
		path := C.GoString(name)
		dev, ino, err = llvmMappedFileIdentity(path, dev, ino)
		return path, dev, ino, err
	}
	if err := scan.Err(); err != nil {
		return "", 0, 0, err
	}
	return "", 0, 0, fmt.Errorf("cannot identify loaded LLVM image mapping")
}
func llvmFileCTime(s *syscall.Stat_t) (int64, int64) { return s.Ctim.Sec, s.Ctim.Nsec }

// Btrfs can report a subvolume device in fstat while proc maps reports the
// superblock device. Do not ignore that mismatch: ask the dynamic loader to
// match an alias to our opened descriptor against its already-loaded objects.
func llvmMappedFileIdentity(path string, dev, ino uint64) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return 0, 0, fmt.Errorf("loaded LLVM image is not a regular file")
	}
	if uint64(stat.Dev) == dev && stat.Ino == ino {
		return dev, ino, nil
	}
	if !llvmFileIsLoadedImage(file) {
		return 0, 0, fmt.Errorf("loaded LLVM image does not match opened file")
	}
	return uint64(stat.Dev), stat.Ino, nil
}

func llvmFileIsLoadedImage(file *os.File) bool {
	// A private, fresh alias cannot match a stale filename cached by dlopen.
	// The alias keeps naming the opened inode even if its original path changes.
	dir, err := os.MkdirTemp("", ".elisac-llvm-image-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	alias := filepath.Join(dir, "image.so")
	if os.Symlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), alias) != nil {
		return false
	}
	name := C.CString(alias)
	defer C.free(unsafe.Pointer(name))
	return C.elisa_llvm_loaded_alias(name) != 0
}
