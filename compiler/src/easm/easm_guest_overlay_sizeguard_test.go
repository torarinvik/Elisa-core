package easm

import "testing"

// docs/107 increment 4: a field tagged `requires size >= N` may only be accessed where a dominating
// guard proves the struct's runtime `size >= N`. CheckGuestOverlaySizeGuard discharges that
// obligation against explicit SizeGuardFact lower-bounds (the front-end derives these from
// dominators). This mirrors the mem_param chain: `flexible`/`ext1`/`ext2` are only valid when
// mem_param.size >= 48.

func memParamLayout() *Layout {
	return &Layout{
		Name: "OrbisKernelMemParam",
		Fields: []LayoutField{
			{Offset: 0, Name: "size", Type: "u64", Width: 8},
			{Offset: 16, Name: "flexible", Type: "u64", Width: 8, RequiresSizeAtLeast: 24},
			{Offset: 40, Name: "ext2", Type: "u64", Width: 8, RequiresSizeAtLeast: 72},
		},
	}
}

func TestSizeGuardAcceptsUnderExactGuard(t *testing.T) {
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "ext2", []SizeGuardFact{{AtLeast: 72}})
	if len(issues) != 0 {
		t.Fatalf("expected accept under `size >= 72` guard, got %+v", issues)
	}
}

func TestSizeGuardAcceptsUnderStrongerGuard(t *testing.T) {
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "ext2", []SizeGuardFact{{AtLeast: 80}})
	if len(issues) != 0 {
		t.Fatalf("expected accept under stronger `size >= 80` guard, got %+v", issues)
	}
}

func TestSizeGuardRejectsNoGuard(t *testing.T) {
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "ext2", nil)
	if len(issues) != 1 || issues[0].Code != "overlay-field-needs-size-guard" {
		t.Fatalf("expected overlay-field-needs-size-guard with no guard, got %+v", issues)
	}
}

func TestSizeGuardRejectsWeakerGuard(t *testing.T) {
	// A `size >= 64` guard does NOT discharge a `requires size >= 72` field.
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "ext2", []SizeGuardFact{{AtLeast: 64}})
	if len(issues) != 1 || issues[0].Code != "overlay-field-needs-size-guard" {
		t.Fatalf("expected overlay-field-needs-size-guard under weaker guard, got %+v", issues)
	}
}

func TestSizeGuardWeakerFactStillDischargesLowerRequirement(t *testing.T) {
	// `flexible` only requires >= 24; a `size >= 24` guard suffices even though it's too weak for ext2.
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "flexible", []SizeGuardFact{{AtLeast: 24}})
	if len(issues) != 0 {
		t.Fatalf("expected accept of flexible under `size >= 24`, got %+v", issues)
	}
}

func TestSizeGuardUnrequiredFieldUnaffected(t *testing.T) {
	// A field with no `requires` tag is accepted with no facts at all (regression guard).
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "size", nil)
	if len(issues) != 0 {
		t.Fatalf("expected unrequired field accepted with no facts, got %+v", issues)
	}
}

func TestSizeGuardUnknownFieldDefersToFieldChecker(t *testing.T) {
	// Unknown field carries no requirement here (CheckGuestOverlayAccess reports it); no double-report.
	issues := CheckGuestOverlaySizeGuard("f.elisa", 10, memParamLayout(), "nope", nil)
	if len(issues) != 0 {
		t.Fatalf("expected size-guard checker silent on unknown field, got %+v", issues)
	}
}

// The `requires size >= N` suffix parses off the layout field line into RequiresSizeAtLeast.
func TestLayoutFieldParsesRequiresSizeSuffix(t *testing.T) {
	src := "" +
		"layout OrbisKernelMemParam size 80:\n" +
		"  0 size: u64\n" +
		"  40 ext2: u64 requires size >= 72\n"
	mod, issues := Parse("mp.elisa", src)
	if len(issues) != 0 {
		t.Fatalf("unexpected parse issues: %+v", issues)
	}
	if len(mod.Layouts) != 1 {
		t.Fatalf("expected 1 layout, got %d", len(mod.Layouts))
	}
	var ext2 *LayoutField
	for i := range mod.Layouts[0].Fields {
		if mod.Layouts[0].Fields[i].Name == "ext2" {
			ext2 = &mod.Layouts[0].Fields[i]
		}
	}
	if ext2 == nil {
		t.Fatalf("ext2 field not parsed")
	}
	if ext2.RequiresSizeAtLeast != 72 {
		t.Fatalf("expected RequiresSizeAtLeast=72, got %d", ext2.RequiresSizeAtLeast)
	}
}
