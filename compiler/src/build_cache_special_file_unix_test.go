//go:build darwin || freebsd || linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBuildCacheRejectsSpecialFilesAndSymlinks(t *testing.T) {
	for _, entry := range []string{"transcript.json", "stdout.bin", "stderr.bin", "out.o"} {
		for _, kind := range []string{"fifo", "symlink"} {
			t.Run(entry+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				built := filepath.Join(dir, "built.o")
				if err := os.WriteFile(built, []byte("object"), 0600); err != nil {
					t.Fatal(err)
				}
				a := buildCacheArtifact{key: "key", dir: filepath.Join(dir, "cache"), object: filepath.Join(dir, "cache", "out.o")}
				if err := publishCachedBuildObject(a, built, buildCacheTranscript{Stdout: []byte("out"), Stderr: []byte("err")}); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(a.dir, entry)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "fifo" {
					if err := syscall.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(built, path); err != nil {
					t.Fatal(err)
				}
				tr, ok := readBuildCacheTranscript(a)
				if entry == "out.o" {
					if !ok {
						t.Fatal("unexpected transcript rejection")
					}
					if buildCacheObjectMatches(a, tr) {
						t.Fatal("special object admitted")
					}
					if err := restoreBuildCacheObject(a, filepath.Join(dir, "output.o"), tr); err == nil {
						t.Fatal("special object restored")
					}
				} else if ok {
					t.Fatal("special transcript entry admitted")
				}
			})
		}
	}
}
