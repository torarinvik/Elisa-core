package main

import (
	"bytes"
	"strings"
	"testing"
)

// Copy-update struct literals `T{..base, field: value}` desugar to an
// ordinary literal reading every unwritten field from `base`.
const structCopyBaseProgram = `struct Inner:
    v: i64
struct P:
    a: i64
    b: i64 = 99
    xs: darray[i64]
    name: sview
    inner: Inner
struct Box:
    p: P
struct Pair[T]:
    l: T
    r: T

def main() -> i64:
    xs: darray[i64] = [1, 2, 3]
    p = P{a: 1, b: 2, xs: xs, name: "hi", inner: Inner{v: 5}}
    q = P{..p, a: 10}
    c = P{..p}
    bx = Box{p: p}
    r = P{..bx.p, b: 7,}
    i2 = Inner{..p.inner}
    g = Pair[i64]{l: 3, r: 4}
    g2 = Pair[i64]{..g, r: 40}
    if q.xs[2] != 3 or r.name != "hi" or q.name != "hi" or c.xs[1] != 2:
        return 1
    if p.a != 1 or p.b != 2 or p.xs[0] != 1 or p.name != "hi" or p.inner.v != 5:
        return 2
    if q.a != 10 or q.b != 2 or q.inner.v != 5:
        return 3
    if c.a != 1 or c.b != 2:
        return 4
    if r.a != 1 or r.b != 7 or i2.v != 5:
        return 5
    if g2.l != 3 or g2.r != 40 or g.r != 4:
        return 6
    return 42
`

func TestStructCopyBaseCompiledRuntime(t *testing.T) {
	status, out := s4CompileRun(t, structCopyBaseProgram)
	if status != "RUNERR" || !strings.Contains(out, "exit status 42") {
		t.Fatalf("expected exit 42, got %s: %s", status, out)
	}
}

func TestStructCopyBaseInterpreted(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "struct_copy_base_interpret.elisa", structCopyBaseProgram)
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("interpret failed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected result 42, got:\n%s", stdout.String())
	}
}

func TestStructLiteralDefaultsInterpreted(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "struct_defaults_interpret.elisa", `struct R:
    a: i64
    b: i64 = 40

def main() -> i64:
    r = R{a: 2}
    s = R{b: 1, a: 5}
    return r.a + r.b + s.a * 0 + s.b * 0 + (s.a - 5) * 100
`)
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("interpret failed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected result 42, got:\n%s", stdout.String())
	}
}

func TestStructCopyBaseRejections(t *testing.T) {
	t.Parallel()
	header := "struct P:\n    a: i64\n    b: i64\nstruct Q:\n    a: i64\n    b: i64\n" +
		"def mk() -> P = P{a: 1, b: 2}\n"
	cases := []struct{ name, body, want string }{
		{"not_first", "    q = P{a: 3, ..p}\n", "must be the first entry"},
		{"two_bases", "    q = P{..p, ..p}\n", "at most one `..base`"},
		{"wrong_type", "    o = Q{a: 1, b: 2}\n    q = P{..o, a: 3}\n", "copy source `..base` must have type"},
		{"unknown_field", "    q = P{..p, c: 3}\n", `has no field "c"`},
		{"duplicate_field", "    q = P{..p, a: 3, a: 4}\n", "specified more than once"},
		{"call_base", "    q = P{..mk(), a: 3}\n", "must be an identifier or field path"},
		{"index_base", "    ps: darray[P] = [p]\n    q = P{..ps[0], a: 3}\n", "must be an identifier or field path"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := header + "def main() -> i64:\n    p = P{a: 1, b: 2}\n" + tc.body + "    return 0\n"
			path := writeImplicitContextFixture(t, "struct_copy_base_"+tc.name+".elisa", src)
			var stdout, stderr bytes.Buffer
			if code := runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr); code == 0 {
				t.Fatalf("expected rejection, got success")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("expected %q in diagnostics, got:\n%s", tc.want, stderr.String())
			}
		})
	}
}
