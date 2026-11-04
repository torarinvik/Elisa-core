//go:build cgo && linux

package backend

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLoadedLLVMDescriptorIdentity(t *testing.T) {
	path, dev, ino, err := llvmLoadedImage()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !llvmFileIsLoadedImage(file) {
		t.Fatal("loader did not recognize its opened LLVM image")
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	expected := info.Sys().(*syscall.Stat_t)
	// Force the fallback even on filesystems whose map and stat devices agree.
	actualDev, actualIno, err := llvmMappedFileIdentity(path, dev^1, ino)
	if err != nil || actualDev != uint64(expected.Dev) || actualIno != expected.Ino {
		t.Fatalf("descriptor identity: %x/%d, %v", actualDev, actualIno, err)
	}

	// Identical bytes at another inode are not the image mapped by this process.
	copyPath := filepath.Join(t.TempDir(), "llvm-copy.so")
	copyFile, err := os.Create(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(copyFile, file); err != nil {
		copyFile.Close()
		t.Fatal(err)
	}
	if err := copyFile.Close(); err != nil {
		t.Fatal(err)
	}
	copied, err := os.Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	if llvmFileIsLoadedImage(copied) {
		t.Fatal("accepted an unmapped copy of LLVM")
	}
	if _, _, err := llvmMappedFileIdentity(copyPath, dev, ino); err == nil {
		t.Fatal("accepted replacement inode as the mapped image")
	}
}

// Exercise the actual C loader check against a tiny DSO, so replacing its
// original pathname cannot accidentally validate the replacement's inode.
func TestLoadedLLVMOriginalPathReplacement(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler unavailable")
	}
	dir := t.TempDir()
	raw, err := os.ReadFile("llvm_library_identity_linux.go")
	if err != nil {
		t.Fatal(err)
	}
	block := strings.SplitN(strings.SplitN(string(raw), "/*", 2)[1], "*/", 2)[0]
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "#cgo") {
			continue
		}
		if line == "#include <llvm-c/Core.h>" {
			line = "extern void *LLVMContextCreate(void);"
		}
		lines = append(lines, line)
	}
	fixture := filepath.Join(dir, "fixture.c")
	if err := os.WriteFile(fixture, []byte("void *LLVMContextCreate(void) { return 0; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	lib := filepath.Join(dir, "libfixture.so")
	run(cc, "-shared", "-fPIC", fixture, "-Wl,-soname,libfixture.so", "-o", lib)
	replacement := lib + ".new"
	contents, err := os.ReadFile(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, contents, 0600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "probe.c")
	source := strings.Join(lines, "\n") + `
#include <stdio.h>
#include <unistd.h>
#include <fcntl.h>
static int matches(int fd) {
 char dir[]="/tmp/elisa-image-test-XXXXXX", alias[512], target[128];
 if (!mkdtemp(dir)) return -1;
 snprintf(alias,sizeof(alias),"%s/image.so",dir);
 snprintf(target,sizeof(target),"/proc/self/fd/%d",fd);
 if (symlink(target,alias)) { rmdir(dir); return -1; }
 int result=elisa_llvm_loaded_alias(alias);
 unlink(alias); rmdir(dir); return result;
}
int main(int argc,char **argv) {
 if(argc!=3)return 10;
 int original=open(argv[1],O_RDONLY); if(original<0)return 11;
 if(matches(original)!=1)return 12;
 if(rename(argv[2],argv[1]))return 13;
 int replacement=open(argv[1],O_RDONLY); if(replacement<0)return 14;
 if(matches(replacement)!=0)return 15;
 if(matches(original)!=1)return 16;
 close(original);close(replacement);return 0;
}
`
	if err := os.WriteFile(probe, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "probe")
	run(cc, "-fPIC", probe, "-L"+dir, "-lfixture", "-Wl,-rpath,"+dir, "-ldl", "-o", binary)
	run(binary, lib, replacement)
}
