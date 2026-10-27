//go:build darwin || linux

package main

import (
	"bytes"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// withPayload validates an immutable artifact and keeps its read-only mapping
// alive only for the duration of fn. Publishing uses rename, so readers see a
// stable inode while decoding and avoid copying the whole snapshot into heap.
func (a analysisDiskArtifact) withPayload(fn func([]byte) error) (resultErr error) {
	f, err := os.Open(a.path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	header := a.header()
	maxInt := int64(int(^uint(0) >> 1))
	if !info.Mode().IsRegular() || info.Size() <= int64(len(header)) || info.Size() > analysisStateMaxBytes || info.Size() > maxInt {
		return fmt.Errorf("invalid analysis cache size")
	}

	mapped, err := unix.Mmap(int(f.Fd()), 0, int(info.Size()), unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		payload, readErr := a.read()
		if readErr != nil {
			return readErr
		}
		return fn(payload)
	}
	defer func() {
		if err := unix.Munmap(mapped); resultErr == nil && err != nil {
			resultErr = err
		}
	}()

	// Match read()'s fail-closed behavior if another actor changes file size
	// while opening and mapping the artifact.
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() {
		return fmt.Errorf("analysis cache size changed during read")
	}
	if !bytes.HasPrefix(mapped, header) {
		return fmt.Errorf("analysis cache identity mismatch")
	}
	return fn(mapped[len(header):])
}
