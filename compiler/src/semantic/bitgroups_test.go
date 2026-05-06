package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeNarrowIntegerConstEnumAndPackedGroups(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "bitgroups_ok.elisa", `const enum Flags of u4:
	None = 0
	Read = 1
	Write = 2

struct Header:
	flags: bitset:
		has_read
		has_write
	layout: bitfield:
		tag: u4
		mode: Flags
		active: u1

def read(header: Header) -> bool:
	return header.flags.has_read
`)
	flags, ok := result.NamedTypes["Flags"].(*ConstEnumType)
	if !ok {
		t.Fatalf("expected Flags const enum, got %T", result.NamedTypes["Flags"])
	}
	if storage, ok := flags.Storage.(*BitIntType); !ok || storage.Signed || storage.Width != 4 {
		t.Fatalf("expected Flags storage u4, got %T %#v", flags.Storage, flags.Storage)
	}
	header, ok := result.NamedTypes["Header"].(*StructType)
	if !ok {
		t.Fatalf("expected Header struct, got %T", result.NamedTypes["Header"])
	}
	bitset, ok := header.Fields["flags"].Type.(*BitGroupType)
	if !ok || bitset.Kind != BitGroupBitset || bitset.BackingWidth != 8 {
		t.Fatalf("expected flags bitset backed by u8, got %T %#v", header.Fields["flags"].Type, header.Fields["flags"].Type)
	}
	bitfield, ok := header.Fields["layout"].Type.(*BitGroupType)
	if !ok || bitfield.Kind != BitGroupBitfield || bitfield.BackingWidth != 16 {
		t.Fatalf("expected layout bitfield backed by u16, got %T %#v", header.Fields["layout"].Type, header.Fields["layout"].Type)
	}
	if got := bitfield.MemberMap["mode"].Width; got != 4 {
		t.Fatalf("expected const enum bitfield member width 4, got %d", got)
	}
}

func TestAnalyzeNarrowConstEnumRejectsOverflow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "bit_enum_overflow.elisa", `const enum Flags of u4:
	Ok = 15
	Bad = 16
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "does not fit storage type u4") {
		t.Fatalf("expected const enum overflow diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeConstEnumInfersUnsignedStorage(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "const_enum_infer_unsigned.elisa", `const enum Mode:
	None
	Read
	Write
	Execute = 7
`)
	mode, ok := result.NamedTypes["Mode"].(*ConstEnumType)
	if !ok {
		t.Fatalf("expected Mode const enum, got %T", result.NamedTypes["Mode"])
	}
	storage, ok := mode.Storage.(*BitIntType)
	if !ok || storage.Signed || storage.Width != 3 {
		t.Fatalf("expected inferred u3 storage, got %T %#v", mode.Storage, mode.Storage)
	}
	if mode.MemberMap["None"].Value != 0 || mode.MemberMap["Read"].Value != 1 || mode.MemberMap["Execute"].Value != 7 {
		t.Fatalf("unexpected inferred member values: %#v", mode.MemberMap)
	}
}

func TestAnalyzeConstEnumInfersSignedStorage(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "const_enum_infer_signed.elisa", `const enum Delta:
	Down = -2
	Flat
	Up = 1
`)
	delta, ok := result.NamedTypes["Delta"].(*ConstEnumType)
	if !ok {
		t.Fatalf("expected Delta const enum, got %T", result.NamedTypes["Delta"])
	}
	storage, ok := delta.Storage.(*BitIntType)
	if !ok || !storage.Signed || storage.Width != 2 {
		t.Fatalf("expected inferred i2 storage, got %T %#v", delta.Storage, delta.Storage)
	}
}

func TestAnalyzeInferredConstEnumWidthFeedsBitfieldLayout(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "const_enum_bitfield_width.elisa", `const enum Mode:
	None
	Fast
	Slow

struct Header:
	layout: bitfield:
		mode: Mode
		active: u1
`)
	header := result.NamedTypes["Header"].(*StructType)
	if !header.HasPackedGroups {
		t.Fatalf("expected Header to record packed group layout")
	}
	layout := header.Fields["layout"].Type.(*BitGroupType)
	if got := layout.MemberMap["mode"].Width; got != 2 {
		t.Fatalf("expected inferred enum bitfield width 2, got %d", got)
	}
	if layout.BackingWidth != 8 {
		t.Fatalf("expected bitfield backing u8, got u%d", layout.BackingWidth)
	}
}

func TestAnalyzePackedGroupStructIsNotCABICompatible(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "packed_group_export.elisa", `struct Header:
	flags: bitset:
		ready
`)
	header := result.NamedTypes["Header"].(*StructType)
	if exportedNamedTypeAllowed(header) || isCABICompatibleType(header) {
		t.Fatalf("expected packed-group struct to be rejected as C ABI compatible")
	}
}

func TestAnalyzeBitGroupRejectsDuplicateMember(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "bitgroup_duplicate.elisa", `struct Header:
	flags: bitset:
		ready
		ready
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "duplicate packed group member") {
		t.Fatalf("expected duplicate packed group diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}
