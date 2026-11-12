package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestRegionlessRefParameters(t *testing.T) {
	container := &ast.BuiltinTypeExpr{Name: "darray"}
	ref := &ast.RefType{Elem: container}
	region, ok := RegionlessRefContainer(ref)
	if !ok {
		t.Fatal("unannotated referenced container should infer a region")
	}
	*region = "inferred"
	if container.Region != "inferred" {
		t.Fatal("region must be placed on the container")
	}
	if _, ok := RegionlessRefContainer(ref); ok {
		t.Fatal("explicit container region must be preserved")
	}
	str := &ast.RefType{Elem: &ast.NamedType{Name: "dstr"}}
	region, ok = RegionlessRefContainer(str)
	if !ok {
		t.Fatal("referenced dstr should infer a region")
	}
	*region = "string_region"
	if str.Region != "string_region" {
		t.Fatal("dstr region must be placed on the reference")
	}
	record := &ast.RefType{Elem: &ast.NamedType{Name: "Record"}}
	region, ok = RegionlessRefStruct(record)
	if !ok {
		t.Fatal("referenced struct should infer a region")
	}
	*region = "record_region"
	if record.Region != "record_region" {
		t.Fatal("struct region must be placed on the reference")
	}
	if _, ok := RegionlessRefStruct(record); ok {
		t.Fatal("explicit reference region must be preserved")
	}
	if _, ok := RegionlessRefContainer(&ast.BuiltinTypeExpr{Name: "darray"}); ok {
		t.Fatal("by-value container must not infer a region")
	}
}
