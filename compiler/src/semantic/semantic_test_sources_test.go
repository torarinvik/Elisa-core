package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readSemanticTestSourceWithIncludes mirrors the driver include expansion for
// tests that exercise Analyze directly. Runtime sources are intentionally
// modular, so parsing a leaf file without expanding its Elisa includes tests a
// representation the compiler never receives from the driver.
func readSemanticTestSourceWithIncludes(t *testing.T, filename string) []byte {
	t.Helper()
	return readSemanticTestSourceWithIncludesSeen(t, filename, map[string]bool{})
}

func readSemanticTestSourceWithIncludesSeen(t *testing.T, filename string, seen map[string]bool) []byte {
	t.Helper()
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatalf("resolve %s: %v", filename, err)
	}
	if seen[abs] {
		return nil
	}
	seen[abs] = true
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		includePath := ""
		for _, prefix := range []string{"# include ", "include "} {
			if strings.HasPrefix(trimmed, prefix) {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
					includePath = rest[1 : len(rest)-1]
				}
				break
			}
		}
		if includePath != "" {
			out.Write(readSemanticTestSourceWithIncludesSeen(t, filepath.Join(filepath.Dir(abs), includePath), seen))
			if out.Len() == 0 || !strings.HasSuffix(out.String(), "\n") {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return []byte(out.String())
}
