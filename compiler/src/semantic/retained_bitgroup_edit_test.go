package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const retainedBitGroupFixture = `const enum Mode of u4:
    Off = 0
    Fast = 3

struct Packet layout(c):
    prefix: u32
    flags: bitset:
        f0
        f1
        f2
        f3
        f4
        f5
        f6
        f7
    bits: bitfield:
        tag: u3
        mode: u4
        active: u1
    state: bitfield:
        tag: u3
        mode: Mode
        active: u1
    suffix: u32

type Alias = Packet
struct Outer:
    packet: Alias
struct Box[T]:
    value: T
struct GenericPacket[T]:
    value: T
    state: bitfield:
        tag: u3
        mode: Mode
        active: u1

def identity[T](value: T) -> T:
    return value

def direct(value: Packet) -> bool:
    return identity(value.flags).f7 and value.bits.mode == 13

def nested(value: Outer) -> bool:
    return identity(value.packet.state).mode == Mode.Fast

def generic(value: Box[Alias]) -> bool:
    return identity(value.value.bits).mode == 13

def generic_state(value: GenericPacket[u8]) -> bool:
    return identity(value.state).mode == Mode.Fast

def layout() -> usize:
    return size_of(Alias) + size_of(Outer) + offset_of(Packet, suffix)
`

func TestRetainedInlineBitGroupDiskEdits(t *testing.T) {
	// Cold parallel workers assign invocation-local symbol identities in
	// independent maps. Compare the exact identity partition under the same
	// serial body schedule as retained reanalysis (as summary parity tests do).
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	ordinary := strings.Replace(retainedBitGroupFixture, "    prefix: u32", "    wide_prefix: u64 = 0\n    prefix: u32", 1)
	wider := strings.Replace(ordinary, "        f7\n", "        f7\n        f8\n", 1)
	wider = strings.Replace(wider, "        tag: u3\n        mode: u4\n        active: u1", "        active: u1\n        mode: u5\n        tag: u3", 1)
	enumWider := strings.Replace(wider, "Mode of u4", "Mode of u5", 1)
	kind := strings.Replace(enumWider, "    flags: bitset:\n        f0\n        f1\n        f2\n        f3\n        f4\n        f5\n        f6\n        f7\n        f8", "    flags: bitfield:\n        f7: u1\n        extra: i9", 1)
	_, state := AnalyzeRetained(retainedParse(t, retainedBitGroupFixture), retainedParse(t, retainedBitGroupFixture), []byte(retainedBitGroupFixture), AnalyzeOptions{})
	for step, source := range []string{ordinary, wider, enumWider, kind, retainedBitGroupFixture} {
		t.Logf("inline group revision %d (%s)", step, []string{"ordinary field", "group width/order", "enum storage", "group kind", "restore base"}[step])
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		packet := state.a.namedTypes["Packet"].(*StructType)
		groups := map[string]*BitGroupType{}
		for _, name := range []string{"flags", "bits", "state"} {
			groups[name] = packet.Fields[name].Type.(*BitGroupType)
		}
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("diagnostic parity:\n%s\n%s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
		// The kind-changing step deliberately makes the frozen boolean getter
		// invalid; all other edits must retain a fully valid program.
		if source != kind && len(got.Errors()) != 0 {
			t.Fatalf("unexpected errors: %v", got.Errors())
		}
		if got.NamedTypes["Packet"] != packet {
			t.Fatal("record identity changed")
		}
		for name, old := range groups {
			group := packet.Fields[name].Type.(*BitGroupType)
			expected := want.NamedTypes["Packet"].(*StructType).Fields[name].Type.(*BitGroupType)
			if group != old || group.Kind != expected.Kind || group.BackingWidth != expected.BackingWidth || len(group.Members) != len(expected.Members) || len(group.MemberMap) != len(expected.MemberMap) {
				t.Fatalf("stale group %s: %+v, expected %+v", name, group, expected)
			}
			for i, member := range group.Members {
				e := expected.Members[i]
				if member.Name != e.Name || member.Type.String() != e.Type.String() || member.Width != e.Width || member.Offset != e.Offset || member.Signed != e.Signed || member.Decl != &group.Decl.Members[i] || group.MemberMap[member.Name] != member {
					t.Fatalf("stale %s member %+v, expected %+v", name, member, e)
				}
			}
		}
		genericGroup := got.NamedTypes["GenericPacket"].(*StructType).Fields["state"].Type.(*BitGroupType)
		coldGroup := want.NamedTypes["GenericPacket"].(*StructType).Fields["state"].Type.(*BitGroupType)
		if genericGroup.BackingWidth != coldGroup.BackingWidth || genericGroup.MemberMap["mode"].Width != coldGroup.MemberMap["mode"].Width {
			t.Fatal("generic owner retained stale enum storage width")
		}
		// Retained analyses intentionally keep detached old body facts in their
		// backing maps (see assertRetainedLiveResultParity). Compare every live
		// semantic fact with cold analysis, including projection result types.
		assertRetainedCanonicalLiveResultParity(t, want, got)
		visit := func(node any) {
			field, ok := node.(*ast.FieldExpr)
			if !ok {
				return
			}
			if owner, ok := got.ExprTypes[field.Object].(*BitGroupType); ok {
				if typ := got.ExprTypes[field]; !SameType(typ, owner.MemberMap[field.Field].Type) {
					t.Fatalf("live projection result %s disagrees with %s", typ, owner.MemberMap[field.Field].Type)
				}
			}
		}
		for _, decl := range retainedWalkDecls(got.File) {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				walkStoreScanNodes(funcScanStmts(fn), visit)
				walkStoreScanNodes(funcDefaultArgStmts(fn), visit)
			}
		}
	}
}

func TestRetainedInlineBitGroupDependencyAndFlow(t *testing.T) {
	enum := &ConstEnumType{Name: "Mode"}
	group := &BitGroupType{Name: "Packet.state", Members: []BitGroupMember{{Name: "mode", Type: enum}}}
	packet := &StructType{Name: "Packet", Fields: map[string]Field{"state": {Name: "state", Type: group}}}
	a := &Analyzer{namedTypes: map[string]Type{"Packet": packet}}
	targets := map[Type]bool{enum: true}
	a.widenLayoutTargets(targets)
	if !targets[packet] || !retainedTypeReaches(group, targets, map[Type]bool{}) {
		t.Fatal("group storage dependency did not reach owning record and projected group")
	}
	if !retainedFlowNeutral(nil, packet.Fields) || !retainedFlowNeutral(packet.Fields, nil) {
		t.Fatal("inline scalar group changed address flow")
	}
}

func TestRetainedInlineBitGroupKeepsRecordProtocolGuards(t *testing.T) {
	const source = "struct Packet:\n    flags: bitset:\n        ready\n"
	old := retainedParse(t, source).Decls[0].(*ast.StructDecl)
	if !retainedStructFieldEditAllowed(old, old) {
		t.Fatal("plain inline group rejected")
	}
	for name, mutate := range map[string]func(*ast.StructDecl){
		"affine":    func(d *ast.StructDecl) { d.Affine = true },
		"resource":  func(d *ast.StructDecl) { d.Resource = true },
		"droppable": func(d *ast.StructDecl) { d.Droppable = true },
		"ghost":     func(d *ast.StructDecl) { d.Fields[0].Ghost = true },
		"tail":      func(d *ast.StructDecl) { d.Fields[0].IsTail = true },
		"field annotation": func(d *ast.StructDecl) {
			d.Fields[0].Annotations = []ast.Annotation{{Name: "align", Args: []string{"8"}}}
		},
		"record annotation": func(d *ast.StructDecl) { d.Annotations = []ast.Annotation{{Name: "fixed_layout"}} },
		"layout":            func(d *ast.StructDecl) { d.Layout = ast.StructLayoutSOA },
	} {
		t.Run(name, func(t *testing.T) {
			d := retainedParse(t, source).Decls[0].(*ast.StructDecl)
			mutate(d)
			if retainedStructFieldEditAllowed(old, d) {
				t.Fatal("record protocol guard was bypassed")
			}
		})
	}
}
