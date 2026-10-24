//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeFragmentsAcrossProcesses(t *testing.T) {
	if os.Getenv("ELISA_TEST_FRAGMENT_CHILD") == "1" {
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "1")
		source := incrementalObjectFixture(32)
		want := 54
		revision := os.Getenv("ELISA_TEST_FRAGMENT_REVISION")
		if revision == "corrupt" {
			source = strings.Replace(source, "return value + 1\n", "return value + 8\n", 1)
			want = 61
		}
		if revision == "body" {
			source = strings.Replace(source, "return value + 1\n", "return value + 7\n", 1)
			want = 60
		}
		result := parseAndAnalyzeBackendTest(t, "native-fragments.elisa", source)
		session := NewIncrementalObjectSession(1, 32<<20)
		session.minWeight = 0
		session.EnableDiskCache(os.Getenv("ELISA_TEST_FRAGMENT_ROOT"), "fragment-regression", 64<<20)
		output := filepath.Join(t.TempDir(), "module.o")
		if err := session.WriteObject(result, output, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}); err != nil {
			t.Fatal(err)
		}
		stats := session.Stats()
		switch revision {
		case "base", "corrupt":
			if stats.NativeFragmentsEmitted != nativeFragmentCount || stats.NativeFragmentsReused != 0 {
				t.Fatalf("cold fragments: %+v", stats)
			}
		case "body":
			if stats.NativeFragmentsEmitted == 0 || stats.NativeFragmentsReused == 0 || stats.NativeFragmentsEmitted+stats.NativeFragmentsReused != nativeFragmentCount {
				t.Fatalf("missing exact fragment reuse: %+v", stats)
			}
		default:
			if stats.DiskPartitionsReused != 1 || stats.NativeFragmentsEmitted != 0 {
				t.Fatalf("whole-unit repeat: %+v", stats)
			}
		}
		if stats.NativeFragmentFallbacks != 0 {
			t.Fatalf("unexpected decline: %+v", stats)
		}
		if got := incrementalObjectNativeStatus(t, output); got != want {
			t.Fatalf("native=%d want %d", got, want)
		}
		return
	}
	root := t.TempDir()
	for _, revision := range []string{"base", "repeat", "body", "revert", "corrupt"} {
		if revision == "corrupt" {
			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && strings.Contains(path, "native-fragments-v1") && strings.HasSuffix(path, ".part") {
					return os.WriteFile(path, []byte("BROKEN"), 0600)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeFragmentsAcrossProcesses$", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "ELISACORE_TEST_NATIVE_WORKERS=1", "ELISA_TEST_FRAGMENT_CHILD=1", "ELISA_TEST_FRAGMENT_ROOT="+root, "ELISA_TEST_FRAGMENT_REVISION="+revision)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s: %v\n%s", revision, err, output)
		}
	}
}

func TestNativeFragmentMergeKeyTracksToolAndArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "linker")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	session := NewIncrementalObjectSession(1, 1<<20)
	t.Setenv("ELISA_LD", path)
	first, err := session.nativeFragmentMergeKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELISA_LD", path+" --changed")
	second, err := session.nativeFragmentMergeKey()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("merger arguments omitted from key")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	third, err := session.nativeFragmentMergeKey()
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("merger contents omitted from key")
	}
}
