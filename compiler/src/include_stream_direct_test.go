package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkReadSourceNestedDirectIncludes(b *testing.B) {
	dir := b.TempDir()
	for i := 15; i >= 0; i-- {
		src := strings.Repeat("value: i64 = 1\n", 256)
		if i < 15 {
			src += fmt.Sprintf("    include \"part%d.elisa\"\n", i+1)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("part%d.elisa", i)), []byte(src), 0600); err != nil {
			b.Fatal(err)
		}
	}
	root := filepath.Join(dir, "part0.elisa")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := readSourceWithIncludes(root, map[string]bool{}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDirectAndProjectIncludeStreamingAgree(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"root.elisa":   "extend Outer:\n    include \"middle.elisa\"\ninclude \"leaf.elisa\"\nend: i64 = 0",
		"middle.elisa": "extend Inner:\n\tinclude \"leaf.elisa\"\n\n#line 900 pretend.elisa\n    #line\t1000 fake.elisa\nlast: i64 = 2",
		"leaf.elisa":   "\nvalue: i64 = 1\n\nvalue2: i64 = 2",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, "root.elisa")
	direct, err := readSourceWithIncludes(root, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	project, err := readSourceWithIncludesWithOptions(root, map[string]bool{}, sourceExpandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(direct, project) {
		t.Fatalf("direct and project expansion differ:\n%s\n===\n%s", direct, project)
	}
	if strings.Count(string(direct), "value2: i64 = 2") != 1 {
		t.Fatal("repeated include was not deduplicated")
	}

}

func TestDirectIncludeKeepsMissingFilePathError(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.elisa")
	if err := os.WriteFile(root, []byte("include \"missing.elisa\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := readSourceWithIncludes(root, map[string]bool{})
	if !os.IsNotExist(err) {
		t.Fatalf("missing direct include should retain filesystem error, got %v", err)
	}
}
