package backend

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Freeze all Body selector contexts, not just variables named decl or fn.
// This closes the reviewed source inventory against a new unrecorded read or
// a changed receiver context at an old site. It does not certify semantic
// reflection/memo reads, which remain explicit gaps in every trace report.
func TestIRBodyReadSourceInventoryRejectsUnreviewedSites(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	data, err := os.ReadFile(filepath.Join(directory, "testdata", "ir_body_read_sites.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Files map[string]struct {
			SHA256         string `json:"sha256"`
			Selectors      int    `json:"selectors"`
			Classification string `json:"classification"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(directory, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		set := token.NewFileSet()
		parsed, err := parser.ParseFile(set, file.Name(), source, 0)
		if err != nil {
			t.Fatal(err)
		}
		var positions []string
		goast.Inspect(parsed, func(node goast.Node) bool {
			if selector, ok := node.(*goast.SelectorExpr); ok && selector.Sel.Name == "Body" {
				positions = append(positions, set.Position(selector.Pos()).String())
			}
			return true
		})
		if len(positions) == 0 {
			continue
		}
		expected, reviewed := inventory.Files[file.Name()]
		if !reviewed || expected.Selectors != len(positions) || expected.SHA256 != fmt.Sprintf("%x", sha256.Sum256(source)) {
			t.Errorf("review input/body read contexts before accepting changed source %s: %v", file.Name(), positions)
		}
		seen[file.Name()] = true
	}
	for file := range inventory.Files {
		if !seen[file] {
			t.Errorf("reviewed input/body read source disappeared: %s", file)
		}
	}
}
