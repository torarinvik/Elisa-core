package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A view (view[T] / sview) is a read-only borrow — a fat pointer with no owned backing — so it is
// NOT a valid extend/bulk-push/spread SOURCE. To bulk-append you pass the owning `mutable darray&`.
// (Appending a view's contents is still expressible via a comprehension, `dst.extend([x for x in
// v])`, which is a darray-typed source; the view itself just can't be an extend participant.)
func TestExtendRejectsViewSource(t *testing.T) {
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
	cases := []struct{ name, body, wantType string }{
		{"view_slice.elisa", `def x(buf: mutable darray[i64]&, src: darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        v: view[i64] = src[0:2]
        buf.extend(v)
`, "view[i64]"},
		{"sview_direct.elisa", `def x(buf: mutable darray[u8]&, sv: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        buf.extend(sv)
`, "sview"},
	}
	for _, c := range cases {
		src := write(c.name, c.body)
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code == 0 {
			t.Fatalf("%s: expected a view extend source to be rejected, but it compiled", c.name)
		}
		out := stderr.String()
		if !strings.Contains(out, "darray extend expects a compatible darray or array source") || !strings.Contains(out, c.wantType) {
			t.Fatalf("%s: expected rejection naming %q and NOT mentioning view as valid, got:\n%s", c.name, c.wantType, out)
		}
		if strings.Contains(out, "darray, view, or array") {
			t.Fatalf("%s: error message still advertises view as a valid source:\n%s", c.name, out)
		}
	}
}
