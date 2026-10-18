//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"testing"
)

func TestRegionPolyAutoAdoptionRequiresCompilerGeneratedRegion(t *testing.T) {
	fnType := &semantic.FuncType{RegionPolymorphic: true}
	for _, tc := range []struct {
		name string
		stmt *ast.RegionStmt
		want bool
	}{
		{name: "compiler-generated auto", stmt: &ast.RegionStmt{Name: "__auto_123", Lazy: true}, want: true},
		{name: "user-written in auto", stmt: &ast.RegionStmt{Name: "__auto_123", Lazy: true, UserAuto: true}},
		{name: "explicit region with synthesized-looking name", stmt: &ast.RegionStmt{Name: "__auto_fake"}},
		{name: "non-lazy synthesized-looking region", stmt: &ast.RegionStmt{Name: "__auto_123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backendRegionPolyAutoAdopts(fnType, true, "", tc.stmt); got != tc.want {
				t.Fatalf("backendRegionPolyAutoAdopts(%+v) = %t, want %t", tc.stmt, got, tc.want)
			}
		})
	}

	if got := backendRegionPolyAutoAdopts(nil, true, "", &ast.RegionStmt{Name: "__auto_123", Lazy: true}); got {
		t.Fatal("region adoption requires a function type")
	}
	if got := backendRegionPolyAutoAdopts(fnType, false, "", &ast.RegionStmt{Name: "__auto_123", Lazy: true}); got {
		t.Fatal("region adoption requires a caller arena")
	}
	if got := backendRegionPolyAutoAdopts(fnType, true, "", nil); got {
		t.Fatal("region adoption requires a region statement")
	}
}

func TestSynthesizedAutoRegionFlagUsesCompilerProvenance(t *testing.T) {
	generated := &ast.RegionStmt{Name: "__auto_123", Lazy: true}
	userAuto := &ast.RegionStmt{Name: "__auto_123", Lazy: true, UserAuto: true}
	for _, tc := range []struct {
		name         string
		parentActive bool
		stmt         *ast.RegionStmt
		want         bool
	}{
		{name: "generated scope activates flag", stmt: generated, want: true},
		{name: "user in auto leaves flag inactive", stmt: userAuto, want: false},
		{name: "nested user scope preserves generated parent", parentActive: true, stmt: userAuto, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backendSynthesizedAutoRegionActive(tc.parentActive, tc.stmt); got != tc.want {
				t.Fatalf("backendSynthesizedAutoRegionActive(%t, %+v) = %t, want %t", tc.parentActive, tc.stmt, got, tc.want)
			}
		})
	}
}
