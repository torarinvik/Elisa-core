//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/semantic"
)

func TestSubmitTypeProvenance(t *testing.T) {
	a := &semantic.StructType{Namespace: "partition.1", Name: "captureA"}
	b := &semantic.StructType{Namespace: "partition.1", Name: "captureB"}
	p := &submitTypeProvenance{}
	if !p.register(a) || !p.register(b) {
		t.Fatal("register")
	}
	wrapper := &semantic.StructType{Name: "Wrapper", Fields: map[string]semantic.Field{"field": {Type: b}}}
	clone := &semantic.StructType{Namespace: a.Namespace, Name: a.Name}
	cycle := &semantic.StructType{Name: "Cycle"}
	cycle.Fields = map[string]semantic.Field{"self": {Type: cycle}, "capture": {Type: b}}
	var nilStruct *semantic.StructType
	tests := []struct {
		name          string
		bindings      []semantic.Type
		tainted, safe bool
	}{
		{"ordinary", []semantic.Type{&semantic.BuiltinType{Name: "i64"}}, false, true},
		{"direct", []semantic.Type{a}, true, true},
		{"encoded wrapper", []semantic.Type{&semantic.GenericInstanceType{Name: "Ref", Base: &semantic.BuiltinType{Name: "i64"}, Args: []semantic.Type{a}}}, true, true},
		{"hidden despite visible namespace", []semantic.Type{a, wrapper}, true, false},
		{"same capture visible and hidden", []semantic.Type{b, wrapper}, true, false},
		{"unverified clone", []semantic.Type{clone}, true, false},
		{"unregistered partition type", []semantic.Type{&semantic.StructType{Name: "elisa.partition.other.1.__submit_capture_0_capture"}}, true, false},
		{"enum payload", []semantic.Type{&semantic.EnumType{Name: "E", Variants: []*semantic.EnumVariant{{Payload: []semantic.Type{a}}}}}, true, false},
		{"cycle", []semantic.Type{cycle}, true, false},
		{"nil", []semantic.Type{nil}, false, false},
		{"typed nil", []semantic.Type{nilStruct}, false, false},
		{"unresolved", []semantic.Type{&semantic.TypeParamType{Name: "T"}}, false, false},
		{"opaque generic nominal", []semantic.Type{&semantic.StructType{Name: "Box$generic_v1$deadbeef"}}, false, false},
		{"unresolved generic nominal", []semantic.Type{&semantic.StructType{Name: "Box", TypeParams: []string{"T"}}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tainted, safe := p.classify(tt.bindings)
			if tainted != tt.tainted || safe != tt.safe {
				t.Fatalf("got tainted=%v safe=%v; want %v %v", tainted, safe, tt.tainted, tt.safe)
			}
		})
	}
	if p.register(clone) {
		t.Fatal("duplicate nominal identity accepted")
	}
}
