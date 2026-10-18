package backend

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIncrementalMergerOverridesStayCurrent(t *testing.T) {
	session := NewIncrementalObjectSession(8, 0)
	for _, value := range []string{"first-linker --first", "second-linker --second"} {
		t.Setenv("ELISA_LD", value)
		want := []string{"first-linker", "--first"}
		if value == "second-linker --second" {
			want = []string{"second-linker", "--second"}
		}
		if got := session.objectMergerCommand(); !reflect.DeepEqual(got, want) {
			t.Fatalf("override=%v want %v", got, want)
		}
	}
	if session.defaultMerger != "" {
		t.Fatal("explicit override unnecessarily discovered a default toolchain")
	}
}

func TestIncrementalMergerRespectsCustomPathLinker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ld"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ELISA_LD", "")
	got := NewIncrementalObjectSession(8, 0).objectMergerCommand()
	if !reflect.DeepEqual(got, []string{"ld"}) {
		t.Fatalf("custom linker bypassed: %v", got)
	}
}
