package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A packed-enum ctor whose darray payload element is a STRUCT that itself owns a container
// (`Arm{body: darray[Node]}`) must DEEP-adopt: the shallow memcpy of the arms backing copies each
// Arm's `body` darray header verbatim, still pointing at the builder's region, which frees on
// return — a use-after-free (this is the self-host next_token crash, via Stmt.Match(arms:
// darray[MatchArm]) where MatchArm holds body: darray[Stmt]). The fix emits a per-element loop
// (adopt.deep.*) that copies each owned-container field's backing into the store arena too. This
// pins that the deep-adopt loop is emitted; without it the nested backing dangles.
func TestPackedCtorDeepAdoptsNestedContainerField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "deep_adopt.elisa")
	prog := "enum Node layout(handle: u32):\n" +
		"    pass\n" +
		"struct Arm:\n" +
		"    tag: i64\n" +
		"    body: darray[Node]\n" +
		"enum Stmt is Node:\n" +
		"    Leaf(value: i64)\n" +
		"    Match(arms: darray[Arm], line: i64)\n" +
		"    Nest(child: Node)\n" +
		"def make_leaf(v: i64) -> Node:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        return new Stmt.Leaf(value: v)\n" +
		"def build(n: i64) -> Node:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        arms: mutable darray[Arm] = []\n" +
		"        body: mutable darray[Node] = []\n" +
		"        body.push(make_leaf(n))\n" +
		"        arms.push(Arm{tag: n, body: body})\n" +
		"        return new Stmt.Match(arms: arms, line: 1)\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected deep-adopt fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	if !strings.Contains(ir, "adopt.deep") {
		t.Fatalf("packed ctor with a struct-with-darray element must emit the deep-adopt loop (adopt.deep.*); got no such block:\n%s", ir)
	}
}
