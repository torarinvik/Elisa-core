package easm

import "testing"

// Stage 4b of docs/101: assemble a verified template into bytes + patch-points using the trusted
// llvm-mc assembler, with hole offsets located by zero-vs-ones diffing. This is what finally
// replaces the hand-counted ModRM/offset bytes in core/linker.elisa.

func TestAssembleTemplateBytesAndPatchPoints(t *testing.T) {
	if !AssemblerAvailable() {
		t.Skip("llvm-mc not available")
	}
	src := `module thunk
target x86_64
template def thunk(target: HostCallable, host_fs: HostFsSelector):
    movw %fs, %r10w
    pushq %r10
    movw host_fs, %r10w
    movw %r10w, %fs
    movabs target, %r11
    call *%r11
    popq %r10
    movw %r10w, %fs
    ret
`
	module, issues := Parse("thunk.easm", src)
	if len(issues) != 0 {
		t.Fatalf("template parse issues: %#v", issues)
	}
	img, err := AssembleTemplate(&module.Templates[0], "x86_64-apple-darwin")
	if err != nil {
		t.Fatal(err)
	}
	// Known-good encoding of this exact body (cross-checked against llvm-mc --show-encoding).
	if len(img.Bytes) != 33 {
		t.Fatalf("expected 33 assembled bytes, got %d", len(img.Bytes))
	}
	if img.Bytes[0] != 0x66 {
		t.Fatalf("first byte should be the operand-size prefix 0x66, got %#x", img.Bytes[0])
	}
	pp := map[string]PatchByte{}
	for _, p := range img.Patches {
		pp[p.Hole] = p
	}
	if got := pp["host_fs"]; got.Offset != 9 || got.Width != 2 {
		t.Fatalf("host_fs slot wrong: %#v", got)
	}
	if got := pp["target"]; got.Offset != 16 || got.Width != 8 {
		t.Fatalf("target slot wrong: %#v", got)
	}
	// Holes are left zeroed in the base image; the runtime fills them.
	for _, p := range img.Patches {
		for i := 0; i < p.Width; i++ {
			if img.Bytes[p.Offset+i] != 0 {
				t.Fatalf("hole %s byte %d should be zero in the base image", p.Hole, i)
			}
		}
	}
}

func TestInstantiateMatchesDirectAssembly(t *testing.T) {
	if !AssemblerAvailable() {
		t.Skip("llvm-mc not available")
	}
	src := `module thunk
target x86_64
template def thunk(target: HostCallable, host_fs: HostFsSelector):
    movw %fs, %r10w
    pushq %r10
    movw host_fs, %r10w
    movw %r10w, %fs
    movabs target, %r11
    call *%r11
    popq %r10
    movw %r10w, %fs
    ret
`
	module, issues := Parse("thunk.easm", src)
	if len(issues) != 0 {
		t.Fatalf("parse issues: %#v", issues)
	}
	tpl := &module.Templates[0]
	img, err := AssembleTemplate(tpl, "x86_64-apple-darwin")
	if err != nil {
		t.Fatal(err)
	}
	// Fill the holes at "run time"...
	filled, err := InstantiateImage(img, map[string]uint64{
		"target":  0x1122334455667788,
		"host_fs": 0x006f,
	})
	if err != nil {
		t.Fatal(err)
	}
	// ...and prove it is byte-identical to assembling the body with those concrete immediates.
	expected, err := assembleTemplateBody(tpl, map[string]string{
		"target":  "$0x1122334455667788",
		"host_fs": "$0x6f",
	}, "x86_64-apple-darwin")
	if err != nil {
		t.Fatal(err)
	}
	if len(filled) != len(expected) {
		t.Fatalf("length mismatch: filled %d vs direct %d", len(filled), len(expected))
	}
	for i := range expected {
		if filled[i] != expected[i] {
			t.Fatalf("byte %d differs: filled %#x vs direct %#x", i, filled[i], expected[i])
		}
	}
}

func TestAssembleTemplateRejectsBadInstruction(t *testing.T) {
	if !AssemblerAvailable() {
		t.Skip("llvm-mc not available")
	}
	tpl := &Template{
		Name:         "bad",
		Holes:        []Param{{Name: "x", Type: "u64"}},
		Instructions: []Instruction{{Op: "notarealop", Text: "notarealop x, %r11"}},
	}
	if _, err := AssembleTemplate(tpl, "x86_64-apple-darwin"); err == nil {
		t.Fatal("expected llvm-mc to reject an invalid instruction")
	}
}
