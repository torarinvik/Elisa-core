//go:build cgo && (darwin || linux)

package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// The dynamic loader can continue executing a previously mapped library after
// its pathname has been replaced. Pin the image only if the opened file is
// the same inode as the mapping containing an LLVM function. This also covers
// statically linked LLVM: its mapping is the running executable.
type pinnedLLVMLibrary struct {
	file                *os.File
	dev, ino            uint64
	size                int64
	mtime               int64
	ctimeSec, ctimeNsec int64
	mu                  sync.Mutex
	digest              string
}

var llvmLibrary, llvmLibraryError = pinLLVMLibrary()

func pinLLVMLibrary() (*pinnedLLVMLibrary, error) {
	path, dev, ino, err := llvmLoadedImage()
	if err != nil {
		return nil, err
	}
	return pinLLVMLibraryFile(path, dev, ino)
}

func pinLLVMLibraryFile(path string, dev, ino uint64) (*pinnedLLVMLibrary, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || uint64(stat.Dev) != dev || stat.Ino != ino {
		file.Close()
		return nil, fmt.Errorf("loaded LLVM image does not match opened file")
	}
	sec, nsec := llvmFileCTime(stat)
	return &pinnedLLVMLibrary{file: file, dev: dev, ino: ino, size: info.Size(), mtime: info.ModTime().UnixNano(), ctimeSec: sec, ctimeNsec: nsec}, nil
}

// LLVMLibraryIdentity returns a content digest for the LLVM image actually
// mapped into this process. Failures mean native caches must not be used.
func LLVMLibraryIdentity() (string, error) {
	if llvmLibraryError != nil {
		return "", llvmLibraryError
	}
	if llvmLibrary == nil {
		return "", fmt.Errorf("loaded LLVM image unavailable")
	}
	return llvmLibrary.stamp()
}

// LLVMLibraryCacheDependency returns the path and content digest for the LLVM
// image mapped into this process. Outer compiler caches use this pair to
// invalidate results when LLVM is replaced without rebuilding Stage0.
func LLVMLibraryCacheDependency() (string, string, error) {
	if llvmLibraryError != nil {
		return "", "", llvmLibraryError
	}
	if llvmLibrary == nil || llvmLibrary.file == nil {
		return "", "", fmt.Errorf("loaded LLVM image unavailable")
	}
	path, err := filepath.Abs(llvmLibrary.file.Name())
	if err != nil {
		return "", "", err
	}
	digest, err := llvmLibrary.stamp()
	if err != nil {
		return "", "", err
	}
	return path, digest, nil
}

func (p *pinnedLLVMLibrary) stamp() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	unchanged := func() error {
		info, err := p.file.Stat()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("LLVM image file identity unavailable")
		}
		sec, nsec := llvmFileCTime(stat)
		if !info.Mode().IsRegular() || uint64(stat.Dev) != p.dev || stat.Ino != p.ino || info.Size() != p.size || info.ModTime().UnixNano() != p.mtime || sec != p.ctimeSec || nsec != p.ctimeNsec {
			return fmt.Errorf("loaded LLVM image file changed; native cache disabled")
		}
		return nil
	}
	if err := unchanged(); err != nil {
		return "", err
	}
	if p.digest != "" {
		return p.digest, nil
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(p.file, 0, p.size)); err != nil {
		return "", err
	}
	if err := unchanged(); err != nil {
		return "", err
	}
	p.digest = hex.EncodeToString(hash.Sum(nil))
	return p.digest, nil
}
