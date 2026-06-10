package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeImplicitContextFixture writes a one-off .elisa fixture into a temp dir
// and returns its path. (The name predates the implicit-context feature removal;
// it is a generic fixture writer shared by several CLI feature tests.)
func writeImplicitContextFixture(t *testing.T, name string, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture %s: %v", name, err)
	}
	return path
}
