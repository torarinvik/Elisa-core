package artifactidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenedHashTracksOpenArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	old, err := OpenedHash(f, info)
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	again, err := OpenedHash(f, info)
	if err != nil || again != old {
		t.Fatalf("opened identity changed: %v", err)
	}
}

func TestOpenedHashRejectsChangedOrMissingArtifact(t *testing.T) {
	if _, err := OpenedHash(nil, nil); err == nil {
		t.Fatal("accepted missing artifact")
	}
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("different length"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenedHash(f, info); err == nil {
		t.Fatal("accepted modified opened artifact")
	}
}
