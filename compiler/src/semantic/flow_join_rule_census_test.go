//go:build cgo

package semantic

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// TestJoinRuleCensus measures — it does not assert — the number of statement-join ambient
// mutation sites (docs/125 §1d, R7) across the stage1 compiler corpus and stdlib, bucketed
// by how many outer variables each join phi-reconstructs. Run with:
//
//	go test ./src/semantic/ -run TestJoinRuleCensus -v -count=1
//
// The output calibrates the warn-tier threshold: the rule should fire only on the strong
// signal (many variables reconstructed), never on a routine two-variable guard update.
func TestJoinRuleCensus(t *testing.T) {
	// The stdlib always ships with the repo; an external corpus (e.g. the self-hosted stage1
	// compiler) is measured too when JOIN_CENSUS_CORPUS points at its source root.
	roots := []string{"../../runtime/elisacore_std"}
	if corpus := os.Getenv("JOIN_CENSUS_CORPUS"); corpus != "" {
		roots = append(roots, corpus)
	}
	buckets := map[int]int{} // varCount -> site count
	perThreshold := map[int]int{}
	var examples []string
	files := 0
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		_ = filepath.Walk(abs, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".elisa") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			l := lexer.New(path, src)
			toks := l.Tokenize()
			if len(l.Errors()) != 0 {
				return nil
			}
			p := parser.New(toks)
			file := p.ParseFile(path)
			if len(p.Errors()) != 0 {
				return nil
			}
			files++
			for _, decl := range file.Decls {
				forEachFuncBody(decl, func(body []ast.Stmt) {
					for _, site := range findJoinRuleSites(body, 2) {
						n := len(site.vars)
						buckets[n]++
						for k := 2; k <= n; k++ {
							perThreshold[k]++
						}
						if n >= 2 && len(examples) < 25 {
							examples = append(examples, filepath.Base(path)+":"+strconv.Itoa(site.pos.Line)+"  {"+strings.Join(site.vars, ", ")+"}")
						}
					}
				})
			}
			return nil
		})
	}
	t.Logf("join-rule census: %d files parsed", files)
	var counts []int
	for n := range buckets {
		counts = append(counts, n)
	}
	sort.Ints(counts)
	for _, n := range counts {
		t.Logf("  %d-variable joins: %d sites", n, buckets[n])
	}
	for k := 2; k <= 6; k++ {
		t.Logf("  threshold >=%d vars: %d sites total", k, perThreshold[k])
	}
	t.Logf("examples (>=3 vars):")
	for _, e := range examples {
		t.Logf("    %s", e)
	}
}

// forEachFuncBody invokes fn with the body of every function/method declaration, recursing
// into module/extend namespaces.
func forEachFuncBody(decl ast.Decl, fn func([]ast.Stmt)) {
	switch n := decl.(type) {
	case *ast.FuncDecl:
		fn(n.Body)
	case *ast.NamespaceDecl:
		for _, d := range n.Decls {
			forEachFuncBody(d, fn)
		}
	}
}
