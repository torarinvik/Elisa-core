package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A view (view[T] / sview) is a contiguous, read-only fat pointer over its elements.
// It can never be the extend TARGET (a borrow has no owned backing to grow), but it IS
// a valid extend SOURCE: reading its bytes into `dst` is a plain value copy (dst owns
// the result, the view is untouched), and a contiguous view lowers to the SAME
// arena_memcpy a darray source does — strictly better than the `dst.extend([x for x in
// v])` fill loop it replaces. (docs/119 dogfooding: `push_sview` is `buf.extend(sv)`.)
func TestExtendAcceptsViewSourceAsMemcpy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	std := filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	rel, _ := filepath.Rel(dir, std)
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("# include \""+filepath.ToSlash(rel)+"\"\n"+body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	cases := []struct{ name, body string }{
		{"view_slice.elisa", `def x(buf: mutable darray[i64]&, src: darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        v: view[i64] = src[0:2]
        buf.extend(v)
`},
		{"sview_direct.elisa", `def x(buf: mutable darray[u8]&, sv: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        buf.extend(sv)
`},
	}
	for _, c := range cases {
		src := write(c.name, c.body)
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", "-O0", src}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: a contiguous view extend source must compile, got exit %d:\n%s", c.name, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "arena_memcpy") {
			t.Fatalf("%s: extend from a view must lower to arena_memcpy, got:\n%s", c.name, stdout.String())
		}
	}
}
