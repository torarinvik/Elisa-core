package semantic

import (
	"elisacore/src/ast"
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

func TestAnalyzeStructLayoutModes(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_layout_modes.elisa", `struct Header layout packed:
	tag: u4
	arity: u3
	active: u1

struct CHeader layout c:
	kind: u32
	flags: u32
	size: usize

layout aos struct Particle:
	x: f32
	y: f32

layout soa struct ParticleRows:
	x: f32
	y: f32

struct Plain:
	value: i32
`)
	header := result.NamedTypes["Header"].(*StructType)
	if !header.PackedLayout || header.Layout != ast.StructLayoutPacked || header.ReprC {
		t.Fatalf("expected Header to be packed and not C ABI by default, got packed=%v layout=%v reprC=%v", header.PackedLayout, header.Layout, header.ReprC)
	}
	if exportedNamedTypeAllowed(header) || isCABICompatibleType(header) {
		t.Fatalf("expected packed layout struct to be rejected as C ABI compatible")
	}
	cHeader := result.NamedTypes["CHeader"].(*StructType)
	if cHeader.PackedLayout || cHeader.Layout != ast.StructLayoutC || !cHeader.ReprC {
		t.Fatalf("expected CHeader to be explicit C layout, got packed=%v layout=%v reprC=%v", cHeader.PackedLayout, cHeader.Layout, cHeader.ReprC)
	}
	if !exportedNamedTypeAllowed(cHeader) || !isCABICompatibleType(cHeader) {
		t.Fatalf("expected explicit C layout struct with scalar fields to be C ABI compatible")
	}
	particle := result.NamedTypes["Particle"].(*StructType)
	if particle.Layout != ast.StructLayoutAOS || particle.Store || particle.StoreDecl != nil {
		t.Fatalf("expected Particle to stay an AOS struct, got layout=%v store=%v storeDecl=%#v", particle.Layout, particle.Store, particle.StoreDecl)
	}
	particleRows := result.NamedTypes["ParticleRows"].(*StructType)
	if particleRows.Layout != ast.StructLayoutSOA || !particleRows.Store || particleRows.StoreDecl == nil || !particleRows.StoreDecl.Soa {
		t.Fatalf("expected ParticleRows to analyze as SOA-backed store layout, got layout=%v store=%v storeDecl=%#v", particleRows.Layout, particleRows.Store, particleRows.StoreDecl)
	}
	plain := result.NamedTypes["Plain"].(*StructType)
	if plain.ReprC || plain.Layout != ast.StructLayoutDefault || exportedNamedTypeAllowed(plain) || isCABICompatibleType(plain) {
		t.Fatalf("expected default struct to use Elisa layout rather than C ABI layout, got layout=%v reprC=%v", plain.Layout, plain.ReprC)
	}
}

func TestAnalyzeStructCBindAnnotation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_c_bind.elisa", `@c_bind(stddef.h, Header)
struct Header layout c:
	tag: u32
	count: usize
`)
	header, ok := result.NamedTypes["Header"].(*StructType)
	if !ok {
		t.Fatalf("expected Header struct, got %T", result.NamedTypes["Header"])
	}
	if header.CBindHeader != "stddef.h" || header.CBindName != "Header" {
		t.Fatalf("expected c bind metadata, got header=%q name=%q", header.CBindHeader, header.CBindName)
	}
}

func TestAnalyzeStructCBindRequiresCLayout(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_c_bind_requires_c_layout.elisa", `@c_bind(stddef.h, Header)
struct Header:
	tag: u32
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "requires `layout c`") {
		t.Fatalf("expected c_bind layout diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStructRegionOwnerScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_region_owner_scope.elisa", `struct Expr in owner:
	left: owner Expr&?
	right: owner Expr&?

struct Explicit[region arena]:
	next: arena Explicit&?
`)
	expr := result.NamedTypes["Expr"].(*StructType)
	if len(expr.RegionParams) != 1 || expr.RegionParams[0] != "owner" || expr.RegionOwner != "owner" {
		t.Fatalf("expected Expr to record owner region sugar, got params=%v owner=%q", expr.RegionParams, expr.RegionOwner)
	}
	leftRef, ok := expr.Fields["left"].Type.(*RefType)
	if !ok {
		t.Fatalf("expected Expr.left to resolve as a ref type, got %T", expr.Fields["left"].Type)
	}
	if leftRef.Region != "owner" {
		t.Fatalf("expected Expr.left region qualifier to resolve to owner, got %q", leftRef.Region)
	}
	explicit := result.NamedTypes["Explicit"].(*StructType)
	if len(explicit.RegionParams) != 1 || explicit.RegionParams[0] != "arena" || explicit.RegionOwner != "" {
		t.Fatalf("expected Explicit to record explicit region parameter only, got params=%v owner=%q", explicit.RegionParams, explicit.RegionOwner)
	}
	nextRef, ok := explicit.Fields["next"].Type.(*RefType)
	if !ok {
		t.Fatalf("expected Explicit.next to resolve as a ref type, got %T", explicit.Fields["next"].Type)
	}
	if nextRef.Region != "arena" {
		t.Fatalf("expected Explicit.next region qualifier to resolve to arena, got %q", nextRef.Region)
	}
}

func TestAnalyzeStructRegionOwnerUseSiteInstantiation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_region_owner_use_site.elisa", `struct Expr in owner:
	next: owner Expr&?

def build() -> void:
	region scratch(1024)
	head: Expr[scratch] = Expr{
		next: null
	}
	_ = head.next
	destroy scratch
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected region-owned struct instantiation to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStructRegionOwnerAcceptsArenaValueArgument(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_region_owner_arena_value_arg.elisa", `struct Expr in owner:
	next: owner Expr&?

def build(owner: Arena) -> void:
	head: Expr[owner] = Expr{
		next: null
	}
	_ = head.next
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected Arena value to be accepted as a region generic argument, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStructRegionOwnerWithTypeParams(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_region_owner_type_param.elisa", `struct Box[T] in owner:
	value: T
	next: owner Box[T, owner]&?

def build() -> void:
	region scratch(1024)
	box: Box[i64, scratch] = Box{
		value: 42,
		next: null
	}
	_ = box.next
	destroy scratch
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected mixed type and region struct params to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeLayoutIntrospectionChecksOffsetFields(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "layout_introspection_missing_field.elisa", `struct Header layout c:
	tag: u8
	count: u32

def read() -> usize:
	return offset_of(Header, missing)
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `struct "Header" has no field "missing"`) {
		t.Fatalf("expected missing offset_of field diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeDeprecatedLegacyLayoutIntrospectionNames(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "layout_introspection_legacy_names.elisa", `struct Header layout c:
	tag: u8
	count: u32

def read() -> usize:
	return sizeof(Header) + alignof(Header) + offsetof(Header, count)
`)
	warnings := strings.Join(result.Deprecations(), "\n")
	for _, check := range []string{
		"`sizeof` is deprecated; use `size_of`",
		"`alignof` is deprecated; use `align_of`",
		"`offsetof` is deprecated; use `offset_of`",
	} {
		if !strings.Contains(warnings, check) {
			t.Fatalf("expected warning %q, got:\n%s", check, warnings)
		}
	}
}

func TestAnalyzeLayoutIntrospectionRejectsNonStructOffsetBase(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "layout_introspection_non_struct.elisa", `def read() -> usize:
	return offset_of(i64, count)
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `field access requires struct type`) {
		t.Fatalf("expected non-struct offset_of diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
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
