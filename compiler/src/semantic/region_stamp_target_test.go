package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestRegionStampTargetsPreserveExplicitRegions(t *testing.T) {
	container := &ast.BuiltinTypeExpr{Name: "darray"}
	ref := &ast.RefType{Elem: container}
	target, ok := regionlessRefContainer(&ast.MutableType{Elem: ref})
	if !ok || target != &container.Region {
		t.Fatal("container stamp must target the backing type")
	}
	*target = "caller"
	if _, ok := regionlessRefContainer(ref); ok {
		t.Fatal("explicit container region must remain unchanged")
	}
	for _, name := range []string{"dstr", "Record"} {
		ref := &ast.RefType{Elem: &ast.NamedType{Name: name}}
		var target *string
		if name == "dstr" {
			target, ok = regionlessRefContainer(ref)
		} else {
			target, ok = regionlessRefStruct(ref)
		}
		if !ok || target != &ref.Region {
			t.Fatalf("%s stamp must target the reference", name)
		}
		*target = "caller"
		if _, ok := regionlessRefContainer(ref); ok {
			t.Fatal("explicit reference region must remain unchanged")
		}
		if _, ok := regionlessRefStruct(ref); ok {
			t.Fatal("explicit reference region must remain unchanged")
		}
	}
}

var regionStampTargetSink *string

func TestRegionStampTargetDoesNotAllocate(t *testing.T) {
	ref := &ast.RefType{Elem: &ast.BuiltinTypeExpr{Name: "darray"}}
	allocations := testing.AllocsPerRun(100, func() {
		regionStampTargetSink, _ = regionlessRefContainer(ref)
	})
	if allocations != 0 {
		t.Fatalf("region target lookup allocated %g objects", allocations)
	}
}
