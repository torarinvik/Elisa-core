//go:build cgo && darwin

package backend

/*
#include <dlfcn.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <llvm-c/Core.h>

static int elisa_loaded_llvm_image(char *path, size_t n, uint64_t *dev, uint64_t *ino) {
    Dl_info info;
    struct proc_regionwithpathinfo region;
    if (!dladdr((const void *)&LLVMContextCreate, &info) || !info.dli_fbase) return 0;
    int count = proc_pidinfo(getpid(), PROC_PIDREGIONPATHINFO, (uint64_t)(uintptr_t)info.dli_fbase, &region, sizeof(region));
    if (count != sizeof(region) || !region.prp_vip.vip_path[0]) return 0;
    size_t len = strlen(region.prp_vip.vip_path);
    if (len + 1 > n) return 0;
    memcpy(path, region.prp_vip.vip_path, len + 1);
    *dev = region.prp_vip.vip_vi.vi_stat.vst_dev;
    *ino = region.prp_vip.vip_vi.vi_stat.vst_ino;
    return *ino != 0;
}
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"
)

func llvmLoadedImage() (string, uint64, uint64, error) {
	var path [4096]C.char
	var dev, ino C.uint64_t
	if C.elisa_loaded_llvm_image((*C.char)(unsafe.Pointer(&path[0])), C.size_t(len(path)), &dev, &ino) == 0 {
		return "", 0, 0, fmt.Errorf("cannot identify loaded LLVM image mapping")
	}
	return C.GoString(&path[0]), uint64(dev), uint64(ino), nil
}
func llvmFileCTime(s *syscall.Stat_t) (int64, int64) { return s.Ctimespec.Sec, s.Ctimespec.Nsec }
