package easm

import "testing"

// Stage 4a of docs/101: `template def` — a routine assembled at build time with typed,
// runtime-filled holes. A hole is just a parameter filled at instantiate time; it is referenced
// by bare name in the body, so a template reads like an ordinary Elisa function rather than a
// bolted-on macro. These tests pin the patch-point model and the hole-type safety check (a 16-bit
// selector cannot be baked where a 64-bit address/target is expected). Byte assembly is Stage 4b.

func patchPointFor(tpl *Template, hole string) *PatchPoint {
	for i := range tpl.PatchPoints {
		if tpl.PatchPoints[i].Hole == hole {
			return &tpl.PatchPoints[i]
		}
	}
	return nil
}

func TestTemplateParsesHolesAndPatchPoints(t *testing.T) {
	src := `module thunk
target x86_64
template def hle_boundary_thunk(target: HostCallable, host_fs: HostFsSelector):
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
		t.Fatalf("expected a clean template, got %#v", issues)
	}
	if len(module.Functions) != 0 {
		t.Fatalf("a template is not an emittable export: %v", functionNames(module))
	}
	if len(module.Templates) != 1 {
		t.Fatalf("expected one template, got %d", len(module.Templates))
	}
	tpl := &module.Templates[0]
	if tpl.Name != "hle_boundary_thunk" || len(tpl.Holes) != 2 {
		t.Fatalf("unexpected template head: %#v", tpl)
	}
	hf := patchPointFor(tpl, "host_fs")
	if hf == nil || hf.Class != "sel16" {
		t.Fatalf("host_fs should be a sel16 patch-point: %#v", hf)
	}
	tg := patchPointFor(tpl, "target")
	if tg == nil || tg.Class != "wide64" {
		t.Fatalf("target should be a wide64 patch-point: %#v", tg)
	}
}

func TestTemplateRejectsSelectorInAddressSlot(t *testing.T) {
	// host_fs is a 16-bit selector; movabs bakes a 64-bit immediate. The mismatch is exactly
	// the class of bug the typed holes exist to prevent.
	src := `module bad
target x86_64
template def bake(host_fs: HostFsSelector):
    movabs host_fs, %r11
    ret
`
	_, issues := Parse("bad.easm", src)
	if !containsIssue(issues, "template-hole-type-mismatch") {
		t.Fatalf("expected template-hole-type-mismatch, got %#v", issues)
	}
}

func TestTemplateRejectsUnusedHole(t *testing.T) {
	src := `module bad2
target x86_64
template def bake(target: HostCallable, dead: HostFsSelector):
    movabs target, %r11
    call *%r11
    ret
`
	_, issues := Parse("bad2.easm", src)
	if !containsIssue(issues, "unused-template-hole") {
		t.Fatalf("expected unused-template-hole, got %#v", issues)
	}
}
