package easm

import (
	"os"
	"path/filepath"
	"testing"
)

// Stage 3 of docs/101: derive the caller-facing Unsafe.*/Segment.* effect set from the verified
// `requires:` contract instead of hand-authoring can[...]. DerivedEffects is the complete,
// drift-proof projection; these tests pin the mapping table and that it is surfaced in the report.

func hasEffect(effects []string, want string) bool {
	for _, e := range effects {
		if e == want {
			return true
		}
	}
	return false
}

func TestDerivedEffectsFloorOnly(t *testing.T) {
	// A benign raw routine (pause) carries only the honest floor — not a soundness hazard.
	got := DerivedEffects(&Function{Requires: []string{"x86_64.sse.pause"}})
	if !equalStrings(got, []string{"Unsafe.RawExtern"}) {
		t.Fatalf("expected floor only, got %v", got)
	}
}

func TestDerivedEffectsGuestSegmentInstall(t *testing.T) {
	fn := &Function{
		Requires:     []string{"x86_64.segment.fs", "x86_64.segment.write", "x86_64.segment.persistent"},
		Instructions: []Instruction{{Op: "state", Text: "fs: guest", Pseudo: true}},
	}
	got := DerivedEffects(fn)
	for _, want := range []string{"Unsafe.RawExtern", "Unsafe.SegmentMutation", "Unsafe.GuestSegmentInstall", "Segment.Guest"} {
		if !hasEffect(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if hasEffect(got, "Segment.Host") {
		t.Fatalf("guest-final routine should not derive Segment.Host: %v", got)
	}
}

func TestDerivedEffectsHostRestore(t *testing.T) {
	fn := &Function{
		Requires:     []string{"x86_64.segment.write", "x86_64.segment.restore"},
		Instructions: []Instruction{{Op: "state", Text: "fs: host", Pseudo: true}},
	}
	got := DerivedEffects(fn)
	if !hasEffect(got, "Unsafe.SegmentMutation") || !hasEffect(got, "Segment.Host") {
		t.Fatalf("expected SegmentMutation + Segment.Host, got %v", got)
	}
	if hasEffect(got, "Segment.Guest") || hasEffect(got, "Unsafe.GuestSegmentInstall") {
		t.Fatalf("host-restore routine must not derive guest-install effects: %v", got)
	}
}

func TestDerivedEffectsIndirectAndPointerCast(t *testing.T) {
	indirect := DerivedEffects(&Function{Requires: []string{"control.indirect"}})
	if !hasEffect(indirect, "Unsafe.IndirectCall") {
		t.Fatalf("expected Unsafe.IndirectCall, got %v", indirect)
	}
	ptr := DerivedEffects(&Function{Requires: []string{"memory.base.untyped"}})
	if !hasEffect(ptr, "Unsafe.PointerCast") {
		t.Fatalf("expected Unsafe.PointerCast, got %v", ptr)
	}
}

func TestBuildReportSurfacesDerivedEffects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg.easm")
	src := `module seg
target x86_64
export def load_guest_fs(sel: GuestFsSelector) -> void abi c:
    inputs: sel = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent
    body:
        movw %di, %fs
        state fs: guest
        ret
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	report, _ := BuildReport([]string{path}, "x86_64")
	var effects []string
	for _, m := range report.Modules {
		for _, e := range m.Exports {
			if e.Name == "load_guest_fs" {
				effects = e.DerivedEffects
			}
		}
	}
	if !hasEffect(effects, "Unsafe.SegmentMutation") || !hasEffect(effects, "Segment.Guest") {
		t.Fatalf("report did not surface derived effects for load_guest_fs: %v", effects)
	}
}
