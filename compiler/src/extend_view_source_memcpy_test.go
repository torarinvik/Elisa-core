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
		// A string LITERAL has a compile-time-known length, so it is a bounded source: it
		// adapts to an sview and lowers to the SAME arena_memcpy a view source does (unlike
		// a bare `static u8&` byte pointer, which has no length and is rejected).
		{"string_literal.elisa", `def x(buf: mutable darray[u8]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf.extend("hello")
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

// A bare `static u8&` byte-ref binding (NOT a literal) has no length in its type, so it
// cannot bound the copy — it stays rejected, but with an actionable diagnostic that names
// the two length-carrying fixes (a bounded view, or a length-carrying string) instead of
// the opaque shape error.
func TestExtendRejectsUnboundedStaticU8Ref(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	std := filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	rel, _ := filepath.Rel(dir, std)
	src := filepath.Join(dir, "unbounded_ref.elisa")
	body := "# include \"" + filepath.ToSlash(rel) + "\"\n" + `def x(buf: mutable darray[u8]&, s: static u8&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf.extend(s)
`
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "semantic", "-O0", src}, &stdout, &stderr); code == 0 {
		t.Fatalf("extend from an unbounded static u8& must be rejected, got clean compile")
	}
	if !strings.Contains(stderr.String(), "unbounded") || !strings.Contains(stderr.String(), "bounded view") {
		t.Fatalf("expected the sharpened unbounded-pointer diagnostic, got:\n%s", stderr.String())
	}
}
