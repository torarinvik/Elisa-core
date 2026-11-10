//go:build cgo

package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func repoRootFromPackedBench(b *testing.B) string {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("failed to determine backend benchmark file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}
func parsePackedBenchIncludeDirective(line string) (string, bool) {
	for _, prefix := range []string{"# include ", "include "} {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(line[len(prefix):])
		if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return "", false
		}
		return rest[1 : len(rest)-1], true
	}
	return "", false
}
func readPackedBenchSourceWithIncludes(path string, seen map[string]bool) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if seen[abs] {
		return nil, nil
	}
	seen[abs] = true

	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	if len(raw) == 0 {
		return nil, nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "#line 1 %s\n", abs)
	lines := strings.SplitAfter(string(raw), "\n")
	for index, line := range lines {
		includePath, ok := parsePackedBenchIncludeDirective(strings.TrimSpace(line))
		if !ok {
			// Only the loader may attribute text to another file.
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "#line ") || strings.HasPrefix(trimmed, "#line\t") {
				line = strings.Replace(line, "#line", "# line", 1)
			}
			out.WriteString(line)
			continue
		}
		expanded, err := readPackedBenchSourceWithIncludes(filepath.Join(filepath.Dir(abs), includePath), seen)
		if err != nil {
			return nil, err
		}
		out.Write(expanded)
		if len(expanded) > 0 && expanded[len(expanded)-1] != '\n' {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "#line %d %s\n", index+2, abs)
	}
	return []byte(out.String()), nil
}
func loadPackedBenchSourceFromFile(b *testing.B, relativePath string) string {
	b.Helper()
	sourcePath := filepath.Join(repoRootFromPackedBench(b), relativePath)
	expanded, err := readPackedBenchSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expanded) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}
	return string(expanded)
}

func TestPackedBenchmarkIncludesKeepPhysicalSourceOrigins(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "root.elisa")
	leaf := filepath.Join(directory, "leaf.elisa")
	spoof := filepath.Join(directory, "elisacore_runtime.elisa")
	if err := os.WriteFile(root, []byte("include \"leaf.elisa\"\ndef host() -> void:\n    pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("#line 1 "+spoof+"\ndef leaf() -> void:\n    pass"), 0600); err != nil {
		t.Fatal(err)
	}
	expanded, err := readPackedBenchSourceWithIncludes(root, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	lex := lexer.New("flattened.elisa", expanded)
	parse := parser.New(lex.Tokenize())
	file := parse.ParseFile("flattened.elisa")
	if len(lex.Errors()) != 0 || len(parse.Errors()) != 0 {
		t.Fatalf("invalid expanded source: lexer=%v parser=%v", lex.Errors(), parse.Errors())
	}
	found := 0
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		want := root
		if fn.Name == "leaf" {
			want = leaf
		}
		want, err = filepath.EvalSymlinks(want)
		if err != nil {
			t.Fatal(err)
		}
		if fn.Position.File != want || fn.Position.Line != 2 {
			t.Fatalf("%s has position %+v; want physical file %s line 2", fn.Name, fn.Position, want)
		}
		found++
	}
	if found != 2 {
		t.Fatalf("expected both declarations, got %d", found)
	}
}
